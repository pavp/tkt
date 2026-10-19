package adf

import (
	"encoding/json"
	"testing"
)

func doc(content string) json.RawMessage {
	return json.RawMessage(`{"type":"doc","version":1,"content":[` + content + `]}`)
}

func TestToMarkdown(t *testing.T) {
	tests := []struct {
		name string
		in   json.RawMessage
		want string
	}{
		{"null", json.RawMessage(`null`), ""},
		{"empty", nil, ""},
		{"plain string", json.RawMessage(`"  legacy text "`), "legacy text"},
		{"paragraphs and marks", doc(`
			{"type":"paragraph","content":[
				{"type":"text","text":"Make "},
				{"type":"text","text":"bold","marks":[{"type":"strong"}]},
				{"type":"text","text":", "},
				{"type":"text","text":"slanted","marks":[{"type":"em"}]},
				{"type":"text","text":", "},
				{"type":"text","text":"fn()","marks":[{"type":"code"}]},
				{"type":"text","text":" and "},
				{"type":"text","text":"a link","marks":[{"type":"link","attrs":{"href":"https://example.com"}}]}]},
			{"type":"paragraph","content":[{"type":"text","text":"line one"},{"type":"hardBreak"},{"type":"text","text":"line two"}]}`),
			"Make **bold**, _slanted_, `fn()` and [a link](https://example.com)\n\nline one\nline two"},
		{"heading", doc(`{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Acceptance"}]}`), "## Acceptance"},
		{"bullet list with nesting", doc(`{"type":"bulletList","content":[
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]},
				{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"nested"}]}]}]}]},
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}]}`),
			"- one\n  - nested\n- two"},
		{"ordered list with start", doc(`{"type":"orderedList","attrs":{"order":3},"content":[
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"three"}]}]},
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"four"}]}]}]}`),
			"3. three\n4. four"},
		{"task list", doc(`{"type":"taskList","content":[
			{"type":"taskItem","attrs":{"state":"DONE"},"content":[{"type":"text","text":"done"}]},
			{"type":"taskItem","attrs":{"state":"TODO"},"content":[{"type":"text","text":"todo"}]}]}`),
			"- [x] done\n- [ ] todo"},
		{"code block", doc(`{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x := 1\ny := 2"}]}`), "```go\nx := 1\ny := 2\n```"},
		{"blockquote", doc(`{"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"quoted"}]},{"type":"paragraph","content":[{"type":"text","text":"twice"}]}]}`), "> quoted\n>\n> twice"},
		{"panel", doc(`{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"careful"}]}]}`), "> **Warning:**\n> careful"},
		{"rule", doc(`{"type":"paragraph","content":[{"type":"text","text":"a"}]},{"type":"rule"},{"type":"paragraph","content":[{"type":"text","text":"b"}]}`), "a\n\n---\n\nb"},
		{"table", doc(`{"type":"table","content":[
			{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Field"}]}]},{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Rule"}]}]}]},
			{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"from"}]}]},{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"a|b"}]}]}]}]}`),
			"| Field | Rule |\n| --- | --- |\n| from | a\\|b |"},
		{"inline nodes", doc(`{"type":"paragraph","content":[
			{"type":"mention","attrs":{"text":"@Alex"}},{"type":"text","text":" "},
			{"type":"emoji","attrs":{"shortName":":tada:","text":"🎉"}},{"type":"text","text":" "},
			{"type":"status","attrs":{"text":"BLOCKED"}},{"type":"text","text":" "},
			{"type":"date","attrs":{"timestamp":"1760832000000"}},{"type":"text","text":" "},
			{"type":"inlineCard","attrs":{"url":"https://example.com/x"}}]}`),
			"@Alex 🎉 [BLOCKED] 2025-10-19 <https://example.com/x>"},
		{"media", doc(`{"type":"mediaSingle","content":[{"type":"media","attrs":{"alt":"mock.png","id":"abc"}}]}`), "[image: mock.png]"},
		{"unknown nodes keep their text", doc(`{"type":"futureBlock","content":[{"type":"paragraph","content":[{"type":"text","text":"still here"}]}]}`), "still here"},
		{"expand", doc(`{"type":"expand","attrs":{"title":"Details"},"content":[{"type":"paragraph","content":[{"type":"text","text":"hidden"}]}]}`), "**Details**\n\nhidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToMarkdown(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestToMarkdownRejectsGarbage(t *testing.T) {
	if _, err := ToMarkdown(json.RawMessage(`{not json`)); err == nil {
		t.Error("expected an error")
	}
}
