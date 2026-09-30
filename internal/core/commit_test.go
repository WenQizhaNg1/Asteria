package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	k "asteria/internal/knowledge"
	"asteria/internal/store"
)

func testService(t *testing.T) *Service {
	t.Helper()
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(storage, nil)
	_, err = service.Init(context.Background(), k.LocalActor(), k.Schema{
		LibraryID: "traffic", Name: "交通",
		Domains: []k.Domain{
			{ID: "transport", Name: "交通运行", Scope: "道路交通与停车"},
			{ID: "parking", Parent: "transport", Name: "停车管理", Scope: "停车场和入口排队"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func testSource(t *testing.T, service *Service, id, content string) k.Entry {
	t.Helper()
	source, err := service.Store.SaveSource("case://"+id, content)
	if err != nil {
		t.Fatal(err)
	}
	entry := k.Entry{ID: id, LibraryID: "traffic", Home: "parking", Title: id, Content: content, Origin: "source", Sources: []k.Source{source}}
	result := proposeEntry(t, service, k.LocalActor(), entry)
	if result.Status != "committed" {
		t.Fatalf("source not committed: %+v", result)
	}
	entry, err = service.Read(k.LocalActor(), k.Ref{LibraryID: "traffic", EntryID: id})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func proposeEntry(t *testing.T, service *Service, actor k.Actor, entry k.Entry) k.CommitResult {
	t.Helper()
	head, err := service.Store.Head()
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Propose(context.Background(), actor, head, "test change", []k.Change{{Kind: "entry", Entry: &entry}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestReviewConflictAndVersionedEvidence(t *testing.T) {
	service := testService(t)
	original := testSource(t, service, "case-a", "# 入口排队\n停车场接近饱和，需要上游分流。")
	first := k.CloneEntry(original)
	first.Content += "\n接驳运力不足时停止分流。"
	pending := proposeEntry(t, service, k.LocalActor(), first)
	if pending.Status != "pending" || !pending.Proposal.RequiresReview {
		t.Fatal("editing existing Markdown must require review")
	}
	if pending.Proposal.Diff[0].Before == pending.Proposal.Diff[0].After {
		t.Fatal("review must show the exact changed file")
	}
	agent := k.Actor{ID: "agent", Admin: true}
	if _, err := service.Approve(context.Background(), agent, pending.Proposal.ID, pending.Proposal.Version); !errors.Is(err, k.ErrForbidden) {
		t.Fatalf("agent approved its own change: %v", err)
	}
	if _, err := service.Approve(context.Background(), k.LocalActor(), pending.Proposal.ID, "stale"); !errors.Is(err, k.ErrConflict) {
		t.Fatalf("approval accepted a wrong proposal version: %v", err)
	}
	second := k.CloneEntry(original)
	second.Content += "\n优先发布停车余位信息。"
	other := proposeEntry(t, service, k.LocalActor(), second)
	if _, err := service.Approve(context.Background(), k.LocalActor(), other.Proposal.ID, other.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(context.Background(), k.LocalActor(), pending.Proposal.ID, pending.Proposal.Version); !errors.Is(err, k.ErrConflict) {
		t.Fatalf("old approval overwrote new knowledge: %v", err)
	}
	historical, err := service.Read(k.LocalActor(), original.Ref())
	if err != nil || historical.Content != original.Content {
		t.Fatalf("old evidence is no longer readable: %v", err)
	}
	history, err := service.Store.History()
	if err != nil || len(history) != 3 {
		t.Fatalf("history lost changes: %d, %v", len(history), err)
	}
}

func TestSourceRevisionMarksRelationForReview(t *testing.T) {
	service := testService(t)
	source := testSource(t, service, "case-a", "停车场接近饱和，需要上游分流。")
	ref := source.Ref()
	ref.Quote = source.Content
	insight := k.Entry{
		ID: "insight-a", LibraryID: "traffic", Home: "parking", Title: "候选联系", Origin: "inferred",
		Content: "<a id=\"flow-control\"></a>\n## 上游分流\n分流可能缓解排队，但需要接驳运力。", Evidence: []k.Ref{ref},
		Relations: []k.Relation{{
			ID: "flow-control", Subject: source.Ref(), Predicate: "supports", Object: source.Ref(),
			Origin: "inferred", Status: "candidate", Evidence: []k.Ref{ref}, Conditions: "接驳有余量",
		}},
	}
	created := proposeEntry(t, service, k.LocalActor(), insight)
	if created.Status != "committed" {
		t.Fatalf("valid candidate should be automatically saved: %+v", created.Proposal.Issues)
	}
	saved, _ := service.Read(k.LocalActor(), k.Ref{LibraryID: "traffic", EntryID: insight.ID})
	saved.Relations[0].Status = "verified"
	saved.Relations[0].Reviewer = "local"
	saved.Relations[0].ReviewedAt = k.Now()
	saved.Relations[0].ReviewNote = "人工检查来源及适用条件"
	verification := proposeEntry(t, service, k.LocalActor(), saved)
	if _, err := service.Approve(context.Background(), k.LocalActor(), verification.Proposal.ID, verification.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	source.Content += "\n补充：接驳不足时分流无效。"
	revision := proposeEntry(t, service, k.LocalActor(), source)
	if _, err := service.Approve(context.Background(), k.LocalActor(), revision.Proposal.ID, revision.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Read(k.LocalActor(), k.Ref{LibraryID: "traffic", EntryID: insight.ID})
	if err != nil || updated.Relations[0].Status != "needs_review" || updated.Status != "needs_review" {
		t.Fatalf("stale inference still appears verified: %+v, %v", updated, err)
	}
	if updated.Relations[0].Evidence[0].Version != ref.Version {
		t.Fatal("evidence was silently migrated to a different version")
	}
}

func TestIndexFailureDoesNotRollbackCommit(t *testing.T) {
	service := testService(t)
	indexPath := filepath.Join(service.Store.Root(), "index.json")
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(indexPath, 0700); err != nil {
		t.Fatal(err)
	}
	source, _ := service.Store.SaveSource("case://a", "原始资料")
	entry := k.Entry{ID: "case-a", LibraryID: "traffic", Home: "parking", Title: "案例", Content: "原始资料", Origin: "source", Sources: []k.Source{source}}
	result := proposeEntry(t, service, k.LocalActor(), entry)
	if result.Status != "committed" || result.IndexStatus != "pending" || result.Warning == "" {
		t.Fatalf("incorrect partial success result: %+v", result)
	}
	if _, err := service.Read(k.LocalActor(), k.Ref{LibraryID: "traffic", EntryID: entry.ID}); err != nil {
		t.Fatal("committed file unavailable:", err)
	}
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if err := service.Reindex(context.Background(), k.LocalActor()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := service.Store.Snapshot()
	var index store.Index
	data, err := os.ReadFile(indexPath)
	if err != nil || json.Unmarshal(data, &index) != nil || len(index.Entries) != 1 || index.CommitID != snapshot.Commit.ID {
		t.Fatalf("index not reconstructed: %+v %v", index, err)
	}
}

func TestSchemaMovePreservesIDsAndEvidence(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "case-a", "版本化证据")
	state, _ := service.Store.Snapshot()
	schema := state.Libraries["traffic"].Schema
	schema.Domains[1].Parent = ""
	result, err := service.Propose(context.Background(), k.LocalActor(), state.Commit.ID, "move domain", []k.Change{{Kind: "schema", Schema: &schema}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "pending" {
		t.Fatal("domain moves need review")
	}
	if _, err := service.Approve(context.Background(), k.LocalActor(), result.Proposal.ID, result.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	moved, err := service.Read(k.LocalActor(), entry.Ref())
	if err != nil || moved.Version != entry.Version || moved.ID != entry.ID {
		t.Fatal("domain move changed entry identity or evidence")
	}
	head, _ := service.Store.Head()
	if _, err := os.Stat(filepath.Join(service.Store.Root(), "versions", head, "traffic", "parking", entry.ID+".md")); err != nil {
		t.Fatal("domain directory was not moved:", err)
	}
}

func TestCircularInsightsDoNotBecomeEvidence(t *testing.T) {
	entries := map[string]k.Entry{
		"a": {ID: "a", LibraryID: "traffic", Version: "va", Origin: "inferred", Content: "断言 A", Evidence: []k.Ref{{LibraryID: "traffic", EntryID: "b", Version: "vb", Quote: "断言 B"}}},
		"b": {ID: "b", LibraryID: "traffic", Version: "vb", Origin: "inferred", Content: "断言 B", Evidence: []k.Ref{{LibraryID: "traffic", EntryID: "a", Version: "va", Quote: "断言 A"}}},
	}
	err := validateEvidenceChain([]k.Ref{{LibraryID: "traffic", EntryID: "a", Version: "va", Quote: "断言 A"}}, func(ref k.Ref) (k.Entry, error) {
		return entries[ref.EntryID], nil
	})
	if err == nil || !strings.Contains(err.Error(), "no original evidence") {
		t.Fatalf("circular inference accepted as original evidence: %v", err)
	}
}

func TestScopeAndUnsupportedInsight(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "case-a", "受限来源")
	stranger := k.Actor{ID: "outsider", Grants: []k.Scope{{LibraryID: "another"}}}
	if _, err := service.Read(stranger, entry.Ref()); !errors.Is(err, k.ErrNotFound) {
		t.Fatalf("entry leaked outside caller scope: %v", err)
	}
	inferred := k.Entry{ID: "guess", LibraryID: "traffic", Home: "parking", Title: "猜测", Origin: "inferred", Content: "没有依据的推测"}
	result := proposeEntry(t, service, k.LocalActor(), inferred)
	if result.Status != "pending" || len(result.Proposal.Issues) == 0 {
		t.Fatal("unsupported inference was committed")
	}
	if _, err := service.Approve(context.Background(), k.LocalActor(), result.Proposal.ID, result.Proposal.Version); !errors.Is(err, k.ErrInvalid) {
		t.Fatalf("approval bypassed evidence validation: %v", err)
	}
}
