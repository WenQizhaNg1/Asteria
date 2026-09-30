// Package knowledge defines the file format and the model-independent rules.
package knowledge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	ErrInvalid   = errors.New("invalid request")
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	ErrConflict  = errors.New("version conflict")
)

type Domain struct {
	ID       string `json:"id" yaml:"id"`
	Parent   string `json:"parent,omitempty" yaml:"parent,omitempty"`
	Name     string `json:"name" yaml:"name"`
	Scope    string `json:"scope" yaml:"scope"`
	Overview string `json:"overview,omitempty" yaml:"overview,omitempty"`
}

type Schema struct {
	LibraryID string   `json:"library_id" yaml:"library_id"`
	Name      string   `json:"name" yaml:"name"`
	Domains   []Domain `json:"domains" yaml:"domains"`
}

type Source struct {
	ID         string `json:"id" yaml:"id"`
	Origin     string `json:"origin" yaml:"origin"`
	Snapshot   string `json:"snapshot" yaml:"snapshot"`
	CapturedAt string `json:"captured_at" yaml:"captured_at"`
}

// Ref identifies a definition or a version-bound evidence fragment.
type Ref struct {
	LibraryID string `json:"library_id" yaml:"library_id"`
	EntryID   string `json:"entry_id" yaml:"entry_id"`
	Version   string `json:"version,omitempty" yaml:"version,omitempty"`
	Fragment  string `json:"fragment,omitempty" yaml:"fragment,omitempty"`
	StartLine int    `json:"start_line,omitempty" yaml:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty" yaml:"end_line,omitempty"`
	Quote     string `json:"quote,omitempty" yaml:"quote,omitempty"`
}

func (r Ref) Key() string { return r.LibraryID + "/" + r.EntryID }

type Relation struct {
	ID           string `json:"id" yaml:"id"`
	Subject      Ref    `json:"subject" yaml:"subject"`
	Predicate    string `json:"predicate" yaml:"predicate"`
	Object       Ref    `json:"object" yaml:"object"`
	Origin       string `json:"origin" yaml:"origin"`
	Status       string `json:"status" yaml:"status"`
	Evidence     []Ref  `json:"evidence" yaml:"evidence"`
	Conditions   string `json:"conditions" yaml:"conditions"`
	Differences  string `json:"differences,omitempty" yaml:"differences,omitempty"`
	Reviewer     string `json:"reviewer,omitempty" yaml:"reviewer,omitempty"`
	ReviewedAt   string `json:"reviewed_at,omitempty" yaml:"reviewed_at,omitempty"`
	ReviewNote   string `json:"review_note,omitempty" yaml:"review_note,omitempty"`
	ReviewReason string `json:"review_reason,omitempty" yaml:"review_reason,omitempty"`
}

type Entry struct {
	ID           string     `json:"id" yaml:"id"`
	LibraryID    string     `json:"library_id" yaml:"library_id"`
	Home         string     `json:"home" yaml:"home"`
	Entrances    []string   `json:"entrances,omitempty" yaml:"entrances,omitempty"`
	Title        string     `json:"title" yaml:"title"`
	Summary      string     `json:"summary,omitempty" yaml:"summary,omitempty"`
	Origin       string     `json:"origin" yaml:"origin"`
	Status       string     `json:"status,omitempty" yaml:"status,omitempty"`
	ReviewReason string     `json:"review_reason,omitempty" yaml:"review_reason,omitempty"`
	Sources      []Source   `json:"sources,omitempty" yaml:"sources,omitempty"`
	Evidence     []Ref      `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	Relations    []Relation `json:"relations,omitempty" yaml:"relations,omitempty"`
	Withdrawn    bool       `json:"withdrawn,omitempty" yaml:"withdrawn,omitempty"`
	Content      string     `json:"content" yaml:"-"`
	Version      string     `json:"version" yaml:"-"`
}

func (e Entry) Ref() Ref {
	return Ref{LibraryID: e.LibraryID, EntryID: e.ID, Version: e.Version}
}

func CloneEntry(e Entry) Entry {
	e.Entrances = slices.Clone(e.Entrances)
	e.Sources = slices.Clone(e.Sources)
	e.Evidence = slices.Clone(e.Evidence)
	e.Relations = slices.Clone(e.Relations)
	for i := range e.Relations {
		e.Relations[i].Evidence = slices.Clone(e.Relations[i].Evidence)
	}
	return e
}

type Library struct {
	Schema  Schema           `json:"schema"`
	Entries map[string]Entry `json:"entries"`
}

type Commit struct {
	ID         string            `json:"id"`
	Parent     string            `json:"parent,omitempty"`
	Actor      string            `json:"actor"`
	ProposalID string            `json:"proposal_id,omitempty"`
	Reason     string            `json:"reason"`
	CreatedAt  string            `json:"created_at"`
	Applied    map[string]string `json:"applied_proposals"`
	Changed    []string          `json:"changed"`
}

type Snapshot struct {
	Commit    Commit             `json:"commit"`
	Libraries map[string]Library `json:"libraries"`
}

type Scope struct {
	LibraryID string `json:"library_id"`
	DomainID  string `json:"domain_id,omitempty"`
}

// Actor is supplied by a trusted transport, never by a model response.
type Actor struct {
	ID     string  `json:"id"`
	Human  bool    `json:"human"`
	Admin  bool    `json:"admin"`
	Grants []Scope `json:"grants,omitempty"`
}

func LocalActor() Actor { return Actor{ID: "local", Human: true, Admin: true} }

func (a Actor) Allows(schema Schema, domain string) bool {
	if a.Admin {
		return true
	}
	for _, grant := range a.Grants {
		if grant.LibraryID == schema.LibraryID && (grant.DomainID == "" || IsWithin(schema, domain, grant.DomainID)) {
			return true
		}
	}
	return false
}

func (a Actor) CanSeeLibrary(id string) bool {
	if a.Admin {
		return true
	}
	for _, grant := range a.Grants {
		if grant.LibraryID == id {
			return true
		}
	}
	return false
}

func (a Actor) ManagesLibrary(id string) bool {
	if a.Admin {
		return true
	}
	for _, grant := range a.Grants {
		if grant.LibraryID == id && grant.DomainID == "" {
			return true
		}
	}
	return false
}

func IsWithin(schema Schema, domain, ancestor string) bool {
	for steps := 0; domain != "" && steps <= len(schema.Domains); steps++ {
		if domain == ancestor {
			return true
		}
		found := false
		for _, d := range schema.Domains {
			if d.ID == domain {
				domain, found = d.Parent, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return false
}

func InScope(schema Schema, domain string, scopes []Scope) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, scope := range scopes {
		if scope.LibraryID == schema.LibraryID && (scope.DomainID == "" || IsWithin(schema, domain, scope.DomainID)) {
			return true
		}
	}
	return false
}

var safeID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$`)

func ValidID(id string) bool { return safeID.MatchString(id) }

func NewID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func Digest(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err) // Domain records contain only JSON-compatible value types.
	}
	return Hash(data)
}

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func ValidateSchema(s Schema) error {
	if !ValidID(s.LibraryID) || strings.TrimSpace(s.Name) == "" || len(s.Domains) == 0 {
		return fmt.Errorf("%w: schema needs a library ID, name and at least one domain", ErrInvalid)
	}
	domains := make(map[string]Domain, len(s.Domains))
	for _, d := range s.Domains {
		if !ValidID(d.ID) || strings.TrimSpace(d.Name) == "" || strings.TrimSpace(d.Scope) == "" {
			return fmt.Errorf("%w: each domain needs a safe ID, name and scope", ErrInvalid)
		}
		if _, exists := domains[d.ID]; exists {
			return fmt.Errorf("%w: duplicate domain %s", ErrInvalid, d.ID)
		}
		domains[d.ID] = d
	}
	for _, d := range s.Domains {
		seen := map[string]bool{d.ID: true}
		for parent := d.Parent; parent != ""; {
			p, ok := domains[parent]
			if !ok || seen[parent] {
				return fmt.Errorf("%w: missing parent or domain cycle at %s", ErrInvalid, d.ID)
			}
			seen[parent], parent = true, p.Parent
		}
	}
	return nil
}

func DomainPath(s Schema, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("%w: entry needs a home domain", ErrInvalid)
	}
	var parts []string
	seen := make(map[string]bool)
	for id != "" {
		if seen[id] {
			return "", fmt.Errorf("%w: domain cycle", ErrInvalid)
		}
		seen[id] = true
		found := false
		for _, d := range s.Domains {
			if d.ID == id {
				parts = append([]string{d.ID}, parts...)
				id, found = d.Parent, true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("%w: domain %s", ErrNotFound, id)
		}
	}
	return strings.Join(parts, "/"), nil
}
