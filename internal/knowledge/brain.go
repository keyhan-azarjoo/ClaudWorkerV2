package knowledge

import (
	"fmt"
	"strings"
	"time"
)

// Brain is the deterministic lifecycle over the append-only Store. It owns the invariants: stable
// ids, versioned history, and "current = highest version". It contains no AI and spends no tokens.
type Brain struct {
	store Store
	now   func() time.Time // injectable clock → deterministic timestamps in tests
}

// Option configures a Brain.
type Option func(*Brain)

// WithClock overrides the timestamp source (tests inject a fixed clock for deterministic records).
func WithClock(now func() time.Time) Option { return func(b *Brain) { b.now = now } }

// New returns a Brain backed by store.
func New(store Store, opts ...Option) *Brain {
	b := &Brain{store: store, now: time.Now}
	for _, o := range opts {
		o(b)
	}
	return b
}

func (b *Brain) stamp() string { return b.now().UTC().Format(time.RFC3339) }

// validateID enforces a stable, filesystem-safe id charset (letters, digits, '-', '_', '.'). This
// keeps FileStore's id↔filename mapping collision-free and prevents path traversal.
func validateID(id string) error {
	if id == "" {
		return fmt.Errorf("knowledge: id is required")
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.'
		if !ok {
			return fmt.Errorf("knowledge: invalid id %q (allowed: letters, digits, - _ .)", id)
		}
	}
	return nil
}

// Get returns the CURRENT entry for id (highest version), or ok=false if unknown.
func (b *Brain) Get(id string) (*Entry, bool, error) {
	hist, ok, err := b.store.History(id)
	if err != nil || !ok {
		return nil, ok, err
	}
	return hist[len(hist)-1].clone(), true, nil
}

// History returns every version for id in ascending order (nothing is ever deleted, invariant 5).
func (b *Brain) History(id string) ([]*Entry, bool, error) { return b.store.History(id) }

// List returns the CURRENT entry for every id, sorted by id.
func (b *Brain) List() ([]*Entry, error) {
	ids, err := b.store.IDs()
	if err != nil {
		return nil, err
	}
	out := make([]*Entry, 0, len(ids))
	for _, id := range ids {
		cur, ok, err := b.Get(id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, cur)
		}
	}
	return out, nil
}

// Create writes version 1 of a new entry. It FAILS if the id already exists — an id is never
// duplicated (invariant 2); use Update to evolve an existing id.
func (b *Brain) Create(id, category, title, body string, src Source, status Status) (*Entry, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	if _, exists, err := b.store.History(id); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("knowledge %q: already exists (use Update; ids are never duplicated)", id)
	}
	ts := b.stamp()
	e := &Entry{
		ID: id, Version: 1, Category: category, Title: title, Body: body,
		Source: src, Status: status, CreatedAt: ts, UpdatedAt: ts,
	}
	if err := e.validate(); err != nil {
		return nil, err
	}
	if err := b.store.Append(e); err != nil {
		return nil, err
	}
	return e.clone(), nil
}

// Change is the set of content edits Update may apply. Nil fields are carried forward from the
// current version (partial update); CreatedAt and the id are always preserved.
type Change struct {
	Category *string
	Title    *string
	Body     *string
	Source   *Source
	Status   *Status
}

// Update appends a NEW version under the same id (invariant 2). It preserves the id and original
// CreatedAt, increments Version, and sets UpdatedAt to now. Unspecified fields carry forward.
func (b *Brain) Update(id string, ch Change) (*Entry, error) {
	cur, ok, err := b.Get(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("knowledge %q: not found", id)
	}
	next := cur.clone()
	next.Version = cur.Version + 1
	next.UpdatedAt = b.stamp()
	next.SchemaVersion = SchemaVersion
	if ch.Category != nil {
		next.Category = *ch.Category
	}
	if ch.Title != nil {
		next.Title = *ch.Title
	}
	if ch.Body != nil {
		next.Body = *ch.Body
	}
	if ch.Source != nil {
		next.Source = *ch.Source
	}
	if ch.Status != nil {
		next.Status = *ch.Status
	}
	if err := next.validate(); err != nil {
		return nil, err
	}
	if err := b.store.Append(next); err != nil {
		return nil, err
	}
	return next.clone(), nil
}

// setStatus is the shared helper for the status-transition lifecycle methods.
func (b *Brain) setStatus(id string, st Status) (*Entry, error) {
	return b.Update(id, Change{Status: &st})
}

// Deprecate marks the current entry deprecated (excluded from prompts; history kept).
func (b *Brain) Deprecate(id string) (*Entry, error) { return b.setStatus(id, StatusDeprecated) }

// Archive marks the current entry archived (retired; history kept).
func (b *Brain) Archive(id string) (*Entry, error) { return b.setStatus(id, StatusArchived) }

// Restore returns an entry to active (e.g. un-archiving or approving a draft).
func (b *Brain) Restore(id string) (*Entry, error) { return b.setStatus(id, StatusActive) }

// Propose is the ONLY write path AI is permitted: it creates a Draft entry for a human to approve
// (P4 — AI may propose knowledge, never assert it as truth). Approval is a later Restore/Update.
func (b *Brain) Propose(id, category, title, body string, src Source) (*Entry, error) {
	return b.Create(id, category, title, body, src, StatusDraft)
}

// normalise lower-cases and trims a token for case-insensitive matching.
func normalise(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
