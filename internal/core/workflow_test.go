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
)

func modelOutput(value, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func TestQueryNavigationCitationValidationAndReadOnly(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "parking-case", "停车场接近饱和时，先核实接驳运力再分流。")
	head, _ := service.Store.Head()
	service.Model = ModelFunc(func(ctx context.Context, _ string, input, output any) error {
		args := input.(map[string]any)
		switch args["operation"] {
		case "navigate":
			return modelOutput(map[string]any{"read": []k.Ref{entry.Ref()}, "done": true}, output)
		case "answer":
			ref := entry.Ref()
			ref.Quote = entry.Content
			bad := ref
			bad.Quote = "这段话不存在"
			return modelOutput(map[string]any{"claims": []Claim{
				{Text: "分流前需要核实接驳运力。", Kind: "supported", Evidence: []k.Ref{ref}},
				{Text: "没有依据的模型结论。", Kind: "supported", Evidence: []k.Ref{bad}},
			}}, output)
		}
		return errors.New("unexpected model operation")
	})
	result, err := service.Query(context.Background(), k.LocalActor(), QueryRequest{Question: "停车场饱和时如何分流？"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Claims) != 1 || result.Claims[0].Evidence[0].Version != entry.Version || len(result.Unresolved) != 1 {
		t.Fatalf("unverified citation was not removed: %+v", result)
	}
	if result.Reads != 1 || result.Steps != 2 {
		t.Fatalf("unexpected exploration accounting: %+v", result)
	}
	current, _ := service.Store.Head()
	if current != head {
		t.Fatal("query modified knowledge")
	}
	if _, err := os.Stat(filepath.Join(service.Store.Root(), "traces", result.TraceID+".json")); err != nil {
		t.Fatal("query trace missing:", err)
	}
}

func TestQueryPermissionBudgetAndPromptIsolation(t *testing.T) {
	service := testService(t)
	public := testSource(t, service, "public", "停车案例")
	privateSource, _ := service.Store.SaveSource("private://origin", "秘密内容")
	private := k.Entry{ID: "private", LibraryID: "traffic", Home: "transport", Title: "秘密条目", Origin: "source", Sources: []k.Source{privateSource}, Content: "秘密内容"}
	proposeEntry(t, service, k.LocalActor(), private)
	actor := k.Actor{ID: "parking-agent", Grants: []k.Scope{{LibraryID: "traffic", DomainID: "parking"}}}
	service.Model = ModelFunc(func(ctx context.Context, _ string, input, output any) error {
		data, _ := json.Marshal(input)
		if strings.Contains(string(data), "秘密内容") || strings.Contains(string(data), "秘密条目") {
			t.Fatal("unauthorized content entered the model context")
		}
		if input.(map[string]any)["operation"] == "navigate" {
			return modelOutput(map[string]any{"read": []k.Ref{private.Ref(), public.Ref()}, "done": true}, output)
		}
		return modelOutput(map[string]any{"claims": []Claim{}}, output)
	})
	result, err := service.Query(context.Background(), actor, QueryRequest{Question: "停车", Budget: Budget{MaxReads: 1}})
	if err != nil || len(result.Evidence) != 1 || result.Evidence[0].Ref.EntryID != public.ID {
		t.Fatalf("query escaped its grants: %+v, %v", result, err)
	}
}

func TestWriteDedupeAndUngroupedMaterial(t *testing.T) {
	service := testService(t)
	request := WriteRequest{Content: "# 停车案例\n停车场已满。", Source: "case://one", LibraryID: "traffic", DomainID: "parking"}
	first, err := service.Write(context.Background(), k.LocalActor(), request)
	if err != nil || first.Status != "committed" {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := service.Write(context.Background(), k.LocalActor(), request)
	if err != nil || second.Status != "duplicate" || second.Duplicate == nil {
		t.Fatalf("%+v %v", second, err)
	}
	request.Source, request.DomainID = "case://unknown", ""
	pending, err := service.Write(context.Background(), k.LocalActor(), request)
	if err != nil || pending.Status != "pending" || len(pending.Proposal.Issues) == 0 {
		t.Fatalf("ungrouped material was forced into a domain: %+v %v", pending, err)
	}
}

func TestReflectCandidateThenSourceRevision(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "case-a", "停车场饱和时，接驳运力不足会使分流失效。")
	ref := entry.Ref()
	ref.Quote = entry.Content
	service.Model = ModelFunc(func(ctx context.Context, _ string, input, output any) error {
		switch input.(map[string]any)["operation"] {
		case "navigate":
			return modelOutput(map[string]any{"read": []k.Ref{entry.Ref()}, "done": true}, output)
		case "reflect":
			return modelOutput(reflectionPlan{Insights: []InsightDraft{{
				LibraryID: "traffic", Home: "parking", Title: "分流的约束", Evidence: []k.Ref{ref},
				Content:   "<a id=\"capacity\"></a>\n## 候选联系\n接驳容量可能构成分流约束。\n## 条件与差异\n仅用于该案例，尚需核查其他情境。\n## 待验证\n检查有余量时的反例。",
				Relations: []k.Relation{{ID: "capacity", Subject: entry.Ref(), Object: entry.Ref(), Predicate: "constrains", Origin: "inferred", Status: "verified", Evidence: []k.Ref{ref}, Conditions: "当前案例的接驳运力不足"}},
			}}}, output)
		}
		return errors.New("unexpected model operation")
	})
	result, err := service.Reflect(context.Background(), k.LocalActor(), ReflectRequest{Goal: "分流约束"})
	if err != nil || len(result.Changes) != 1 || result.Changes[0].Status != "committed" {
		t.Fatalf("reflect did not save candidate: %+v %v", result, err)
	}
	insightID := result.Changes[0].Proposal.Changes[0].Entry.ID
	insight, _ := service.Read(k.LocalActor(), k.Ref{LibraryID: "traffic", EntryID: insightID})
	if insight.Relations[0].Status != "candidate" {
		t.Fatal("model self-verification bypassed governance")
	}
	service.Model = nil
	content := entry.Content + "\n新增反例：运力充足也可能受到道路容量限制。"
	revision, err := service.Revise(context.Background(), k.LocalActor(), ReviseRequest{Target: entry.ID, Instruction: "补充失效条件", Content: &content})
	if err != nil || revision.Status != "pending" {
		t.Fatalf("%+v %v", revision, err)
	}
	if _, err := service.Approve(context.Background(), k.LocalActor(), revision.Proposal.ID, revision.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	insight, _ = service.Read(k.LocalActor(), k.Ref{LibraryID: "traffic", EntryID: insightID})
	if insight.Relations[0].Status != "needs_review" {
		t.Fatal("reflection evidence was not invalidated")
	}
	if old, err := service.Read(k.LocalActor(), ref); err != nil || old.Content != entry.Content {
		t.Fatal("old evidence was lost")
	}
}

func TestRestoreCreatesANewReviewedVersion(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "case-a", "原始正文")
	content := "修订正文"
	revised, err := service.Revise(context.Background(), k.LocalActor(), ReviseRequest{Target: entry.ID, Instruction: "修订", Content: &content})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(context.Background(), k.LocalActor(), revised.Proposal.ID, revised.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	restore, err := service.Revise(context.Background(), k.LocalActor(), ReviseRequest{Target: entry.ID, Instruction: "恢复", RestoreVersion: entry.Version})
	if err != nil || restore.Status != "pending" {
		t.Fatalf("%+v %v", restore, err)
	}
	if _, err := service.Approve(context.Background(), k.LocalActor(), restore.Proposal.ID, restore.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	history, _ := service.Store.History()
	current, _ := service.Read(k.LocalActor(), k.Ref{LibraryID: "traffic", EntryID: entry.ID})
	if len(history) != 4 || current.Content != entry.Content {
		t.Fatal("restore erased later history")
	}
}
