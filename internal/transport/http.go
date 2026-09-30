// Package transport keeps wire formats separate from knowledge operations.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"asteria/internal/core"
	k "asteria/internal/knowledge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func HTTP(service *core.Service, access Access) http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/libraries", func(w http.ResponseWriter, r *http.Request) {
		result, err := service.Libraries(actorFrom(r.Context()))
		respond(w, result, err)
	})
	api.HandleFunc("POST /v1/libraries", endpoint(func(ctx context.Context, actor k.Actor, schema k.Schema) (any, error) {
		result, err := service.Init(ctx, actor, schema)
		if err != nil {
			return nil, err
		}
		return map[string]string{"commit_id": result.Commit.ID, "library_id": schema.LibraryID}, nil
	}))
	api.HandleFunc("POST /v1/query", endpoint(service.Query))
	api.HandleFunc("POST /v1/write", endpoint(service.Write))
	api.HandleFunc("POST /v1/revise", endpoint(service.Revise))
	api.HandleFunc("POST /v1/reflect", endpoint(service.Reflect))
	api.HandleFunc("GET /v1/browse", func(w http.ResponseWriter, r *http.Request) {
		result, err := service.Browse(actorFrom(r.Context()), k.Scope{LibraryID: r.URL.Query().Get("library"), DomainID: r.URL.Query().Get("domain")})
		respond(w, result, err)
	})
	api.HandleFunc("GET /v1/entries/{library}/{entry}", func(w http.ResponseWriter, r *http.Request) {
		result, err := service.Read(actorFrom(r.Context()), k.Ref{
			LibraryID: r.PathValue("library"), EntryID: r.PathValue("entry"), Version: r.URL.Query().Get("version"),
		})
		respond(w, result, err)
	})
	api.HandleFunc("GET /v1/proposals", func(w http.ResponseWriter, r *http.Request) {
		result, err := service.Proposals(actorFrom(r.Context()))
		respond(w, result, err)
	})
	api.HandleFunc("GET /v1/proposals/{id}", func(w http.ResponseWriter, r *http.Request) {
		result, err := service.Proposal(actorFrom(r.Context()), r.PathValue("id"))
		respond(w, result, err)
	})
	type reviewRequest struct {
		Version string `json:"version"`
		Reason  string `json:"reason,omitempty"`
	}
	api.HandleFunc("POST /v1/proposals/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		var request reviewRequest
		if err := decode(w, r, &request); err != nil {
			respond(w, nil, err)
			return
		}
		result, err := service.Approve(r.Context(), actorFrom(r.Context()), r.PathValue("id"), request.Version)
		respond(w, result, err)
	})
	api.HandleFunc("POST /v1/proposals/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		var request reviewRequest
		if err := decode(w, r, &request); err != nil {
			respond(w, nil, err)
			return
		}
		result, err := service.Reject(r.Context(), actorFrom(r.Context()), r.PathValue("id"), request.Version, request.Reason)
		respond(w, result, err)
	})
	api.HandleFunc("GET /v1/history", func(w http.ResponseWriter, r *http.Request) {
		result, err := service.History(actorFrom(r.Context()))
		respond(w, result, err)
	})
	api.HandleFunc("GET /v1/traces/{id}", func(w http.ResponseWriter, r *http.Request) {
		result, err := service.Trace(actorFrom(r.Context()), r.PathValue("id"))
		respond(w, result, err)
	})
	api.HandleFunc("POST /v1/reindex", endpoint(func(ctx context.Context, actor k.Actor, _ struct{}) (any, error) {
		err := service.Reindex(ctx, actor)
		return map[string]string{"status": "ready"}, err
	}))
	api.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return MCP(service, actorFrom(r.Context()))
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	root.Handle("/", access.protect(http.NewCrossOriginProtection().Handler(api)))
	return root
}

func endpoint[Request, Result any](fn func(context.Context, k.Actor, Request) (Result, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request Request
		if err := decode(w, r, &request); err != nil {
			respond(w, nil, err)
			return
		}
		result, err := fn(r.Context(), actorFrom(r.Context()), request)
		respond(w, result, err)
	}
}

func decode(w http.ResponseWriter, r *http.Request, output any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("%w: invalid JSON request: %v", k.ErrInvalid, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%w: expected one JSON value", k.ErrInvalid)
	}
	return nil
}

func respond(w http.ResponseWriter, result any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, result)
		return
	}
	code, status, message := "internal", http.StatusInternalServerError, "internal operation failed"
	switch {
	case errors.Is(err, k.ErrInvalid):
		code, status, message = "invalid", http.StatusBadRequest, err.Error()
	case errors.Is(err, k.ErrNotFound):
		code, status, message = "not_found", http.StatusNotFound, err.Error()
	case errors.Is(err, k.ErrForbidden):
		code, status, message = "forbidden", http.StatusForbidden, err.Error()
	case errors.Is(err, k.ErrConflict):
		code, status, message = "conflict", http.StatusConflict, err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		code, status, message = "timeout", http.StatusGatewayTimeout, "operation exceeded its time budget"
	default:
		slog.Error("operation failed", "error", err)
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
