// Package lease is the Lease Manager (docs/15, docs/21 S7B — renamed from Lock Manager).
//
// It uses LEASE semantics, not permanent-lock semantics: ownership is bounded by TIME. A lease models
// "owner X owns resource R until T", which naturally gives ClaudWorker crash recovery, automatic
// cleanup, resource reclamation, restart safety, and ownership transfer — none of which a permanent
// lock provides. An abandoned lease simply expires; reclaiming it never requires human intervention.
//
// Responsibilities: Issue Lease, Resource Lease, Merge Lease, renewal, expiration, persistence,
// recovery, transfer, validation. Nothing else.
//
// Separation of concerns (owner-mandated):
//   - Resource Manager (S7A) answers "what resources exist?"
//   - Lease Manager   (S7B) answers "who currently owns them, until when?"
//
// This package is a leaf (stdlib only): it does NOT import the resource or assignment packages. A
// lease's Resource/Owner are opaque identifiers (a resource id, an issue key, a merge target / the
// owning Assignment's issue key), so the two managers stay completely independent.
package lease

import (
	"fmt"
	"time"
)

// SpecVersion is the persisted-record FORMAT version (metadata, mirroring the S3/S4 stores). Bump it
// only when the on-disk layout changes, adding a migrate() branch.
const SpecVersion = 1

// Kind namespaces a lease so one resource can be leased independently per purpose.
type Kind string

const (
	KindIssue    Kind = "issue"    // an Assignment owns a Jira issue
	KindResource Kind = "resource" // an Assignment owns a fleet resource (S7A) for the duration of use
	KindMerge    Kind = "merge"    // an Assignment owns the right to merge into a target
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindIssue || k == KindResource || k == KindMerge }

// Lease is time-bounded ownership. Every field mandated by the design rules is present: Resource,
// Owner (the Assignment), CreatedAt, ExpiresAt, Renewable, Reason. SpecVersion is format metadata.
type Lease struct {
	ID          string    `json:"id"`           // deterministic: "<kind>/<resource>" (one active lease per kind+resource)
	Kind        Kind      `json:"kind"`         //
	Resource    string    `json:"resource"`     // what is leased
	Owner       string    `json:"owner"`        // the owning Assignment (issue key)
	CreatedAt   time.Time `json:"created_at"`   // when first granted
	ExpiresAt   time.Time `json:"expires_at"`   // ownership ends here unless renewed
	Renewable   bool      `json:"renewable"`    // may Renew extend it?
	Reason      string    `json:"reason"`       // why the lease was taken (observability)
	SpecVersion int       `json:"spec_version"` // record format metadata
}

// Active reports whether the lease still holds at time now (ownership is valid strictly before
// expiry). Expiry is a pure function of timestamps → deterministic recovery with no human step.
func (l *Lease) Active(now time.Time) bool { return now.Before(l.ExpiresAt) }

func (l *Lease) clone() *Lease { cp := *l; return &cp }

// migrate deterministically upgrades a loaded record or refuses an unknown/newer format (never
// silently ignored — same policy as the Assignment/Knowledge stores).
func migrate(l *Lease) error {
	switch {
	case l.SpecVersion == SpecVersion:
		return nil
	case l.SpecVersion == 0:
		l.SpecVersion = 1
		return nil
	case l.SpecVersion > SpecVersion:
		return fmt.Errorf("lease %q: record format v%d is newer than this engine supports (v%d) — upgrade the engine; refusing to guess",
			l.ID, l.SpecVersion, SpecVersion)
	default:
		return fmt.Errorf("lease %q: unknown record format v%d (no deterministic migration path)", l.ID, l.SpecVersion)
	}
}

// Store is the durable backing for leases (persistence + recovery). Leases MUST survive a restart so
// ownership is known after a crash; this is the durable ownership the Resource Manager deliberately
// omitted. The Manager never knows the implementation (JSON files, memory, or a future DB).
type Store interface {
	Save(l *Lease) error
	Load(id string) (*Lease, bool, error)
	List() ([]*Lease, error)
	Delete(id string) error
}
