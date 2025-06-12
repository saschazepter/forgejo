// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"errors"
	"net/url"
	"os/exec"
	"testing"

	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_CmdKeys(t *testing.T) {
	onGiteaRun(t, func(*testing.T, *url.URL) {
		tests := []struct {
			name           string
			args           []string
			wantErr        bool
			expectedOutput string
		}{
			{"test_empty_1", []string{"--username=git", "--type=test", "--content=test"}, true, "Command error: internal API error response, status=500, err=public key does not exist [id: 0]\n"},
			{"test_empty_2", []string{"-e", "git", "-u", "git", "-t", "test", "-k", "test"}, true, "Command error: internal API error response, status=500, err=public key does not exist [id: 0]\n"},
			{
				"with_key",
				[]string{"-e", "git", "-u", "git", "-t", "ssh-rsa", "-k", "AAAAB3NzaC1yc2EAAAADAQABAAABgQDWVj0fQ5N8wNc0LVNA41wDLYJ89ZIbejrPfg/avyj3u/ZohAKsQclxG4Ju0VirduBFF9EOiuxoiFBRr3xRpqzpsZtnMPkWVWb+akZwBFAx8p+jKdy4QXR/SZqbVobrGwip2UjSrri1CtBxpJikojRIZfCnDaMOyd9Jp6KkujvniFzUWdLmCPxUE9zhTaPu0JsEP7MW0m6yx7ZUhHyfss+NtqmFTaDO+QlMR7L2QkDliN2Jl3Xa3PhuWnKJfWhdAq1Cw4oraKUOmIgXLkuiuxVQ6mD3AiFupkmfqdHq6h+uHHmyQqv3gU+/sD8GbGAhf6ftqhTsXjnv1Aj4R8NoDf9BS6KRkzkeun5UisSzgtfQzjOMEiJtmrep2ZQrMGahrXa+q4VKr0aKJfm+KlLfwm/JztfsBcqQWNcTURiCFqz+fgZw0Ey/de0eyMzldYTdXXNRYCKjs9bvBK+6SSXRM7AhftfQ0ZuoW5+gtinPrnmoOaSCEJbAiEiTO/BzOHgowiM="},
				false,
				"# gitea public key\ncommand=\"" + setting.AppPath + " --config=" + util.ShellEscape(setting.CustomConf) + " serv key-1\",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty,no-user-rc,restrict ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDWVj0fQ5N8wNc0LVNA41wDLYJ89ZIbejrPfg/avyj3u/ZohAKsQclxG4Ju0VirduBFF9EOiuxoiFBRr3xRpqzpsZtnMPkWVWb+akZwBFAx8p+jKdy4QXR/SZqbVobrGwip2UjSrri1CtBxpJikojRIZfCnDaMOyd9Jp6KkujvniFzUWdLmCPxUE9zhTaPu0JsEP7MW0m6yx7ZUhHyfss+NtqmFTaDO+QlMR7L2QkDliN2Jl3Xa3PhuWnKJfWhdAq1Cw4oraKUOmIgXLkuiuxVQ6mD3AiFupkmfqdHq6h+uHHmyQqv3gU+/sD8GbGAhf6ftqhTsXjnv1Aj4R8NoDf9BS6KRkzkeun5UisSzgtfQzjOMEiJtmrep2ZQrMGahrXa+q4VKr0aKJfm+KlLfwm/JztfsBcqQWNcTURiCFqz+fgZw0Ey/de0eyMzldYTdXXNRYCKjs9bvBK+6SSXRM7AhftfQ0ZuoW5+gtinPrnmoOaSCEJbAiEiTO/BzOHgowiM= user2@localhost\n",
			},
			{"invalid", []string{"--not-a-flag=git"}, true, "Incorrect Usage: flag provided but not defined: -not-a-flag\n\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				out, err := runMainApp("keys", tt.args...)

				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					t.Log(string(exitErr.Stderr))
				}
				if tt.wantErr {
					require.Error(t, err)
					assert.Equal(t, tt.expectedOutput, string(exitErr.Stderr))
				} else {
					require.NoError(t, err)
					assert.Equal(t, tt.expectedOutput, out)
				}
			})
		}
	})
}
