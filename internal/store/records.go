package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"asteria/internal/knowledge"
)

func (s *Store) SaveSource(origin, content string) (knowledge.Source, error) {
	if strings.TrimSpace(origin) == "" || strings.TrimSpace(content) == "" {
		return knowledge.Source{}, fmt.Errorf("%w: source origin and content are required", knowledge.ErrInvalid)
	}
	hash := knowledge.Hash([]byte(content))
	if err := atomicWrite(filepath.Join(s.root, "sources", hash), []byte(content)); err != nil {
		return knowledge.Source{}, err
	}
	return knowledge.Source{ID: "source-" + hash[:24], Origin: origin, Snapshot: hash, CapturedAt: knowledge.Now()}, nil
}

func (s *Store) Source(source knowledge.Source) (string, error) {
	if len(source.Snapshot) != 64 || !knowledge.ValidID(source.Snapshot) {
		return "", fmt.Errorf("%w: invalid source snapshot", knowledge.ErrInvalid)
	}
	data, err := os.ReadFile(filepath.Join(s.root, "sources", source.Snapshot))
	if err != nil {
		return "", err
	}
	if knowledge.Hash(data) != source.Snapshot {
		return "", fmt.Errorf("source integrity mismatch")
	}
	return string(data), nil
}

func (s *Store) SaveProposal(ctx context.Context, p knowledge.Proposal) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if !knowledge.ValidID(p.ID) || p.Version != p.Fingerprint() {
		return fmt.Errorf("%w: invalid proposal fingerprint", knowledge.ErrInvalid)
	}
	path := filepath.Join(s.root, "proposals", p.ID+".json")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: proposal IDs are immutable", knowledge.ErrConflict)
	}
	return writeJSON(path, p)
}

func (s *Store) Proposal(id string) (knowledge.Proposal, error) {
	var p knowledge.Proposal
	if !knowledge.ValidID(id) {
		return p, fmt.Errorf("%w: invalid proposal ID", knowledge.ErrInvalid)
	}
	if err := readJSON(filepath.Join(s.root, "proposals", id+".json"), &p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return p, fmt.Errorf("%w: proposal", knowledge.ErrNotFound)
		}
		return p, err
	}
	if p.ID != id || p.Version != p.Fingerprint() {
		return p, fmt.Errorf("%w: proposal was modified", knowledge.ErrConflict)
	}
	head, err := s.Head()
	if err != nil || head == "" {
		return p, err
	}
	m, err := s.readManifest(head)
	if err != nil {
		return p, err
	}
	if m.Commit.Applied[id] == p.Version {
		p.Status = "committed"
	}
	return p, nil
}

func (s *Store) Reject(ctx context.Context, id, version, actor, note string) (knowledge.Proposal, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return knowledge.Proposal{}, err
	}
	defer unlock()
	p, err := s.Proposal(id)
	if err != nil {
		return p, err
	}
	if p.Version != version || p.Status != "pending" {
		return p, fmt.Errorf("%w: proposal version or status changed", knowledge.ErrConflict)
	}
	p.Status, p.ReviewedBy, p.ReviewNote = "rejected", actor, note
	return p, writeJSON(filepath.Join(s.root, "proposals", id+".json"), p)
}

func (s *Store) ProposalIDs() ([]string, error) {
	return recordIDs(filepath.Join(s.root, "proposals"))
}

func (s *Store) SaveTrace(id string, trace any) error {
	if !knowledge.ValidID(id) {
		return knowledge.ErrInvalid
	}
	return writeJSON(filepath.Join(s.root, "traces", id+".json"), trace)
}

func (s *Store) Trace(id string) (json.RawMessage, error) {
	if !knowledge.ValidID(id) {
		return nil, fmt.Errorf("%w: invalid trace ID", knowledge.ErrInvalid)
	}
	data, err := os.ReadFile(filepath.Join(s.root, "traces", id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: trace", knowledge.ErrNotFound)
	}
	return data, err
}

func (s *Store) TraceIDs() ([]string, error) {
	return recordIDs(filepath.Join(s.root, "traces"))
}

func recordIDs(directory string) ([]string, error) {
	items, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, item := range items {
		if !item.IsDir() && strings.HasSuffix(item.Name(), ".json") {
			ids = append(ids, strings.TrimSuffix(item.Name(), ".json"))
		}
	}
	return ids, nil
}
