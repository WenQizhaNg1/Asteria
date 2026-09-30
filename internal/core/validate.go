package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	k "asteria/internal/knowledge"
)

func prettyJSON(value any) string {
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data)
}

func (s *Service) lookup(snapshot *k.Snapshot, ref k.Ref) (k.Entry, error) {
	lib, exists := snapshot.Libraries[ref.LibraryID]
	if !exists {
		return k.Entry{}, fmt.Errorf("%w: referenced library", k.ErrNotFound)
	}
	current, exists := lib.Entries[ref.EntryID]
	if !exists {
		return k.Entry{}, fmt.Errorf("%w: referenced entry", k.ErrNotFound)
	}
	if ref.Version == "" || ref.Version == current.Version {
		return current, nil
	}
	return s.Store.ReadVersion(ref)
}

func (s *Service) validate(snapshot *k.Snapshot, actor k.Actor, changes []k.Change) []string {
	var issues []string
	for _, lib := range snapshot.Libraries {
		for _, e := range lib.Entries {
			if _, err := k.DomainPath(lib.Schema, e.Home); err != nil {
				issues = append(issues, "schema would orphan entry "+e.ID)
			}
			for _, entrance := range e.Entrances {
				if _, err := k.DomainPath(lib.Schema, entrance); err != nil {
					issues = append(issues, "schema would orphan an entry entrance")
				}
			}
			refs, err := k.Links(e.Content)
			if err != nil {
				issues = append(issues, "a committed Markdown reference is malformed")
			}
			for _, relation := range e.Relations {
				refs = append(refs, relation.Subject, relation.Object)
			}
			for _, ref := range refs {
				if ref.Version != "" {
					continue
				}
				target, exists := snapshot.Libraries[ref.LibraryID].Entries[ref.EntryID]
				if !exists || !k.HasFragment(target.Content, ref.Fragment) {
					issues = append(issues, "change would break an existing definition reference")
					break
				}
			}
		}
	}
	for _, change := range changes {
		if change.Kind != "entry" {
			continue
		}
		entry := snapshot.Libraries[change.Entry.LibraryID].Entries[change.Entry.ID]
		if err := s.validateEntry(snapshot, actor, entry); err != nil {
			issues = append(issues, entry.ID+": "+err.Error())
		}
	}
	return issues
}

func (s *Service) validateEntry(snapshot *k.Snapshot, actor k.Actor, e k.Entry) error {
	if !utf8.ValidString(e.Content) || len(e.Content) > 2<<20 {
		return fmt.Errorf("%w: Markdown must be UTF-8 and at most 2 MiB", k.ErrInvalid)
	}
	switch e.Origin {
	case "source", "extracted":
		if len(e.Sources) == 0 {
			return fmt.Errorf("%w: original material needs a source snapshot", k.ErrInvalid)
		}
	case "inferred":
		if e.Status != "candidate" && e.Status != "needs_review" && e.Status != "rejected" {
			return fmt.Errorf("%w: insight status must remain candidate, needs_review or rejected", k.ErrInvalid)
		}
		if len(e.Evidence) == 0 {
			return fmt.Errorf("%w: insight needs version-bound evidence", k.ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown entry origin", k.ErrInvalid)
	}
	for _, source := range e.Sources {
		if source.ID == "" || strings.TrimSpace(source.Origin) == "" || source.CapturedAt == "" {
			return fmt.Errorf("%w: incomplete source", k.ErrInvalid)
		}
		if _, err := s.Store.Source(source); err != nil {
			return fmt.Errorf("%w: source snapshot missing or corrupt", k.ErrInvalid)
		}
	}
	check := func(ref k.Ref, evidence bool) error {
		target, err := s.readEntry(snapshot, actor, nil, ref)
		if err != nil {
			return fmt.Errorf("%w: inaccessible or unresolved reference", k.ErrInvalid)
		}
		return k.ValidateLocation(target, ref, evidence)
	}
	links, err := k.Links(e.Content)
	if err != nil {
		return err
	}
	for _, ref := range links {
		if err := check(ref, false); err != nil {
			return err
		}
	}
	for _, ref := range e.Evidence {
		if err := check(ref, true); err != nil {
			return err
		}
	}
	if e.Origin == "inferred" {
		if err := validateEvidenceChain(e.Evidence, func(ref k.Ref) (k.Entry, error) { return s.readEntry(snapshot, actor, nil, ref) }); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, relation := range e.Relations {
		if !k.ValidID(relation.ID) || seen[relation.ID] || !k.HasFragment(e.Content, relation.ID) {
			return fmt.Errorf("%w: relations need unique, stable Markdown anchors", k.ErrInvalid)
		}
		seen[relation.ID] = true
		if relation.Predicate == "" || relation.Conditions == "" || len(relation.Evidence) == 0 {
			return fmt.Errorf("%w: relations need a predicate, conditions and evidence", k.ErrInvalid)
		}
		if relation.Origin != "inferred" && relation.Origin != "extracted" && relation.Origin != "predefined" {
			return fmt.Errorf("%w: invalid relation origin", k.ErrInvalid)
		}
		switch relation.Status {
		case "candidate", "needs_review", "rejected":
		case "verified":
			if relation.Reviewer == "" || relation.ReviewedAt == "" || relation.ReviewNote == "" {
				return fmt.Errorf("%w: verification requires a reviewer, date and review note", k.ErrInvalid)
			}
			for _, ref := range relation.Evidence {
				current := snapshot.Libraries[ref.LibraryID].Entries[ref.EntryID]
				if current.Withdrawn || ref.Version != current.Version {
					return fmt.Errorf("%w: verification evidence is stale or withdrawn", k.ErrInvalid)
				}
			}
		default:
			return fmt.Errorf("%w: invalid relation status", k.ErrInvalid)
		}
		if relation.Status == "rejected" && relation.ReviewNote == "" {
			return fmt.Errorf("%w: rejection needs a reason", k.ErrInvalid)
		}
		if err := check(relation.Subject, false); err != nil {
			return err
		}
		if err := check(relation.Object, false); err != nil {
			return err
		}
		for _, ref := range relation.Evidence {
			if err := check(ref, true); err != nil {
				return err
			}
		}
		if err := validateEvidenceChain(relation.Evidence, func(ref k.Ref) (k.Entry, error) { return s.readEntry(snapshot, actor, nil, ref) }); err != nil {
			return err
		}
	}
	return nil
}

// validateEvidenceChain follows versioned evidence rather than narrative links.
// A closed cycle of model-generated insights cannot substitute for a source.
func validateEvidenceChain(refs []k.Ref, lookup func(k.Ref) (k.Entry, error)) error {
	visited := map[string]bool{}
	hasSource := false
	var walk func(k.Ref) error
	walk = func(ref k.Ref) error {
		key := ref.Key() + "@" + ref.Version
		if visited[key] {
			return nil
		}
		visited[key] = true
		if ref.Version == "" {
			return fmt.Errorf("%w: evidence must identify a version", k.ErrInvalid)
		}
		e, err := lookup(ref)
		if err != nil {
			return fmt.Errorf("%w: cannot trace evidence to original material", k.ErrInvalid)
		}
		if err := k.ValidateLocation(e, ref, true); err != nil {
			return err
		}
		if e.Origin == "source" || e.Origin == "extracted" {
			hasSource = hasSource || len(e.Sources) > 0
			return nil
		}
		for _, evidence := range e.Evidence {
			if err := walk(evidence); err != nil {
				return err
			}
		}
		return nil
	}
	for _, ref := range refs {
		if err := walk(ref); err != nil {
			return err
		}
	}
	if !hasSource {
		return fmt.Errorf("%w: no original evidence; circular or unsupported inference", k.ErrInvalid)
	}
	return nil
}
