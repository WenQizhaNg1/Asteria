package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	k "asteria/internal/knowledge"
)

func TestNoOpDoesNotInvalidateDependents(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "case-a", "原始证据")
	head, _ := service.Store.Head()
	result := proposeEntry(t, service, k.LocalActor(), entry)
	after, _ := service.Store.Head()
	if result.Status != "unchanged" || after != head {
		t.Fatalf("no-op created a new version: %+v", result)
	}
}

func TestRetryApprovalReturnsOriginalCommit(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "case-a", "原始正文")
	entry.Content += "，补充内容"
	pending := proposeEntry(t, service, k.LocalActor(), entry)
	first, err := service.Approve(context.Background(), k.LocalActor(), pending.Proposal.ID, pending.Proposal.Version)
	if err != nil {
		t.Fatal(err)
	}
	testSource(t, service, "another", "后续资料")
	before, _ := service.Store.Head()
	retry, err := service.Approve(context.Background(), k.LocalActor(), pending.Proposal.ID, pending.Proposal.Version)
	if err != nil || retry.CommitID != first.CommitID {
		t.Fatalf("retry lost original receipt: %+v %v", retry, err)
	}
	if after, _ := service.Store.Head(); after != before {
		t.Fatal("retry made another commit")
	}
}

func TestRemovingReferencedDefinitionRetainsUnappliedProposal(t *testing.T) {
	service := testService(t)
	definition := testSource(t, service, "definition", "<a id=\"queue\"></a>\n## 排队\n这是定义。")
	source, _ := service.Store.SaveSource("case://use", "引用该定义")
	ref := definition.Ref()
	ref.Version, ref.Fragment = "", "queue"
	owner := k.Entry{ID: "owner", LibraryID: "traffic", Home: "parking", Title: "引用", Origin: "source", Sources: []k.Source{source}, Content: "[定义](" + k.RefURL(ref) + ")"}
	proposeEntry(t, service, k.LocalActor(), owner)
	definition.Content = "删除了原定义小节"
	result := proposeEntry(t, service, k.LocalActor(), definition)
	if result.Status != "pending" || len(result.Proposal.Issues) == 0 {
		t.Fatal("removing a referenced stable fragment silently broke links")
	}
}

func TestLibraryScopedHumanCanReviewSchema(t *testing.T) {
	service := testService(t)
	state, _ := service.Store.Snapshot()
	schema := state.Libraries["traffic"].Schema
	schema.Domains = append(schema.Domains, k.Domain{ID: "shuttle", Parent: "transport", Name: "接驳", Scope: "接驳组织"})
	agent := k.Actor{ID: "agent", Grants: []k.Scope{{LibraryID: "traffic"}}}
	result, err := service.Propose(context.Background(), agent, state.Commit.ID, "扩展领域", []k.Change{{Kind: "schema", Schema: &schema}})
	if err != nil || result.Status != "pending" || len(result.Proposal.Diff) == 0 {
		t.Fatalf("%+v %v", result, err)
	}
	human := agent
	human.ID, human.Human = "reviewer", true
	shown, err := service.Proposal(human, result.Proposal.ID)
	if err != nil || len(shown.Diff) != 1 || !strings.Contains(shown.Diff[0].After, "shuttle") {
		t.Fatalf("schema diff not visible: %+v %v", shown, err)
	}
	if _, err := service.Approve(context.Background(), human, shown.ID, shown.Version); err != nil {
		t.Fatal(err)
	}
}

func TestTraceAccessRetainsOriginalScope(t *testing.T) {
	service := testService(t)
	testSource(t, service, "case-a", "停车排队")
	actor := k.Actor{ID: "reader", Grants: []k.Scope{{LibraryID: "traffic", DomainID: "parking"}}}
	result, err := service.Query(context.Background(), actor, QueryRequest{Question: "停车"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Trace(actor, result.TraceID); err != nil {
		t.Fatal(err)
	}
	revoked := k.Actor{ID: "reader", Grants: []k.Scope{{LibraryID: "other"}}}
	if _, err := service.Trace(revoked, result.TraceID); !errors.Is(err, k.ErrNotFound) {
		t.Fatal("revoked scope still allowed trace access")
	}
	stranger := actor
	stranger.ID = "another-user"
	if _, err := service.Trace(stranger, result.TraceID); !errors.Is(err, k.ErrNotFound) {
		t.Fatal("another user's private query was exposed")
	}
}

func TestModelOutputCannotBecomeANewOriginalSource(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "case-a", "原始资料")
	_, err := service.Revise(context.Background(), k.LocalActor(), ReviseRequest{Target: entry.ID, Instruction: "模型补充", Source: "invented://source"})
	if !errors.Is(err, k.ErrInvalid) {
		t.Fatalf("model output could masquerade as original evidence: %v", err)
	}
}

func TestRevisionRespectsReadAndByteBudgets(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "case-a", "原始正文")
	evidence := testSource(t, service, "case-b", "补充证据")
	ref := evidence.Ref()
	ref.Quote = evidence.Content
	head, _ := service.Store.Head()
	service.Model = ModelFunc(func(ctx context.Context, _ string, input, output any) error {
		t.Fatal("revision called the model after exceeding its read budget")
		return nil
	})
	for _, budget := range []Budget{{MaxReads: 1}, {MaxBytes: len(entry.Content) - 1}, {MaxBytes: len(entry.Content) + 1}} {
		_, err := service.Revise(context.Background(), k.LocalActor(), ReviseRequest{
			Target: entry.ID, Instruction: "参考补充资料修订", Evidence: []k.Ref{ref}, Budget: budget,
		})
		if !errors.Is(err, k.ErrInvalid) {
			t.Fatalf("revision escaped its read budget: %v", err)
		}
	}
	if after, _ := service.Store.Head(); after != head {
		t.Fatal("over-budget revision changed knowledge")
	}
}

func TestModelSchemaChangesRespectNarrowOperationScope(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "case-a", "停车资料")
	state, _ := service.Store.Snapshot()
	schema := state.Libraries["traffic"].Schema
	schema.Domains[0].Scope = "改写请求范围之外的领域说明"
	service.Model = ModelFunc(func(ctx context.Context, _ string, input, output any) error {
		switch input.(map[string]any)["operation"] {
		case "navigate":
			return modelOutput(navigation{Read: []k.Ref{entry.Ref()}, Done: true}, output)
		case "reflect":
			return modelOutput(reflectionPlan{Schemas: []k.Schema{schema}}, output)
		case "write":
			return modelOutput(writePlan{LibraryID: "traffic", Home: "parking", Schema: &schema}, output)
		}
		t.Fatal("unexpected model operation")
		return nil
	})
	scope := []k.Scope{{LibraryID: "traffic", DomainID: "parking"}}
	if _, err := service.Reflect(context.Background(), k.LocalActor(), ReflectRequest{Scope: scope}); !errors.Is(err, k.ErrForbidden) {
		t.Fatalf("reflection changed a schema outside its requested scope: %v", err)
	}
	if _, err := service.Write(context.Background(), k.LocalActor(), WriteRequest{
		Content: "新的停车资料", Source: "case://new", LibraryID: "traffic", DomainID: "parking", Scope: scope,
	}); !errors.Is(err, k.ErrForbidden) {
		t.Fatalf("write changed a schema outside its requested scope: %v", err)
	}
	if after, _ := service.Store.Head(); after != state.Commit.ID {
		t.Fatal("out-of-scope operation changed knowledge")
	}
}
