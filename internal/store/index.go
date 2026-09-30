package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"asteria/internal/knowledge"
)

type IndexItem struct {
	Ref        knowledge.Ref   `json:"ref"`
	Home       string          `json:"home"`
	Title      string          `json:"title"`
	Summary    string          `json:"summary"`
	References []knowledge.Ref `json:"references,omitempty"`
}

type Index struct {
	CommitID string      `json:"commit_id"`
	Entries  []IndexItem `json:"entries"`
}

func BuildIndex(snapshot *knowledge.Snapshot) (Index, error) {
	index := Index{CommitID: snapshot.Commit.ID}
	for _, lib := range snapshot.Libraries {
		for _, e := range lib.Entries {
			if e.Withdrawn {
				continue
			}
			refs, err := knowledge.Links(e.Content)
			if err != nil {
				return index, err
			}
			refs = append(refs, e.Evidence...)
			for _, relation := range e.Relations {
				refs = append(refs, relation.Subject, relation.Object)
				refs = append(refs, relation.Evidence...)
			}
			index.Entries = append(index.Entries, IndexItem{
				Ref: e.Ref(), Home: e.Home, Title: e.Title, Summary: e.Summary, References: refs,
			})
		}
	}
	sort.Slice(index.Entries, func(i, j int) bool { return index.Entries[i].Ref.Key() < index.Entries[j].Ref.Key() })
	return index, nil
}

func (s *Store) Reindex(ctx context.Context, snapshot *knowledge.Snapshot) error {
	index, err := BuildIndex(snapshot)
	if err != nil {
		return err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	head, err := s.Head()
	if err != nil {
		return err
	}
	if head != snapshot.Commit.ID {
		return fmt.Errorf("%w: refusing to publish an old index", knowledge.ErrConflict)
	}
	return writeJSON(filepath.Join(s.root, "index.json"), index)
}
