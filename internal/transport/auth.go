package transport

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strings"

	k "asteria/internal/knowledge"
)

type Credential struct {
	Token string  `json:"token"`
	Actor k.Actor `json:"actor"`
}

type Access struct {
	Credentials []Credential `json:"credentials"`
	LocalOnly   bool         `json:"local_only"`
}

type actorKey struct{}

func actorFrom(ctx context.Context) k.Actor {
	actor, _ := ctx.Value(actorKey{}).(k.Actor)
	return actor
}

func (a Access) authenticate(r *http.Request) (k.Actor, error) {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		token := strings.TrimPrefix(header, "Bearer ")
		for _, credential := range a.Credentials {
			if len(token) > 0 && subtle.ConstantTimeCompare([]byte(token), []byte(credential.Token)) == 1 {
				return credential.Actor, nil
			}
		}
		return k.Actor{}, fmt.Errorf("invalid access token")
	}
	if a.LocalOnly && len(a.Credentials) == 0 {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		requestHost := r.Host
		if h, _, splitErr := net.SplitHostPort(r.Host); splitErr == nil {
			requestHost = h
		}
		requestIP := net.ParseIP(requestHost)
		if err == nil && ip != nil && ip.IsLoopback() && (requestHost == "localhost" || (requestIP != nil && requestIP.IsLoopback())) {
			// Local API clients are agents. Human approval is done by CLI or
			// an explicitly configured human credential.
			return k.Actor{ID: "local-api", Admin: true, Human: false}, nil
		}
	}
	return k.Actor{}, fmt.Errorf("authentication required")
}

func (a Access) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, err := a.authenticate(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "unauthorized", "message": err.Error()}})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, actor)))
	})
}
