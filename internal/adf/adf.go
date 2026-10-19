// Package adf converts Atlassian Document Format (Jira's rich text) to Markdown.
package adf

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Node struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Attrs   map[string]any `json:"attrs"`
	Marks   []Mark         `json:"marks"`
	Content []Node         `json:"content"`
}

type Mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

// ToMarkdown converts an ADF document. Plain strings (older fields, custom text fields) pass through,
// and an empty or null value yields "".
func ToMarkdown(raw json.RawMessage) (string, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return "", nil
	}
	if strings.HasPrefix(s, `"`) {
		var plain string
		if err := json.Unmarshal(raw, &plain); err != nil {
			return "", err
		}
		return strings.TrimSpace(plain), nil
	}
	var doc Node
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("parse ADF: %w", err)
	}
	var r renderer
	r.blocks(doc.Content, "")
	return strings.TrimSpace(r.b.String()), nil
}

type renderer struct {
	b strings.Builder
}

// blocks renders block nodes separated by blank lines; prefix starts every line (for quotes and lists).
func (r *renderer) blocks(nodes []Node, prefix string) {
	for i, n := range nodes {
		if i > 0 {
			r.b.WriteString(strings.TrimRight(prefix, " ") + "\n")
		}
		r.block(n, prefix)
	}
}

func (r *renderer) line(prefix, s string) {
	for _, l := range strings.Split(s, "\n") {
		r.b.WriteString(prefix + l + "\n")
	}
}

func (r *renderer) block(n Node, prefix string) {
	switch n.Type {
	case "paragraph":
		r.line(prefix, inline(n.Content))
	case "heading":
		level := intAttr(n.Attrs, "level", 1)
		if level < 1 || level > 6 {
			level = 1
		}
		r.line(prefix, strings.Repeat("#", level)+" "+inline(n.Content))
	case "bulletList":
		r.list(n.Content, prefix, func(int) string { return "- " })
	case "orderedList":
		start := intAttr(n.Attrs, "order", 1)
		r.list(n.Content, prefix, func(i int) string { return fmt.Sprintf("%d. ", start+i) })
	case "taskList":
		r.list(n.Content, prefix, nil)
	case "codeBlock":
		lang, _ := n.Attrs["language"].(string)
		r.b.WriteString(prefix + "```" + lang + "\n")
		r.line(prefix, plainText(n.Content))
		r.b.WriteString(prefix + "```\n")
	case "blockquote":
		r.blocks(n.Content, prefix+"> ")
	case "panel":
		kind, _ := n.Attrs["panelType"].(string)
		if kind == "" {
			kind = "info"
		}
		r.b.WriteString(prefix + "> **" + strings.ToUpper(kind[:1]) + kind[1:] + ":**\n")
		r.blocks(n.Content, prefix+"> ")
	case "rule":
		r.b.WriteString(prefix + "---\n")
	case "table":
		r.table(n, prefix)
	case "mediaSingle", "mediaGroup":
		for _, m := range n.Content {
			r.line(prefix, media(m))
		}
	case "media":
		r.line(prefix, media(n))
	case "expand", "nestedExpand":
		if title, _ := n.Attrs["title"].(string); title != "" {
			r.line(prefix, "**"+title+"**")
			r.b.WriteString(strings.TrimRight(prefix, " ") + "\n")
		}
		r.blocks(n.Content, prefix)
	case "blockCard", "embedCard":
		r.line(prefix, cardLink(n.Attrs))
	default:
		if len(n.Content) > 0 {
			r.blocks(n.Content, prefix)
		} else if n.Text != "" {
			r.line(prefix, n.Text)
		}
	}
}

// list renders items with a marker; a nil marker means task items, whose checkbox comes from their state.
func (r *renderer) list(items []Node, prefix string, marker func(int) string) {
	for i, item := range items {
		m := ""
		if marker != nil {
			m = marker(i)
		} else if s, _ := item.Attrs["state"].(string); s == "DONE" {
			m = "- [x] "
		} else {
			m = "- [ ] "
		}
		indent := prefix + strings.Repeat(" ", len(m))
		if item.Type == "taskItem" {
			r.b.WriteString(prefix + m + inline(item.Content) + "\n")
			continue
		}
		for j, child := range item.Content {
			switch {
			case j == 0 && child.Type == "paragraph":
				first, rest, _ := strings.Cut(inline(child.Content), "\n")
				r.b.WriteString(prefix + m + first + "\n")
				if rest != "" {
					r.line(indent, rest)
				}
			case j == 0:
				r.b.WriteString(prefix + strings.TrimRight(m, " ") + "\n")
				r.block(child, indent)
			default:
				r.block(child, indent)
			}
		}
	}
}

func (r *renderer) table(n Node, prefix string) {
	var rows [][]string
	header := false
	for i, row := range n.Content {
		var cells []string
		for _, cell := range row.Content {
			if i == 0 && cell.Type == "tableHeader" {
				header = true
			}
			var parts []string
			for _, p := range cell.Content {
				parts = append(parts, inline(p.Content))
			}
			cells = append(cells, strings.ReplaceAll(strings.Join(parts, " "), "|", `\|`))
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	emit := func(row []string) {
		for len(row) < width {
			row = append(row, "")
		}
		r.b.WriteString(prefix + "| " + strings.Join(row, " | ") + " |\n")
	}
	sep := make([]string, width)
	for i := range sep {
		sep[i] = "---"
	}
	if header {
		emit(rows[0])
		emit(sep)
		rows = rows[1:]
	} else {
		emit(make([]string, width))
		emit(sep)
	}
	for _, row := range rows {
		emit(row)
	}
}

func inline(nodes []Node) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n.Type {
		case "text":
			b.WriteString(applyMarks(n.Text, n.Marks))
		case "hardBreak":
			b.WriteString("\n")
		case "mention":
			b.WriteString(stringAttr(n.Attrs, "text", "@someone"))
		case "emoji":
			if t := stringAttr(n.Attrs, "text", ""); t != "" {
				b.WriteString(t)
			} else {
				b.WriteString(stringAttr(n.Attrs, "shortName", ""))
			}
		case "inlineCard":
			b.WriteString(cardLink(n.Attrs))
		case "status":
			b.WriteString("[" + stringAttr(n.Attrs, "text", "") + "]")
		case "date":
			b.WriteString(dateAttr(n.Attrs))
		default:
			if len(n.Content) > 0 {
				b.WriteString(inline(n.Content))
			} else {
				b.WriteString(n.Text)
			}
		}
	}
	return b.String()
}

func applyMarks(text string, marks []Mark) string {
	if text == "" {
		return ""
	}
	var link string
	for _, m := range marks {
		switch m.Type {
		case "code":
			text = "`" + text + "`"
		case "strong":
			text = "**" + text + "**"
		case "em":
			text = "_" + text + "_"
		case "strike":
			text = "~~" + text + "~~"
		case "link":
			link = stringAttr(m.Attrs, "href", "")
		}
	}
	if link != "" {
		return "[" + text + "](" + link + ")"
	}
	return text
}

func plainText(nodes []Node) string {
	var b strings.Builder
	for _, n := range nodes {
		if n.Type == "hardBreak" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(n.Text)
		b.WriteString(plainText(n.Content))
	}
	return b.String()
}

func media(n Node) string {
	name := stringAttr(n.Attrs, "alt", "")
	if name == "" {
		name = stringAttr(n.Attrs, "id", "attachment")
	}
	return "[image: " + name + "]"
}

func cardLink(attrs map[string]any) string {
	u := stringAttr(attrs, "url", "")
	if u == "" {
		return ""
	}
	return "<" + u + ">"
}

func stringAttr(attrs map[string]any, key, def string) string {
	if v, ok := attrs[key].(string); ok && v != "" {
		return v
	}
	return def
}

func intAttr(attrs map[string]any, key string, def int) int {
	if v, ok := attrs[key].(float64); ok {
		return int(v)
	}
	return def
}

func dateAttr(attrs map[string]any) string {
	ts := stringAttr(attrs, "timestamp", "")
	var ms int64
	if _, err := fmt.Sscan(ts, &ms); err != nil || ms == 0 {
		return ts
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02")
}
