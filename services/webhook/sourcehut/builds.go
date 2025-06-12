// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package sourcehut

import (
	"cmp"
	"context"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strings"

	webhook_model "forgejo.org/models/webhook"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	webhook_module "forgejo.org/modules/webhook"
	gitea_context "forgejo.org/services/context"
	"forgejo.org/services/forms"
	"forgejo.org/services/webhook/shared"

	"code.forgejo.org/go-chi/binding"
	"gopkg.in/yaml.v3"
)

type BuildsHandler struct{}

func (BuildsHandler) Type() webhook_module.HookType { return webhook_module.SOURCEHUT_BUILDS }
func (BuildsHandler) Metadata(w *webhook_model.Webhook) any {
	s := &BuildsMeta{}
	if err := json.Unmarshal([]byte(w.Meta), s); err != nil {
		log.Error("sourcehut.BuildsHandler.Metadata(%d): %v", w.ID, err)
	}
	return s
}

func (BuildsHandler) Icon(size int) template.HTML {
	return shared.ImgIcon("sourcehut.svg", size)
}

type buildsForm struct {
	forms.WebhookCoreForm
	PayloadURL   string `binding:"Required;ValidUrl"`
	ManifestPath string `binding:"Required"`
	Visibility   string `binding:"Required;In(PUBLIC,UNLISTED,PRIVATE)"`
	Secrets      bool
	AccessToken  string `binding:"Required"`
}

var _ binding.Validator = &buildsForm{}

// Validate implements binding.Validator.
func (f *buildsForm) Validate(req *http.Request, errs binding.Errors) binding.Errors {
	ctx := gitea_context.GetWebContext(req)
	if !fs.ValidPath(f.ManifestPath) {
		errs = append(errs, binding.Error{
			FieldNames:     []string{"ManifestPath"},
			Classification: "",
			Message:        ctx.Locale.TrString("repo.settings.add_webhook.invalid_path"),
		})
	}
	f.AuthorizationHeader = "Bearer " + strings.TrimSpace(f.AccessToken)
	return errs
}

func (BuildsHandler) UnmarshalForm(bind func(any)) forms.WebhookForm {
	var form buildsForm
	bind(&form)

	return forms.WebhookForm{
		WebhookCoreForm: form.WebhookCoreForm,
		URL:             form.PayloadURL,
		ContentType:     webhook_model.ContentTypeJSON,
		Secret:          "",
		HTTPMethod:      http.MethodPost,
		Metadata: &BuildsMeta{
			ManifestPath: form.ManifestPath,
			Visibility:   form.Visibility,
			Secrets:      form.Secrets,
		},
	}
}

type (
	graphqlPayload[V any] struct {
		Query     string `json:"query,omitempty"`
		Error     string `json:"error,omitempty"`
		Variables V      `json:"variables,omitempty"`
	}
	// buildsVariables according to https://man.sr.ht/builds.sr.ht/graphql.md
	buildsVariables struct {
		Manifest   string   `json:"manifest"`
		Tags       []string `json:"tags"`
		Note       string   `json:"note"`
		Secrets    bool     `json:"secrets"`
		Execute    bool     `json:"execute"`
		Visibility string   `json:"visibility"`
	}

	// BuildsMeta contains the metadata for the webhook
	BuildsMeta struct {
		ManifestPath string `json:"manifest_path"`
		Visibility   string `json:"visibility"`
		Secrets      bool   `json:"secrets"`
	}
)

type sourcehutConvertor struct {
	ctx  context.Context
	meta BuildsMeta
}

var _ shared.PayloadConvertor[graphqlPayload[buildsVariables]] = sourcehutConvertor{}

func (BuildsHandler) NewRequest(ctx context.Context, w *webhook_model.Webhook, t *webhook_model.HookTask) (*http.Request, []byte, error) {
	meta := BuildsMeta{}
	if err := json.Unmarshal([]byte(w.Meta), &meta); err != nil {
		return nil, nil, fmt.Errorf("newSourcehutRequest meta json: %w", err)
	}
	pc := sourcehutConvertor{
		ctx:  ctx,
		meta: meta,
	}
	return shared.NewJSONRequest(pc, w, t, false)
}

// Create implements PayloadConvertor Create method
func (pc sourcehutConvertor) Create(p *api.CreatePayload) (graphqlPayload[buildsVariables], error) {
	return pc.newPayload(p.Repo, p.Sha, p.Ref, p.RefType+" "+git.RefName(p.Ref).ShortName()+" created", true)
}

// Delete implements PayloadConvertor Delete method
func (pc sourcehutConvertor) Delete(_ *api.DeletePayload) (graphqlPayload[buildsVariables], error) {
	return graphqlPayload[buildsVariables]{}, shared.ErrPayloadTypeNotSupported
}

// Fork implements PayloadConvertor Fork method
func (pc sourcehutConvertor) Fork(_ *api.ForkPayload) (graphqlPayload[buildsVariables], error) {
	return graphqlPayload[buildsVariables]{}, shared.ErrPayloadTypeNotSupported
}

// Push implements PayloadConvertor Push method
func (pc sourcehutConvertor) Push(p *api.PushPayload) (graphqlPayload[buildsVariables], error) {
	return pc.newPayload(p.Repo, p.HeadCommit.ID, p.Ref, p.HeadCommit.Message, true)
}

// Issue implements PayloadConvertor Issue method
func (pc sourcehutConvertor) Issue(_ *api.IssuePayload) (graphqlPayload[buildsVariables], error) {
	return graphqlPayload[buildsVariables]{}, shared.ErrPayloadTypeNotSupported
}

// IssueComment implements PayloadConvertor IssueComment method
func (pc sourcehutConvertor) IssueComment(_ *api.IssueCommentPayload) (graphqlPayload[buildsVariables], error) {
	return graphqlPayload[buildsVariables]{}, shared.ErrPayloadTypeNotSupported
}

// PullRequest implements PayloadConvertor PullRequest method
func (pc sourcehutConvertor) PullRequest(_ *api.PullRequestPayload) (graphqlPayload[buildsVariables], error) {
	// TODO
	return graphqlPayload[buildsVariables]{}, shared.ErrPayloadTypeNotSupported
}

// Review implements PayloadConvertor Review method
func (pc sourcehutConvertor) Review(_ *api.PullRequestPayload, _ webhook_module.HookEventType) (graphqlPayload[buildsVariables], error) {
	return graphqlPayload[buildsVariables]{}, shared.ErrPayloadTypeNotSupported
}

// Repository implements PayloadConvertor Repository method
func (pc sourcehutConvertor) Repository(_ *api.RepositoryPayload) (graphqlPayload[buildsVariables], error) {
	return graphqlPayload[buildsVariables]{}, shared.ErrPayloadTypeNotSupported
}

// Wiki implements PayloadConvertor Wiki method
func (pc sourcehutConvertor) Wiki(_ *api.WikiPayload) (graphqlPayload[buildsVariables], error) {
	return graphqlPayload[buildsVariables]{}, shared.ErrPayloadTypeNotSupported
}

// Release implements PayloadConvertor Release method
func (pc sourcehutConvertor) Release(_ *api.ReleasePayload) (graphqlPayload[buildsVariables], error) {
	return graphqlPayload[buildsVariables]{}, shared.ErrPayloadTypeNotSupported
}

func (pc sourcehutConvertor) Package(_ *api.PackagePayload) (graphqlPayload[buildsVariables], error) {
	return graphqlPayload[buildsVariables]{}, shared.ErrPayloadTypeNotSupported
}

func (pc sourcehutConvertor) Action(_ *api.ActionPayload) (graphqlPayload[buildsVariables], error) {
	return graphqlPayload[buildsVariables]{}, shared.ErrPayloadTypeNotSupported
}

// newPayload opens and adjusts the manifest to submit to the builds service
//
// in case of an error the Error field will be set, to be visible by the end-user under recent deliveries
func (pc sourcehutConvertor) newPayload(repo *api.Repository, commitID, ref, note string, trusted bool) (graphqlPayload[buildsVariables], error) {
	manifest, err := pc.constructManifest(repo, commitID, ref)
	if err != nil {
		if len(manifest) == 0 {
			return graphqlPayload[buildsVariables]{}, err
		}
		// the manifest contains an error for the user: log the actual error and construct the payload
		// the error will be visible under the "recent deliveries" of the webhook settings.
		log.Warn("sourcehut.builds: could not construct manifest for %s: %v", repo.FullName, err)
		msg := fmt.Sprintf("%s:%s %s", repo.FullName, ref, manifest)
		return graphqlPayload[buildsVariables]{
			Error: msg,
		}, nil
	}

	gitRef := git.RefName(ref)
	return graphqlPayload[buildsVariables]{
		Query: `mutation (
	$manifest: String!
	$tags: [String!]
	$note: String!
	$secrets: Boolean!
	$execute: Boolean!
	$visibility: Visibility!
) {
	submit(
		manifest: $manifest
		tags: $tags
		note: $note
		secrets: $secrets
		execute: $execute
		visibility: $visibility
	) {
		id
	}
}`, Variables: buildsVariables{
			Manifest:   string(manifest),
			Tags:       []string{repo.FullName, gitRef.RefType() + "/" + gitRef.ShortName(), pc.meta.ManifestPath},
			Note:       note,
			Secrets:    pc.meta.Secrets && trusted,
			Execute:    trusted,
			Visibility: cmp.Or(pc.meta.Visibility, "PRIVATE"),
		},
	}, nil
}

// constructManifest opens and adjusts the manifest to submit to the builds service
// in case of an error the []byte might contain an error that can be displayed to the user
func (pc sourcehutConvertor) constructManifest(repo *api.Repository, commitID, gitRef string) ([]byte, error) {
	gitRepo, err := gitrepo.OpenRepository(pc.ctx, repo)
	if err != nil {
		msg := "could not open repository"
		return []byte(msg), fmt.Errorf(msg+": %w", err)
	}
	defer gitRepo.Close()

	commit, err := gitRepo.GetCommit(commitID)
	if err != nil {
		msg := fmt.Sprintf("could not get commit %q", commitID)
		return []byte(msg), fmt.Errorf(msg+": %w", err)
	}
	entry, err := commit.GetTreeEntryByPath(pc.meta.ManifestPath)
	if err != nil {
		msg := fmt.Sprintf("could not open manifest %q", pc.meta.ManifestPath)
		return []byte(msg), fmt.Errorf(msg+": %w", err)
	}
	r, err := entry.Blob().DataAsync()
	if err != nil {
		msg := fmt.Sprintf("could not read manifest %q", pc.meta.ManifestPath)
		return []byte(msg), fmt.Errorf(msg+": %w", err)
	}
	defer r.Close()

	return adjustManifest(repo, commitID, gitRef, r, pc.meta.ManifestPath)
}

func adjustManifest(repo *api.Repository, commitID, gitRef string, r io.Reader, path string) ([]byte, error) {
	// reference: https://man.sr.ht/builds.sr.ht/manifest.md
	var manifest struct {
		Sources     []string          `yaml:"sources"`
		Environment map[string]string `yaml:"environment"`

		Rest map[string]yaml.Node `yaml:",inline"`
	}
	if err := yaml.NewDecoder(r).Decode(&manifest); err != nil {
		msg := fmt.Sprintf("could not decode manifest %q", path)
		return []byte(msg), fmt.Errorf(msg+": %w", err)
	}

	if manifest.Environment == nil {
		manifest.Environment = make(map[string]string)
	}
	manifest.Environment["BUILD_SUBMITTER"] = "forgejo"
	manifest.Environment["BUILD_SUBMITTER_URL"] = setting.AppURL
	manifest.Environment["GIT_REF"] = gitRef

	found := false
	for i, s := range manifest.Sources {
		if s == repo.CloneURL || s == repo.SSHURL {
			manifest.Sources[i] = s + "#" + commitID
			found = true
			break
		}
	}
	if !found {
		source := repo.CloneURL
		if repo.Private || setting.Repository.DisableHTTPGit {
			// default to ssh for private repos or when git clone is disabled over http
			source = repo.SSHURL
		}
		source += "#" + commitID
		manifest.Sources = append(manifest.Sources, source)
	}

	return yaml.Marshal(manifest)
}
