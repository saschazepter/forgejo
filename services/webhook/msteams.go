// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	actions_model "forgejo.org/models/actions"
	webhook_model "forgejo.org/models/webhook"
	"forgejo.org/modules/git"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/util"
	webhook_module "forgejo.org/modules/webhook"
	"forgejo.org/services/forms"
	"forgejo.org/services/webhook/shared"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type msteamsHandler struct{}

func (msteamsHandler) Available(webhook_module.Scope) bool { return true }
func (msteamsHandler) Type() webhook_module.HookType       { return webhook_module.MSTEAMS }
func (msteamsHandler) Metadata(*webhook_model.Webhook) any { return nil }
func (msteamsHandler) Icon(size int) template.HTML         { return shared.ImgIcon("msteams.png", size) }

func (msteamsHandler) UnmarshalForm(bind func(any)) forms.WebhookForm {
	var form struct {
		forms.WebhookCoreForm
		PayloadURL string `binding:"Required;ValidUrl"`
	}
	bind(&form)

	return forms.WebhookForm{
		WebhookCoreForm: form.WebhookCoreForm,
		URL:             form.PayloadURL,
		ContentType:     webhook_model.ContentTypeJSON,
		Secret:          "",
		HTTPMethod:      http.MethodPost,
		Metadata:        nil,
	}
}

type (
	// MSTeamsAction defines an action for an Adaptive Card, for example, opening a URL.
	MSTeamsAction struct {
		Type           string   `json:"type"`                     // e.g. "Action.OpenUrl" or "Action.ToggleVisibility"
		Title          string   `json:"title,omitempty"`          // The button title
		URL            string   `json:"url,omitempty"`            // The URL to open
		TargetElements []string `json:"targetElements,omitempty"` // For Action.ToggleVisibility
	}

	// MSTeamsTextBlock defines the Adaptive Card TextBlock element.
	MSTeamsTextBlock struct {
		Type         string         `json:"type"`                          // Must be "TextBlock"
		Text         string         `json:"text"`                          // The text content
		Size         string         `json:"size,omitempty"`                // e.g., "Small", "Default", "Medium", "Large", "ExtraLarge"
		Weight       string         `json:"weight,omitempty"`              // e.g., "Lighter", "Default", "Bolder"
		Color        string         `json:"color,omitempty"`               // text color, e.g., "Default", "Dark", "Light", "Accent", "Good", "Warning", "Attention"
		IsSubtle     *bool          `json:"isSubtle,omitempty"`            // Optional: makes text less prominent (nil->omitted)
		Wrap         *bool          `json:"wrap,omitempty"`                // Optional: enables text wrapping (nil->omitted)
		MaxLines     int            `json:"maxLines,omitempty"`            // Optional: maximum number of lines to display
		HorizAlign   string         `json:"horizontalAlignment,omitempty"` // e.g., "Left", "Center", "Right"
		Spacing      string         `json:"spacing,omitempty"`             // e.g., "None", "Small", "Default", "Medium", "Large", "ExtraLarge", "Padding"
		FontType     string         `json:"fontType,omitempty"`            // e.g., "Default", "Monospace"
		Style        string         `json:"style,omitempty"`               // e.g., "default","columnHeader","heading"
		SelectAction *MSTeamsAction `json:"selectAction,omitempty"`        // Optional: action when the TextBlock is clicked
		IsVisible    *bool          `json:"isVisible,omitempty"`           // Optional: is visible or not, default is true (nil->omitted)
	}

	MSTeamsBadge struct {
		Type         string         `json:"type"`                   // Must be "Badge"
		Text         string         `json:"text,omitempty"`         // The text content. Either this or Icon should be present
		Icon         string         `json:"icon,omitempty"`         // Icon to display. Either this or Text should be present
		Appearance   string         `json:"appearance,omitempty"`   // e.g., "Filled", "Tint"
		IconPosition string         `json:"iconPosition,omitempty"` // e.g., "Before", "After"
		Shape        string         `json:"shape,omitempty"`        // e.g., "Square", "Rounded", "Circular"
		Size         string         `json:"size,omitempty"`         // e.g., "Medium", "Large", "ExtraLarge"
		Style        string         `json:"style,omitempty"`        // e.g., "Default", "Subtle", "Informative", "Accent", "Good", "Attention", "Warning"
		Spacing      string         `json:"spacing,omitempty"`      // e.g., "None", "Small", "Default", "Medium", "Large", "ExtraLarge", "Padding"
		SelectAction *MSTeamsAction `json:"selectAction,omitempty"` // Optional: action when the IconRun is clicked
		ID           string         `json:"id,omitempty"`
	}

	// MSTeamsRickTextBlock defines the Adaptive Card RichTextBlock element, along with its contents (TextRun, IconRun, ImageRun)
	MSTeamsRichTextBlock struct {
		Type       string `json:"type"`                          // Must be "RichTextBlock"
		Inlines    []any  `json:"inlines"`                       // Content, should be array of either TextRun, IconRun, ImageRun
		LabelFor   string `json:"labelFor,omitempty"`            // Optional: label for the RichTextBlock
		HorizAlign string `json:"horizontalAlignment,omitempty"` // e.g., "Left", "Center", "Right"
		Spacing    string `json:"spacing,omitempty"`             // e.g., "None", "Small", "Default", "Medium", "Large", "ExtraLarge", "Padding"
		ID         string `json:"id,omitempty"`                  // ID
		IsVisible  *bool  `json:"isVisible,omitempty"`           // Optional: is visible or not, default is true (nil->omitted)
	}

	MSTeamsRichTextTextRun struct {
		Type          string         `json:"type"` // Must be "TextRun"
		Text          string         `json:"text"` // The text content
		Highlight     *bool          `json:"highlight,omitempty"`
		Italic        *bool          `json:"italic,omitempty"`
		Strikethrough *bool          `json:"strikethrough,omitempty"`
		Underline     *bool          `json:"underline,omitempty"`
		Color         string         `json:"color,omitempty"`        // text color, e.g., "Default", "Dark", "Light", "Accent", "Good", "Warning", "Attention"
		FontType      string         `json:"fontType,omitempty"`     // e.g., "Default", "Monospace"
		IsSubtle      *bool          `json:"isSubtle,omitempty"`     // Optional: makes text less prominent (nil->omitted)
		Size          string         `json:"size,omitempty"`         // e.g., "Small", "Default", "Medium", "Large", "ExtraLarge"
		Weight        string         `json:"weight,omitempty"`       // e.g., "Lighter", "Default", "Bolder"
		SelectAction  *MSTeamsAction `json:"selectAction,omitempty"` // Optional: action when the TextRun is clicked
		ID            string         `json:"id,omitempty"`
	}

	MSTeamsIcon struct {
		Type         string         `json:"type"`                   // Must be "Icon"
		Name         string         `json:"name"`                   // The icon name to display
		Color        string         `json:"color,omitempty"`        // text color, e.g., "Default", "Dark", "Light", "Accent", "Good", "Warning", "Attention"
		Size         string         `json:"size,omitempty"`         // e.g., "xxSmall", "xSmall", "Small", "Standard", "Medium", "Large", "xLarge", "xxLarge"
		Style        string         `json:"style,omitempty"`        // e.g., "Regular", "Filled"
		SelectAction *MSTeamsAction `json:"selectAction,omitempty"` // Optional: action when the IconRun is clicked
		ID           string         `json:"id,omitempty"`
	}

	// MSTeamsImage defines the Adaptive Card Image element.
	MSTeamsImage struct {
		Type         string         `json:"type"`                   // Must be "Image"
		URL          string         `json:"url"`                    // URL of the image
		Alt          string         `json:"altText"`                // Alt text for the image
		Size         string         `json:"size,omitempty"`         // e.g., "Auto", "Stretch", "Small", "Medium", "Large"
		Style        string         `json:"style,omitempty"`        // e.g., "Default", "Person", "RoundedCorners"
		SelectAction *MSTeamsAction `json:"selectAction,omitempty"` // Optional: action when the columnset is clicked
	}

	// MSTeamsColumn defines a column in an Adaptive Card ColumnSet.
	MSTeamsColumn struct {
		Type          string         `json:"type"`                               // Must be "Column"
		Items         []any          `json:"items,omitempty"`                    // Array of card elements (TextBlock, Image, etc.)
		Width         any            `json:"width,omitempty"`                    // "auto", "stretch", or number/string for fixed width
		Style         string         `json:"style,omitempty"`                    // e.g., "default", "emphasis"
		VerticalAlign string         `json:"verticalContentAlignment,omitempty"` // "Top", "Center", "Bottom"
		Bleed         *bool          `json:"bleed,omitempty"`                    // Optional: allow content to bleed outside padding (nil->omitted)
		Separator     *bool          `json:"separator,omitempty"`                // Optional: draw a separating line (nil->omitted)
		Spacing       string         `json:"spacing,omitempty"`                  // e.g., "none", "small", "default", "medium", "large", "extraLarge", "padding"
		SelectAction  *MSTeamsAction `json:"selectAction,omitempty"`             // Optional: action when column is clicked
		IsVisible     *bool          `json:"isVisible,omitempty"`                // Optional: is visible or not, default is true (nil->omitted)
	}

	// MSTeamsColumnSet defines a row of columns in an Adaptive Card.
	MSTeamsColumnSet struct {
		Type         string          `json:"type"`                   // Must be "ColumnSet"
		Columns      []MSTeamsColumn `json:"columns"`                // Array of columns
		Spacing      string          `json:"spacing,omitempty"`      // e.g., "none", "small", "default", "medium", "large", "extraLarge", "padding"
		Separator    *bool           `json:"separator,omitempty"`    // Optional: draw a separating line (nil->omitted)
		Bleed        *bool           `json:"bleed,omitempty"`        // Optional: allow content to bleed outside padding (nil->omitted)
		SelectAction *MSTeamsAction  `json:"selectAction,omitempty"` // Optional: action when columnset is clicked
		IsVisible    *bool           `json:"isVisible,omitempty"`    // Optional: is visible or not, default is true (nil->omitted)
	}

	// MSTeamsFact represents a fact in an Adaptive Card FactSet.
	MSTeamsFact struct {
		Title string `json:"title"`
		Value string `json:"value"`
	}

	// MSTeamsFactSet represents the Adaptive Card FactSet element.
	MSTeamsFactSet struct {
		Type      string        `json:"type"`                // Must be "FactSet"
		Facts     []MSTeamsFact `json:"facts"`               // List of facts
		Separator *bool         `json:"separator,omitempty"` // Optional: draw a separating line (nil->omitted)
		Spacing   string        `json:"spacing,omitempty"`   // e.g., "None", "ExtraSmall", "Small", "Default", "Medium", "Large", "ExtraLarge", "Padding"
	}

	// MSTeamsContainer corresponds to an Adaptive Card container.
	MSTeamsContainer struct {
		Type           string `json:"type"`                               // Must be "Container"
		Items          []any  `json:"items,omitempty"`                    // Array of card elements (TextBlock, Image, FactSet, etc.)
		ShowBorder     *bool  `json:"showBorder,omitempty"`               // Optional: draw a border around the container (nil->omitted)
		RoundedCorners *bool  `json:"roundedCorners,omitempty"`           // Optional: round the corners of the container (nil->omitted)
		Spacing        string `json:"spacing,omitempty"`                  // e.g., "None", "ExtraSmall", "Small", "Default", "Medium", "Large", "ExtraLarge", "Padding"
		Bleed          *bool  `json:"bleed,omitempty"`                    // Optional: allow content to bleed outside padding (nil->omitted)
		Style          string `json:"style,omitempty"`                    // color theme of the container, e.g. "default", "emphasis", "accent", "good", "attention", "warning"
		ID             string `json:"id,omitempty"`                       // ID
		IsVisible      *bool  `json:"isVisible,omitempty"`                // Optional: is visible or not, default is true (nil->omitted)
		VerticalAlign  string `json:"verticalContentAlignment,omitempty"` // "Top", "Center", "Bottom"
	}

	MSTeamsOptions struct {
		Width string `json:"width"` // use "Full" to make card full width
	}

	// MSTeamsPayload represents the Adaptive Card payload.
	// Adaptive Cards use "body" for visual elements and "actions" for interactive buttons.
	MSTeamsPayload struct {
		Type    string             `json:"type"`              // Must be "AdaptiveCard"
		Schema  string             `json:"$schema"`           // e.g., "http://adaptivecards.io/schemas/adaptive-card.json"
		Version string             `json:"version"`           // e.g., "1.5"
		MsTeams MSTeamsOptions     `json:"msteams"`           // Optional: settings for Microsoft Teams
		Body    []MSTeamsContainer `json:"body"`              // Array of containers (sections)
		Actions []MSTeamsAction    `json:"actions,omitempty"` // Optional array of actions
		Style   string             `json:"style,omitempty"`   // color theme of the card, e.g. "default", "emphasis", "accent", "good", "attention", "warning"
	}
)

var (
	defaultStyle     = "default"     // default colour
	emphasisStyle    = "emphasis"    // darker
	accentStyle      = "accent"      // blue-purple
	goodStyle        = "good"        // green
	attentionStyle   = "attention"   // red
	warningStyle     = "warning"     // yellow
	informativeStyle = "informative" // gray with white text
	subtleStyle      = "subtle"      // gray with dark text
)

func capitalise(s string) string {
	caser := cases.Title(language.English)
	return caser.String(strings.ToLower(s))
}

func markdownLinkFormatter(url, text string) string {
	if url == "" {
		return text
	}
	return fmt.Sprintf(`[%s](%s)`, text, url)
}

var msTeamsPayloadFormatter = webhookPayloadFormatter{
	nameFormatter: noneNameFormatter,
	linkFormatter: noneLinkFormatter,
	withSender:    false,
	withRepoName:  false,
}

func makeBadgeRow(badgeText, badgeIcon, badgeStyle string, refInfo ...string) MSTeamsContainer {
	extraRefInfoText := []any{}
	for _, info := range refInfo {
		extraRefInfoText = append(extraRefInfoText, MSTeamsTextBlock{
			Type:     "TextBlock",
			Text:     info,
			Size:     "Small",
			IsSubtle: new(true),
			Spacing:  "None",
			Wrap:     new(true),
		})
	}

	return MSTeamsContainer{
		Type:          "Container",
		VerticalAlign: "Center",
		Items: []any{
			MSTeamsColumnSet{
				Type: "ColumnSet",
				Columns: []MSTeamsColumn{
					{
						Type:  "Column",
						Width: "auto",
						Items: []any{
							MSTeamsBadge{
								Type:  "Badge",
								Size:  "Large",
								Icon:  badgeIcon,
								Text:  badgeText,
								Style: badgeStyle,
							},
						},
					},
					{
						Type:  "Column",
						Width: "stretch",
						Items: extraRefInfoText,
					},
				},
			},
		},
	}
}

func pullRequestBranchInfo(p *api.PullRequestPayload) string {
	if p == nil || p.PullRequest == nil {
		return ""
	}

	headRef, baseRef := "", ""
	if p.PullRequest.Head != nil {
		headRef = p.PullRequest.Head.Ref
	}
	if p.PullRequest.Base != nil {
		baseRef = p.PullRequest.Base.Ref
	}

	switch {
	case headRef == "" && baseRef == "":
		return ""
	case headRef == "":
		return baseRef
	case baseRef == "":
		return headRef
	default:
		return fmt.Sprintf("%s → %s", headRef, baseRef)
	}
}

// Create implements PayloadConvertor Create method
func (m msteamsConvertor) Create(p *api.CreatePayload) (MSTeamsPayload, error) {
	// created tag/branch
	refName := git.RefName(p.Ref).ShortName()
	actionTitle := fmt.Sprintf("%s created: %s", capitalise(p.RefType), refName)

	return createMSTeamsPayload(
		p.Repo,
		p.Sender,
		actionTitle,
		nil,
		p.Repo.HTMLURL+"/src/"+util.PathEscapeSegments(refName),
		goodStyle,
	), nil
}

// Delete implements PayloadConvertor Delete method
func (m msteamsConvertor) Delete(p *api.DeletePayload) (MSTeamsPayload, error) {
	// deleted tag/branch
	refName := git.RefName(p.Ref).ShortName()
	actionTitle := fmt.Sprintf("%s deleted: %s", capitalise(p.RefType), refName)

	return createMSTeamsPayload(
		p.Repo,
		p.Sender,
		actionTitle,
		nil,
		p.Repo.HTMLURL,
		attentionStyle,
	), nil
}

// Fork implements PayloadConvertor Fork method
func (m msteamsConvertor) Fork(p *api.ForkPayload) (MSTeamsPayload, error) {
	actionTitle := fmt.Sprintf("%s is forked to %s", markdownLinkFormatter(p.Forkee.HTMLURL, p.Forkee.FullName), p.Repo.FullName)

	return createMSTeamsPayload(
		p.Repo,
		p.Sender,
		actionTitle,
		nil,
		p.Repo.HTMLURL,
		accentStyle,
	), nil
}

// Push implements PayloadConvertor Push method
func (m msteamsConvertor) Push(p *api.PushPayload) (MSTeamsPayload, error) {
	var (
		branchName  = git.RefName(p.Ref).ShortName()
		actionTitle string
	)

	var diffLink string
	if p.TotalCommits == 1 {
		actionTitle = fmt.Sprintf("[%s] 1 new commit", branchName)
		diffLink = p.Commits[0].URL
	} else {
		actionTitle = fmt.Sprintf("[%s] %d new commits", branchName, p.TotalCommits)
		diffLink = p.CompareURL
	}
	if diffLink == "" {
		diffLink = p.Repo.HTMLURL + "/src/" + util.PathEscapeSegments(branchName)
	}

	var shownCommits []any
	var hiddenCommits []any
	const limit = 5

	for i, commit := range p.Commits {
		currentCommitTextBlock := MSTeamsColumnSet{
			Type: "ColumnSet",
			Columns: []MSTeamsColumn{
				{
					Type:  "Column",
					Width: "auto",
					Items: []any{
						MSTeamsTextBlock{
							Type:     "TextBlock",
							Text:     markdownLinkFormatter(commit.URL, commit.ID[:8]),
							Size:     "Small",
							FontType: "Monospace",
						},
					},
				},
				{
					Type:  "Column",
					Width: "stretch",
					Items: []any{
						MSTeamsTextBlock{
							Type:     "TextBlock",
							Text:     strings.TrimRight(commit.Message, "\r\n"),
							Size:     "Small",
							MaxLines: 3,
							Wrap:     new(true),
						},
					},
				},
			},
		}
		if i < limit {
			shownCommits = append(shownCommits, currentCommitTextBlock)
		} else {
			hiddenCommits = append(hiddenCommits, currentCommitTextBlock)
		}
	}

	bodyContainer := MSTeamsContainer{
		Type:       "Container",
		Style:      emphasisStyle,
		ShowBorder: new(true),
		Items:      shownCommits,
	}

	if len(hiddenCommits) > 0 {
		var extraCommitText string
		remaining := len(hiddenCommits)
		if remaining == 1 {
			extraCommitText = "*and 1 more commit*"
		} else {
			extraCommitText = fmt.Sprintf("*and %d more commits*", remaining)
		}

		// Expand control
		showMore := MSTeamsContainer{
			Type: "Container",
			ID:   "showMore",
			Items: []any{
				MSTeamsColumnSet{
					Type: "ColumnSet",
					SelectAction: &MSTeamsAction{
						Type:           "Action.ToggleVisibility",
						TargetElements: []string{"showMore", "showLess", "hiddenCommits"},
					},
					Columns: []MSTeamsColumn{
						{
							Type: "Column",
							Items: []any{
								MSTeamsTextBlock{
									Type: "TextBlock",
									Text: extraCommitText,
									Size: "Small",
								},
							},
							Width: "auto",
						},
						{
							Type: "Column",
							Items: []any{
								MSTeamsIcon{
									Type: "Icon",
									Name: "ChevronDown",
									Size: "xxSmall",
								},
							},
							Width: "auto",
						},
					},
				},
			},
		}

		// Hidden commit messages
		hiddenBlock := MSTeamsContainer{
			Type:      "Container",
			ID:        "hiddenCommits",
			Items:     hiddenCommits,
			IsVisible: new(false),
		}

		// Collapse control
		showLess := MSTeamsContainer{
			Type:      "Container",
			ID:        "showLess",
			IsVisible: new(false),
			Items: []any{
				MSTeamsColumnSet{
					Type: "ColumnSet",
					SelectAction: &MSTeamsAction{
						Type:           "Action.ToggleVisibility",
						TargetElements: []string{"showMore", "showLess", "hiddenCommits"},
					},
					Columns: []MSTeamsColumn{
						{
							Type: "Column",
							Items: []any{
								MSTeamsTextBlock{
									Type: "TextBlock",
									Text: "*Collapse*",
									Size: "Small",
								},
							},
							Width: "auto",
						},
						{
							Type: "Column",
							Items: []any{
								MSTeamsIcon{
									Type: "Icon",
									Name: "ChevronUp",
									Size: "xxSmall",
								},
							},
							Width: "auto",
						},
					},
				},
			},
		}

		bodyContainer.Items = append(bodyContainer.Items, showMore, hiddenBlock, showLess)
	}

	return createMSTeamsPayload(
		p.Repo,
		p.Sender,
		actionTitle,
		[]MSTeamsContainer{bodyContainer},
		diffLink,
		defaultStyle,
	), nil
}

// Issue implements PayloadConvertor Issue method
func (m msteamsConvertor) Issue(p *api.IssuePayload) (MSTeamsPayload, error) {
	actionTitle, _, attachmentText, _ := msTeamsPayloadFormatter.getIssuesPayloadInfo(p)

	badgeStyle := attentionStyle
	if p.Action == api.HookIssueReOpened {
		badgeStyle = warningStyle
	} else if p.Issue.State == api.StateOpen {
		badgeStyle = goodStyle
	}

	bodySections := []MSTeamsContainer{
		makeBadgeRow(capitalise(string(p.Issue.State)), "Target", badgeStyle, fmt.Sprintf("%s#%d", p.Repository.FullName, p.Issue.Index)),
	}

	if attachmentText != "" {
		bodySections = append(bodySections, MSTeamsContainer{
			Type:       "Container",
			Style:      emphasisStyle,
			ShowBorder: new(true),
			Items: []any{
				MSTeamsTextBlock{
					Type: "TextBlock",
					Text: attachmentText,
					Wrap: new(true),
				},
			},
		})
	}

	factSet := MSTeamsFactSet{
		Type:      "FactSet",
		Facts:     []MSTeamsFact{},
		Separator: new(true),
	}

	if p.Issue.Ref != "" && (p.Action == api.HookIssueOpened || p.Action == api.HookIssueReOpened) {
		factSet.Facts = append(factSet.Facts, MSTeamsFact{
			Title: "Ref",
			Value: p.Issue.Ref,
		})
	}

	if len(p.Issue.Labels) > 0 &&
		(p.Action == api.HookIssueLabelUpdated || p.Action == api.HookIssueLabelCleared || p.Action == api.HookIssueOpened || p.Action == api.HookIssueReOpened) {
		list := make([]string, len(p.Issue.Labels))
		for i, label := range p.Issue.Labels {
			list[i] = label.Name
		}
		if len(list) > 0 {
			factSet.Facts = append(factSet.Facts, MSTeamsFact{
				Title: "Labels",
				Value: strings.Join(list, ", "),
			})
		}
	}

	if len(p.Issue.Assignees) > 0 &&
		(p.Action == api.HookIssueAssigned || p.Action == api.HookIssueUnassigned || p.Action == api.HookIssueOpened || p.Action == api.HookIssueReOpened) {
		list := make([]string, len(p.Issue.Assignees))
		for i, user := range p.Issue.Assignees {
			list[i] = "@" + user.UserName
		}
		if len(list) > 0 {
			factSet.Facts = append(factSet.Facts, MSTeamsFact{
				Title: "Assignee",
				Value: strings.Join(list, ", "),
			})
		}
	}

	if p.Issue.Milestone != nil &&
		(p.Action == api.HookIssueMilestoned || p.Action == api.HookIssueOpened || p.Action == api.HookIssueReOpened) {
		factSet.Facts = append(factSet.Facts, MSTeamsFact{
			Title: "Milestone",
			Value: p.Issue.Milestone.Title,
		})
	}

	if len(factSet.Facts) > 0 {
		bodySections = append(bodySections, MSTeamsContainer{
			Type: "Container",
			Items: []any{
				factSet,
			},
		})
	}

	return createMSTeamsPayload(
		p.Repository,
		p.Sender,
		actionTitle,
		bodySections,
		p.Issue.HTMLURL,
		defaultStyle,
	), nil
}

// IssueComment implements PayloadConvertor IssueComment method
func (m msteamsConvertor) IssueComment(p *api.IssueCommentPayload) (MSTeamsPayload, error) {
	actionTitle, _, _ := msTeamsPayloadFormatter.getIssueCommentPayloadInfo(p)

	var style string
	switch p.Action {
	case api.HookIssueCommentCreated:
		style = goodStyle
	case api.HookIssueCommentDeleted:
		style = attentionStyle
	default:
		style = emphasisStyle
	}

	bodySections := []MSTeamsContainer{
		{
			Type:       "Container",
			Style:      style,
			ShowBorder: new(true),
			Items: []any{
				MSTeamsTextBlock{
					Type:   "TextBlock",
					Text:   "Comment",
					Size:   "Small",
					Weight: "Bolder",
				},
				MSTeamsTextBlock{
					Type: "TextBlock",
					Text: p.Comment.Body,
					Wrap: new(true),
				},
			},
		},
	}

	return createMSTeamsPayload(
		p.Repository,
		p.Sender,
		actionTitle,
		bodySections,
		p.Comment.HTMLURL,
		defaultStyle,
	), nil
}

// PullRequest implements PayloadConvertor PullRequest method
func (m msteamsConvertor) PullRequest(p *api.PullRequestPayload) (MSTeamsPayload, error) {
	actionTitle, _, attachmentText, _ := msTeamsPayloadFormatter.getPullRequestPayloadInfo(p)

	badgeStyle := attentionStyle
	badgeState := "Closed"
	badgeIcon := "BranchRequest"
	if p.Action == api.HookIssueReOpened {
		badgeStyle = warningStyle
		badgeState = "Open"
	} else if p.PullRequest.State == api.StateOpen {
		badgeStyle = goodStyle
		badgeState = "Open"
	} else if p.PullRequest.HasMerged {
		badgeStyle = accentStyle
		badgeState = "Merged"
		badgeIcon = "Branch"
	}

	bodySections := []MSTeamsContainer{
		makeBadgeRow(
			badgeState,
			badgeIcon,
			badgeStyle,
			pullRequestBranchInfo(p),
			fmt.Sprintf("%s#%d", p.Repository.FullName, p.PullRequest.Index),
		),
	}

	if attachmentText != "" {
		bodySections = append(bodySections, MSTeamsContainer{
			Type:       "Container",
			Style:      emphasisStyle,
			ShowBorder: new(true),
			Items: []any{
				MSTeamsTextBlock{
					Type: "TextBlock",
					Text: attachmentText,
					Wrap: new(true),
				},
			},
		})
	}

	factSet := MSTeamsFactSet{
		Type:      "FactSet",
		Facts:     []MSTeamsFact{},
		Separator: new(true),
	}

	if len(p.PullRequest.Labels) > 0 &&
		(p.Action == api.HookIssueLabelUpdated || p.Action == api.HookIssueLabelCleared || p.Action == api.HookIssueOpened || p.Action == api.HookIssueReOpened) {
		list := make([]string, len(p.PullRequest.Labels))
		for i, label := range p.PullRequest.Labels {
			list[i] = label.Name
		}
		if len(list) > 0 {
			factSet.Facts = append(factSet.Facts, MSTeamsFact{
				Title: "Labels",
				Value: strings.Join(list, ", "),
			})
		}
	}

	if len(p.PullRequest.Assignees) > 0 &&
		(p.Action == api.HookIssueAssigned || p.Action == api.HookIssueUnassigned || p.Action == api.HookIssueOpened || p.Action == api.HookIssueReOpened) {
		list := make([]string, len(p.PullRequest.Assignees))
		for i, user := range p.PullRequest.Assignees {
			list[i] = "@" + user.UserName
		}
		if len(list) > 0 {
			factSet.Facts = append(factSet.Facts, MSTeamsFact{
				Title: "Assignee",
				Value: strings.Join(list, ", "),
			})
		}
	}

	if p.PullRequest.Milestone != nil &&
		(p.Action == api.HookIssueMilestoned || p.Action == api.HookIssueOpened || p.Action == api.HookIssueReOpened) {
		factSet.Facts = append(factSet.Facts, MSTeamsFact{
			Title: "Milestone",
			Value: p.PullRequest.Milestone.Title,
		})
	}

	if (p.PullRequest.RequestedReviewers != nil || p.PullRequest.RequestedReviewersTeams != nil) &&
		(p.Action == api.HookIssueReviewRequested || p.Action == api.HookIssueReviewRequestRemoved || p.Action == api.HookIssueOpened || p.Action == api.HookIssueReOpened) {
		list := make(
			[]string, 0,
			len(p.PullRequest.RequestedReviewers)+
				len(p.PullRequest.RequestedReviewersTeams),
		)
		for _, u := range p.PullRequest.RequestedReviewers {
			list = append(list, u.UserName)
		}
		for _, t := range p.PullRequest.RequestedReviewersTeams {
			list = append(list, t.Name)
		}
		if len(list) > 0 {
			factSet.Facts = append(factSet.Facts, MSTeamsFact{
				Title: "Reviewers",
				Value: strings.Join(list, ", "),
			})
		}
	}

	bodySections = append(bodySections, MSTeamsContainer{
		Type:  "Container",
		Items: []any{factSet},
	})

	return createMSTeamsPayload(
		p.Repository,
		p.Sender,
		actionTitle,
		bodySections,
		p.PullRequest.HTMLURL,
		defaultStyle,
	), nil
}

// Review implements PayloadConvertor Review method
func (m msteamsConvertor) Review(p *api.PullRequestPayload, event webhook_module.HookEventType) (MSTeamsPayload, error) {
	badgeStyle := attentionStyle
	badgeState := "Closed"
	badgeIcon := "BranchRequest"
	if p.Action == api.HookIssueReOpened {
		badgeStyle = warningStyle
		badgeState = "Re-opened"
	} else if p.PullRequest.State == api.StateOpen {
		badgeStyle = goodStyle
		badgeState = "Open"
	} else if p.PullRequest.HasMerged {
		badgeStyle = accentStyle
		badgeState = "Merged"
		badgeIcon = "Branch"
	}

	bodySections := []MSTeamsContainer{
		makeBadgeRow(
			badgeState,
			badgeIcon,
			badgeStyle,
			pullRequestBranchInfo(p),
			fmt.Sprintf("%s#%d", p.Repository.FullName, p.PullRequest.Index),
		),
	}

	var actionTitle, bodyStyle string
	if p.Action == api.HookIssueReviewed {
		action, err := parseHookPullRequestEventType(event)
		if err != nil {
			return MSTeamsPayload{}, err
		}

		actionTitle = fmt.Sprintf("Pull request review %s: #%d %s", action, p.Index, p.PullRequest.Title)

		switch event {
		case webhook_module.HookEventPullRequestReviewApproved:
			bodyStyle = goodStyle
		case webhook_module.HookEventPullRequestReviewRejected:
			bodyStyle = attentionStyle
		}

		bodySections = append(bodySections, MSTeamsContainer{
			Type:       "Container",
			Style:      bodyStyle,
			ShowBorder: new(true),
			Items: []any{
				MSTeamsTextBlock{
					Type: "TextBlock",
					Text: p.Review.Content,
					Wrap: new(true),
				},
			},
		})
	}

	return createMSTeamsPayload(
		p.Repository,
		p.Sender,
		actionTitle,
		bodySections,
		p.PullRequest.HTMLURL,
		defaultStyle,
	), nil
}

// Repository implements PayloadConvertor Repository method
func (m msteamsConvertor) Repository(p *api.RepositoryPayload) (MSTeamsPayload, error) {
	var actionTitle, url string
	style := emphasisStyle
	switch p.Action {
	case api.HookRepoCreated:
		actionTitle = fmt.Sprintf("Repository created: %s", p.Repository.FullName)
		url = p.Repository.HTMLURL
		style = goodStyle
	case api.HookRepoDeleted:
		actionTitle = fmt.Sprintf("Repository deleted: %s", p.Repository.FullName)
		style = attentionStyle
	}

	return createMSTeamsPayload(
		p.Repository,
		p.Sender,
		actionTitle,
		nil,
		url,
		style,
	), nil
}

// Wiki implements PayloadConvertor Wiki method
func (m msteamsConvertor) Wiki(p *api.WikiPayload) (MSTeamsPayload, error) {
	actionTitle, _, _ := msTeamsPayloadFormatter.getWikiPayloadInfo(p, false)

	var style string

	switch p.Action {
	case api.HookWikiCreated:
		style = goodStyle
	case api.HookWikiEdited:
		style = warningStyle
	case api.HookWikiDeleted:
		style = attentionStyle
	}

	var bodySections []MSTeamsContainer
	if p.Action != api.HookWikiDeleted {
		bodySections = append(bodySections, MSTeamsContainer{
			Type:       "Container",
			Style:      defaultStyle,
			ShowBorder: new(true),
			Items: []any{
				MSTeamsFactSet{
					Type: "FactSet",
					Facts: []MSTeamsFact{
						{
							Title: "Comment",
							Value: p.Comment,
						},
					},
				},
			},
		})
	}

	return createMSTeamsPayload(
		p.Repository,
		p.Sender,
		actionTitle,
		bodySections,
		p.Repository.HTMLURL+"/wiki/"+url.PathEscape(p.Page),
		style,
	), nil
}

// Release implements PayloadConvertor Release method
func (m msteamsConvertor) Release(p *api.ReleasePayload) (MSTeamsPayload, error) {
	actionTitle, color := msTeamsPayloadFormatter.getReleasePayloadInfo(p)
	var style string

	switch color {
	case greenColor:
		style = goodStyle
	case yellowColor:
		style = warningStyle
	case redColor:
		style = attentionStyle
	}

	return createMSTeamsPayload(
		p.Repository,
		p.Sender,
		actionTitle,
		nil,
		p.Release.HTMLURL,
		style,
	), nil
}

func (m msteamsConvertor) Package(p *api.PackagePayload) (MSTeamsPayload, error) {
	actionTitle, color := msTeamsPayloadFormatter.getPackagePayloadInfo(p)
	var style string

	switch color {
	case greenColor:
		style = goodStyle
	case yellowColor:
		style = warningStyle
	case redColor:
		style = attentionStyle
	}

	return createMSTeamsPayload(
		p.Repository,
		p.Sender,
		actionTitle,
		nil,
		p.Package.HTMLURL,
		style,
	), nil
}

func (m msteamsConvertor) Action(p *api.ActionPayload) (MSTeamsPayload, error) {
	var actionTitle string

	var badgeStyle, badgeState string

	switch p.Action {
	case api.HookActionFailure:
		actionTitle = fmt.Sprintf("Action run #%d failed: %s", p.Run.ID, p.Run.Title)
		badgeStyle = attentionStyle
		badgeState = "Failed"
	case api.HookActionRecover:
		actionTitle = fmt.Sprintf("Action run #%d recovered: %s", p.Run.ID, p.Run.Title)
		badgeStyle = goodStyle
		badgeState = "Success"
	case api.HookActionSuccess:
		actionTitle = fmt.Sprintf("Action run #%d succeeded: %s", p.Run.ID, p.Run.Title)
		badgeStyle = goodStyle
		badgeState = "Success"
	}

	bodySections := []MSTeamsContainer{
		makeBadgeRow(
			badgeState,
			"PlayCircle",
			badgeStyle,
			fmt.Sprintf("On %s", p.Run.PrettyRef),
			fmt.Sprintf("%s Run #%d", p.Run.Repo.FullName, p.Run.ID),
		),
	}

	// TODO: is TriggerUser correct here?
	// if you'd like to test these proprietary services, see the discussion on: https://codeberg.org/forgejo/forgejo/pulls/7508
	return createMSTeamsPayload(
		p.Run.Repo,
		p.Run.TriggerUser,
		actionTitle,
		bodySections,
		p.Run.HTMLURL,
		defaultStyle,
	), nil
}

func (m msteamsConvertor) WorkflowRun(p *api.WorkflowRunPayload) (MSTeamsPayload, error) {
	var title string
	var badgeStyle string

	switch p.Run.Status {
	case actions_model.StatusBlocked.String():
		title = fmt.Sprintf("Workflow run %q is blocked, triggered", p.Run.Title)
		badgeStyle = warningStyle
	case actions_model.StatusCancelled.String():
		title = fmt.Sprintf("Workflow run %q was cancelled, triggered", p.Run.Title)
		badgeStyle = informativeStyle
	case actions_model.StatusFailure.String():
		title = fmt.Sprintf("Workflow run %q has failed, triggered", p.Run.Title)
		badgeStyle = attentionStyle
	case actions_model.StatusRunning.String():
		title = fmt.Sprintf("Workflow run %q has started running, triggered", p.Run.Title)
		badgeStyle = defaultStyle
	case actions_model.StatusSkipped.String():
		title = fmt.Sprintf("Workflow run %q was skipped, triggered", p.Run.Title)
		badgeStyle = subtleStyle
	case actions_model.StatusSuccess.String():
		title = fmt.Sprintf("Workflow run %q has completed successfully, triggered", p.Run.Title)
		badgeStyle = goodStyle
	case actions_model.StatusWaiting.String():
		title = fmt.Sprintf("Workflow run %q is waiting, triggered", p.Run.Title)
		badgeStyle = accentStyle
	}

	body := []MSTeamsContainer{
		makeBadgeRow(
			p.Run.Status,
			"PlayCircle",
			badgeStyle,
			fmt.Sprintf("Repository: %s", p.Run.Repo.FullName),
			fmt.Sprintf("Run: %s", p.Run.Title),
		),
	}

	return createMSTeamsPayload(p.Run.Repo, p.Run.TriggerUser, title, body, p.Run.HTMLURL, defaultStyle), nil
}

func (m msteamsConvertor) WorkflowJob(p *api.WorkflowJobPayload) (MSTeamsPayload, error) {
	var title string
	var badgeStyle string

	switch p.Job.Status {
	case actions_model.StatusBlocked.String():
		title = fmt.Sprintf("Workflow job %q is blocked, triggered", p.Job.Name)
		badgeStyle = warningStyle
	case actions_model.StatusCancelled.String():
		title = fmt.Sprintf("Workflow job %q was cancelled, triggered", p.Job.Name)
		badgeStyle = informativeStyle
	case actions_model.StatusFailure.String():
		title = fmt.Sprintf("Workflow job %q has failed, triggered", p.Job.Name)
		badgeStyle = attentionStyle
	case actions_model.StatusRunning.String():
		title = fmt.Sprintf("Workflow job %q has started running, triggered", p.Job.Name)
		badgeStyle = defaultStyle
	case actions_model.StatusSkipped.String():
		title = fmt.Sprintf("Workflow job %q was skipped, triggered", p.Job.Name)
		badgeStyle = subtleStyle
	case actions_model.StatusSuccess.String():
		title = fmt.Sprintf("Workflow job %q has completed successfully, triggered", p.Job.Name)
		badgeStyle = goodStyle
	case actions_model.StatusWaiting.String():
		title = fmt.Sprintf("Workflow job %q is waiting, triggered", p.Job.Name)
		badgeStyle = accentStyle
	}

	body := []MSTeamsContainer{
		makeBadgeRow(
			p.Job.Status,
			"PlayCircle",
			badgeStyle,
			fmt.Sprintf("Repository: %s", p.Run.Repo.FullName),
			fmt.Sprintf("Run: %s", p.Run.Title),
			fmt.Sprintf("Job: %s", p.Job.Name),
		),
	}

	return createMSTeamsPayload(p.Run.Repo, p.Run.TriggerUser, title, body, p.Job.HTMLURL, defaultStyle), nil
}

func createMSTeamsPayload(r *api.Repository, s *api.User, actionTitle string, bodySections []MSTeamsContainer, actionTarget, style string) MSTeamsPayload {
	// Update header adding the repository name and link
	var updatedRepo string
	if r != nil {
		updatedRepo = fmt.Sprintf(" | [%s](%s)", r.FullName, r.HTMLURL)
	}
	if style == "" {
		style = defaultStyle
	}

	repoHeaderSection := MSTeamsContainer{
		Type: "Container",
		Items: []any{
			MSTeamsTextBlock{
				Type:     "TextBlock",
				Text:     "💬 Update" + updatedRepo,
				Weight:   "Bolder",
				Size:     "Small",
				IsSubtle: new(true),
				Wrap:     new(true),
			},
		},
	}

	if s != nil {
		// get display name
		username := "@" + s.UserName
		if s.FullName != "" {
			username = s.FullName + " (@" + s.UserName + ")"
		}

		actionTitle = fmt.Sprintf("%s by %s", actionTitle, markdownLinkFormatter(s.HTMLURL, username))
	}

	// Sender info section
	actionTitleSection := MSTeamsContainer{
		Type:  "Container",
		Style: style,
		Items: []any{
			MSTeamsTextBlock{
				Type:  "TextBlock",
				Style: "heading",
				Text:  actionTitle,
				Wrap:  new(true),
			},
		},
	}

	// Combine sections in order
	body := []MSTeamsContainer{repoHeaderSection, actionTitleSection}
	body = append(body, bodySections...)

	// Create action button for navigation
	actionButton := MSTeamsAction{
		Type:  "Action.OpenUrl",
		Title: "View in Forgejo",
		URL:   actionTarget,
	}

	return MSTeamsPayload{
		Type:    "AdaptiveCard",
		Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
		Version: "1.5",
		MsTeams: MSTeamsOptions{
			Width: "Full",
		},
		Body:    body,
		Actions: []MSTeamsAction{actionButton},
		Style:   defaultStyle,
	}
}

type msteamsConvertor struct{}

var _ shared.PayloadConvertor[MSTeamsPayload] = msteamsConvertor{}

func (msteamsHandler) NewRequest(ctx context.Context, w *webhook_model.Webhook, t *webhook_model.HookTask) (*http.Request, []byte, error) {
	return shared.NewJSONRequest(msteamsConvertor{}, w, t, true)
}
