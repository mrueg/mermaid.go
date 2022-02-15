package mermaid_go

import (
	"context"
	"strings"
	"testing"
)

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
		{content: `gitGraph:
options
{
    "nodeSpacing": 150,
    "nodeRadius": 10
}
end
commit
branch newbranch
checkout newbranch
commit
commit
checkout master
commit
commit
merge newbranch`},
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
    A-->;`, err_has_prefix: "exception \"Uncaught\""},
	}

	ctx1 := context.Background()
	re1 := NewRenderEngine(ctx1)
	defer re1.Cancel()
	for _, tt := range cases {
		t.Run("", func(t *testing.T) {
			got, err := re1.Render(tt.content)
			if err != nil {
				if strings.HasPrefix(err.Error(), tt.err_has_prefix) {
					return
				}
				t.Errorf("Render() error = %v", err)
				return
			}
			if !strings.HasPrefix(got, "<svg") {
				t.Errorf("Render() got = %v", got)
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
	re1 := NewRenderEngine(ctx1)
	_ = re1.Init()
	for i := 0; i < b.N; i++ {
		_, _ = re1.Render(case1)
	}
	re1.Cancel()
}