package core

import (
	"context"
	"fmt"
	"testing"

	k "asteria/internal/knowledge"
)

func TestReviewedOrganizationReducesNavigationSteps(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "parking-case", "接驳运力不足时分流方案会失效。")
	state, _ := service.Store.Snapshot()
	schema := state.Libraries["traffic"].Schema
	schema.Domains[1].Parent = ""
	question := "如何评估措施的失效条件？"
	service.Model = ModelFunc(func(ctx context.Context, _ string, input, output any) error {
		args := input.(map[string]any)
		switch args["operation"] {
		case "navigate":
			// The same navigator follows directories in both runs; no hidden ID
			// is supplied until the entry appears in a visible directory.
			for _, directory := range args["directories"].([]Directory) {
				for _, item := range directory.Entries {
					if item.Ref.EntryID == entry.ID {
						return modelOutput(navigation{Read: []k.Ref{item.Ref}, Done: true}, output)
					}
				}
			}
			return modelOutput(navigation{Browse: []k.Scope{{LibraryID: "traffic", DomainID: "parking"}}}, output)
		case "answer":
			ref := entry.Ref()
			ref.Quote = entry.Content
			return modelOutput(map[string]any{"claims": []Claim{{Text: entry.Content, Kind: "supported", Evidence: []k.Ref{ref}}}}, output)
		case "reflect":
			feedback := args["feedback"].([]feedback)
			if len(feedback) == 0 || feedback[0].Question != question {
				t.Fatal("reflection did not receive the preceding query")
			}
			return modelOutput(reflectionPlan{Schemas: []k.Schema{schema}}, output)
		}
		return fmt.Errorf("unexpected model operation")
	})
	request := QueryRequest{Question: question, Budget: Budget{MaxSteps: 4, MaxReads: 1}}
	before, err := service.Query(context.Background(), k.LocalActor(), request)
	if err != nil {
		t.Fatal(err)
	}
	reflection, err := service.Reflect(context.Background(), k.LocalActor(), ReflectRequest{Goal: "缩短停车案例的目录查找路径"})
	if err != nil || len(reflection.Changes) != 1 || reflection.Changes[0].Status != "pending" {
		t.Fatalf("organization change must await review: %+v %v", reflection, err)
	}
	proposal := reflection.Changes[0].Proposal
	if _, err := service.Approve(context.Background(), k.LocalActor(), proposal.ID, proposal.Version); err != nil {
		t.Fatal(err)
	}
	after, err := service.Query(context.Background(), k.LocalActor(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Claims) != 1 || len(after.Claims) != 1 || before.Claims[0].Evidence[0] != after.Claims[0].Evidence[0] {
		t.Fatal("organization change lost the expected source or its stable version")
	}
	if before.Reads != 1 || after.Reads != 1 || before.Steps != 3 || after.Steps != 2 {
		t.Fatalf("expected fewer navigation calls with the same question, model and budget: before=%+v after=%+v", before, after)
	}
	t.Logf("same evidence and budget: model calls %d -> %d, reads %d -> %d", before.Steps, after.Steps, before.Reads, after.Reads)
}

func TestReflectionReceivesOnlyAuthorizedFeedback(t *testing.T) {
	service := testService(t)
	entry := testSource(t, service, "parking-case", "停车排队")
	actor := k.Actor{ID: "reader", Grants: []k.Scope{{LibraryID: "traffic"}}}
	parking := []k.Scope{{LibraryID: "traffic", DomainID: "parking"}}
	stranger := actor
	stranger.ID = "someone-else"
	for _, tc := range []struct {
		actor   k.Actor
		request QueryRequest
	}{
		{actor, QueryRequest{Question: "更大范围的私人问题"}},
		{stranger, QueryRequest{Question: "其他人的私人问题", Scope: parking}},
		{actor, QueryRequest{Question: "停车排队的缺口", Scope: parking}},
	} {
		if _, err := service.Query(context.Background(), tc.actor, tc.request); err != nil {
			t.Fatal(err)
		}
	}
	called := false
	service.Model = ModelFunc(func(ctx context.Context, _ string, input, output any) error {
		args := input.(map[string]any)
		if args["operation"] == "navigate" {
			return modelOutput(navigation{Read: []k.Ref{entry.Ref()}, Done: true}, output)
		}
		called = true
		items := args["feedback"].([]feedback)
		if len(items) != 1 || items[0].Question != "停车排队的缺口" {
			t.Fatalf("reflection exposed private or out-of-scope feedback: %+v", items)
		}
		return modelOutput(reflectionPlan{}, output)
	})
	if _, err := service.Reflect(context.Background(), actor, ReflectRequest{Scope: parking}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("reflection model was not called")
	}
}
