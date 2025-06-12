// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/json"
	modules_session "forgejo.org/modules/session"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"
	"forgejo.org/tests"

	"code.forgejo.org/go-chi/session"
	"github.com/stretchr/testify/require"
)

var rootPathRe = regexp.MustCompile("\\[repository\\]\nROOT\\s=\\s.*")

func onForgejoRunTB(t testing.TB, callback func(testing.TB, *url.URL), prepare ...bool) {
	if len(prepare) == 0 || prepare[0] {
		defer tests.PrepareTestEnv(t, 1)()
	}
	createSessions(t)

	s := http.Server{
		Handler: testE2eWebRoutes,
	}

	u, err := url.Parse(setting.AppURL)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", u.Host)
	i := 0
	for err != nil && i <= 10 {
		time.Sleep(100 * time.Millisecond)
		listener, err = net.Listen("tcp", u.Host)
		i++
	}
	require.NoError(t, err)
	u.Host = listener.Addr().String()

	// Override repository root in config.
	conf, err := os.ReadFile(setting.CustomConf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(setting.CustomConf, rootPathRe.ReplaceAll(conf, []byte("[repository]\nROOT = "+setting.RepoRootPath)), 0o644))

	defer func() {
		require.NoError(t, os.WriteFile(setting.CustomConf, conf, 0o644))
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		s.Shutdown(ctx)
		cancel()
	}()

	go s.Serve(listener)
	// Started by config go ssh.Listen(setting.SSH.ListenHost, setting.SSH.ListenPort, setting.SSH.ServerCiphers, setting.SSH.ServerKeyExchanges, setting.SSH.ServerMACs)

	callback(t, u)
}

func onForgejoRun(t *testing.T, callback func(*testing.T, *url.URL), prepare ...bool) {
	onForgejoRunTB(t, func(t testing.TB, u *url.URL) {
		callback(t.(*testing.T), u)
	}, prepare...)
}

func createSessions(t testing.TB) {
	t.Helper()
	// copied from playwright.config.ts
	browsers := []string{
		"chromium",
		"firefox",
		"webkit",
		"Mobile Chrome",
		"Mobile Safari",
	}
	scopes := []string{
		"shared",
	}
	users := []string{
		"user1",
		"user2",
		"user12",
		"user18",
		"user29",
		"user40",
	}

	authState := filepath.Join(filepath.Dir(setting.AppPath), "tests", "e2e", ".auth")
	err := os.RemoveAll(authState)
	require.NoError(t, err)

	err = os.MkdirAll(authState, os.ModePerm)
	require.NoError(t, err)

	createSessionCookie := stateHelper(t)

	for _, user := range users {
		u := unittest.AssertExistsAndLoadBean(t, &user_model.User{LowerName: strings.ToLower(user)})
		for _, browser := range browsers {
			for _, scope := range scopes {
				stateFile := strings.ReplaceAll(strings.ToLower(fmt.Sprintf("state-%s-%s-%s.json", browser, user, scope)), " ", "-")
				createSessionCookie(filepath.Join(authState, stateFile), u)
			}
		}
	}
}

func stateHelper(t testing.TB) func(stateFile string, user *user_model.User) {
	type Cookie struct {
		Name     string `json:"name"`
		Value    string `json:"value"`
		Domain   string `json:"domain"`
		Path     string `json:"path"`
		Expires  int    `json:"expires"`
		HTTPOnly bool   `json:"httpOnly"`
		Secure   bool   `json:"secure"`
		SameSite string `json:"sameSite"`
	}

	type BrowserState struct {
		Cookies []Cookie `json:"cookies"`
		Origins []string `json:"origins"`
	}

	options := session.Options{
		Provider:       setting.SessionConfig.Provider,
		ProviderConfig: setting.SessionConfig.ProviderConfig,
		CookieName:     setting.SessionConfig.CookieName,
		CookiePath:     setting.SessionConfig.CookiePath,
		Gclifetime:     setting.SessionConfig.Gclifetime,
		Maxlifetime:    setting.SessionConfig.Maxlifetime,
		Secure:         setting.SessionConfig.Secure,
		SameSite:       setting.SessionConfig.SameSite,
		Domain:         setting.SessionConfig.Domain,
	}

	opt := session.PrepareOptions([]session.Options{options})

	vsp := modules_session.VirtualSessionProvider{}
	err := vsp.Init(opt.Maxlifetime, opt.ProviderConfig)
	require.NoError(t, err)

	return func(stateFile string, user *user_model.User) {
		sessionID := hex.EncodeToString(util.CryptoRandomBytes(int64(opt.IDLength) / 2))

		s, err := vsp.Read(sessionID)
		require.NoError(t, err)

		err = s.Set("uid", user.ID)
		require.NoError(t, err)

		err = s.Release()
		require.NoError(t, err)

		state := BrowserState{
			Cookies: []Cookie{
				{
					Name:     opt.CookieName,
					Value:    sessionID,
					Domain:   setting.Domain,
					Path:     "/",
					Expires:  -1,
					HTTPOnly: true,
					Secure:   false,
					SameSite: "Lax",
				},
			},
			Origins: []string{},
		}

		jsonData, err := json.Marshal(state)
		require.NoError(t, err)

		err = os.WriteFile(stateFile, jsonData, 0o644)
		require.NoError(t, err)
	}
}
