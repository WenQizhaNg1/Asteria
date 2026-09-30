package knowledge

import "testing"

func TestFragmentsExcludeCodeAndAmbiguousAnchors(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want          bool
	}{
		{"html", "<a id=\"capacity\"></a>\n## 容量\n正文", true},
		{"comment", "<!-- id: capacity -->\n## 容量\n正文", true},
		{"fenced", "```html\n<a id=\"capacity\"></a>\n```", false},
		{"indented", "    <a id=\"capacity\"></a>", false},
		{"inline code", "示例 `<a id=\"capacity\"></a>`", false},
		{"comment code", "`<!-- id: capacity -->`", false},
		{"escaped", "\\<a id=\"capacity\"></a>", false},
		{"inside comment", "<!-- example: <a id=\"capacity\"></a> -->", false},
		{"script", "<script>\nconst example = '<a id=\"capacity\"></a>';\n</script>", false},
		{"duplicate", "<a id=\"capacity\"></a>\n\n<!-- id: capacity -->", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasFragment(tc.content, "capacity"); got != tc.want {
				t.Fatalf("HasFragment = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvidenceMustStayInsideCitedFragment(t *testing.T) {
	entry := Entry{Version: "v1", Content: "<a id=\"capacity\"></a>\n## 容量\n接驳运力需要核实。\n### 失效条件\n候车区也有容量约束。\n## 其他\n这段在另一个小节。\n\n<!-- id: next -->\n下一处证据。"}
	for _, tc := range []struct {
		name  string
		ref   Ref
		valid bool
	}{
		{"section", Ref{Fragment: "capacity", Quote: "接驳运力需要核实。"}, true},
		{"subsection", Ref{Fragment: "capacity", Quote: "候车区也有容量约束。"}, true},
		{"wrong section", Ref{Fragment: "capacity", Quote: "这段在另一个小节。"}, false},
		{"bounded lines", Ref{Fragment: "capacity", StartLine: 3, EndLine: 5, Quote: "容量约束"}, true},
		{"lines escape", Ref{Fragment: "capacity", StartLine: 3, EndLine: 7}, false},
		{"quote escapes lines", Ref{Fragment: "capacity", StartLine: 3, EndLine: 3, Quote: "容量约束"}, false},
		{"comment section", Ref{Fragment: "next", Quote: "下一处证据。"}, true},
		{"quote only", Ref{Quote: "这段在另一个小节。"}, true},
		{"missing fragment", Ref{Fragment: "missing"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.ref.Version = entry.Version
			if err := ValidateLocation(entry, tc.ref, true); (err == nil) != tc.valid {
				t.Fatalf("ValidateLocation = %v, valid = %v", err, tc.valid)
			}
		})
	}
}

func TestNextAnchorBoundsFragmentWithoutHeadings(t *testing.T) {
	entry := Entry{Version: "v1", Content: "<!-- id: first -->\n第一处证据。\n\n<!-- id: second -->\n第二处证据。"}
	ref := Ref{Version: entry.Version, Fragment: "first", Quote: "第二处证据。"}
	if err := ValidateLocation(entry, ref, true); err == nil {
		t.Fatal("quote in next anchor's content was accepted")
	}
}
