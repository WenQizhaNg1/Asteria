package core

import (
	"fmt"
	"os"
	"path/filepath"

	k "asteria/internal/knowledge"
)

// Export writes a current working copy. It is not a replacement for backing up
// the data directory, which also contains history, pending proposals and traces.
func (s *Service) Export(actor k.Actor, destination string) error {
	snapshot, err := s.Store.Snapshot()
	if err != nil {
		return err
	}
	if items, err := os.ReadDir(destination); err == nil && len(items) != 0 {
		return fmt.Errorf("%w: export destination must be empty", k.ErrInvalid)
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	write := func(path string, data []byte) error {
		full := filepath.Join(destination, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			return err
		}
		return os.WriteFile(full, data, 0600)
	}
	for _, schema := range accessibleSchemas(snapshot, actor, nil) {
		libraryID := schema.LibraryID
		lib := snapshot.Libraries[libraryID]
		if err := write(filepath.Join(libraryID, "schema.json"), []byte(prettyJSON(schema))); err != nil {
			return err
		}
		for _, entry := range lib.Entries {
			if !actor.Allows(lib.Schema, entry.Home) {
				continue
			}
			domainPath, err := k.DomainPath(lib.Schema, entry.Home)
			if err != nil {
				return err
			}
			data, err := k.EncodeEntry(entry)
			if err != nil {
				return err
			}
			if err := write(filepath.Join(libraryID, domainPath, entry.ID+".md"), data); err != nil {
				return err
			}
			for _, source := range entry.Sources {
				raw, err := s.Store.Source(source)
				if err != nil {
					return err
				}
				if err := write(filepath.Join("sources", source.Snapshot), []byte(raw)); err != nil {
					return err
				}
			}
		}
	}
	return write("export.json", []byte(prettyJSON(map[string]string{"commit_id": snapshot.Commit.ID, "kind": "working_copy"})))
}
