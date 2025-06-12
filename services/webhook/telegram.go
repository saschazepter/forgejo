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

	webhook_model "forgejo.org/models/webhook"
	"forgejo.org/modules/git"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/markup"
	api "forgejo.org/modules/structs"
	webhook_module "forgejo.org/modules/webhook"
	"forgejo.org/services/forms"
	"forgejo.org/services/webhook/shared"
)

type telegramHandler struct{}

func (telegramHandler) Type() webhook_module.HookType { return webhook_module.TELEGRAM }
func (telegramHandler) Icon(size int) template.HTML   { return shared.ImgIcon("telegram.png", size) }

func (telegramHandler) UnmarshalForm(bind func(any)) forms.WebhookForm {
	var form struct {
		forms.WebhookCoreForm
		BotToken string `binding:"Required"`
		ChatID   string `binding:"Required"`
		ThreadID string
	}
	bind(&form)

	return forms.WebhookForm{
		WebhookCoreForm: form.WebhookCoreForm,
		URL:             fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?chat_id=%s&message_thread_id=%s", url.PathEscape(form.BotToken), url.QueryEscape(form.ChatID), url.QueryEscape(form.ThreadID)),
		ContentType:     webhook_model.ContentTypeJSON,
		Secret:          "",
		HTTPMethod:      http.MethodPost,
		Metadata: &TelegramMeta{
			BotToken: form.BotToken,
			ChatID:   form.ChatID,
			ThreadID: form.ThreadID,
		},
	}
}

type (
	// TelegramPayload represents
	TelegramPayload struct {
		Message           string `json:"text"`
		ParseMode         string `json:"parse_mode"`
		DisableWebPreview bool   `json:"disable_web_page_preview"`
	}

	// TelegramMeta contains the telegram metadata
	TelegramMeta struct {
		BotToken string `json:"bot_token"`
		ChatID   string `json:"chat_id"`
		ThreadID string `json:"thread_id"`
	}
)

// Metadata returns telegram metadata
func (telegramHandler) Metadata(w *webhook_model.Webhook) any {
	s := &TelegramMeta{}
	if err := json.Unmarshal([]byte(w.Meta), s); err != nil {
		log.Error("telegramHandler.Metadata(%d): %v", w.ID, err)
	}
	return s
}

// Create implements PayloadConvertor Create method
func (t telegramConvertor) Create(p *api.CreatePayload) (TelegramPayload, error) {
	// created tag/branch
	refName := git.RefName(p.Ref).ShortName()
	title := fmt.Sprintf(`[%s] %s <a href="%s">%s</a> created`, p.Repo.FullName, p.RefType,
		p.Repo.HTMLURL+"/src/"+refName, refName)

	return createTelegramPayload(title), nil
}

// Delete implements PayloadConvertor Delete method
func (t telegramConvertor) Delete(p *api.DeletePayload) (TelegramPayload, error) {
	// created tag/branch
	refName := git.RefName(p.Ref).ShortName()
	title := fmt.Sprintf(`[%s] %s <a href="%s">%s</a> deleted`, p.Repo.FullName, p.RefType,
		p.Repo.HTMLURL+"/src/"+refName, refName)

	return createTelegramPayload(title), nil
}

// Fork implements PayloadConvertor Fork method
func (t telegramConvertor) Fork(p *api.ForkPayload) (TelegramPayload, error) {
	title := fmt.Sprintf(`%s is forked to <a href="%s">%s</a>`, p.Forkee.FullName, p.Repo.HTMLURL, p.Repo.FullName)

	return createTelegramPayload(title), nil
}

// Push implements PayloadConvertor Push method
func (t telegramConvertor) Push(p *api.PushPayload) (TelegramPayload, error) {
	var (
		branchName = git.RefName(p.Ref).ShortName()
		commitDesc string
	)
	if p.TotalCommits == 1 {
		commitDesc = "1 new commit"
	} else {
		commitDesc = fmt.Sprintf("%d new commits", p.TotalCommits)
	}

	title := fmt.Sprintf(`[%s:%s] %s`, p.Repo.FullName, branchName, commitDesc)

	var text string
	// for each commit, generate attachment text
	for i, commit := range p.Commits {
		var authorName string
		if commit.Author != nil {
			authorName = " - " + commit.Author.Name
		}
		text += fmt.Sprintf(`[<a href="%s">%s</a>] %s`, commit.URL, commit.ID[:7],
			strings.TrimRight(commit.Message, "\r\n")) + authorName
		// add linebreak to each commit but the last
		if i < len(p.Commits)-1 {
			text += "\n"
		}
	}

	return createTelegramPayload(title + "\n" + text), nil
}

// Issue implements PayloadConvertor Issue method
func (t telegramConvertor) Issue(p *api.IssuePayload) (TelegramPayload, error) {
	text, _, attachmentText, _ := getIssuesPayloadInfo(p, htmlLinkFormatter, true)

	return createTelegramPayload(text + "\n\n" + attachmentText), nil
}

// IssueComment implements PayloadConvertor IssueComment method
func (t telegramConvertor) IssueComment(p *api.IssueCommentPayload) (TelegramPayload, error) {
	text, _, _ := getIssueCommentPayloadInfo(p, htmlLinkFormatter, true)

	return createTelegramPayload(text + "\n" + p.Comment.Body), nil
}

// PullRequest implements PayloadConvertor PullRequest method
func (t telegramConvertor) PullRequest(p *api.PullRequestPayload) (TelegramPayload, error) {
	text, _, attachmentText, _ := getPullRequestPayloadInfo(p, htmlLinkFormatter, true)

	return createTelegramPayload(text + "\n" + attachmentText), nil
}

// Review implements PayloadConvertor Review method
func (t telegramConvertor) Review(p *api.PullRequestPayload, event webhook_module.HookEventType) (TelegramPayload, error) {
	var text, attachmentText string
	if p.Action == api.HookIssueReviewed {
		action, err := parseHookPullRequestEventType(event)
		if err != nil {
			return TelegramPayload{}, err
		}

		text = fmt.Sprintf("[%s] Pull request review %s: #%d %s", p.Repository.FullName, action, p.Index, p.PullRequest.Title)
		attachmentText = p.Review.Content
	}

	return createTelegramPayload(text + "\n" + attachmentText), nil
}

// Repository implements PayloadConvertor Repository method
func (t telegramConvertor) Repository(p *api.RepositoryPayload) (TelegramPayload, error) {
	var title string
	switch p.Action {
	case api.HookRepoCreated:
		title = fmt.Sprintf(`[<a href="%s">%s</a>] Repository created`, p.Repository.HTMLURL, p.Repository.FullName)
		return createTelegramPayload(title), nil
	case api.HookRepoDeleted:
		title = fmt.Sprintf("[%s] Repository deleted", p.Repository.FullName)
		return createTelegramPayload(title), nil
	}
	return TelegramPayload{}, nil
}

// Wiki implements PayloadConvertor Wiki method
func (t telegramConvertor) Wiki(p *api.WikiPayload) (TelegramPayload, error) {
	text, _, _ := getWikiPayloadInfo(p, htmlLinkFormatter, true)

	return createTelegramPayload(text), nil
}

// Release implements PayloadConvertor Release method
func (t telegramConvertor) Release(p *api.ReleasePayload) (TelegramPayload, error) {
	text, _ := getReleasePayloadInfo(p, htmlLinkFormatter, true)

	return createTelegramPayload(text), nil
}

func (t telegramConvertor) Package(p *api.PackagePayload) (TelegramPayload, error) {
	text, _ := getPackagePayloadInfo(p, htmlLinkFormatter, true)

	return createTelegramPayload(text), nil
}

func (telegramConvertor) Action(p *api.ActionPayload) (TelegramPayload, error) {
	text, _ := getActionPayloadInfo(p, htmlLinkFormatter)

	return createTelegramPayload(text), nil
}

func createTelegramPayload(message string) TelegramPayload {
	return TelegramPayload{
		Message:           markup.Sanitize(strings.TrimSpace(message)),
		ParseMode:         "HTML",
		DisableWebPreview: true,
	}
}

type telegramConvertor struct{}

var _ shared.PayloadConvertor[TelegramPayload] = telegramConvertor{}

func (telegramHandler) NewRequest(ctx context.Context, w *webhook_model.Webhook, t *webhook_model.HookTask) (*http.Request, []byte, error) {
	return shared.NewJSONRequest(telegramConvertor{}, w, t, true)
}
