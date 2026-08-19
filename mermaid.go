package mermaid_go

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/inspector"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

//go:embed mermaid.min.js
var SourceMermaid string

var DefaultPage = `data:text/html,<!DOCTYPE html>
<html lang="en">
    <head><meta charset="utf-8"></head>
    <body></body>
</html>`

// DefaultRenderTimeout bounds a single Render/RenderAsPng call. Without it a
// hung page (or a chrome target that never reports the node as visible) would
// block forever, because the engine context has no deadline of its own.
const DefaultRenderTimeout = 30 * time.Second

var (
	ErrMermaidNotReady = errors.New("mermaid.js initial failed")
	ErrFailedEncoding  = errors.New("failed to encode")
	// ErrTargetCrashed reports that chrome sent Inspector.targetCrashed (or
	// detached for a reason other than a normal shutdown). Renders issued
	// against a crashed target fail with this error joined to the underlying
	// chromedp error.
	ErrTargetCrashed = errors.New("chrome target crashed")
)

type BoxModel = dom.BoxModel

type RenderEngine struct {
	mu              sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	allocatorCancel context.CancelFunc
	// renderTimeout is atomic so it can be adjusted while a render holds mu.
	renderTimeout atomic.Int64

	// crashMu guards the crash bookkeeping below. It is deliberately separate
	// from mu: the chromedp event goroutine takes it while a Render call may be
	// holding mu, and taking mu from the event goroutine would deadlock.
	crashMu      sync.Mutex
	crashed      bool
	detachReason string
	lastReported string
	crashHandler func(error)
}

var jsonMarshal = json.Marshal

func NewRenderEngine(ctx context.Context, statements []string, options ...chromedp.ExecAllocatorOption) (*RenderEngine, error) {
	var (
		result string
	)

	args := make([]chromedp.ExecAllocatorOption, 0, len(chromedp.DefaultExecAllocatorOptions)+len(options)+1)
	args = append(args, chromedp.DefaultExecAllocatorOptions[:]...)

	deadline, ok := ctx.Deadline()
	if ok {
		timeout := time.Until(deadline)
		if timeout < 20*time.Second {
			timeout = 20 * time.Second
		}
		args = append(args, chromedp.WSURLReadTimeout(timeout))
	}
	args = append(args, options...)
	actx, allocatorCancel := chromedp.NewExecAllocator(ctx, args...)
	ctx, cancel := chromedp.NewContext(actx)

	engine := &RenderEngine{
		ctx:             ctx,
		cancel:          cancel,
		allocatorCancel: allocatorCancel,
	}
	engine.renderTimeout.Store(int64(DefaultRenderTimeout))
	// chromedp enables the Inspector domain during target setup, so this picks
	// up crashes for the lifetime of the engine. Registering before the first
	// Run is safe: chromedp queues listeners until the target exists.
	chromedp.ListenTarget(ctx, engine.handleTargetEvent)

	actions := []chromedp.Action{
		chromedp.Navigate(DefaultPage),
		chromedp.Evaluate(SourceMermaid, nil),
		chromedp.Evaluate("mermaid.initialize({startOnLoad:false})", nil),
	}
	for _, stmt := range statements {
		actions = append(actions, chromedp.Evaluate(stmt, nil))
	}
	actions = append(actions, chromedp.Evaluate("typeof mermaid", &result))
	err := chromedp.Run(ctx, actions...)
	if err == nil && result != "object" {
		err = ErrMermaidNotReady
	}
	if err != nil {
		cancel()
		if allocatorCancel != nil {
			allocatorCancel()
		}
		return nil, err
	}
	return engine, nil
}

// handleTargetEvent records chrome crash notifications. It runs on chromedp's
// event goroutine and must not block.
func (r *RenderEngine) handleTargetEvent(ev any) {
	switch e := ev.(type) {
	case *inspector.EventTargetCrashed:
		r.noteCrash("")
	case *inspector.EventDetached:
		// A normal Cancel() detaches too; only unexpected reasons such as
		// "Render process gone." indicate a crash.
		switch e.Reason {
		case inspector.DetachReasonTargetClosed, inspector.DetachReasonCanceledByUser:
		default:
			r.noteCrash(e.Reason.String())
		}
	case *inspector.EventTargetReloadedAfterCrash:
		r.crashMu.Lock()
		r.crashed, r.detachReason, r.lastReported = false, "", ""
		r.crashMu.Unlock()
	}
}

func (r *RenderEngine) noteCrash(reason string) {
	r.crashMu.Lock()
	r.crashed = true
	if reason != "" {
		r.detachReason = reason
	}
	err := r.crashErrLocked()
	handler := r.crashHandler
	// Crash and detach arrive as two events, the second one carrying the
	// reason. Report each time the message gains information, not per event.
	report := handler != nil && err != nil && err.Error() != r.lastReported
	if report {
		r.lastReported = err.Error()
	}
	r.crashMu.Unlock()

	if report {
		handler(err)
	}
}

func (r *RenderEngine) crashErrLocked() error {
	if !r.crashed {
		return nil
	}
	if r.detachReason != "" {
		return fmt.Errorf("%w: %s", ErrTargetCrashed, r.detachReason)
	}
	return ErrTargetCrashed
}

// CrashError reports ErrTargetCrashed (wrapped with chrome's detach reason when
// one was given) if the underlying target has crashed, and nil otherwise. The
// state is cleared if chrome reloads the target after the crash.
func (r *RenderEngine) CrashError() error {
	r.crashMu.Lock()
	defer r.crashMu.Unlock()
	return r.crashErrLocked()
}

// SetTargetCrashedHandler installs fn to be called when chrome reports that the
// target crashed, with the same error CrashError returns. fn runs on chromedp's
// event goroutine, so it must return promptly and must not call back into the
// engine; hand the error to a logger or a buffered channel instead. It may be
// called more than once for a single crash as chrome supplies more detail. Pass
// nil to remove a previously installed handler.
func (r *RenderEngine) SetTargetCrashedHandler(fn func(error)) {
	r.crashMu.Lock()
	defer r.crashMu.Unlock()
	r.crashHandler = fn
}

// SetRenderTimeout overrides DefaultRenderTimeout for subsequent renders. A
// value <= 0 disables the per-render deadline, restoring the old behaviour of
// waiting as long as the engine context allows.
func (r *RenderEngine) SetRenderTimeout(d time.Duration) {
	r.renderTimeout.Store(int64(d))
}

type RenderOption func(*renderOptions)

type renderOptions struct {
	bundle     bool
	timeout    time.Duration
	hasTimeout bool
}

func WithBundle() RenderOption {
	return func(o *renderOptions) {
		o.bundle = true
	}
}

// WithTimeout overrides the engine's render timeout for a single call. A value
// <= 0 disables the deadline for that call.
func WithTimeout(d time.Duration) RenderOption {
	return func(o *renderOptions) {
		o.timeout = d
		o.hasTimeout = true
	}
}

// renderContext derives the context for one render. Cancelling a context
// derived from the engine context only aborts the in-flight commands, so the
// engine stays usable after a timeout.
func (r *RenderEngine) renderContext(opts *renderOptions) (context.Context, context.CancelFunc) {
	timeout := time.Duration(r.renderTimeout.Load())
	if opts.hasTimeout {
		timeout = opts.timeout
	}
	if timeout <= 0 {
		return r.ctx, func() {}
	}
	return context.WithTimeout(r.ctx, timeout)
}

// annotateCrash joins the crash error to err so callers can tell a timeout
// caused by a dead browser apart from a slow diagram.
func (r *RenderEngine) annotateCrash(err error) error {
	if err == nil {
		return nil
	}
	if crashErr := r.CrashError(); crashErr != nil {
		return errors.Join(err, crashErr)
	}
	return err
}

func (r *RenderEngine) Render(content string, opts ...RenderOption) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var (
		result string
	)

	renderOpts := &renderOptions{}
	for _, opt := range opts {
		opt(renderOpts)
	}

	encodedContent, err := jsonMarshal(content)
	if err != nil {
		return "", ErrFailedEncoding
	}

	var script string
	if renderOpts.bundle {
		script = fmt.Sprintf(`document.body.innerHTML = ''; mermaid.render('mermaid', %s).then(({ svg }) => {
			const parser = new DOMParser();
			const doc = parser.parseFromString(svg, 'image/svg+xml');
			const svgElem = doc.querySelector('svg');
			const desc = doc.createElementNS('http://www.w3.org/2000/svg', 'desc');
			desc.textContent = %s;
			svgElem.insertBefore(desc, svgElem.firstChild);
			return new XMLSerializer().serializeToString(doc);
		});`, string(encodedContent), string(encodedContent))
	} else {
		script = fmt.Sprintf("document.body.innerHTML = ''; mermaid.render('mermaid', %s).then(({ svg }) => { return svg; });", string(encodedContent))
	}

	ctx, cancel := r.renderContext(renderOpts)
	defer cancel()

	err = chromedp.Run(ctx,
		chromedp.Evaluate(script, &result, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		}),
	)
	return result, r.annotateCrash(err)
}

func (r *RenderEngine) RenderAsScaledPng(content string, scale float64, opts ...RenderOption) ([]byte, *BoxModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var (
		result_in_bytes []byte
		model           *dom.BoxModel
	)

	renderOpts := &renderOptions{}
	for _, opt := range opts {
		opt(renderOpts)
	}

	encodedContent, err := jsonMarshal(content)
	if err != nil {
		return nil, nil, ErrFailedEncoding
	}
	script := fmt.Sprintf("document.body.innerHTML = ''; mermaid.render('mermaid', %s).then(({ svg }) => { document.body.innerHTML = svg; });", string(encodedContent))

	ctx, cancel := r.renderContext(renderOpts)
	defer cancel()

	err = chromedp.Run(ctx,
		chromedp.Evaluate(script, nil, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		}),
		chromedp.ScreenshotScale("#mermaid", scale, &result_in_bytes, chromedp.ByID),
		chromedp.Dimensions("#mermaid", &model, chromedp.ByID),
	)
	return result_in_bytes, model, r.annotateCrash(err)
}

func (r *RenderEngine) RenderAsPng(content string, opts ...RenderOption) ([]byte, *BoxModel, error) {
	return r.RenderAsScaledPng(content, 1.0, opts...)
}

func (r *RenderEngine) Cancel() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancel()
	if r.allocatorCancel != nil {
		r.allocatorCancel()
	}
}
