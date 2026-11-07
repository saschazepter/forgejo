// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package markup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"forgejo.org/modules/git"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"
	"forgejo.org/modules/util/donotpanic"

	"github.com/yuin/goldmark/ast"
)

type RenderMetaMode string

const (
	sniffLen                           = 1024
	RenderMetaAsDetails RenderMetaMode = "details" // default
	RenderMetaAsNone    RenderMetaMode = "none"
	RenderMetaAsTable   RenderMetaMode = "table"
)

type ProcessorHelper struct {
	IsUsernameMentionable func(ctx context.Context, username string) bool
	GetRepoFileBlob       func(ctx context.Context, ownerName, repoName, commitSha, filePath string, language *string) (*git.Blob, error)

	ElementDir string // the direction of the elements, eg: "ltr", "rtl", "auto", default to no direction attribute
}

var DefaultProcessorHelper ProcessorHelper

// Init initialize regexps for markdown parsing
func Init(ph *ProcessorHelper) {
	if ph != nil {
		DefaultProcessorHelper = *ph
	}

	NewSanitizer()
	if len(setting.Markdown.CustomURLSchemes) > 0 {
		CustomLinkURLSchemes(setting.Markdown.CustomURLSchemes)
	}

	// since setting maybe changed extensions, this will reload all renderer extensions mapping
	extRenderers = make(map[string]Renderer)
	for _, renderer := range renderers {
		for _, ext := range renderer.Extensions() {
			extRenderers[strings.ToLower(ext)] = renderer
		}
	}
}

// Header holds the data about a header.
type Header struct {
	Level int
	Text  string
	ID    string
}

// RenderContext represents a render context
type RenderContext struct {
	Ctx              context.Context
	RelativePath     string // relative path from tree root of the branch
	Type             string
	IsWiki           bool
	Links            Links
	Metas            map[string]string
	DefaultLink      string
	GitRepo          *git.Repository
	ShaExistCache    map[string]bool
	cancelFn         func()
	SidebarTocNode   ast.Node
	InStandalonePage bool // used by external render. the router "/org/repo/render/..." will output the rendered content in a standalone page
}

type Links struct {
	AbsolutePrefix bool
	Base           string
	BranchPath     string
	TreePath       string
}

func (l *Links) Prefix() string {
	if l.AbsolutePrefix {
		return setting.AppURL
	}
	return setting.AppSubURL
}

func (l *Links) HasBranchInfo() bool {
	return l.BranchPath != ""
}

func (l *Links) SrcLink() string {
	return util.URLJoin(l.Base, "src", l.BranchPath, l.TreePath)
}

func (l *Links) SrcLinkBase() string {
	return util.URLJoin(l.Base, "src", l.BranchPath)
}

func (l *Links) MediaLink() string {
	return util.URLJoin(l.Base, "media", l.BranchPath, l.TreePath)
}

func (l *Links) MediaLinkBase() string {
	return util.URLJoin(l.Base, "media", l.BranchPath)
}

func (l *Links) RawLink() string {
	return util.URLJoin(l.Base, "raw", l.BranchPath, l.TreePath)
}

func (l *Links) WikiLink() string {
	return util.URLJoin(l.Base, "wiki")
}

func (l *Links) WikiRawLink() string {
	return util.URLJoin(l.Base, "wiki/raw")
}

func (l *Links) ResolveMediaLink(isWiki, isRepoRoot bool) string {
	if isWiki {
		return l.WikiRawLink()
	} else if l.HasBranchInfo() {
		if isRepoRoot {
			return l.MediaLinkBase()
		}
		return l.MediaLink()
	}
	return l.Base
}

func (ctx *RenderContext) IsRepoFile() bool {
	return ctx.RelativePath != ""
}

// Cancel runs any cleanup functions that have been registered for this Ctx
func (ctx *RenderContext) Cancel() {
	if ctx == nil {
		return
	}
	ctx.ShaExistCache = map[string]bool{}
	if ctx.cancelFn == nil {
		return
	}
	ctx.cancelFn()
}

// AddCancel adds the provided fn as a Cleanup for this Ctx
func (ctx *RenderContext) AddCancel(fn func()) {
	if ctx == nil {
		return
	}
	oldCancelFn := ctx.cancelFn
	if oldCancelFn == nil {
		ctx.cancelFn = fn
		return
	}
	ctx.cancelFn = func() {
		defer oldCancelFn()
		fn()
	}
}

// Renderer defines an interface for rendering markup file to HTML
type Renderer interface {
	Name() string // markup format name
	Extensions() []string
	SanitizerRules() []setting.MarkupSanitizerRule
	Render(ctx *RenderContext, input io.Reader, output io.Writer) error
}

// PostProcessRenderer defines an interface for renderers who need post process
type PostProcessRenderer interface {
	NeedPostProcess() bool
}

// PostProcessRenderer defines an interface for external renderers
type ExternalRenderer interface {
	// SanitizerDisabled disabled sanitize if return true
	SanitizerDisabled() bool

	// DisplayInIFrame represents whether render the content with an iframe
	DisplayInIFrame() bool
}

// RendererContentDetector detects if the content can be rendered
// by specified renderer
type RendererContentDetector interface {
	CanRender(filename string, input io.Reader) bool
}

var (
	extRenderers = make(map[string]Renderer)
	renderers    = make(map[string]Renderer)
)

// RegisterRenderer registers a new markup file renderer
func RegisterRenderer(renderer Renderer) {
	renderers[renderer.Name()] = renderer
	for _, ext := range renderer.Extensions() {
		extRenderers[strings.ToLower(ext)] = renderer
	}
}

// FullExtension returns the full extension of path, i.e. everything after and including
// the first period in the basename of path.
func FullExtension(path string) string {
	_, extension, found := strings.Cut(strings.ToLower(filepath.Base(path)), ".")
	if !found {
		return ""
	}
	return "." + extension
}

// GetRendererByExtension returns the most specific registered renderer for extension.
func GetRendererByExtension(extension string) Renderer {
	_, extension, found := strings.Cut(extension, ".")
	checkedExtensions := 0
	for found && checkedExtensions < 10 {
		if renderer, ok := extRenderers["."+extension]; ok {
			return renderer
		}
		checkedExtensions++
		_, extension, found = strings.Cut(extension, ".")
	}
	return nil
}

// GetRendererByType returns a renderer according type
func GetRendererByType(tp string) Renderer {
	return renderers[tp]
}

// DetectRendererType detects the markup type of the content
//
// Uses 1024 bytes at most.
func DetectRendererType(filename string, input io.Reader) string {
	buf, err := io.ReadAll(io.LimitReader(input, sniffLen))
	if err != nil {
		return ""
	}
	for _, renderer := range renderers {
		if detector, ok := renderer.(RendererContentDetector); ok && detector.CanRender(filename, bytes.NewReader(buf)) {
			return renderer.Name()
		}
	}
	return ""
}

// Render renders markup file to HTML with all specific handling stuff.
func Render(ctx *RenderContext, input io.Reader, output io.Writer) error {
	if ctx.Type != "" {
		return renderByType(ctx, input, output)
	} else if ctx.RelativePath != "" {
		return renderFile(ctx, input, output)
	}
	return errors.New("Render options both filename and type missing")
}

// RenderString renders Markup string to HTML with all specific handling stuff and return string
func RenderString(ctx *RenderContext, content string) (string, error) {
	var buf strings.Builder
	if err := Render(ctx, strings.NewReader(content), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error { return nil }

func renderIFrame(ctx *RenderContext, output io.Writer) error {
	// set height="300", otherwise if the postMessage mechanism breaks, we are left with a 0-height iframe
	// at the moment, only "allow-scripts" is allowed for sandbox mode.
	// "allow-same-origin" should never be used, it leads to XSS attack, and it makes the JS in iframe can access parent window's config and CSRF token
	// TODO: when using dark theme, if the rendered content doesn't have proper style, the default text color is black, which is not easy to read
	_, err := io.WriteString(output, fmt.Sprintf(`
<iframe src="%s/%s/%s/render/%s/%s"
class="external-render"
width="100%%" height="300" frameborder="0"
sandbox="allow-scripts"
></iframe>`,
		setting.AppSubURL,
		url.PathEscape(ctx.Metas["user"]),
		url.PathEscape(ctx.Metas["repo"]),
		ctx.Metas["BranchNameSubURL"],
		url.PathEscape(ctx.RelativePath),
	))
	return err
}

func postProcessOrCopy(ctx *RenderContext, renderer Renderer, reader io.Reader, writer io.Writer) (err error) {
	if r, ok := renderer.(PostProcessRenderer); ok && r.NeedPostProcess() {
		err = PostProcess(ctx, reader, writer)
	} else {
		_, err = io.Copy(writer, reader)
	}
	return err
}

func render(ctx *RenderContext, renderer Renderer, input io.Reader, output io.Writer) error {
	var wg sync.WaitGroup
	var err error
	pr, pw := io.Pipe()
	defer func() {
		_ = pr.Close()
		_ = pw.Close()
	}()

	var pr2 io.ReadCloser
	var pw2 io.WriteCloser

	var sanitizerDisabled bool
	if r, ok := renderer.(ExternalRenderer); ok {
		sanitizerDisabled = r.SanitizerDisabled()
	}

	if !sanitizerDisabled {
		pr2, pw2 = io.Pipe()
		defer func() {
			_ = pr2.Close()
			_ = pw2.Close()
		}()

		wg.Go(func() {
			err = donotpanic.SafeFuncWithError(func() error { return SanitizeReader(pr2, renderer.Name(), output) })
			_ = pr2.Close()
		})
	} else {
		pw2 = nopCloser{output}
	}

	wg.Go(func() {
		err = donotpanic.SafeFuncWithError(func() error { return postProcessOrCopy(ctx, renderer, pr, pw2) })
		_ = pr.Close()
		_ = pw2.Close()
	})

	if err1 := renderer.Render(ctx, input, pw); err1 != nil {
		return err1
	}

	if r, ok := renderer.(ExternalRenderer); ok && r.DisplayInIFrame() {
		// Append a short script to the iframe's contents, which will communicate the scroll height of the embedded document via postMessage, either once loaded (in case the containing page loads first) in response to a postMessage from external.js, in case the iframe loads first
		// We use '*' as a target origin for postMessage, because can be certain we are embedded on the same domain, due to X-Frame-Options configured elsewhere. (Plus, the offsetHeight of an embedded document is likely not sensitive data anyway.)
		_, _ = pw.Write([]byte("<script>{let postHeight = () => {window.parent.postMessage({frameHeight: document.documentElement.offsetHeight || document.documentElement.scrollHeight}, '*')}; window.addEventListener('load', postHeight); window.addEventListener('message', (event) => {if (event.source === window.parent && event.data.requestOffsetHeight) postHeight()});}</script>"))
	}
	_ = pw.Close()

	wg.Wait()
	return err
}

// ErrUnsupportedRenderType represents
type ErrUnsupportedRenderType struct {
	Type string
}

func (err ErrUnsupportedRenderType) Error() string {
	return fmt.Sprintf("Unsupported render type: %s", err.Type)
}

func renderByType(ctx *RenderContext, input io.Reader, output io.Writer) error {
	if renderer, ok := renderers[ctx.Type]; ok {
		return render(ctx, renderer, input, output)
	}
	return ErrUnsupportedRenderType{ctx.Type}
}

// ErrMissingExtension represents the error when a path does not have any extension.
type ErrMissingExtension struct {
	Path string
}

func IsErrMissingExtension(err error) bool {
	_, ok := err.(ErrMissingExtension)
	return ok
}

func (err ErrMissingExtension) Error() string {
	return fmt.Sprintf("path '%s' does not have an extension", err.Path)
}

// ErrUnsupportedRenderExtension represents the error when extension doesn't supported to render
type ErrUnsupportedRenderExtension struct {
	Extension string
}

func IsErrUnsupportedRenderExtension(err error) bool {
	_, ok := err.(ErrUnsupportedRenderExtension)
	return ok
}

func (err ErrUnsupportedRenderExtension) Error() string {
	return fmt.Sprintf("Unsupported render extension: %s", err.Extension)
}

func renderFile(ctx *RenderContext, input io.Reader, output io.Writer) error {
	extension := FullExtension(ctx.RelativePath)
	if extension == "" {
		return ErrMissingExtension{ctx.RelativePath}
	}
	if renderer := GetRendererByExtension(extension); renderer != nil {
		if r, ok := renderer.(ExternalRenderer); ok && r.DisplayInIFrame() {
			if !ctx.InStandalonePage {
				// for an external render, it could only output its content in a standalone page
				// otherwise, a <iframe> should be outputted to embed the external rendered page
				return renderIFrame(ctx, output)
			}
		}
		return render(ctx, renderer, input, output)
	}
	return ErrUnsupportedRenderExtension{extension}
}

// Type returns if markup format via the filename
func Type(filename string) string {
	if parser := GetRendererByExtension(FullExtension(filename)); parser != nil {
		return parser.Name()
	}
	return ""
}

// IsMarkupFile reports whether file is a markup type file
func IsMarkupFile(name, markup string) bool {
	if parser := GetRendererByExtension(FullExtension(name)); parser != nil {
		return parser.Name() == markup
	}
	return false
}

func PreviewableExtensions() []string {
	extensions := make([]string, 0, len(extRenderers))
	for extension := range extRenderers {
		extensions = append(extensions, extension)
	}
	return extensions
}
