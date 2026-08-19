package mermaid_go

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/inspector"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// renderTimeout bounds engine startup in the subtests that launch their own
// browser; chrome plus the 3.5MB mermaid bundle is slow under -race.
var renderTimeout = 60 * time.Second

func TestRenderEngine_Render(t *testing.T) {
	cases := []struct {
		content/*, result */ string
		err_has_prefix string
	}{
		{content: `graph TD;
    A-->B;
    A-->C;
    B-->D;
    C-->D;`},
		{content: `sequenceDiagram
			participant Alice
			participant Bob
			Alice->>John: Hello John, how are you?
			loop Healthcheck
			John->>John: Fight against hypochondria
			end
			Note right of John: Rational thoughts <br/>prevail!
			John-->>Alice: Great!
			John->>Bob: How about you?
			Bob-->>John: Jolly good!`},
		{content: `gantt
dateFormat  YYYY-MM-DD
title Adding GANTT diagram to mermaid
excludes weekdays 2014-01-10

section A section
Completed task            :done,    des1, 2014-01-06,2014-01-08
Active task               :active,  des2, 2014-01-09, 3d
Future task               :         des3, after des2, 5d
Future task2               :         des4, after des3, 5d`},
		{content: `classDiagram
Class01 <|-- AveryLongClass : Cool
Class03 *-- Class04
Class05 o-- Class06
Class07 .. Class08
Class09 --> C2 : Where am i?
Class09 --* C3
Class09 --|> Class07
Class07 : equals()
Class07 : Object[] elementData
Class01 : size()
Class01 : int chimp
Class01 : int gorilla
Class08 <--> C2: Cool label`},
		{content: `gitGraph
       commit
       commit
       branch develop
       commit
       commit
       commit
       checkout main
       commit
       commit
       merge develop
       commit
       commit`},
		{content: `erDiagram
    CUSTOMER ||--o{ ORDER : places
    ORDER ||--|{ LINE-ITEM : contains
    CUSTOMER }|..|{ DELIVERY-ADDRESS : uses
`},
		{content: `journey
    title My working day
    section Go to work
      Make tea: 5: Me
      Go upstairs: 3: Me
      Do work: 1: Me, Cat
    section Go home
      Go downstairs: 5: Me
      Sit down: 5: Me`},
		{content: `graph TD;
    A-->B['name'];
    A-->C["pic"];
    B-->D;
    C-->D;`},
		{content: `graph TD;
    A-->B['name'];
    A-->;`, err_has_prefix: `exception "Uncaught`},
		{content: `graph TD;
	A-->B["` + "`Hello World`" + `"];
	B-->C;`},
	}

	// The engine outlives the whole suite, so it must not be tied to a
	// per-render deadline; each render is bounded by DefaultRenderTimeout.
	re1, err := NewRenderEngine(context.Background(),
		[]string{`mermaid.initialize({'theme': 'base', 'themeVariables': { 'primaryColor': '#1473e6'}});`},
		chromedp.WSURLReadTimeout(renderTimeout))
	if err != nil {
		t.Fatalf("NewRenderEngine() error = %v", err)
	}

	defer re1.Cancel()

	t.Run("BundleDiagram", func(t *testing.T) {
		content := "graph TD; A-->B;"
		got, err := re1.Render(content, WithBundle())
		if err != nil {
			t.Errorf("Render() error = %v", err)
		}
		expected := "graph TD; A--&gt;B;"
		if !strings.Contains(got, "<desc>"+expected+"</desc>") {
			t.Errorf("Render() expected to contain <desc>%s</desc>, but got %s", expected, got)
		}
	})

	t.Run("InvalidSyntax", func(t *testing.T) {
		content := "graph TD; A---;" // Invalid syntax
		_, err := re1.Render(content)
		if err == nil {
			t.Error("Render() expected error for invalid syntax, but got nil")
		}
	})

	t.Run("Scaling", func(t *testing.T) {
		content := "graph TD; A-->B;"
		img1, _, err := re1.RenderAsScaledPng(content, 1.0)
		if err != nil {
			t.Fatalf("RenderAsScaledPng(1.0) error = %v", err)
		}
		img2, _, err := re1.RenderAsScaledPng(content, 2.0)
		if err != nil {
			t.Fatalf("RenderAsScaledPng(2.0) error = %v", err)
		}

		if len(img2) <= len(img1) {
			t.Errorf("RenderAsScaledPng() expected larger image data for 2.0 scale than 1.0, got %v bytes vs %v bytes", len(img2), len(img1))
		}
	})

	t.Run("SequentialDifferentPngs", func(t *testing.T) {
		content1 := "graph TD; A-->B;"
		content2 := "sequenceDiagram; Alice->>Bob: Hello John, how are you?; Bob-->>Alice: Fine!"
		img1, box1, err := re1.RenderAsPng(content1)
		if err != nil {
			t.Fatalf("RenderAsPng(content1) error = %v", err)
		}
		img2, box2, err := re1.RenderAsPng(content2)
		if err != nil {
			t.Fatalf("RenderAsPng(content2) error = %v", err)
		}
		// Render content2 again
		img3, box3, err := re1.RenderAsPng(content2)
		if err != nil {
			t.Fatalf("RenderAsPng(content2 second time) error = %v", err)
		}
		t.Logf("box1: %#v, box2: %#v, box3: %#v", box1, box2, box3)
		if string(img2) == string(img1) {
			t.Errorf("img2 (sequence diagram) was identical to img1 (flowchart) because screenshot was taken before promise resolved!")
		}
		if string(img2) != string(img3) {
			t.Errorf("img2 and img3 should both be sequence diagrams, but img2 was stale!")
		}
	})

	t.Run("InvalidSyntaxPng", func(t *testing.T) {
		content := "graph TD; A---;" // Invalid syntax
		_, _, err := re1.RenderAsPng(content)
		if err == nil {
			t.Error("RenderAsPng() expected error for invalid syntax, but got nil")
		}
	})

	t.Run("ConcurrentRenders", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				content := "graph TD; A-->B;"
				svg, err := re1.Render(content)
				if err != nil {
					t.Errorf("Concurrent Render() error = %v", err)
				}
				if !strings.HasPrefix(svg, "<svg") {
					t.Errorf("Concurrent Render() invalid svg")
				}
			}(i)
		}
		wg.Wait()
	})

	t.Run("CancelledContextStartup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		engine, err := NewRenderEngine(ctx, nil)
		if err == nil {
			t.Error("NewRenderEngine() expected error with cancelled context, got nil")
		}
		if engine != nil {
			t.Error("NewRenderEngine() expected nil engine on error, got non-nil")
		}
	})

	t.Run("Cancel", func(t *testing.T) {
		ctx := context.Background()
		re, err := NewRenderEngine(ctx, nil)
		if err != nil {
			t.Fatalf("NewRenderEngine() error = %v", err)
		}
		re.Cancel()
		_, err = re.Render("graph TD; A-->B;")
		if err == nil {
			t.Error("Render() expected error after Cancel(), but got nil")
		}
	})

	t.Run("SequentialRenders", func(t *testing.T) {
		contents := []string{
			"graph TD; A-->B;",
			"sequenceDiagram; Alice->>Bob: Hello;",
			"pie title Rats; \"Cats\" : 45; \"Dogs\" : 55",
		}
		for _, content := range contents {
			got, err := re1.Render(content)
			if err != nil {
				t.Errorf("Render(%s) error = %v", content, err)
			}
			if !strings.HasPrefix(got, "<svg") {
				t.Errorf("Render(%s) got invalid svg", content)
			}
		}
	})

	t.Run("ErrMermaidNotReady", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), renderTimeout)
		defer cancel()
		engine, err := NewRenderEngine(ctx, []string{"delete window.mermaid"})
		if !errors.Is(err, ErrMermaidNotReady) {
			t.Errorf("NewRenderEngine() expected ErrMermaidNotReady, got %v", err)
		}
		if engine != nil {
			t.Error("NewRenderEngine() expected nil engine on error, got non-nil")
		}
	})

	t.Run("ErrFailedEncoding", func(t *testing.T) {
		oldJSONMarshal := jsonMarshal
		defer func() { jsonMarshal = oldJSONMarshal }()
		jsonMarshal = func(v any) ([]byte, error) {
			return nil, errors.New("mock marshal error")
		}

		content := "graph TD; A-->B;"
		_, err := re1.Render(content)
		if !errors.Is(err, ErrFailedEncoding) {
			t.Errorf("Render() expected ErrFailedEncoding, got %v", err)
		}

		_, _, err = re1.RenderAsScaledPng(content, 1.0)
		if !errors.Is(err, ErrFailedEncoding) {
			t.Errorf("RenderAsScaledPng() expected ErrFailedEncoding, got %v", err)
		}

		_, _, err = re1.RenderAsPng(content)
		if !errors.Is(err, ErrFailedEncoding) {
			t.Errorf("RenderAsPng() expected ErrFailedEncoding, got %v", err)
		}
	})

	t.Run("AllocatorOptions", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), renderTimeout)
		defer cancel()
		engine, err := NewRenderEngine(ctx, nil, chromedp.NoSandbox)
		if err != nil {
			t.Fatalf("NewRenderEngine() with custom options error = %v", err)
		}
		defer engine.Cancel()

		svg, err := engine.Render("graph TD; A-->B;")
		if err != nil {
			t.Errorf("Render() error = %v", err)
		}
		if !strings.HasPrefix(svg, "<svg") {
			t.Errorf("Render() invalid svg")
		}
	})

	t.Run("MultipleCancel", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), renderTimeout)
		defer cancel()
		engine, err := NewRenderEngine(ctx, nil)
		if err != nil {
			t.Fatalf("NewRenderEngine() error = %v", err)
		}
		engine.Cancel()
		// Calling Cancel a second time should be safe and non-panicking
		engine.Cancel()
	})

	t.Run("DeadlineContext", func(t *testing.T) {
		deadline := time.Now().Add(renderTimeout)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		engine, err := NewRenderEngine(ctx, nil)
		if err != nil {
			t.Fatalf("NewRenderEngine() with deadline error = %v", err)
		}
		defer engine.Cancel()

		svg, err := engine.Render("graph TD; A-->B;")
		if err != nil {
			t.Errorf("Render() error = %v", err)
		}
		if !strings.HasPrefix(svg, "<svg") {
			t.Errorf("Render() invalid svg")
		}
	})

	for _, tt := range cases {
		t.Run("", func(t *testing.T) {
			got, err := re1.Render(tt.content)
			t.Logf("got %s, error %s", got, err)
			if err != nil {
				if tt.err_has_prefix != "" && strings.HasPrefix(err.Error(), tt.err_has_prefix) {
					// expected exception
					return
				}
				t.Errorf("Render() error = %v", err)
			}
			if !strings.HasPrefix(got, "<svg") {
				t.Errorf("Render() got an invalid svg = %v, err = %s", got, err)
			}

			result_in_bytes, box, err := re1.RenderAsPng(tt.content)
			if err != nil {
				if !strings.HasPrefix(err.Error(), tt.err_has_prefix) {
					t.Errorf("Render() error = %v", err)
					return
				}
			}
			if box == nil {
				t.Errorf("RenderAsPng() returned an empty box")
			} else if box.Width < 1 || box.Height < 1 {
				t.Errorf("RenderAsPng() got empty image = w:%d, h:%d)", box.Width, box.Height)
			}
			content_type := http.DetectContentType(result_in_bytes)
			if content_type != "image/png" {
				t.Errorf("RenderAsPng() return an '%s' rather than 'image/png'", content_type)
			}
		})
	}
}

func BenchmarkRenderEngine_Render(b *testing.B) {
	case1 := `graph TD;
    A-->B;
    A-->C;
    B-->D;
    C-->D;`
	ctx1 := context.Background()
	re1, _ := NewRenderEngine(ctx1, nil)
	for i := 0; i < b.N; i++ {
		_, _ = re1.Render(case1)
	}
	re1.Cancel()
}

func TestRenderEngine_RenderTimeout(t *testing.T) {
	// No deadline on the engine: each render carries its own. Without a
	// deadline chromedp's 20s default dial budget applies, which a loaded
	// machine can exceed, so ask for a generous one explicitly.
	re, err := NewRenderEngine(context.Background(), nil, chromedp.WSURLReadTimeout(renderTimeout))
	if err != nil {
		t.Fatalf("NewRenderEngine() error = %v", err)
	}
	defer re.Cancel()

	content := "graph TD; A-->B;"

	t.Run("PerCallTimeout", func(t *testing.T) {
		if _, err := re.Render(content, WithTimeout(time.Nanosecond)); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Render() expected context.DeadlineExceeded, got %v", err)
		}
		if _, _, err := re.RenderAsPng(content, WithTimeout(time.Nanosecond)); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("RenderAsPng() expected context.DeadlineExceeded, got %v", err)
		}
		if _, _, err := re.RenderAsScaledPng(content, 2.0, WithTimeout(time.Nanosecond)); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("RenderAsScaledPng() expected context.DeadlineExceeded, got %v", err)
		}
	})

	t.Run("EngineSurvivesTimeout", func(t *testing.T) {
		svg, err := re.Render(content)
		if err != nil {
			t.Fatalf("Render() after a timed out render error = %v", err)
		}
		if !strings.HasPrefix(svg, "<svg") {
			t.Errorf("Render() got an invalid svg = %v", svg)
		}
	})

	t.Run("EngineTimeout", func(t *testing.T) {
		re.SetRenderTimeout(time.Nanosecond)
		if _, err := re.Render(content); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Render() expected context.DeadlineExceeded, got %v", err)
		}
		// A per-call option still wins over the engine default.
		if _, err := re.Render(content, WithTimeout(renderTimeout)); err != nil {
			t.Errorf("Render() with a per-call timeout error = %v", err)
		}
		re.SetRenderTimeout(DefaultRenderTimeout)
	})

	t.Run("DisabledTimeout", func(t *testing.T) {
		re.SetRenderTimeout(0)
		defer re.SetRenderTimeout(DefaultRenderTimeout)
		if _, err := re.Render(content, WithTimeout(0)); err != nil {
			t.Errorf("Render() with the deadline disabled error = %v", err)
		}
	})
}

func TestRenderEngine_TargetCrashed(t *testing.T) {
	// The crash bookkeeping is driven purely by Inspector events, so it can be
	// exercised without a browser.
	re := &RenderEngine{}

	var reported []error
	re.SetTargetCrashedHandler(func(err error) { reported = append(reported, err) })

	if err := re.CrashError(); err != nil {
		t.Errorf("CrashError() on a healthy engine = %v, want nil", err)
	}

	for _, reason := range []inspector.DetachReason{inspector.DetachReasonTargetClosed, inspector.DetachReasonCanceledByUser} {
		re.handleTargetEvent(&inspector.EventDetached{Reason: reason})
		if err := re.CrashError(); err != nil {
			t.Errorf("CrashError() after a %q detach = %v, want nil", reason, err)
		}
	}

	re.handleTargetEvent(&inspector.EventTargetCrashed{})
	err := re.CrashError()
	if !errors.Is(err, ErrTargetCrashed) {
		t.Fatalf("CrashError() after a crash = %v, want ErrTargetCrashed", err)
	}
	if len(reported) != 1 {
		t.Fatalf("handler called %d times, want 1", len(reported))
	}

	// A repeated event carries nothing new, so it must not be reported again.
	re.handleTargetEvent(&inspector.EventTargetCrashed{})
	if len(reported) != 1 {
		t.Errorf("handler called %d times for a duplicate crash, want 1", len(reported))
	}

	// The detach that follows a crash carries the reason chrome gives.
	re.handleTargetEvent(&inspector.EventDetached{Reason: inspector.DetachReasonRenderProcessGone})
	err = re.CrashError()
	if !errors.Is(err, ErrTargetCrashed) {
		t.Fatalf("CrashError() after a crash detach = %v, want ErrTargetCrashed", err)
	}
	if !strings.Contains(err.Error(), inspector.DetachReasonRenderProcessGone.String()) {
		t.Errorf("CrashError() = %q, want it to mention %q", err, inspector.DetachReasonRenderProcessGone)
	}
	if len(reported) != 2 {
		t.Fatalf("handler called %d times, want 2", len(reported))
	}
	if !errors.Is(reported[1], ErrTargetCrashed) || !strings.Contains(reported[1].Error(), inspector.DetachReasonRenderProcessGone.String()) {
		t.Errorf("handler got %v, want a crash error mentioning the detach reason", reported[1])
	}

	// Render errors are annotated so callers can tell a dead browser apart
	// from a slow diagram.
	annotated := re.annotateCrash(context.DeadlineExceeded)
	if !errors.Is(annotated, context.DeadlineExceeded) || !errors.Is(annotated, ErrTargetCrashed) {
		t.Errorf("annotateCrash() = %v, want both the original and the crash error", annotated)
	}
	if re.annotateCrash(nil) != nil {
		t.Error("annotateCrash(nil) expected nil")
	}

	// Chrome can bring the target back; the engine must not stay poisoned.
	re.handleTargetEvent(&inspector.EventTargetReloadedAfterCrash{})
	if err := re.CrashError(); err != nil {
		t.Errorf("CrashError() after a reload = %v, want nil", err)
	}
	if got := re.annotateCrash(context.DeadlineExceeded); !errors.Is(got, context.DeadlineExceeded) || errors.Is(got, ErrTargetCrashed) {
		t.Errorf("annotateCrash() after a reload = %v, want the original error only", got)
	}

	re.SetTargetCrashedHandler(nil)
	re.handleTargetEvent(&inspector.EventTargetCrashed{})
	if len(reported) != 2 {
		t.Errorf("handler called %d times after being removed, want 2", len(reported))
	}
}

func TestRenderEngine_TargetCrashedLive(t *testing.T) {
	re, err := NewRenderEngine(context.Background(), nil, chromedp.WSURLReadTimeout(renderTimeout))
	if err != nil {
		t.Fatalf("NewRenderEngine() error = %v", err)
	}
	defer re.Cancel()

	crashed := make(chan error, 4)
	re.SetTargetCrashedHandler(func(err error) {
		select {
		case crashed <- err:
		default:
		}
	})

	// Page.crash is the documented way to kill the renderer on purpose. It is
	// far more portable than navigating to chrome://crash, whose handling
	// varies between builds. The command itself never gets a reply, since the
	// renderer it is addressed to is gone, so the deadline here is the success
	// path rather than a failure.
	crashCtx, crashCancel := context.WithTimeout(re.ctx, 10*time.Second)
	defer crashCancel()
	if err := chromedp.Run(crashCtx, chromedp.ActionFunc(page.Crash().Do)); err != nil {
		t.Logf("Page.crash returned %v (expected)", err)
	}

	select {
	case err := <-crashed:
		if !errors.Is(err, ErrTargetCrashed) {
			t.Errorf("handler got %v, want ErrTargetCrashed", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("chrome did not report a crash within 15s")
	}

	if err := re.CrashError(); !errors.Is(err, ErrTargetCrashed) {
		t.Errorf("CrashError() = %v, want ErrTargetCrashed", err)
	}

	// A render against the dead target must fail promptly and say why.
	_, err = re.Render("graph TD; A-->B;", WithTimeout(10*time.Second))
	if err == nil {
		t.Fatal("Render() on a crashed target expected an error, got nil")
	}
	if !errors.Is(err, ErrTargetCrashed) {
		t.Errorf("Render() error = %v, want it to wrap ErrTargetCrashed", err)
	}
}
