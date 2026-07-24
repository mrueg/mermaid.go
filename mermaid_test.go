package mermaid_go

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

var renderTimeout = 30 * time.Second

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

	ctx1, cancel := context.WithTimeout(context.Background(), renderTimeout)
	defer cancel()
	re1, err := NewRenderEngine(ctx1, []string{`mermaid.initialize({'theme': 'base', 'themeVariables': { 'primaryColor': '#1473e6'}});`})
	if err != nil {
		t.Errorf("NewRenderEngine() error = %v", err)
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
