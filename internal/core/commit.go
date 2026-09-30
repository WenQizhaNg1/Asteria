package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	k "asteria/internal/knowledge"
)

// Propose accepts model-independent file changes and always applies the same
// governance rules, including when its caller is a human.
func (s *Service) Propose(ctx context.Context, actor k.Actor, base, reason string, changes []k.Change) (k.CommitResult, error) {
	if len(changes) == 0 || strings.TrimSpace(reason) == "" {
		return k.CommitResult{}, fmt.Errorf("%w: changes and reason are required", k.ErrInvalid)
	}
	snapshot, err := s.Store.At(base)
	if err != nil {
		return k.CommitResult{}, err
	}
	if err := authorizeChanges(snapshot, actor, changes); err != nil {
		return k.CommitResult{}, err
	}
	p := k.Proposal{
		ID: k.NewID("proposal"), Base: base, Actor: actor.ID, Reason: reason,
		CreatedAt: k.Now(), Status: "pending", Changes: changes,
	}
	before := filesForDiff(snapshot, actor)
	p.RequiresReview = needsReview(snapshot, changes)
	changed, err := applyChanges(snapshot, changes)
	if err != nil {
		p.Issues = append(p.Issues, err.Error())
	} else {
		markDependents(snapshot, changed)
		p.Issues = s.validate(snapshot, actor, changes)
		after := filesForDiff(snapshot, actor)
		p.Diff = diffFiles(before, after)
	}
	if len(p.Issues) == 0 && len(p.Diff) == 0 {
		return k.CommitResult{Status: "unchanged", CommitID: base}, nil
	}
	p.Version = p.Fingerprint()
	if err := s.Store.SaveProposal(ctx, p); err != nil {
		return k.CommitResult{}, err
	}
	if p.RequiresReview || len(p.Issues) != 0 {
		return k.CommitResult{Proposal: p, Status: "pending"}, nil
	}
	return s.commit(ctx, actor, p, false)
}

func (s *Service) commit(ctx context.Context, actor k.Actor, p k.Proposal, approved bool) (k.CommitResult, error) {
	result := k.CommitResult{Proposal: p, Status: p.Status}
	if p.Status == "rejected" || len(p.Issues) != 0 {
		return result, fmt.Errorf("%w: rejected or unresolved proposal cannot be committed", k.ErrInvalid)
	}
	if p.RequiresReview && (!approved || !actor.Human) {
		return result, fmt.Errorf("%w: exact human approval required", k.ErrForbidden)
	}
	next, err := s.Store.Publish(ctx, p.Base, actor.ID, p.ID, p.Version, p.Reason, func(snapshot *k.Snapshot) ([]string, error) {
		// Reload the proposal inside the same writer lock used for publication.
		stored, err := s.Store.Proposal(p.ID)
		if err != nil {
			return nil, err
		}
		if stored.Version != p.Version || stored.Status != "pending" {
			return nil, fmt.Errorf("%w: proposal changed", k.ErrConflict)
		}
		if err := authorizeChanges(snapshot, actor, p.Changes); err != nil {
			return nil, err
		}
		before := filesForDiff(snapshot, actor)
		changed, err := applyChanges(snapshot, p.Changes)
		if err != nil {
			return nil, err
		}
		changed = markDependents(snapshot, changed)
		after := filesForDiff(snapshot, actor)
		for _, diff := range p.Diff {
			if before[diff.Path] != diff.Before || after[diff.Path] != diff.After {
				return nil, fmt.Errorf("%w: proposal no longer renders as reviewed; create a new proposal", k.ErrConflict)
			}
		}
		if issues := s.validate(snapshot, actor, p.Changes); len(issues) != 0 {
			return nil, fmt.Errorf("%w: %s", k.ErrInvalid, strings.Join(issues, "; "))
		}
		sort.Strings(changed)
		return changed, nil
	})
	if err != nil {
		return result, err
	}
	result.Status, result.Proposal.Status, result.CommitID = "committed", "committed", next.Commit.ID
	// A retried approval may arrive after subsequent commits.
	if next.Commit.ProposalID != p.ID {
		history, err := s.Store.History()
		if err != nil {
			return result, err
		}
		for _, commit := range history {
			if commit.ProposalID == p.ID {
				result.CommitID = commit.ID
				break
			}
		}
	}
	result.IndexStatus = "ready"
	if err := s.Store.Reindex(ctx, next); err != nil {
		result.IndexStatus, result.Warning = "pending", "knowledge committed; derived index needs rebuilding"
	}
	return result, nil
}

func needsReview(snapshot *k.Snapshot, changes []k.Change) bool {
	for _, change := range changes {
		if change.Kind == "schema" {
			return true
		}
		e := change.Entry
		old, exists := snapshot.Libraries[e.LibraryID].Entries[e.ID]
		if exists {
			// Adding navigation entrances alone is safe; removing them isn't.
			previous, next := old, *e
			previous.Entrances, next.Entrances = nil, nil
			previous.Version, next.Version = "", ""
			if k.Digest(previous) != k.Digest(next) || !containsAll(e.Entrances, old.Entrances) {
				return true
			}
		}
		for _, r := range e.Relations {
			if r.Status == "verified" || r.Status == "rejected" {
				return true
			}
		}
	}
	return false
}

func containsAll(values, required []string) bool {
	set := map[string]bool{}
	for _, value := range values {
		set[value] = true
	}
	for _, value := range required {
		if !set[value] {
			return false
		}
	}
	return true
}

func applyChanges(snapshot *k.Snapshot, changes []k.Change) ([]string, error) {
	seen := map[string]bool{}
	var changed []string
	for _, change := range changes {
		if change.Kind != "schema" {
			continue
		}
		schema := *change.Schema
		if err := k.ValidateSchema(schema); err != nil {
			return nil, err
		}
		key := schema.LibraryID + "/schema"
		if seen[key] {
			return nil, fmt.Errorf("%w: duplicate change", k.ErrInvalid)
		}
		seen[key] = true
		lib := snapshot.Libraries[schema.LibraryID]
		schemaChanged := k.Digest(lib.Schema) != k.Digest(schema)
		lib.Schema = schema
		snapshot.Libraries[schema.LibraryID] = lib
		if schemaChanged {
			changed = append(changed, key)
		}
	}
	for _, change := range changes {
		if change.Kind != "entry" {
			continue
		}
		e := k.CloneEntry(*change.Entry)
		e.Content = k.NormalizeBody(e.Content)
		key := e.LibraryID + "/" + e.ID
		if seen[key] {
			return nil, fmt.Errorf("%w: duplicate change", k.ErrInvalid)
		}
		seen[key] = true
		if !k.ValidID(e.ID) || strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.Content) == "" {
			return nil, fmt.Errorf("%w: entry ID, title and Markdown are required", k.ErrInvalid)
		}
		if e.Origin == "inferred" && e.Status == "" {
			e.Status = "candidate"
		}
		data, err := k.EncodeEntry(e)
		if err != nil {
			return nil, err
		}
		e.Version = k.Hash(data)
		lib := snapshot.Libraries[e.LibraryID]
		old, exists := lib.Entries[e.ID]
		lib.Entries[e.ID] = e
		snapshot.Libraries[e.LibraryID] = lib
		if !exists || old.Version != e.Version {
			changed = append(changed, key)
		}
	}
	return changed, nil
}

// Review metadata is maintained even across libraries. Full snapshot CAS
// prevents a concurrent new dependency from slipping past this scan.
func markDependents(snapshot *k.Snapshot, changed []string) []string {
	dirty := map[string]bool{}
	for _, key := range changed {
		dirty[key] = true
	}
	for progress := true; progress; {
		progress = false
		for libraryID, lib := range snapshot.Libraries {
			for id, e := range lib.Entries {
				updated := false
				if e.Origin == "inferred" && e.Status != "needs_review" && e.Status != "rejected" && dependsOn(snapshot, e.Evidence, dirty) {
					e.Status, e.ReviewReason = "needs_review", "supporting knowledge changed"
					updated = true
				}
				for i := range e.Relations {
					r := &e.Relations[i]
					if r.Status != "needs_review" && r.Status != "rejected" && dependsOn(snapshot, r.Evidence, dirty) {
						r.Status, r.ReviewReason = "needs_review", "supporting knowledge changed"
						updated = true
					}
				}
				if updated {
					data, _ := k.EncodeEntry(e)
					e.Version = k.Hash(data)
					lib.Entries[id] = e
					key := libraryID + "/" + id
					if !dirty[key] {
						changed = append(changed, key)
						dirty[key], progress = true, true
					}
				}
			}
			snapshot.Libraries[libraryID] = lib
		}
	}
	return changed
}

func dependsOn(snapshot *k.Snapshot, refs []k.Ref, dirty map[string]bool) bool {
	for _, ref := range refs {
		if dirty[ref.Key()] {
			current := snapshot.Libraries[ref.LibraryID].Entries[ref.EntryID]
			if current.Withdrawn || ref.Version != current.Version {
				return true
			}
		}
	}
	return false
}

func filesForDiff(snapshot *k.Snapshot, actor k.Actor) map[string]string {
	files := map[string]string{}
	for libraryID, lib := range snapshot.Libraries {
		if actor.ManagesLibrary(libraryID) {
			files[libraryID+"/schema.json"] = prettyJSON(lib.Schema)
		}
		for _, entry := range lib.Entries {
			if actor.Allows(lib.Schema, entry.Home) {
				content, _ := k.EncodeEntry(entry)
				files[libraryID+"/"+entry.ID+".md"] = string(content)
			}
		}
	}
	return files
}

func diffFiles(before, after map[string]string) []k.FileDiff {
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	var result []k.FileDiff
	for key := range keys {
		if before[key] != after[key] {
			result = append(result, k.FileDiff{Path: key, Before: before[key], After: after[key]})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}
