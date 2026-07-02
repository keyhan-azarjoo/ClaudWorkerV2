// Package knowledge is the Knowledge Brain (docs/04_ProjectBrain.md, docs/21 S4).
//
// The Knowledge Brain stores ONLY durable engineering knowledge — architecture, patterns,
// standards, decisions, rules, glossary — never execution state (that is the Assignment Store, S3).
// It is 100% deterministic Go: it does NOT embed, vectorise, or semantically rank anything, and it
// spends ZERO model tokens (Law 18, P9). AI may only PROPOSE knowledge (written as a Draft entry for
// a human to approve); it never invents knowledge that the Brain treats as truth.
//
// Design invariants (owner-approved S4 modifications):
//  1. Category is a documented-vocabulary STRING, not a hard-coded enum — a project may introduce a
//     new category with no code change (RecommendedCategories is guidance, not validation).
//  2. ID is stable forever. An Update writes a NEW version under the SAME id; the version history
//     belongs to the id; a duplicate id is never created.
//  3. Source is one of a fixed set (for future trust evaluation).
//  4. Status is one of {active, deprecated, archived, draft}; the Prompt Builder returns only
//     active entries unless non-active are explicitly requested.
//  5. Storage is append-only: a version record is never rewritten or deleted; the current entry is
//     DERIVED as the highest-version record for an id.
package knowledge

import "fmt"

// SchemaVersion is the on-disk record FORMAT version (metadata, mirroring assignment.StateVersion).
// Bump it only when the stored record layout changes, adding a migrate() branch so old records
// upgrade deterministically. It is NOT knowledge content.
const SchemaVersion = 1

// Source identifies WHERE a knowledge entry originated. It is a closed set so a later trust model
// can weight entries by provenance (e.g. Human/Architecture over Code/Migration). Every entry must
// record exactly one.
type Source string

const (
	SourceHuman         Source = "human"         // stated by the owner/a person
	SourceArchitecture  Source = "architecture"  // derived from the frozen architecture docs
	SourceACP           Source = "acp"           // established by an Architecture Change Proposal
	SourceDocumentation Source = "documentation" // extracted from project documentation
	SourceCode          Source = "code"          // derived from the repository (idioms, structure)
	SourcePlugin        Source = "plugin"        // produced by a project plugin
	SourceMigration     Source = "migration"     // seeded by the migration phase (docs/22)
)

// Sources is the closed provenance set (also the CLI/vocabulary reference).
var Sources = []Source{
	SourceHuman, SourceArchitecture, SourceACP, SourceDocumentation,
	SourceCode, SourcePlugin, SourceMigration,
}

// Valid reports whether s is a known provenance.
func (s Source) Valid() bool {
	for _, v := range Sources {
		if s == v {
			return true
		}
	}
	return false
}

// Status controls prompt eligibility and lifecycle. Nothing is ever permanently deleted; retiring
// an entry is a status change (a new version), so its history is preserved (invariant 5).
type Status string

const (
	StatusActive     Status = "active"     // current, trusted; eligible for prompts
	StatusDeprecated Status = "deprecated" // superseded but kept for history; excluded from prompts
	StatusArchived   Status = "archived"   // retired; excluded from prompts
	StatusDraft      Status = "draft"      // proposed (e.g. by AI), not yet approved; excluded from prompts
)

// Statuses is the closed status set.
var Statuses = []Status{StatusActive, StatusDeprecated, StatusArchived, StatusDraft}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	for _, v := range Statuses {
		if s == v {
			return true
		}
	}
	return false
}

// RecommendedCategories is the DOCUMENTED vocabulary for Category. It is guidance for consistency,
// NOT an allow-list: Create/Update accept any non-empty category so a project can add its own
// (invariant 1). Keeping this list lets the CLI/docs suggest the common ones.
var RecommendedCategories = []string{
	"architecture", "pattern", "standard", "decision", "rule", "glossary",
}

// Entry is ONE immutable version of a knowledge entry. Entries sharing an ID form a version history
// (ascending Version); the current entry is the highest-version record (invariant 5). An Entry value
// is never mutated in place after being appended.
//
// The nine knowledge fields (owner spec) are: ID, Category, Title, Body, Source, CreatedAt,
// UpdatedAt, Status, Version. SchemaVersion is sanctioned FORMAT metadata (like S3's spec_version),
// not a knowledge field.
type Entry struct {
	ID            string `json:"id"`             // stable forever (invariant 2)
	Version       int    `json:"version"`        // 1,2,3…; history belongs to the id
	Category      string `json:"category"`       // documented-vocabulary string (invariant 1)
	Title         string `json:"title"`          // short human label
	Body          string `json:"body"`           // the knowledge text
	Source        Source `json:"source"`         // provenance (invariant 3)
	Status        Status `json:"status"`         // lifecycle/eligibility (invariant 4)
	CreatedAt     string `json:"created_at"`     // RFC3339; when the id first appeared (v1)
	UpdatedAt     string `json:"updated_at"`     // RFC3339; when THIS version was written
	SchemaVersion int    `json:"schema_version"` // record format metadata
}

// clone returns a deep copy so callers can never mutate stored state by reference.
func (e *Entry) clone() *Entry {
	cp := *e
	return &cp
}

// validate checks the invariant-bearing fields before an entry is appended.
func (e *Entry) validate() error {
	switch {
	case e.ID == "":
		return fmt.Errorf("knowledge: id is required")
	case e.Category == "":
		return fmt.Errorf("knowledge %q: category is required", e.ID)
	case e.Title == "":
		return fmt.Errorf("knowledge %q: title is required", e.ID)
	case !e.Source.Valid():
		return fmt.Errorf("knowledge %q: invalid source %q (want one of %v)", e.ID, e.Source, Sources)
	case !e.Status.Valid():
		return fmt.Errorf("knowledge %q: invalid status %q (want one of %v)", e.ID, e.Status, Statuses)
	case e.Version < 1:
		return fmt.Errorf("knowledge %q: version must be >= 1", e.ID)
	}
	return nil
}

// migrate deterministically upgrades a just-loaded record to the current SchemaVersion, or returns
// an error for an unknown/newer format (a version mismatch is NEVER silently ignored — same policy
// as the Assignment Store, S3).
func migrate(e *Entry) error {
	switch {
	case e.SchemaVersion == SchemaVersion:
		return nil
	case e.SchemaVersion == 0:
		e.SchemaVersion = 1 // pre-versioning record → v1 (identical fields)
		return nil
	case e.SchemaVersion > SchemaVersion:
		return fmt.Errorf("knowledge %q: record format v%d is newer than this engine supports (v%d) — upgrade the engine; refusing to guess",
			e.ID, e.SchemaVersion, SchemaVersion)
	default:
		return fmt.Errorf("knowledge %q: unknown record format v%d (no deterministic migration path)",
			e.ID, e.SchemaVersion)
	}
}

// Store is the ONLY view the Brain has of persistence. It is append-only by contract: Append adds a
// version record and never rewrites or deletes one. The Brain never knows whether the backing is
// JSONL files, memory, or a future database (storage-agnostic, matching S3's Store inversion).
type Store interface {
	// Append persists one immutable version record. Implementations MUST NOT rewrite or delete any
	// previously appended record.
	Append(e *Entry) error
	// History returns every version for id in ascending Version order, or ok=false if the id is
	// unknown. Each returned record has been validated + migrated.
	History(id string) (versions []*Entry, ok bool, err error)
	// IDs returns every known id (order unspecified).
	IDs() ([]string, error)
}
