package core

import (
	"encoding/json"
	"fmt"
	"sort"

	k "asteria/internal/knowledge"
)

type feedback struct {
	Question   string   `json:"question"`
	StopReason string   `json:"stop_reason"`
	Unresolved []string `json:"unresolved,omitempty"`
	CreatedAt  string   `json:"created_at"`
}

type traceHeader struct {
	Actor     string    `json:"actor"`
	Scope     []k.Scope `json:"scope"`
	CreatedAt string    `json:"created_at"`
	Request   struct {
		Question string `json:"question"`
	} `json:"request"`
	Result struct {
		StopReason string   `json:"stop_reason"`
		Unresolved []string `json:"unresolved"`
	} `json:"result"`
}

func effectiveScopes(snapshot *k.Snapshot, actor k.Actor, requested []k.Scope) []k.Scope {
	var result []k.Scope
	for _, lib := range snapshot.Libraries {
		for _, domain := range lib.Schema.Domains {
			if actor.Allows(lib.Schema, domain.ID) && k.InScope(lib.Schema, domain.ID, requested) {
				result = append(result, k.Scope{LibraryID: lib.Schema.LibraryID, DomainID: domain.ID})
			}
		}
	}
	return result
}

func traceAllowed(snapshot *k.Snapshot, actor k.Actor, requested []k.Scope, header traceHeader) bool {
	if !actor.Admin && actor.ID != header.Actor {
		return false
	}
	if len(header.Scope) == 0 {
		return false
	}
	for _, scope := range header.Scope {
		lib, exists := snapshot.Libraries[scope.LibraryID]
		if !exists || !actor.Allows(lib.Schema, scope.DomainID) || !k.InScope(lib.Schema, scope.DomainID, requested) {
			return false
		}
	}
	return true
}

func (s *Service) Trace(actor k.Actor, id string) (json.RawMessage, error) {
	data, err := s.Store.Trace(id)
	if err != nil {
		return nil, err
	}
	var header traceHeader
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, err
	}
	snapshot, err := s.Store.Snapshot()
	if err != nil {
		return nil, err
	}
	if !traceAllowed(snapshot, actor, nil, header) {
		return nil, fmt.Errorf("%w: trace", k.ErrNotFound)
	}
	return data, nil
}

func (s *Service) recentFeedback(snapshot *k.Snapshot, actor k.Actor, requested []k.Scope) []feedback {
	ids, err := s.Store.TraceIDs()
	if err != nil {
		return nil
	}
	var result []feedback
	for _, id := range ids {
		data, err := s.Store.Trace(id)
		if err != nil {
			continue
		}
		var header traceHeader
		if json.Unmarshal(data, &header) != nil || header.Request.Question == "" || !traceAllowed(snapshot, actor, requested, header) {
			continue
		}
		result = append(result, feedback{Question: header.Request.Question, StopReason: header.Result.StopReason, Unresolved: header.Result.Unresolved, CreatedAt: header.CreatedAt})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt > result[j].CreatedAt })
	if len(result) > 5 {
		result = result[:5]
	}
	return result
}
