package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	k "asteria/internal/knowledge"
)

func TestProposalChecksTheBodyThatWillBeStored(t *testing.T) {
	service := testService(t)
	source, _ := service.Store.SaveSource("case://line-number", "\n原始证据\n")
	entry := k.Entry{ID: "new-source", LibraryID: "traffic", Home: "parking", Title: "资料", Origin: "source", Content: "\n原始证据\n", Sources: []k.Source{source}}
	data, _ := k.EncodeEntry(entry)
	ref := k.Ref{LibraryID: entry.LibraryID, EntryID: entry.ID, Version: k.Hash(data), StartLine: 2, EndLine: 2, Quote: "原始证据"}
	insight := k.Entry{ID: "new-insight", LibraryID: "traffic", Home: "parking", Title: "洞察", Origin: "inferred", Content: "候选说明", Evidence: []k.Ref{ref}}
	head, _ := service.Store.Head()
	result, err := service.Propose(context.Background(), k.LocalActor(), head, "check saved line numbers", []k.Change{{Kind: "entry", Entry: &entry}, {Kind: "entry", Entry: &insight}})
	if err != nil || result.Status != "pending" || len(result.Proposal.Issues) == 0 {
		t.Fatalf("accepted a line reference that breaks after serialization: %+v %v", result, err)
	}
	ref.StartLine, ref.EndLine = 1, 1
	insight.Evidence = []k.Ref{ref}
	result, err = service.Propose(context.Background(), k.LocalActor(), head, "correct saved line numbers", []k.Change{{Kind: "entry", Entry: &entry}, {Kind: "entry", Entry: &insight}})
	if err != nil || result.Status != "committed" {
		t.Fatalf("valid same-commit evidence rejected: %+v %v", result, err)
	}
	saved, _ := service.Read(k.LocalActor(), k.Ref{LibraryID: insight.LibraryID, EntryID: insight.ID})
	if saved.Status != "candidate" {
		t.Fatal("a reference to the newly published source was incorrectly invalidated")
	}
}

func TestMergePreservesInsightProvenanceAndEntrances(t *testing.T) {
	service := testService(t)
	original := testSource(t, service, "source-a", "停车原始证据")
	ref := original.Ref()
	ref.Quote = original.Content
	insight := k.Entry{ID: "insight-a", LibraryID: "traffic", Home: "parking", Title: "停车候选联系", Origin: "inferred", Content: "停车候选联系", Entrances: []string{"transport"}, Evidence: []k.Ref{ref}}
	proposeEntry(t, service, k.LocalActor(), insight)
	service.Model = ModelFunc(func(ctx context.Context, _ string, input, output any) error {
		return modelOutput(writePlan{LibraryID: "traffic", Home: "parking", Title: "合并后的候选联系", Content: "补充停车联系的解释", MergeTarget: insight.ID}, output)
	})
	result, err := service.Write(context.Background(), k.LocalActor(), WriteRequest{Content: "停车相关的新资料", Source: "case://new", LibraryID: "traffic", DomainID: "parking"})
	if err != nil || result.Status != "pending" || len(result.Proposal.Issues) != 0 {
		t.Fatalf("merge should produce a valid reviewed proposal: %+v %v", result, err)
	}
	if _, err := service.Approve(context.Background(), k.LocalActor(), result.Proposal.ID, result.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	saved, err := service.Read(k.LocalActor(), k.Ref{LibraryID: insight.LibraryID, EntryID: insight.ID})
	if err != nil || saved.Origin != "inferred" || !slices.Equal(saved.Entrances, insight.Entrances) || !slices.Equal(saved.Evidence, insight.Evidence) {
		t.Fatalf("merge lost identity, evidence or navigation metadata: %+v %v", saved, err)
	}
}

func TestHistoryUsesCurrentReadPermissions(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "case-a", "原来可见的资料")
	actor := k.Actor{ID: "reader", Grants: []k.Scope{{LibraryID: "traffic", DomainID: "transport"}}}
	state, _ := service.Store.Snapshot()
	schema := state.Libraries["traffic"].Schema
	schema.Domains[1].Parent = ""
	proposal, err := service.Propose(context.Background(), k.LocalActor(), state.Commit.ID, "move outside grant", []k.Change{{Kind: "schema", Schema: &schema}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(context.Background(), k.LocalActor(), proposal.Proposal.ID, proposal.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read(actor, entry.Ref()); err == nil {
		t.Fatal("entry still readable after domain moved outside grant")
	}
	history, err := service.History(actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, commit := range history {
		if slices.Contains(commit.Changed, entry.Ref().Key()) {
			t.Fatal("history exposed an entry hidden by current permissions")
		}
	}
}

func TestEvidenceChainCannotReadOutsideGrants(t *testing.T) {
	service := testService(t)
	source, _ := service.Store.SaveSource("case://private", "私有证据")
	private := k.Entry{ID: "private", LibraryID: "traffic", Home: "transport", Title: "私有资料", Origin: "source", Content: "私有证据", Sources: []k.Source{source}}
	proposeEntry(t, service, k.LocalActor(), private)
	private, _ = service.Read(k.LocalActor(), private.Ref())
	ref := private.Ref()
	ref.Quote = private.Content
	public := k.Entry{ID: "public-insight", LibraryID: "traffic", Home: "parking", Title: "公开候选", Origin: "inferred", Content: "候选说明", Evidence: []k.Ref{ref}}
	proposeEntry(t, service, k.LocalActor(), public)
	public, _ = service.Read(k.LocalActor(), public.Ref())
	ref = public.Ref()
	ref.Quote = public.Content
	derived := k.Entry{ID: "derived", LibraryID: "traffic", Home: "parking", Title: "进一步推导", Origin: "inferred", Content: "进一步推导", Evidence: []k.Ref{ref}}
	actor := k.Actor{ID: "parking-agent", Grants: []k.Scope{{LibraryID: "traffic", DomainID: "parking"}}}
	result := proposeEntry(t, service, actor, derived)
	if result.Status != "pending" || len(result.Proposal.Issues) == 0 {
		t.Fatal("indirect evidence traversal bypassed caller permissions")
	}
}

func TestApprovalRejectsAChangedRenderedDiff(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "case-a", "原始正文")
	entry.Content = "    缩进代码"
	pending := proposeEntry(t, service, k.LocalActor(), entry)
	legacy := pending.Proposal
	legacy.ID = k.NewID("proposal")
	// Reproduce a proposal created by the old serializer, which stripped
	// indentation while retaining the untrimmed change in the plan.
	legacy.Diff[0].After = strings.Replace(legacy.Diff[0].After, "\n    缩进代码\n", "\n缩进代码\n", 1)
	legacy.Version = legacy.Fingerprint()
	if err := service.Store.SaveProposal(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	head, _ := service.Store.Head()
	if _, err := service.Approve(context.Background(), k.LocalActor(), legacy.ID, legacy.Version); !errors.Is(err, k.ErrConflict) {
		t.Fatalf("published different Markdown from the approved diff: %v", err)
	}
	if after, _ := service.Store.Head(); after != head {
		t.Fatal("mismatched approval changed knowledge")
	}
}

func TestAgentCanMoveAnEntryWithoutReverifyingUnchangedRelations(t *testing.T) {
	service := testService(t)
	source := testSource(t, service, "source-a", "停车原始证据")
	ref := source.Ref()
	ref.Quote = source.Content
	insight := k.Entry{ID: "insight-a", LibraryID: "traffic", Home: "parking", Title: "已核查关系", Origin: "inferred", Content: "<!-- id: capacity -->\n容量约束", Evidence: []k.Ref{ref}, Relations: []k.Relation{{
		ID: "capacity", Subject: source.Ref(), Object: source.Ref(), Predicate: "constrains", Origin: "inferred", Status: "verified", Evidence: []k.Ref{ref}, Conditions: "仅用于演示", Reviewer: "local", ReviewedAt: k.Now(), ReviewNote: "已检查依据",
	}}}
	pending := proposeEntry(t, service, k.LocalActor(), insight)
	if _, err := service.Approve(context.Background(), k.LocalActor(), pending.Proposal.ID, pending.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	agent := k.Actor{ID: "agent", Grants: []k.Scope{{LibraryID: "traffic"}}}
	move, err := service.Revise(context.Background(), agent, ReviseRequest{Target: insight.ID, Instruction: "调整归属", Home: "transport"})
	if err != nil || move.Status != "pending" || len(move.Proposal.Issues) != 0 {
		t.Fatalf("a move incorrectly became a new verification: %+v %v", move, err)
	}
	forged := k.CloneEntry(*move.Proposal.Changes[0].Entry)
	forged.Relations[0].Conditions = "擅自扩大适用条件"
	if _, err := service.Propose(context.Background(), agent, move.Proposal.Base, "forged verification", []k.Change{{Kind: "entry", Entry: &forged}}); !errors.Is(err, k.ErrForbidden) {
		t.Fatalf("agent reused a review note for a changed relation: %v", err)
	}
	if _, err := service.Approve(context.Background(), k.LocalActor(), move.Proposal.ID, move.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	saved, _ := service.Read(k.LocalActor(), k.Ref{LibraryID: insight.LibraryID, EntryID: insight.ID})
	if saved.Home != "transport" || saved.Relations[0].Status != "verified" {
		t.Fatal("reviewed move did not retain the unchanged verification")
	}
}
