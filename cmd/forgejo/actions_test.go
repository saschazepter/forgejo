// Copyright The Forgejo Authors.
// SPDX-License-Identifier: MIT

package forgejo

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestActions_getLabels(t *testing.T) {
	type testCase struct {
		args      []string
		hasLabels bool
		hasError  bool
		labels    []string
	}
	type resultType struct {
		labels *[]string
		err    error
	}

	cases := []testCase{
		{
			args:      []string{"x"},
			hasLabels: true,
			hasError:  false,
			labels:    []string{""},
		}, {
			args:      []string{"x", "--labels", "a,b"},
			hasLabels: true,
			hasError:  false,
			labels:    []string{"a", "b"},
		}, {
			args:      []string{"x", "--keep-labels"},
			hasLabels: false,
			hasError:  false,
		}, {
			args:      []string{"x", "--keep-labels", "--labels", "a,b"},
			hasLabels: false,
			hasError:  true,
		}, {
			// this edge-case exists because that's what actually happens
			// when no '--labels ...' options are present
			args:      []string{"x", "--keep-labels", "--labels", ""},
			hasLabels: false,
			hasError:  false,
		},
	}

	flags := SubcmdActionsRegister(t.Context()).Flags
	for _, c := range cases {
		t.Run(fmt.Sprintf("args: %v", c.args), func(t *testing.T) {
			// Create a copy of command to test
			var result *resultType
			app := cli.Command{}
			app.Flags = flags
			app.Action = func(_ context.Context, ctx *cli.Command) error {
				labels, err := getLabels(ctx)
				result = &resultType{labels, err}
				return nil
			}

			// Run it
			_ = app.Run(t.Context(), c.args)

			// Test the results
			require.NotNil(t, result)
			if c.hasLabels {
				assert.NotNil(t, result.labels)
				assert.Equal(t, c.labels, *result.labels)
			} else {
				assert.Nil(t, result.labels)
			}
			if c.hasError {
				require.Error(t, result.err)
			} else {
				assert.NoError(t, result.err)
			}
		})
	}
}
