package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"asteria/internal/core"
	k "asteria/internal/knowledge"
	"asteria/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testService(t *testing.T) *core.Service {
	t.Helper()
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := core.New(storage, nil)
	if _, err := service.Init(context.Background(), k.LocalActor(), k.Schema{
		LibraryID: "traffic", Name: "交通", Domains: []k.Domain{{ID: "parking", Name: "停车", Scope: "停车排队"}},
	}); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestHTTPAndMCPShareOperationsAndReviewRules(t *testing.T) {
	service := testService(t)
	handler := HTTP(service, Access{Credentials: []Credential{
		{Token: "agent-key", Actor: k.Actor{ID: "agent", Admin: true}},
		{Token: "human-key", Actor: k.LocalActor()},
	}})
	call := func(method, path, token string, input, output any) int {
		t.Helper()
		data, _ := json.Marshal(input)
		request := httptest.NewRequest(method, path, bytes.NewReader(data))
		request.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if output != nil {
			if err := json.Unmarshal(recorder.Body.Bytes(), output); err != nil {
				t.Fatalf("%s: %v", recorder.Body, err)
			}
		}
		return recorder.Code
	}
	var written k.CommitResult
	status := call("POST", "/v1/write", "agent-key", core.WriteRequest{Content: "停车场饱和时检查接驳运力。", Source: "test://source", LibraryID: "traffic", DomainID: "parking"}, &written)
	if status != http.StatusOK || written.Status != "committed" {
		t.Fatalf("HTTP write failed: %d %+v", status, written)
	}
	entryID := written.Proposal.Changes[0].Entry.ID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := MCP(service, k.Actor{ID: "mcp", Admin: true}).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) < 4 {
		t.Fatalf("MCP tool discovery failed: %v", err)
	}
	mcpResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "query", Arguments: core.QueryRequest{Question: "停车场饱和"}})
	if err != nil || mcpResult.IsError {
		t.Fatalf("MCP query failed: %+v %v", mcpResult, err)
	}
	data, _ := json.Marshal(mcpResult.StructuredContent)
	var fromMCP core.QueryResult
	if err := json.Unmarshal(data, &fromMCP); err != nil {
		t.Fatal(err)
	}
	var fromHTTP core.QueryResult
	if call("POST", "/v1/query", "agent-key", core.QueryRequest{Question: "停车场饱和"}, &fromHTTP) != http.StatusOK {
		t.Fatal("HTTP query failed")
	}
	if len(fromMCP.Evidence) != 1 || len(fromHTTP.Evidence) != 1 || fromMCP.Evidence[0].Ref != fromHTTP.Evidence[0].Ref {
		t.Fatal("HTTP and MCP returned different evidence")
	}
	replacement := "停车场饱和时还需核实道路容量。"
	var revised k.CommitResult
	status = call("POST", "/v1/revise", "agent-key", core.ReviseRequest{Target: entryID, Instruction: "补充条件", Content: &replacement}, &revised)
	if status != http.StatusOK || revised.Status != "pending" {
		t.Fatalf("revision was not pending: %d %+v", status, revised)
	}
	path := "/v1/proposals/" + revised.Proposal.ID + "/approve"
	body := map[string]string{"version": revised.Proposal.Version}
	if call("POST", path, "agent-key", body, nil) != http.StatusForbidden {
		t.Fatal("agent approved its own proposal through HTTP")
	}
	if call("POST", path, "human-key", body, nil) != http.StatusOK {
		t.Fatal("human could not approve the exact proposal")
	}
	if call("GET", "/v1/libraries", "wrong-key", nil, nil) != http.StatusUnauthorized {
		t.Fatal("invalid token accepted")
	}
}

func TestHTTPRejectsForgedActor(t *testing.T) {
	handler := HTTP(testService(t), Access{Credentials: []Credential{{Token: "agent", Actor: k.Actor{ID: "agent", Admin: true}}}})
	request := httptest.NewRequest("POST", "/v1/query", bytes.NewBufferString(`{"question":"停车","actor":{"human":true,"admin":true}}`))
	request.Header.Set("Authorization", "Bearer agent")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("caller-supplied identity accepted: %d", response.Code)
	}
}

type bearerTransport struct{ token string }

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(copy)
}

func TestRemoteMCPKeepsAuthenticatedScope(t *testing.T) {
	service := testService(t)
	written, err := service.Write(context.Background(), k.LocalActor(), core.WriteRequest{
		Content: "停车场排队案例。", Source: "case://remote", LibraryID: "traffic", DomainID: "parking",
	})
	if err != nil || written.Status != "committed" {
		t.Fatalf("%+v %v", written, err)
	}
	server := httptest.NewServer(HTTP(service, Access{Credentials: []Credential{{
		Token: "limited-token", Actor: k.Actor{ID: "remote-agent", Grants: []k.Scope{{LibraryID: "traffic", DomainID: "parking"}}},
	}}}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "remote-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: "limited-token"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "query", Arguments: core.QueryRequest{Question: "停车"}})
	if err != nil || result.IsError {
		t.Fatalf("remote MCP failed: %+v %v", result, err)
	}
	data, _ := json.Marshal(result.StructuredContent)
	var query core.QueryResult
	if err := json.Unmarshal(data, &query); err != nil {
		t.Fatal(err)
	}
	if len(query.Evidence) != 1 || query.Evidence[0].Ref.LibraryID != "traffic" {
		t.Fatalf("remote principal did not reach the shared operation layer: %+v", query)
	}
}
