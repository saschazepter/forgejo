// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"strings"
	"testing"

	"forgejo.org/modules/optional"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRangeDiff(t *testing.T) {
	// SHA1 repo w/ --no-patch --no-color --abbrev=64
	// covering every case:
	// - commits equal
	// - commit added
	// - commit equal but with different indexes
	// - commit changed
	// - commit removed
	input := `1:  918c39d13569ea14f5739c9f43e3e3f38e73fc99 = 1:  918c39d13569ea14f5739c9f43e3e3f38e73fc99 feat: document the new feature
2:  600d619f568c9078aa8ed2c90002a30b6abbafd3 < -:  ---------------------------------------- add FIXME for later
4:  cf1e11ce3133188a7780e0121138d61ae434dda3 = 2:  a895be52878418fa00bc7a26745705c976e87d65 update CI for the new capability
3:  12e783c29c1cfd19bf523e995a1368466c8584f3 ! 3:  9da9f8570aa0fb41afce8df1ae95688925afbfca fix the FIXME
-:  ---------------------------------------- > 4:  b49c37670769fd82192fb6f954abd680ad007da9 lint fixups
`
	got, err := ParseRangeDiff(strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t,
		[]RangeDiffCommitPair{
			{
				Old:     optional.Some(RangeDiffCommit{Index: 1, OID: "918c39d13569ea14f5739c9f43e3e3f38e73fc99"}),
				New:     optional.Some(RangeDiffCommit{Index: 1, OID: "918c39d13569ea14f5739c9f43e3e3f38e73fc99"}),
				Status:  CommitEqual,
				Subject: "feat: document the new feature",
			},
			{
				Old:     optional.Some(RangeDiffCommit{Index: 2, OID: "600d619f568c9078aa8ed2c90002a30b6abbafd3"}),
				New:     optional.None[RangeDiffCommit](),
				Status:  CommitRemoved,
				Subject: "add FIXME for later",
			},
			{
				Old:     optional.Some(RangeDiffCommit{Index: 4, OID: "cf1e11ce3133188a7780e0121138d61ae434dda3"}),
				New:     optional.Some(RangeDiffCommit{Index: 2, OID: "a895be52878418fa00bc7a26745705c976e87d65"}),
				Status:  CommitEqual,
				Subject: "update CI for the new capability",
			},
			{
				Old:     optional.Some(RangeDiffCommit{Index: 3, OID: "12e783c29c1cfd19bf523e995a1368466c8584f3"}),
				New:     optional.Some(RangeDiffCommit{Index: 3, OID: "9da9f8570aa0fb41afce8df1ae95688925afbfca"}),
				Status:  CommitChanged,
				Subject: "fix the FIXME",
			},
			{
				Old:     optional.None[RangeDiffCommit](),
				New:     optional.Some(RangeDiffCommit{Index: 4, OID: "b49c37670769fd82192fb6f954abd680ad007da9"}),
				Status:  CommitAdded,
				Subject: "lint fixups",
			},
		},
		got)
}
