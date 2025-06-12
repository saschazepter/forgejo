// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package quota

import (
	"testing"

	"forgejo.org/models/unittest"

	_ "forgejo.org/models"
	_ "forgejo.org/models/actions"
	_ "forgejo.org/models/activities"
	_ "forgejo.org/models/forgefed"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}
