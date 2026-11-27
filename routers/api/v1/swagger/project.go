// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package swagger

import (
	api "forgejo.org/modules/structs"
)

// Project - Get specific project
// swagger:response Project
type swaggerProject struct {
	// in:body
	Body api.Project `json:"body"`
}

// ProjectIssue - Get specific project issue
// swagger:response ProjectIssue
type swaggerProjectIssue struct {
	// in:body
	Body api.ProjectIssue `json:"body"`
}

// ProjectList - List of projects
// swagger:response ProjectList
type swaggerProjectList struct {
	// in:body
	Body []api.Project `json:"body"`
}

// ProjectIssueList - List of issues in the project
// swagger:response ProjectIssueList
type swaggerProjectIssueList struct {
	// in:body
	Body []api.ProjectIssue `json:"body"`
}
