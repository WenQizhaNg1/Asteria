package knowledge

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var anchorPattern = regexp.MustCompile(`<a\s+id=["']([a-zA-Z0-9_-]+)["']\s*></a>|<!--\s*id:\s*([a-zA-Z0-9_-]+)\s*-->`)

func HasFragment(content, id string) bool {
	_, _, found := fragmentBounds(content, id)
	return found
}

// A fragment ends at the next stable anchor or the next heading of the same
// or higher level. An anchor directly before a heading belongs to that heading.
// Only actual Markdown HTML nodes can define anchors, never code examples.
func fragmentBounds(content, id string) (int, int, bool) {
	if id == "" {
		return 0, len(content), true
	}
	type heading struct{ start, end, level int }
	var headings []heading
	htmlStarts := map[int]bool{}
	source := []byte(content)
	tree := goldmark.DefaultParser().Parse(text.NewReader(source))
	_ = ast.Walk(tree, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.RawHTML:
			if node.Segments.Len() > 0 {
				htmlStarts[node.Segments.At(0).Start] = true
			}
		case *ast.HTMLBlock:
			if node.Lines().Len() > 0 {
				segment := node.Lines().At(0)
				start := segment.Start
				for start < segment.Stop && (content[start] == ' ' || content[start] == '\t') {
					start++
				}
				htmlStarts[start] = true
			}
		case *ast.Heading:
			if node.Lines().Len() > 0 {
				start := node.Lines().At(0).Start
				start = strings.LastIndexByte(content[:start], '\n') + 1
				headings = append(headings, heading{start, node.Lines().At(node.Lines().Len() - 1).Stop, node.Level})
			}
		}
		return ast.WalkContinue, nil
	})
	type anchor struct {
		start, end int
		id         string
	}
	var anchors []anchor
	var target *anchor
	for _, match := range anchorPattern.FindAllStringSubmatchIndex(content, -1) {
		if !htmlStarts[match[0]] {
			continue
		}
		idStart, idEnd := match[2], match[3]
		if idStart < 0 {
			idStart, idEnd = match[4], match[5]
		}
		a := anchor{match[0], match[1], content[idStart:idEnd]}
		anchors = append(anchors, a)
		if a.id == id {
			if target != nil {
				return 0, 0, false
			}
			target = &a
		}
	}
	if target == nil {
		return 0, 0, false
	}
	start, end := target.start, len(content)
	for _, a := range anchors {
		if a.start > start {
			end = a.start
			break
		}
	}
	level := 6
	owner := -1
	for i, h := range headings {
		if h.start <= start && start < h.end || h.start >= target.end && strings.TrimSpace(content[target.end:h.start]) == "" {
			level, owner = h.level, i
			break
		}
	}
	for i, h := range headings {
		if i != owner && h.start > start && h.start < end && h.level <= level {
			end = h.start
			break
		}
	}
	return start, end, true
}
