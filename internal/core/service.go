// Package core is the shared application service used by CLI, HTTP and MCP.
package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	k "asteria/internal/knowledge"
	"asteria/internal/store"
)

type Service struct {
	Store *store.Store
	Model Model
}

func New(storage *store.Store, model Model) *Service {
	return &Service{Store: storage, Model: model}
}

func (s *Service) Init(ctx context.Context, actor k.Actor, schema k.Schema) (*k.Snapshot, error) {
	if !actor.Admin || !actor.Human {
		return nil, fmt.Errorf("%w: initialization requires a human administrator", k.ErrForbidden)
	}
	if err := k.ValidateSchema(schema); err != nil {
		return nil, err
	}
	snapshot, err := s.Store.Snapshot()
	if err != nil {
		return nil, err
	}
	if current, exists := snapshot.Libraries[schema.LibraryID]; exists {
		if k.Digest(current.Schema) == k.Digest(schema) {
			return snapshot, nil
		}
		return nil, fmt.Errorf("%w: library exists; import schema as a reviewed revision", k.ErrConflict)
	}
	next, err := s.Store.Publish(ctx, snapshot.Commit.ID, actor.ID, "", "", "initialize "+schema.LibraryID, func(state *k.Snapshot) ([]string, error) {
		state.Libraries[schema.LibraryID] = k.Library{Schema: schema, Entries: map[string]k.Entry{}}
		return []string{schema.LibraryID + "/schema"}, nil
	})
	if err == nil {
		// Initialization is already durable; a failed derived index is rebuildable.
		_ = s.Store.Reindex(ctx, next)
	}
	return next, err
}

func (s *Service) Libraries(actor k.Actor) ([]k.Schema, error) {
	snapshot, err := s.Store.Snapshot()
	if err != nil {
		return nil, err
	}
	return accessibleSchemas(snapshot, actor, nil), nil
}

func (s *Service) Read(actor k.Actor, ref k.Ref) (k.Entry, error) {
	snapshot, err := s.Store.Snapshot()
	if err != nil {
		return k.Entry{}, err
	}
	return s.readEntry(snapshot, actor, nil, ref)
}

func (s *Service) Resolve(actor k.Actor, target string) (k.Entry, error) {
	snapshot, err := s.Store.Snapshot()
	if err != nil {
		return k.Entry{}, err
	}
	return resolve(snapshot, actor, target)
}

func (s *Service) Proposal(actor k.Actor, id string) (k.Proposal, error) {
	p, err := s.Store.Proposal(id)
	if err != nil {
		return p, err
	}
	snapshot, err := s.Store.Snapshot()
	if err != nil {
		return p, err
	}
	if err := authorizeChanges(snapshot, actor, p.Changes); err != nil {
		return k.Proposal{}, fmt.Errorf("%w: proposal", k.ErrNotFound)
	}
	if !actor.Admin {
		var visibleDiff []k.FileDiff
		for _, diff := range p.Diff {
			libraryID, filename, _ := strings.Cut(diff.Path, "/")
			lib, exists := snapshot.Libraries[libraryID]
			if exists && filename == "schema.json" && actor.ManagesLibrary(libraryID) {
				visibleDiff = append(visibleDiff, diff)
				continue
			}
			entryID := strings.TrimSuffix(filename, ".md")
			entry, found := lib.Entries[entryID]
			if exists && found && actor.Allows(lib.Schema, entry.Home) {
				visibleDiff = append(visibleDiff, diff)
				continue
			}
			// A newly proposed file has no current entry yet.
			for _, change := range p.Changes {
				if change.Entry != nil && change.Entry.LibraryID == libraryID && change.Entry.ID == entryID && actor.Allows(lib.Schema, change.Entry.Home) {
					visibleDiff = append(visibleDiff, diff)
					break
				}
			}
		}
		p.Diff = visibleDiff
	}
	return p, nil
}

func (s *Service) Proposals(actor k.Actor) ([]k.Proposal, error) {
	ids, err := s.Store.ProposalIDs()
	if err != nil {
		return nil, err
	}
	var result []k.Proposal
	for _, id := range ids {
		p, err := s.Proposal(actor, id)
		if err == nil {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt > result[j].CreatedAt })
	return result, nil
}

func (s *Service) Approve(ctx context.Context, actor k.Actor, id, version string) (k.CommitResult, error) {
	if !actor.Human {
		return k.CommitResult{}, fmt.Errorf("%w: only humans can approve proposals", k.ErrForbidden)
	}
	p, err := s.Proposal(actor, id)
	if err != nil {
		return k.CommitResult{}, err
	}
	if version == "" || p.Version != version {
		return k.CommitResult{}, fmt.Errorf("%w: approval must include the displayed proposal version", k.ErrConflict)
	}
	return s.commit(ctx, actor, p, true)
}

func (s *Service) Reject(ctx context.Context, actor k.Actor, id, version, reason string) (k.Proposal, error) {
	if !actor.Human {
		return k.Proposal{}, fmt.Errorf("%w: only humans can reject proposals", k.ErrForbidden)
	}
	if _, err := s.Proposal(actor, id); err != nil {
		return k.Proposal{}, err
	}
	return s.Store.Reject(ctx, id, version, actor.ID, reason)
}

func (s *Service) Reindex(ctx context.Context, actor k.Actor) error {
	if !actor.Admin {
		return fmt.Errorf("%w: reindex requires an administrator", k.ErrForbidden)
	}
	snapshot, err := s.Store.Snapshot()
	if err != nil {
		return err
	}
	return s.Store.Reindex(ctx, snapshot)
}

func (s *Service) History(actor k.Actor) ([]k.Commit, error) {
	history, err := s.Store.History()
	if err != nil {
		return nil, err
	}
	if actor.Admin {
		return history, nil
	}
	current, err := s.Store.Snapshot()
	if err != nil {
		return nil, err
	}
	var result []k.Commit
	for _, commit := range history {
		snapshot, err := s.Store.At(commit.ID)
		if err != nil {
			return nil, err
		}
		var changed []string
		for _, key := range commit.Changed {
			libraryID, entryID, _ := strings.Cut(key, "/")
			lib, exists := snapshot.Libraries[libraryID]
			if !exists {
				continue
			}
			e, found := lib.Entries[entryID]
			if found && canRead(current, actor, nil, e) {
				changed = append(changed, key)
			}
		}
		if len(changed) > 0 {
			commit.Changed, commit.Applied, commit.Reason = changed, nil, ""
			result = append(result, commit)
		}
	}
	return result, nil
}
