package jira

import (
	"encoding/json"
	"strings"
)

// redactPath strips any query values that might carry sensitive data (defensive; Jira paths don't
// carry secrets, but we never want a token or PII in a log).
func redactPath(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i] + "?…"
	}
	return p
}

// parseErrorMessages extracts human-readable messages from a Jira error body.
func parseErrorMessages(raw []byte) []string {
	var e struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		s := strings.TrimSpace(string(raw))
		if s == "" {
			return []string{"(no body)"}
		}
		return []string{s}
	}
	msgs := append([]string{}, e.ErrorMessages...)
	for k, v := range e.Errors {
		msgs = append(msgs, k+": "+v)
	}
	if len(msgs) == 0 {
		return []string{"(unspecified error)"}
	}
	return msgs
}

// adfDoc wraps plain text in a minimal Atlassian Document Format document (required for comments and
// text fields on Jira Cloud v3).
func adfDoc(text string) map[string]any {
	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []any{
			map[string]any{
				"type":    "paragraph",
				"content": []any{map[string]any{"type": "text", "text": text}},
			},
		},
	}
}

// adfToText flattens an ADF document (or a plain string) into text, concatenating all text nodes with
// newlines between paragraphs. Deterministic and dependency-free.
func adfToText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// description may be a plain string (v2) or an ADF object (v3)
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var node adfNode
	if err := json.Unmarshal(raw, &node); err != nil {
		return ""
	}
	var b strings.Builder
	walkADF(&node, &b)
	return strings.TrimSpace(b.String())
}

type adfNode struct {
	Type    string    `json:"type"`
	Text    string    `json:"text"`
	Content []adfNode `json:"content"`
}

func walkADF(n *adfNode, b *strings.Builder) {
	if n.Text != "" {
		b.WriteString(n.Text)
	}
	for i := range n.Content {
		walkADF(&n.Content[i], b)
	}
	// paragraph / heading boundaries -> newline
	switch n.Type {
	case "paragraph", "heading", "listItem", "codeBlock":
		b.WriteString("\n")
	}
}

// extractAC returns the text following an "Acceptance Criteria" heading (case-insensitive), up to the
// next blank-line-separated section; if no such heading exists, returns the full text.
func extractAC(desc string) string {
	lines := strings.Split(desc, "\n")
	for i, ln := range lines {
		norm := strings.ToLower(strings.TrimSpace(strings.Trim(ln, "#* :")))
		if norm == "acceptance criteria" || norm == "acceptance criteria:" {
			var section []string
			for _, s := range lines[i+1:] {
				section = append(section, s)
			}
			out := strings.TrimSpace(strings.Join(section, "\n"))
			if out != "" {
				return out
			}
		}
	}
	return strings.TrimSpace(desc)
}
