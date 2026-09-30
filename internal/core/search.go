package core

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"

	k "asteria/internal/knowledge"
)

type EntrySummary struct {
	Ref     k.Ref  `json:"ref"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Home    string `json:"home"`
	Origin  string `json:"origin"`
	Status  string `json:"status,omitempty"`
}

type Directory struct {
	LibraryID string         `json:"library_id"`
	Domain    *k.Domain      `json:"domain,omitempty"`
	Children  []k.Domain     `json:"children"`
	Entries   []EntrySummary `json:"entries"`
}

func summary(e k.Entry) EntrySummary {
	return EntrySummary{Ref: e.Ref(), Title: e.Title, Summary: e.Summary, Home: e.Home, Origin: e.Origin, Status: e.Status}
}

func visible(snapshot *k.Snapshot, actor k.Actor, scopes []k.Scope, ref k.Ref) bool {
	e := snapshot.Libraries[ref.LibraryID].Entries[ref.EntryID]
	return !e.Withdrawn && canRead(snapshot, actor, scopes, e)
}

func browse(snapshot *k.Snapshot, actor k.Actor, scopes []k.Scope, location k.Scope) (Directory, error) {
	lib, exists := snapshot.Libraries[location.LibraryID]
	result := Directory{LibraryID: location.LibraryID}
	if !exists || !actor.CanSeeLibrary(location.LibraryID) {
		return result, fmt.Errorf("%w: domain", k.ErrNotFound)
	}
	for _, domain := range lib.Schema.Domains {
		if domain.ID == location.DomainID && actor.Allows(lib.Schema, domain.ID) && k.InScope(lib.Schema, domain.ID, scopes) {
			d := domain
			result.Domain = &d
		}
		if domain.Parent == location.DomainID && actor.Allows(lib.Schema, domain.ID) && k.InScope(lib.Schema, domain.ID, scopes) {
			result.Children = append(result.Children, domain)
		}
	}
	if location.DomainID != "" && result.Domain == nil {
		return result, fmt.Errorf("%w: domain", k.ErrNotFound)
	}
	for _, e := range lib.Entries {
		if visible(snapshot, actor, scopes, e.Ref()) && (e.Home == location.DomainID || slices.Contains(e.Entrances, location.DomainID)) {
			result.Entries = append(result.Entries, summary(e))
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Ref.Key() < result.Entries[j].Ref.Key() })
	sort.Slice(result.Children, func(i, j int) bool { return result.Children[i].ID < result.Children[j].ID })
	return result, nil
}

func (s *Service) Browse(actor k.Actor, location k.Scope) (Directory, error) {
	snapshot, err := s.Store.Snapshot()
	if err != nil {
		return Directory{}, err
	}
	return browse(snapshot, actor, nil, location)
}

// Lexical retrieval is deliberately small: words plus CJK bigrams. It provides
// navigation seeds without adding a search server or treating similarity as fact.
func terms(input string) []string {
	set := map[string]bool{}
	var run []rune
	flush := func() {
		if len(run) == 0 {
			return
		}
		word := string(run)
		set[word] = true
		if unicode.Is(unicode.Han, run[0]) {
			for i := 0; i+1 < len(run); i++ {
				set[string(run[i:i+2])] = true
			}
		}
		run = nil
	}
	for _, r := range strings.ToLower(input) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if len(run) > 0 && unicode.Is(unicode.Han, r) != unicode.Is(unicode.Han, run[0]) {
			flush()
		}
		run = append(run, r)
	}
	flush()
	result := make([]string, 0, len(set))
	for term := range set {
		result = append(result, term)
	}
	sort.Strings(result)
	return result
}

func search(snapshot *k.Snapshot, actor k.Actor, scopes []k.Scope, question string, limit int) []EntrySummary {
	type hit struct {
		entry k.Entry
		score int
	}
	var hits []hit
	words := terms(question)
	for _, lib := range snapshot.Libraries {
		for _, e := range lib.Entries {
			if !visible(snapshot, actor, scopes, e.Ref()) {
				continue
			}
			score := 0
			for _, term := range words {
				if strings.Contains(strings.ToLower(e.Title), term) {
					score += 4
				}
				if strings.Contains(strings.ToLower(e.Summary), term) {
					score += 2
				}
				if strings.Contains(strings.ToLower(e.Content), term) {
					score++
				}
			}
			if score > 0 || len(words) == 0 {
				hits = append(hits, hit{e, score})
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].entry.Ref().Key() < hits[j].entry.Ref().Key()
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	result := make([]EntrySummary, 0, len(hits))
	for _, h := range hits {
		result = append(result, summary(h.entry))
	}
	return result
}
