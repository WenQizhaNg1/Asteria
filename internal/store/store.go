// Package store publishes immutable Markdown snapshots through a single HEAD.
package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"asteria/internal/knowledge"
)

type Store struct{ root string }

type manifest struct {
	Commit    knowledge.Commit  `json:"commit"`
	Libraries []string          `json:"libraries"`
	Files     map[string]string `json:"files"`
}

func Open(root string) (*Store, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"versions", "sources", "proposals", "traces"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			return nil, err
		}
	}
	return &Store{root: root}, nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) Head() (string, error) {
	data, err := os.ReadFile(filepath.Join(s.root, "HEAD"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if !knowledge.ValidID(id) {
		return "", fmt.Errorf("invalid HEAD")
	}
	return id, nil
}

func emptySnapshot() *knowledge.Snapshot {
	return &knowledge.Snapshot{
		Commit:    knowledge.Commit{Applied: map[string]string{}},
		Libraries: map[string]knowledge.Library{},
	}
}

func (s *Store) Snapshot() (*knowledge.Snapshot, error) {
	head, err := s.Head()
	if err != nil {
		return nil, err
	}
	return s.At(head)
}

func (s *Store) readManifest(id string) (manifest, error) {
	var m manifest
	if !knowledge.ValidID(id) {
		return m, fmt.Errorf("%w: invalid commit ID", knowledge.ErrInvalid)
	}
	err := readJSON(filepath.Join(s.root, "versions", id, "manifest.json"), &m)
	if err == nil && m.Commit.ID != id {
		err = fmt.Errorf("commit manifest does not match its path")
	}
	return m, err
}

func (s *Store) At(id string) (*knowledge.Snapshot, error) {
	if id == "" {
		return emptySnapshot(), nil
	}
	m, err := s.readManifest(id)
	if err != nil {
		return nil, err
	}
	snapshot := emptySnapshot()
	snapshot.Commit = m.Commit
	fileCount := 0
	for _, libraryID := range m.Libraries {
		if !knowledge.ValidID(libraryID) {
			return nil, fmt.Errorf("invalid library ID in manifest")
		}
		dir := filepath.Join(s.root, "versions", id, libraryID)
		lib := knowledge.Library{Entries: map[string]knowledge.Entry{}}
		if err := readJSON(filepath.Join(dir, "schema.json"), &lib.Schema); err != nil {
			return nil, err
		}
		if err := knowledge.ValidateSchema(lib.Schema); err != nil {
			return nil, err
		}
		if lib.Schema.LibraryID != libraryID {
			return nil, fmt.Errorf("snapshot schema does not match its library")
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlinks are not allowed inside snapshots")
			}
			if d.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			e, err := knowledge.DecodeEntry(data)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			key := libraryID + "/" + e.ID
			if !knowledge.ValidID(e.ID) || e.LibraryID != libraryID || m.Files[key] != e.Version {
				return fmt.Errorf("snapshot file integrity mismatch: %s", key)
			}
			if _, exists := lib.Entries[e.ID]; exists {
				return fmt.Errorf("duplicate entry ID: %s", key)
			}
			lib.Entries[e.ID] = e
			return nil
		})
		if err != nil {
			return nil, err
		}
		snapshot.Libraries[libraryID] = lib
		fileCount += len(lib.Entries)
	}
	if len(m.Files) != fileCount {
		return nil, fmt.Errorf("snapshot is missing knowledge files")
	}
	return snapshot, nil
}

// Publish checks and mutates a fresh snapshot while holding the cross-process
// lock. The callback may reject the operation before any visible state changes.
func (s *Store) Publish(ctx context.Context, base, actor, proposal, version, reason string, apply func(*knowledge.Snapshot) ([]string, error)) (*knowledge.Snapshot, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	snapshot, err := s.Snapshot()
	if err != nil {
		return nil, err
	}
	if proposal != "" && snapshot.Commit.Applied[proposal] == version {
		return snapshot, nil
	}
	if snapshot.Commit.ID != base {
		return nil, fmt.Errorf("%w: expected %s, current %s", knowledge.ErrConflict, base, snapshot.Commit.ID)
	}
	changed, err := apply(snapshot)
	if err != nil {
		return nil, err
	}
	parent := snapshot.Commit.ID
	applied := snapshot.Commit.Applied
	if applied == nil {
		applied = map[string]string{}
	}
	if proposal != "" {
		applied[proposal] = version
	}
	snapshot.Commit = knowledge.Commit{
		ID: knowledge.NewID("commit"), Parent: parent, Actor: actor, ProposalID: proposal,
		Reason: reason, CreatedAt: knowledge.Now(), Applied: applied, Changed: changed,
	}
	if err := s.writeSnapshot(ctx, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *Store) writeSnapshot(ctx context.Context, snapshot *knowledge.Snapshot) error {
	staging, err := os.MkdirTemp(filepath.Join(s.root, "versions"), ".prepare-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	m := manifest{Commit: snapshot.Commit, Files: map[string]string{}}
	for libraryID, lib := range snapshot.Libraries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := knowledge.ValidateSchema(lib.Schema); err != nil {
			return err
		}
		m.Libraries = append(m.Libraries, libraryID)
		if err := writeJSON(filepath.Join(staging, libraryID, "schema.json"), lib.Schema); err != nil {
			return err
		}
		for id, e := range lib.Entries {
			if !knowledge.ValidID(id) || e.ID != id || e.LibraryID != libraryID {
				return fmt.Errorf("%w: invalid entry identity", knowledge.ErrInvalid)
			}
			domainPath, err := knowledge.DomainPath(lib.Schema, e.Home)
			if err != nil {
				return err
			}
			data, err := knowledge.EncodeEntry(e)
			if err != nil {
				return err
			}
			e.Version = knowledge.Hash(data)
			lib.Entries[id] = e
			m.Files[libraryID+"/"+id] = e.Version
			if err := atomicWrite(filepath.Join(staging, libraryID, domainPath, id+".md"), data); err != nil {
				return err
			}
		}
	}
	sort.Strings(m.Libraries)
	if err := writeJSON(filepath.Join(staging, "manifest.json"), m); err != nil {
		return err
	}
	// Flush every directory before publishing, including new domain ancestors.
	if err := filepath.WalkDir(staging, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return syncDir(path)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	target := filepath.Join(s.root, "versions", snapshot.Commit.ID)
	if err := os.Rename(staging, target); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(target)); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.root, "HEAD"), []byte(snapshot.Commit.ID+"\n"))
}

func (s *Store) ReadVersion(ref knowledge.Ref) (knowledge.Entry, error) {
	head, err := s.Head()
	if err != nil {
		return knowledge.Entry{}, err
	}
	for head != "" {
		m, err := s.readManifest(head)
		if err != nil {
			return knowledge.Entry{}, err
		}
		version, exists := m.Files[ref.Key()]
		if exists && (ref.Version == "" || ref.Version == version) {
			snapshot, err := s.At(head)
			if err != nil {
				return knowledge.Entry{}, err
			}
			return snapshot.Libraries[ref.LibraryID].Entries[ref.EntryID], nil
		}
		head = m.Commit.Parent
	}
	return knowledge.Entry{}, fmt.Errorf("%w: entry version", knowledge.ErrNotFound)
}

func (s *Store) History() ([]knowledge.Commit, error) {
	head, err := s.Head()
	if err != nil {
		return nil, err
	}
	var history []knowledge.Commit
	for head != "" {
		m, err := s.readManifest(head)
		if err != nil {
			return nil, err
		}
		history = append(history, m.Commit)
		head = m.Commit.Parent
	}
	return history, nil
}
