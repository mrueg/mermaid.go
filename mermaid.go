package mermaid_go

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/cdproto/dom"
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

var (
	ErrMermaidNotReady = errors.New("mermaid.js initial failed")
	ErrFailedEncoding  = errors.New("failed to encode")
)

type BoxModel = dom.BoxModel

type RenderEngine struct {
	mu              sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	allocatorCancel context.CancelFunc
}

var jsonMarshal = json.Marshal

func NewRenderEngine(ctx context.Context, statements []string, options ...chromedp.ExecAllocatorOption) (*RenderEngine, error) {
	var (
		result string
	)

	args := make([]chromedp.ExecAllocatorOption, 0, len(chromedp.DefaultExecAllocatorOptions)+len(options)+1)
	args = append(args, chromedp.DefaultExecAllocatorOptions[:]...)
	args = append(args, options...)

	deadline, ok := ctx.Deadline()
	if ok {
		args = append(args, chromedp.WSURLReadTimeout(time.Until(deadline)))
	}
	actx, allocatorCancel := chromedp.NewExecAllocator(ctx, args...)
	ctx, cancel := chromedp.NewContext(actx)
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
	return &RenderEngine{
		ctx:             ctx,
		cancel:          cancel,
		allocatorCancel: allocatorCancel,
	}, nil
}

type RenderOption func(*renderOptions)

type renderOptions struct {
	bundle bool
}

func WithBundle() RenderOption {
	return func(o *renderOptions) {
		o.bundle = true
	}
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

	err = chromedp.Run(r.ctx,
		chromedp.Evaluate(script, &result, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		}),
	)
	return result, err
}

func (r *RenderEngine) RenderAsScaledPng(content string, scale float64) ([]byte, *BoxModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var (
		result_in_bytes []byte
		model           *dom.BoxModel
	)
	encodedContent, err := jsonMarshal(content)
	if err != nil {
		return nil, nil, ErrFailedEncoding
	}
	script := fmt.Sprintf("document.body.innerHTML = ''; mermaid.render('mermaid', %s).then(({ svg }) => { document.body.innerHTML = svg; });", string(encodedContent))
	err = chromedp.Run(r.ctx,
		chromedp.Evaluate(script, nil, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		}),
		chromedp.ScreenshotScale("#mermaid", scale, &result_in_bytes, chromedp.ByID),
		chromedp.Dimensions("#mermaid", &model, chromedp.ByID),
	)
	return result_in_bytes, model, err
}

func (r *RenderEngine) RenderAsPng(content string) ([]byte, *BoxModel, error) {
	return r.RenderAsScaledPng(content, 1.0)
}

func (r *RenderEngine) Cancel() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancel()
	if r.allocatorCancel != nil {
		r.allocatorCancel()
	}
}
