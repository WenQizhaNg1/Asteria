package core

import (
	"fmt"
	"slices"
	"sort"

	k "asteria/internal/knowledge"
)

// Current permissions govern both the current file and any historical version.
func canRead(snapshot *k.Snapshot, actor k.Actor, scopes []k.Scope, entry k.Entry) bool {
	lib, exists := snapshot.Libraries[entry.LibraryID]
	current, found := lib.Entries[entry.ID]
	return exists && found && actor.Allows(lib.Schema, current.Home) && actor.Allows(lib.Schema, entry.Home) &&
		k.InScope(lib.Schema, current.Home, scopes) && k.InScope(lib.Schema, entry.Home, scopes)
}

func (s *Service) readEntry(snapshot *k.Snapshot, actor k.Actor, scopes []k.Scope, ref k.Ref) (k.Entry, error) {
	if !canRead(snapshot, actor, scopes, snapshot.Libraries[ref.LibraryID].Entries[ref.EntryID]) {
		return k.Entry{}, fmt.Errorf("%w: entry", k.ErrNotFound)
	}
	entry, err := s.lookup(snapshot, ref)
	if err != nil {
		return k.Entry{}, err
	}
	if !canRead(snapshot, actor, scopes, entry) {
		return k.Entry{}, fmt.Errorf("%w: historical entry", k.ErrNotFound)
	}
	return entry, nil
}

func accessibleSchemas(snapshot *k.Snapshot, actor k.Actor, scopes []k.Scope) []k.Schema {
	var schemas []k.Schema
	for _, lib := range snapshot.Libraries {
		if !actor.CanSeeLibrary(lib.Schema.LibraryID) {
			continue
		}
		schema := lib.Schema
		schema.Domains = nil
		for _, domain := range lib.Schema.Domains {
			if actor.Allows(lib.Schema, domain.ID) && k.InScope(lib.Schema, domain.ID, scopes) {
				schema.Domains = append(schema.Domains, domain)
			}
		}
		if len(schema.Domains) > 0 {
			schemas = append(schemas, schema)
		}
	}
	sort.Slice(schemas, func(i, j int) bool { return schemas[i].LibraryID < schemas[j].LibraryID })
	return schemas
}

func hasLibrary(schemas []k.Schema, id string) bool {
	for _, schema := range schemas {
		if schema.LibraryID == id {
			return true
		}
	}
	return false
}

// Whole-schema replacements must fit the requested scope before and after the
// change; broad actor grants cannot override a narrower operation scope.
func schemaInScope(before, after k.Schema, scopes []k.Scope) bool {
	for _, schema := range []k.Schema{before, after} {
		for _, domain := range schema.Domains {
			if !k.InScope(schema, domain.ID, scopes) {
				return false
			}
		}
	}
	return true
}

func authorizeChanges(snapshot *k.Snapshot, actor k.Actor, changes []k.Change) error {
	for _, change := range changes {
		switch change.Kind {
		case "schema":
			if change.Schema == nil || !actor.ManagesLibrary(change.Schema.LibraryID) {
				return fmt.Errorf("%w: schema proposals require whole-library access", k.ErrForbidden)
			}
			if _, ok := snapshot.Libraries[change.Schema.LibraryID]; !ok {
				return fmt.Errorf("%w: initialize a library first", k.ErrNotFound)
			}
		case "entry":
			if change.Entry == nil {
				return fmt.Errorf("%w: missing entry", k.ErrInvalid)
			}
			e := change.Entry
			lib, ok := snapshot.Libraries[e.LibraryID]
			if !ok || !actor.Allows(lib.Schema, e.Home) {
				return fmt.Errorf("%w: entry scope", k.ErrForbidden)
			}
			old, exists := lib.Entries[e.ID]
			if exists && !actor.Allows(lib.Schema, old.Home) {
				return fmt.Errorf("%w: existing entry scope", k.ErrForbidden)
			}
			for _, relation := range e.Relations {
				if actor.Human || relation.Status != "verified" {
					continue
				}
				retained := k.NormalizeBody(e.Content) == old.Content && slices.ContainsFunc(old.Relations, func(previous k.Relation) bool {
					return k.Digest(previous) == k.Digest(relation)
				})
				if !retained {
					return fmt.Errorf("%w: agents cannot create or alter verified relations", k.ErrForbidden)
				}
			}
		default:
			return fmt.Errorf("%w: unknown change kind", k.ErrInvalid)
		}
	}
	return nil
}
