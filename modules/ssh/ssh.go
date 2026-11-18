// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package ssh

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	asymkey_model "forgejo.org/models/asymkey"
	"forgejo.org/modules/graceful"
	"forgejo.org/modules/log"
	"forgejo.org/modules/process"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"

	"github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"
)

func getExitStatusFromError(err error) int {
	if err == nil {
		return 0
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return 1
	}

	waitStatus, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		// This is a fallback and should at least let us return something useful
		// when running on Windows, even if it isn't completely accurate.
		if exitErr.Success() {
			return 0
		}

		return 1
	}

	return waitStatus.ExitStatus()
}

func sessionHandler(session ssh.Session) {
	keyID := session.ConnPermissions().Extensions["forgejo-key-id"]

	command := session.RawCommand()

	logger.Trace("SSH: Payload: %v", command)

	args := []string{"--config=" + setting.CustomConf, "serv", "key-" + keyID}
	logger.Trace("SSH: Arguments: %v", args)

	// Previously we would use `session.Context()` here, which would cause the context to cancel when the SSH session is
	// closed, which causes the `forgejo` subprocess to be cancelled when the session is closed.  This behaviour was a
	// deviation from OpenSSH, which allows subprocesses to continue past the SSH session and relies on the subprocess's
	// stdin being closed and stdout being unwritable (EPIPE) to indicate that the subprocess should finish.  As an
	// effect of that, `forgejo serv` subprocesses would be killed, and their `git` subprocesses would be killed (due to
	// `SetupCancellableCommand`), and `git`'s subprocess post-receive hooks would be killed, all resulting in a desync
	// between the on-disk repository and Forgejo's knowledge about the repository branches.
	//
	// Instead of that, we try to mirror OpenSSH's behaviour -- create a context that won't automatically kill the
	// subprocesses, and rely on the stdin/stdout pipes to signal to subprocesses their cancellation.  We still
	// terminate those processes if Forgejo's graceful shutdown hits the hammer timeout -- during a graceful shutdown
	// the SSH server still stop accepting new connections, and these connections just need to finish before the hammer
	// timeout, or, be terminated at that timeout.
	ctx, cancel := context.WithCancel(graceful.GetManager().HammerContext())
	defer cancel()

	gitProtocol := ""
	for _, env := range session.Environ() {
		if strings.HasPrefix(env, "GIT_PROTOCOL=") {
			_, gitProtocol, _ = strings.Cut(env, "=")
			break
		}
	}

	cmd := exec.CommandContext(ctx, setting.AppPath, args...)
	cmd.Env = append(
		os.Environ(),
		"SSH_ORIGINAL_COMMAND="+command,
		"SKIP_MINWINSVC=1",
		"GIT_PROTOCOL="+gitProtocol,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Error("SSH: StdoutPipe: %v", err)
		return
	}
	defer stdout.Close()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		logger.Error("SSH: StderrPipe: %v", err)
		return
	}
	defer stderr.Close()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		logger.Error("SSH: StdinPipe: %v", err)
		return
	}
	defer stdin.Close()

	process.SetupCancellableCommand(cmd)

	wg := &sync.WaitGroup{}
	wg.Add(2)

	if err = cmd.Start(); err != nil {
		logger.Error("SSH: Start: %v", err)
		return
	}

	go func() {
		defer stdin.Close()
		if _, err := io.Copy(stdin, session); err != nil {
			logger.Error("Failed to write session to stdin. %s", err)
		}
	}()

	go func() {
		defer wg.Done()
		defer stdout.Close()
		if _, err := io.Copy(session, stdout); err != nil {
			logger.Error("Failed to write stdout to session. %s", err)
		}
	}()

	go func() {
		defer wg.Done()
		defer stderr.Close()
		if _, err := io.Copy(session.Stderr(), stderr); err != nil {
			logger.Error("Failed to write stderr to session. %s", err)
		}
	}()

	// Ensure all the output has been written before we wait on the command
	// to exit.
	wg.Wait()

	// Wait for the command to exit and log any errors we get
	err = cmd.Wait()
	if err != nil {
		// Cannot use errors.Is here because ExitError doesn't implement Is
		// Thus errors.Is will do equality test NOT type comparison
		if _, ok := err.(*exec.ExitError); !ok {
			logger.Error("SSH: Wait: %v", err)
		}
	}

	if err := session.Exit(getExitStatusFromError(err)); err != nil && !errors.Is(err, io.EOF) {
		logger.Error("Session failed to exit. %s", err)
	}
}

func publicKeyHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	if logger.LevelEnabled(log.DEBUG) { // <- FingerprintSHA256 is kinda expensive so only calculate it if necessary
		logger.Debug("Handle Public Key: Fingerprint: %s from %s", gossh.FingerprintSHA256(key), ctx.RemoteAddr())
	}

	if ctx.User() != setting.SSH.BuiltinServerUser {
		logger.Warn("Invalid SSH username %s - must use %s for all git operations via ssh", ctx.User(), setting.SSH.BuiltinServerUser)
		logger.Warn("Failed authentication attempt from %s", ctx.RemoteAddr())
		return false
	}

	// check if we have a certificate
	if cert, ok := key.(*gossh.Certificate); ok {
		if logger.LevelEnabled(log.DEBUG) { // <- FingerprintSHA256 is kinda expensive so only calculate it if necessary
			logger.Debug("Handle Certificate: %s Fingerprint: %s is a certificate", ctx.RemoteAddr(), gossh.FingerprintSHA256(key))
		}

		if len(setting.SSH.TrustedUserCAKeys) == 0 {
			logger.Warn("Certificate Rejected: No trusted certificate authorities for this server")
			logger.Warn("Failed authentication attempt from %s", ctx.RemoteAddr())
			return false
		}

		if cert.CertType != gossh.UserCert {
			logger.Warn("Certificate Rejected: Not a user certificate")
			logger.Warn("Failed authentication attempt from %s", ctx.RemoteAddr())
			return false
		}

		// look for the exact principal
	principalLoop:
		for _, principal := range cert.ValidPrincipals {
			pkey, err := asymkey_model.SearchPublicKeyByContentExact(ctx, principal)
			if err != nil {
				if asymkey_model.IsErrKeyNotExist(err) {
					logger.Debug("Principal Rejected: %s Unknown Principal: %s", ctx.RemoteAddr(), principal)
					continue principalLoop
				}
				logger.Error("SearchPublicKeyByContentExact: %v", err)
				return false
			}

			c := &gossh.CertChecker{
				IsUserAuthority: func(auth gossh.PublicKey) bool {
					marshaled := auth.Marshal()
					for _, k := range setting.SSH.TrustedUserCAKeysParsed {
						if bytes.Equal(marshaled, k.Marshal()) {
							return true
						}
					}

					return false
				},
			}

			// check the CA of the cert
			if !c.IsUserAuthority(cert.SignatureKey) {
				if logger.LevelEnabled(log.DEBUG) {
					logger.Debug("Principal Rejected: %s Untrusted Authority Signature Fingerprint %s for Principal: %s", ctx.RemoteAddr(), gossh.FingerprintSHA256(cert.SignatureKey), principal)
				}
				continue principalLoop
			}

			// validate the cert for this principal
			if err := c.CheckCert(principal, cert); err != nil {
				// User is presenting an invalid certificate - STOP any further processing
				logger.Error("Invalid Certificate KeyID %s with Signature Fingerprint %s presented for Principal: %s from %s", cert.KeyId, gossh.FingerprintSHA256(cert.SignatureKey), principal, ctx.RemoteAddr())
				logger.Warn("Failed authentication attempt from %s", ctx.RemoteAddr())

				return false
			}

			if logger.LevelEnabled(log.DEBUG) { // <- FingerprintSHA256 is kinda expensive so only calculate it if necessary
				logger.Debug("Successfully authenticated: %s Certificate Fingerprint: %s Principal: %s", ctx.RemoteAddr(), gossh.FingerprintSHA256(key), principal)
			}
			if ctx.Permissions().Extensions == nil {
				ctx.Permissions().Extensions = map[string]string{}
			}
			ctx.Permissions().Extensions["forgejo-key-id"] = strconv.FormatInt(pkey.ID, 10)

			return true
		}

		logger.Warn("From %s Fingerprint: %s is a certificate, but no valid principals found", ctx.RemoteAddr(), gossh.FingerprintSHA256(key))
		logger.Warn("Failed authentication attempt from %s", ctx.RemoteAddr())
		return false
	}

	if logger.LevelEnabled(log.DEBUG) { // <- FingerprintSHA256 is kinda expensive so only calculate it if necessary
		logger.Debug("Handle Public Key: %s Fingerprint: %s is not a certificate", ctx.RemoteAddr(), gossh.FingerprintSHA256(key))
	}

	pkey, err := asymkey_model.SearchPublicKeyForSSH(ctx, key)
	if err != nil {
		if asymkey_model.IsErrKeyNotExist(err) {
			logger.Warn("Unknown public key: %s from %s", gossh.FingerprintSHA256(key), ctx.RemoteAddr())
			logger.Warn("Failed authentication attempt from %s", ctx.RemoteAddr())
			return false
		}
		logger.Error("SearchPublicKeyByContent: %v", err)
		return false
	}

	if logger.LevelEnabled(log.DEBUG) { // <- FingerprintSHA256 is kinda expensive so only calculate it if necessary
		logger.Debug("Successfully authenticated: %s Public Key Fingerprint: %s", ctx.RemoteAddr(), gossh.FingerprintSHA256(key))
	}
	if ctx.Permissions().Extensions == nil {
		ctx.Permissions().Extensions = map[string]string{}
	}
	ctx.Permissions().Extensions["forgejo-key-id"] = strconv.FormatInt(pkey.ID, 10)

	return true
}

// sshConnectionFailed logs a failed connection
// -  this mainly exists to give a nice function name in logging
func sshConnectionFailed(conn net.Conn, err error) {
	// Log the underlying error with a specific message
	logger.Warn("Failed connection from %s with error: %v", conn.RemoteAddr(), err)
	// Log with the standard failed authentication from message for simpler fail2ban configuration
	logger.Warn("Failed authentication attempt from %s", conn.RemoteAddr())
}

// Listen starts a SSH server listens on given port.
func Listen(host string, port int, ciphers, keyExchanges, macs []string) {
	srv := ssh.Server{
		Addr:             net.JoinHostPort(host, strconv.Itoa(port)),
		PublicKeyHandler: publicKeyHandler,
		Handler:          sessionHandler,
		ServerConfigCallback: func(ctx ssh.Context) *gossh.ServerConfig {
			config := &gossh.ServerConfig{}
			config.KeyExchanges = keyExchanges
			config.MACs = macs
			config.Ciphers = ciphers
			return config
		},
		ConnectionFailedCallback: sshConnectionFailed,
		// We need to explicitly disable the PtyCallback so text displays
		// properly.
		PtyCallback: func(ctx ssh.Context, pty ssh.Pty) bool {
			return false
		},
	}

	keys := make([]string, 0, len(setting.SSH.ServerHostKeys))
	for _, key := range setting.SSH.ServerHostKeys {
		isExist, err := util.IsExist(key)
		if err != nil {
			log.Fatal("Unable to check if %s exists. Error: %v", setting.SSH.ServerHostKeys, err)
		}
		if isExist {
			keys = append(keys, key)
		}
	}

	if len(keys) == 0 {
		filePath := filepath.Dir(setting.SSH.ServerHostKeys[0])

		if err := os.MkdirAll(filePath, os.ModePerm); err != nil {
			logger.Error("Failed to create dir %s: %v", filePath, err)
		}

		err := GenKeyPair(setting.SSH.ServerHostKeys[0])
		if err != nil {
			log.Fatal("Failed to generate private key: %v", err)
		}
		logger.Trace("New private key is generated: %s", setting.SSH.ServerHostKeys[0])
		keys = append(keys, setting.SSH.ServerHostKeys[0])
	}

	for _, key := range keys {
		logger.Info("Adding SSH host key: %s", key)
		err := srv.SetOption(ssh.HostKeyFile(key))
		if err != nil {
			logger.Error("Failed to set Host Key. %s", err)
		}
	}

	go func() {
		_, _, finished := process.GetManager().AddTypedContext(graceful.GetManager().HammerContext(), "Service: Built-in SSH server", process.SystemProcessType, true)
		defer finished()
		listen(&srv)
	}()
}

// GenKeyPair make a pair of public and private keys for SSH access.
// Public key is encoded in the format for inclusion in an OpenSSH authorized_keys file.
// Private Key generated is PEM encoded
func GenKeyPair(keyPath string) error {
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return err
	}

	privateKeyPEM := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}
	f, err := os.OpenFile(keyPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err = f.Close(); err != nil {
			logger.Error("Close: %v", err)
		}
	}()

	if err := pem.Encode(f, privateKeyPEM); err != nil {
		return err
	}

	// generate public key
	pub, err := gossh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return err
	}

	public := gossh.MarshalAuthorizedKey(pub)
	p, err := os.OpenFile(keyPath+".pub", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err = p.Close(); err != nil {
			logger.Error("Close: %v", err)
		}
	}()
	_, err = p.Write(public)
	return err
}
