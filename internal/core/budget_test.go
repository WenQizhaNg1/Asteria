package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	k "asteria/internal/knowledge"
)

func TestModelOperationBudgetCoversProposalLock(t *testing.T) {
	for _, operation := range []string{"write", "revise"} {
		t.Run(operation, func(t *testing.T) {
			service := testService(t)
			entry := testSource(t, service, "budget-source", "停车资料")
			head, err := service.Store.Head()
			if err != nil {
				t.Fatal(err)
			}
			proposals, err := service.Store.ProposalIDs()
			if err != nil {
				t.Fatal(err)
			}
			service.Model = ModelFunc(func(ctx context.Context, _ string, input, output any) error {
				return modelOutput(map[string]string{
					"home": "parking", "title": "补充资料", "content": "新的停车资料",
				}, output)
			})
			// A concurrent writer holds the same lock used to save proposals.
			lock, err := os.OpenFile(filepath.Join(service.Store.Root(), ".lock"), os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				if operation == "write" {
					_, err = service.Write(ctx, k.LocalActor(), WriteRequest{
						Content: "新的停车资料", Source: "case://budget-write", LibraryID: "traffic", DomainID: "parking", Budget: Budget{Seconds: 1},
					})
				} else {
					_, err = service.Revise(ctx, k.LocalActor(), ReviseRequest{
						Target: entry.ID, Instruction: "补充资料", Budget: Budget{Seconds: 1},
					})
				}
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("expected operation deadline while waiting for the lock, got %v", err)
				}
			case <-time.After(2 * time.Second):
				cancel()
				<-done
				t.Fatal("operation timeout was lost after the model returned")
			}
			current, err := service.Store.Head()
			if err != nil || current != head {
				t.Fatalf("timed-out operation published knowledge: %s %v", current, err)
			}
			currentProposals, err := service.Store.ProposalIDs()
			if err != nil || len(currentProposals) != len(proposals) {
				t.Fatalf("timed-out operation saved a proposal: %v", err)
			}
		})
	}
}
