package knowledge

// Changes are whole-file replacements. The exact before/after files are part of
// the approved proposal; models cannot send arbitrary filesystem operations.
type Change struct {
	Kind   string  `json:"kind"`
	Entry  *Entry  `json:"entry,omitempty"`
	Schema *Schema `json:"schema,omitempty"`
}

type FileDiff struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type Proposal struct {
	ID             string     `json:"id"`
	Version        string     `json:"version"`
	Base           string     `json:"base"`
	Actor          string     `json:"actor"`
	Reason         string     `json:"reason"`
	CreatedAt      string     `json:"created_at"`
	Status         string     `json:"status"`
	RequiresReview bool       `json:"requires_review"`
	Changes        []Change   `json:"changes"`
	Diff           []FileDiff `json:"diff"`
	Issues         []string   `json:"issues,omitempty"`
	ReviewedBy     string     `json:"reviewed_by,omitempty"`
	ReviewNote     string     `json:"review_note,omitempty"`
}

// Fingerprint excludes mutable review state, but binds the complete plan.
func (p Proposal) Fingerprint() string {
	p.Version, p.Status, p.ReviewedBy, p.ReviewNote = "", "", "", ""
	return Digest(p)
}

type CommitResult struct {
	Proposal    Proposal `json:"proposal"`
	CommitID    string   `json:"commit_id,omitempty"`
	Status      string   `json:"status"`
	IndexStatus string   `json:"index_status,omitempty"`
	Warning     string   `json:"warning,omitempty"`
	Duplicate   *Ref     `json:"duplicate,omitempty"`
}
