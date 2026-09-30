package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asteria/internal/core"
	k "asteria/internal/knowledge"
)

func TestCLIEndToEnd(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	run := func(output any, args ...string) error {
		t.Helper()
		var stdout, stderr bytes.Buffer
		command := New(strings.NewReader(""), &stdout, &stderr)
		command.SetArgs(append([]string{"--offline", "--data", dataDir}, args...))
		if err := command.Execute(); err != nil {
			return err
		}
		if output != nil {
			if err := json.Unmarshal(stdout.Bytes(), output); err != nil {
				t.Fatalf("bad CLI JSON %s: %v", stdout.Bytes(), err)
			}
		}
		return nil
	}
	examples := filepath.Join("..", "..", "examples")
	if err := run(nil, "init", "--schema", filepath.Join(examples, "traffic.yaml")); err != nil {
		t.Fatal(err)
	}
	var written k.CommitResult
	if err := run(&written, "write", "--library", "traffic", "--domain", "parking", "--source", "demo://parking", "--file", filepath.Join(examples, "parking.md")); err != nil {
		t.Fatal(err)
	}
	if written.Status != "committed" {
		t.Fatalf("write failed: %+v", written)
	}
	id := written.Proposal.Changes[0].Entry.ID
	var query core.QueryResult
	if err := run(&query, "query", "停车排队", "--scope", "traffic/parking"); err != nil {
		t.Fatal(err)
	}
	if len(query.Evidence) == 0 {
		t.Fatal("CLI did not return evidence")
	}
	var revised k.CommitResult
	if err := run(&revised, "revise", id, "--instruction", "补充失效条件", "--file", filepath.Join(examples, "parking-revised.md")); err != nil {
		t.Fatal(err)
	}
	if revised.Status != "pending" {
		t.Fatal("CLI skipped revision review")
	}
	if err := run(nil, "proposal", "approve", revised.Proposal.ID); err == nil {
		t.Fatal("approval accepted no version")
	}
	if err := run(nil, "proposal", "approve", revised.Proposal.ID, "--version", revised.Proposal.Version); err != nil {
		t.Fatal(err)
	}
	var historical k.Entry
	if err := run(&historical, "read", id, "--version", query.Evidence[0].Ref.Version); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(historical.Content, "新补充的失效条件") {
		t.Fatal("old evidence returned new content")
	}
	exportDir := filepath.Join(t.TempDir(), "export")
	if err := run(nil, "export", exportDir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(exportDir, "traffic", "transport", "parking", id+".md")
	if content, err := os.ReadFile(path); err != nil || !strings.Contains(string(content), "新补充的失效条件") {
		t.Fatalf("working copy was not exported: %v", err)
	}
}

func TestJSONAccessConfigurationUsesAPIFieldNames(t *testing.T) {
	var value map[string]any
	if err := load(filepath.Join("..", "..", "examples", "access.example.json"), &value); err != nil {
		t.Fatal(err)
	}
	if value["local_only"] != false {
		t.Fatal("access configuration was misread")
	}
}

func TestConfigurationRejectsAdditionalYAMLDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.yaml")
	if err := os.WriteFile(path, []byte("library_id: traffic\nname: 交通\n---\nlibrary_id: another\nname: 其他\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var schema k.Schema
	if err := load(path, &schema); err == nil {
		t.Fatal("configuration silently ignored a second YAML document")
	}
}
