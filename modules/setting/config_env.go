// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"bytes"
	"os"
	"regexp"
	"strconv"
	"strings"

	"forgejo.org/modules/log"
)

const (
	EnvConfigKeyPrefixGitea = "^(FORGEJO|GITEA)__"
	EnvConfigKeySuffixFile  = "__FILE"
)

const escapeRegexpString = "_0[xX](([0-9a-fA-F][0-9a-fA-F])+)_"

var escapeRegex = regexp.MustCompile(escapeRegexpString)

func CollectEnvConfigKeys() (keys []string) {
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, EnvConfigKeyPrefixGitea) {
			k, _, _ := strings.Cut(env, "=")
			keys = append(keys, k)
		}
	}
	return keys
}

func ClearEnvConfigKeys() {
	for _, k := range CollectEnvConfigKeys() {
		_ = os.Unsetenv(k)
	}
}

// decodeEnvSectionKey will decode a portable string encoded Section__Key pair
// Portable strings are considered to be of the form [A-Z0-9_]*
// We will encode a disallowed value as the UTF8 byte string preceded by _0X and
// followed by _. E.g. _0X2C_ for a '-' and _0X2E_ for '.'
// Section and Key are separated by a plain '__'.
// The entire section can be encoded as a UTF8 byte string
func decodeEnvSectionKey(encoded string) (ok bool, section, key string) {
	inKey := false
	last := 0
	escapeStringIndices := escapeRegex.FindAllStringIndex(encoded, -1)
	for _, unescapeIdx := range escapeStringIndices {
		preceding := encoded[last:unescapeIdx[0]]
		if !inKey {
			if before, after, ok := strings.Cut(preceding, "__"); ok {
				section += before
				inKey = true
				key += after
			} else {
				section += preceding
			}
		} else {
			key += preceding
		}
		toDecode := encoded[unescapeIdx[0]+3 : unescapeIdx[1]-1]
		decodedBytes := make([]byte, len(toDecode)/2)
		for i := 0; i < len(toDecode)/2; i++ {
			// Can ignore error here as we know these should be hexadecimal from the regexp
			byteInt, _ := strconv.ParseInt(toDecode[2*i:2*i+2], 16, 0)
			decodedBytes[i] = byte(byteInt)
		}
		if inKey {
			key += string(decodedBytes)
		} else {
			section += string(decodedBytes)
		}
		last = unescapeIdx[1]
	}
	remaining := encoded[last:]
	if !inKey {
		if before, after, ok := strings.Cut(remaining, "__"); ok {
			section += before
			key += after
		} else {
			section += remaining
		}
	} else {
		key += remaining
	}
	section = strings.ToLower(section)
	ok = key != ""
	if !ok {
		section = ""
		key = ""
	}
	return ok, section, key
}

// decodeEnvironmentKey decode the environment key to section and key
// The environment key is in the form of GITEA__SECTION__KEY or GITEA__SECTION__KEY__FILE
func decodeEnvironmentKey(prefixRegexp *regexp.Regexp, suffixFile, envKey string) (ok bool, section, key string, useFileValue bool) { //nolint:unparam
	if strings.HasSuffix(envKey, suffixFile) {
		useFileValue = true
		envKey = envKey[:len(envKey)-len(suffixFile)]
	}
	loc := prefixRegexp.FindStringIndex(envKey)
	if loc == nil {
		return false, "", "", false
	}
	ok, section, key = decodeEnvSectionKey(envKey[loc[1]:])
	return ok, section, key, useFileValue
}

func EnvironmentToConfig(cfg ConfigProvider, envs []string) (changed bool) {
	prefixRegexp := regexp.MustCompile(EnvConfigKeyPrefixGitea)
	for _, kv := range envs {
		before, after, ok0 := strings.Cut(kv, "=")
		if !ok0 {
			continue
		}

		// parse the environment variable to config section name and key name
		envKey := before
		keyValue := after
		ok, sectionName, keyName, useFileValue := decodeEnvironmentKey(prefixRegexp, EnvConfigKeySuffixFile, envKey)
		if !ok {
			continue
		}

		// use environment value as config value, or read the file content as value if the key indicates a file
		if useFileValue {
			fileContent, err := os.ReadFile(keyValue)
			if err != nil {
				log.Error("Error reading file for %s : %v", envKey, keyValue, err)
				continue
			}
			if bytes.HasSuffix(fileContent, []byte("\r\n")) {
				fileContent = fileContent[:len(fileContent)-2]
			} else if bytes.HasSuffix(fileContent, []byte("\n")) {
				fileContent = fileContent[:len(fileContent)-1]
			}
			keyValue = string(fileContent)
		}

		// try to set the config value if necessary
		section, err := cfg.GetSection(sectionName)
		if err != nil {
			section, err = cfg.NewSection(sectionName)
			if err != nil {
				log.Error("Error creating section: %s : %v", sectionName, err)
				continue
			}
		}
		key := ConfigSectionKey(section, keyName)
		if key == nil {
			changed = true
			key, err = section.NewKey(keyName, keyValue)
			if err != nil {
				log.Error("Error creating key: %s in section: %s with value: %s : %v", keyName, sectionName, keyValue, err)
				continue
			}
		}
		oldValue := key.Value()
		if !changed && oldValue != keyValue {
			changed = true
		}
		key.SetValue(keyValue)
	}
	return changed
}
