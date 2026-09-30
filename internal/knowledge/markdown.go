package knowledge

import (
	"bytes"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"go.yaml.in/yaml/v3"
)

// NormalizeBody removes framing newlines, preserving Markdown indentation and
// trailing spaces (which can encode hard line breaks).
func NormalizeBody(content string) string { return strings.Trim(content, "\r\n") }

func EncodeEntry(e Entry) ([]byte, error) {
	meta, err := yaml.Marshal(e)
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(meta) + "---\n\n" + NormalizeBody(e.Content) + "\n"), nil
}

func DecodeEntry(data []byte) (Entry, error) {
	var e Entry
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return e, fmt.Errorf("%w: missing front matter", ErrInvalid)
	}
	end := bytes.Index(data[4:], []byte("\n---\n"))
	if end < 0 {
		return e, fmt.Errorf("%w: incomplete front matter", ErrInvalid)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data[4 : 4+end]))
	decoder.KnownFields(true)
	if err := decoder.Decode(&e); err != nil {
		return e, fmt.Errorf("%w: front matter: %v", ErrInvalid, err)
	}
	e.Content = NormalizeBody(string(data[4+end+5:]))
	e.Version = Hash(data)
	return e, nil
}

func RefURL(r Ref) string {
	u := url.URL{Scheme: "asteria", Host: r.LibraryID, Path: "/" + r.EntryID, Fragment: r.Fragment}
	q := url.Values{}
	if r.Version != "" {
		q.Set("version", r.Version)
	}
	if r.StartLine > 0 {
		q.Set("start", strconv.Itoa(r.StartLine))
		q.Set("end", strconv.Itoa(r.EndLine))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Links uses the Markdown AST so examples inside code fences aren't treated as references.
func Links(content string) ([]Ref, error) {
	source := []byte(content)
	tree := goldmark.DefaultParser().Parse(text.NewReader(source))
	var refs []Ref
	err := ast.Walk(tree, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		link, ok := n.(*ast.Link)
		if !ok || !bytes.HasPrefix(link.Destination, []byte("asteria:")) {
			return ast.WalkContinue, nil
		}
		u, err := url.Parse(string(link.Destination))
		if err != nil || u.Scheme != "asteria" || !ValidID(u.Host) || !ValidID(strings.TrimPrefix(u.Path, "/")) {
			return ast.WalkStop, fmt.Errorf("%w: malformed Asteria reference", ErrInvalid)
		}
		r := Ref{LibraryID: u.Host, EntryID: strings.TrimPrefix(u.Path, "/"), Version: u.Query().Get("version"), Fragment: u.Fragment}
		for key, dest := range map[string]*int{"start": &r.StartLine, "end": &r.EndLine} {
			if value := u.Query().Get(key); value != "" {
				*dest, err = strconv.Atoi(value)
				if err != nil {
					return ast.WalkStop, fmt.Errorf("%w: invalid reference line", ErrInvalid)
				}
			}
		}
		refs = append(refs, r)
		return ast.WalkContinue, nil
	})
	return refs, err
}

func ValidateLocation(e Entry, r Ref, evidence bool) error {
	if evidence && (r.Version == "" || (r.Fragment == "" && r.StartLine == 0 && r.Quote == "")) {
		return fmt.Errorf("%w: evidence needs a version and a fragment, lines or quote", ErrInvalid)
	}
	if r.Version != "" && r.Version != e.Version {
		return fmt.Errorf("%w: evidence version mismatch", ErrInvalid)
	}
	start, end, found := fragmentBounds(e.Content, r.Fragment)
	if !found {
		return fmt.Errorf("%w: fragment %s is missing or ambiguous", ErrInvalid, r.Fragment)
	}
	if r.StartLine < 0 || r.EndLine < 0 || (r.StartLine == 0 && r.EndLine != 0) {
		return fmt.Errorf("%w: invalid line range", ErrInvalid)
	}
	if r.StartLine > 0 {
		lines := strings.Split(e.Content, "\n")
		if r.EndLine < r.StartLine || r.EndLine > len(lines) {
			return fmt.Errorf("%w: evidence lines are outside the file", ErrInvalid)
		}
		lineStart := 0
		for _, line := range lines[:r.StartLine-1] {
			lineStart += len(line) + 1
		}
		lineEnd := lineStart + len(strings.Join(lines[r.StartLine-1:r.EndLine], "\n"))
		if lineStart < start || lineEnd > end {
			return fmt.Errorf("%w: evidence lines are outside the fragment", ErrInvalid)
		}
		start, end = lineStart, lineEnd
	}
	if r.Quote != "" && !strings.Contains(e.Content[start:end], r.Quote) {
		return fmt.Errorf("%w: evidence quote not found", ErrInvalid)
	}
	return nil
}
