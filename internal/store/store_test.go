package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	k "asteria/internal/knowledge"
)

func TestConcurrentWritersCheckBaseInsideLock(t *testing.T) {
	root := t.TempDir()
	first, _ := Open(root)
	second, _ := Open(root)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, storage := range []*Store{first, second} {
		workers.Add(1)
		go func(storage *Store) {
			defer workers.Done()
			<-start
			_, err := storage.Publish(context.Background(), "", "writer", "", "", "initialize", func(snapshot *k.Snapshot) ([]string, error) {
				snapshot.Libraries["traffic"] = k.Library{Schema: k.Schema{LibraryID: "traffic", Name: "交通", Domains: []k.Domain{{ID: "parking", Name: "停车", Scope: "停车"}}}, Entries: map[string]k.Entry{}}
				return []string{"traffic/schema"}, nil
			})
			results <- err
		}(storage)
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, k.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("lost update protection failed: success=%d conflict=%d", successes, conflicts)
	}
}

func TestCancelledPreparationLeavesHeadUnchanged(t *testing.T) {
	storage, _ := Open(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	_, err := storage.Publish(ctx, "", "writer", "", "", "cancel", func(snapshot *k.Snapshot) ([]string, error) {
		cancel()
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if head, err := storage.Head(); err != nil || head != "" {
		t.Fatalf("cancelled snapshot became visible: %s %v", head, err)
	}
}

func TestReindexCanCancelWhileWaitingForWriter(t *testing.T) {
	storage, _ := Open(t.TempDir())
	snapshot, _ := storage.Snapshot()
	unlock, err := storage.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- storage.Reindex(ctx, snapshot) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("index rebuild ignored cancellation while waiting for lock")
	}
}

func TestOldRebuildCannotReplaceCurrentIndex(t *testing.T) {
	storage, _ := Open(t.TempDir())
	publish := func(base string) *k.Snapshot {
		t.Helper()
		snapshot, err := storage.Publish(context.Background(), base, "writer", "", "", "test", func(*k.Snapshot) ([]string, error) { return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	old := publish("")
	current := publish(old.Commit.ID)
	if err := storage.Reindex(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(storage.Root(), "index.json")
	before, _ := os.ReadFile(path)
	if err := storage.Reindex(context.Background(), old); !errors.Is(err, k.ErrConflict) {
		t.Fatalf("stale index publication was accepted: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("stale rebuild overwrote the current index")
	}
}

func TestSnapshotRejectsMismatchedSchemaIdentity(t *testing.T) {
	storage, _ := Open(t.TempDir())
	schema := k.Schema{LibraryID: "traffic", Name: "交通", Domains: []k.Domain{{ID: "parking", Name: "停车", Scope: "停车"}}}
	snapshot, err := storage.Publish(context.Background(), "", "writer", "", "", "initialize", func(snapshot *k.Snapshot) ([]string, error) {
		snapshot.Libraries[schema.LibraryID] = k.Library{Schema: schema, Entries: map[string]k.Entry{}}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	schema.LibraryID = "another"
	if err := writeJSON(filepath.Join(storage.Root(), "versions", snapshot.Commit.ID, "traffic", "schema.json"), schema); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Snapshot(); err == nil {
		t.Fatal("accepted a schema with an identity different from its library")
	}
}
