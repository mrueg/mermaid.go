package mermaid_go

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

//go:embed mermaid.min.js
var SOURCE_MERMAID string

var DEFAULT_PAGE = `data:text/html,<!DOCTYPE html>
<html lang="en">
    <head><meta charset="utf-8"></head>
    <body></body>
</html>`

var ERR_MERMAID_NOT_READY = errors.New("mermaid.js initial failed")

type BoxModel = dom.BoxModel

type RenderEngine struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func NewRenderEngine(ctx context.Context, statements ...string) (*RenderEngine, error) {
	ctx, cancel := chromedp.NewContext(ctx)
	var (
		lib_ready *runtime.RemoteObject
	)
	actions := []chromedp.Action{
		chromedp.Navigate(DEFAULT_PAGE),
		chromedp.Evaluate(SOURCE_MERMAID, &lib_ready),
	}
	for _, stmt := range statements {
		actions = append(actions, chromedp.Evaluate(stmt, nil))
	}
	err := chromedp.Run(ctx, actions...)
	if err == nil && lib_ready.ObjectID != "" {
		err = ERR_MERMAID_NOT_READY
	}
	return &RenderEngine{
		ctx:    ctx,
		cancel: cancel,
	}, err
}

func (r *RenderEngine) Render(content string) (string, error) {
	var (
		result string
	)
	err := chromedp.Run(r.ctx,
		chromedp.Evaluate(fmt.Sprintf("mermaid.render('mermaid', `%s`).then(({ svg }) => { return svg; });", content), &result, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		}),
	)
	return result, err
}

func (r *RenderEngine) RenderAsPng(content string) ([]byte, *BoxModel, error) {
	var (
		result_in_bytes []byte
		model           *dom.BoxModel
	)
	err := chromedp.Run(r.ctx,
		chromedp.Evaluate(fmt.Sprintf("mermaid.render('mermaid', `%s`).then(({ svg }) => { document.body.innerHTML = svg; });", content), nil),
		chromedp.Screenshot("#mermaid", &result_in_bytes, chromedp.ByID),
		chromedp.Dimensions("#mermaid", &model, chromedp.ByID),
	)
	return result_in_bytes, interface{}(model).(*BoxModel), err
}

func (r *RenderEngine) Cancel() {
	r.cancel()
}
