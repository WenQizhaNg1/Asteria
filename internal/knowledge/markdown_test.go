package knowledge

import (
	"strings"
	"testing"
)

func TestMarkdownRoundTripAndCodeExampleReferences(t *testing.T) {
	entry := Entry{ID: "case-a", LibraryID: "traffic", Home: "parking", Title: "中文标题", Origin: "source", Content: "# 停车\n\n正文"}
	data, err := EncodeEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEntry(data)
	if err != nil || decoded.Content != entry.Content || decoded.Title != entry.Title || decoded.Version == "" {
		t.Fatalf("Markdown round trip failed: %+v, %v", decoded, err)
	}
	link := "[证据](asteria://traffic/case-a?version=abc#queue)"
	content := link + "\n\n" + strings.Repeat("`", 3) + "text\n" + link + "\n" + strings.Repeat("`", 3)
	refs, err := Links(content)
	if err != nil || len(refs) != 1 || refs[0].Fragment != "queue" || refs[0].Version != "abc" {
		t.Fatalf("code example treated as a real reference: %+v, %v", refs, err)
	}
}

func TestMarkdownPreservesSignificantWhitespace(t *testing.T) {
	body := "    <a id=\"example\"></a>\n\n换行前的两个空格需要保留。  "
	encoded, err := EncodeEntry(Entry{Content: body})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEntry(encoded)
	if err != nil || decoded.Content != body {
		t.Fatalf("Markdown whitespace changed: %q, %v", decoded.Content, err)
	}
	if HasFragment(decoded.Content, "example") {
		t.Fatal("serialization turned indented code into an actual anchor")
	}
}
