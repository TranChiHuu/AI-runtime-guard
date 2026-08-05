// Package audit implements the Learn step's memory: the durable record of what
// was decided and why, and the learned preferences that record grows into.
//
// It influences no decision directly — the decision engine asks it for
// preferences through an interface it declares itself. See
// docs/ARCHITECTURE.md §4.
package audit

import (
	"sync"
	"time"

	"github.com/airuntimeguard/core/domain"
)

// Record is one decision plus the resolution it eventually reached. Resolution
// arrives later than the decision, so it is a separate, nullable field.
type Record struct {
	Decision   domain.Decision
	Resolution *domain.Resolution
}

// Preference is a learned exception, scoped to the specific shape the developer
// approved — this tool, this target scope, this capability set — never to the
// tool in general (Article XI).
type Preference struct {
	ID    string
	Kind  domain.Kind
	Scope domain.Scope
	// Destination narrows a network preference to one host. Empty means the
	// preference is not destination-specific.
	Destination string
	// RequiredCaps is the capability set present when this was taught. The
	// preference applies only when the session looks like that again: approving
	// an upload from a clean session must not silently approve the same upload
	// from a session that has since read credentials.
	RequiredCaps []domain.CapabilityName
	Ceiling      domain.Action
	TaughtBy     string // decision id
	CreatedAt    time.Time
}

// Engine holds decisions and learned preferences.
//
// ponytail: in-memory only. SQLite lands at build step 6 behind this same
// interface; nothing above it should need to change.
type Engine struct {
	mu      sync.RWMutex
	records map[string]*Record // by decision id
	pending map[string]string  // prompt id -> decision id
	prefs   []Preference
	newID   func() string
}

func New(newID func() string) *Engine {
	return &Engine{
		records: map[string]*Record{},
		pending: map[string]string{},
		newID:   newID,
	}
}

// Record stores a decision. Called before the verdict is enforced, so that a
// crash between deciding and acting still leaves a trace.
func (e *Engine) Record(d domain.Decision) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.records[d.ID] = &Record{Decision: d}
	if d.Interaction != nil {
		e.pending[d.Interaction.PromptID] = d.ID
	}
}

// Resolve applies a human's answer — or the reason there wasn't one — and
// learns from it if the chosen option said to.
func (e *Engine) Resolve(sess *domain.Session, res domain.Resolution) (domain.Action, *Preference) {
	e.mu.Lock()
	defer e.mu.Unlock()

	decisionID, ok := e.pending[res.PromptID]
	if !ok {
		return domain.ActionUnspecified, nil
	}
	delete(e.pending, res.PromptID)

	rec := e.records[decisionID]
	if rec == nil {
		return domain.ActionUnspecified, nil
	}
	rec.Resolution = &res

	inter := rec.Decision.Interaction
	if inter == nil {
		return domain.ActionUnspecified, nil
	}

	// Anything other than a human answer resolves to the Brain's headless
	// default. Recording it matters as much as the answer itself: a session
	// full of timeouts is a session nobody is actually supervising.
	if res.Source != domain.ResolutionHuman {
		return inter.HeadlessDefault, nil
	}

	opt, found := inter.Option(res.OptionID)
	if !found {
		return inter.HeadlessDefault, nil
	}
	if !opt.Learns {
		return opt.Effect, nil
	}

	pref := e.learn(sess, rec.Decision, opt)
	return opt.Effect, pref
}

// learn creates a preference from an approval, scoped tightly to the shape that
// was actually approved.
func (e *Engine) learn(sess *domain.Session, d domain.Decision, opt domain.Option) *Preference {
	p := Preference{
		ID:           e.newID(),
		Ceiling:      opt.Effect,
		TaughtBy:     d.ID,
		CreatedAt:    d.DecidedAt,
		RequiredCaps: sess.Capabilities.ActiveNames(),
	}
	e.prefs = append(e.prefs, p)
	return &e.prefs[len(e.prefs)-1]
}

// Bind attaches the signal shape to a preference. The decision alone does not
// carry the signal, so the caller supplies it.
func (e *Engine) Bind(prefID string, sig domain.Signal, destination string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i := range e.prefs {
		if e.prefs[i].ID == prefID {
			e.prefs[i].Kind = sig.Kind
			e.prefs[i].Scope = sig.Target.Scope
			e.prefs[i].Destination = destination
			return
		}
	}
}

// Ceiling implements the decision engine's Preferences interface.
//
// A preference applies only when the session looks the way it did when the
// developer approved it. Learning may lower friction for a specific situation;
// it must never broaden into unrelated ones (Article XI).
func (e *Engine) Ceiling(sess *domain.Session, sig domain.Signal) (domain.Action, string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, p := range e.prefs {
		if p.Kind != sig.Kind || p.Scope != sig.Target.Scope {
			continue
		}
		if p.Destination != "" && p.Destination != sig.Target.Value {
			continue
		}
		// The session must not have gained capabilities since the approval. A
		// yes given by a clean session is not a yes for one that has since read
		// credentials.
		if gainedCapabilities(sess, p.RequiredCaps) {
			continue
		}
		return p.Ceiling, "You previously chose to always allow this.", true
	}
	return domain.ActionUnspecified, "", false
}

// Preferences returns every learned rule, for `guard learned`. They must be
// listable and revocable, or "always allow" becomes a black box of its own.
func (e *Engine) Preferences() []Preference {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]Preference(nil), e.prefs...)
}

// Revoke removes a learned preference.
func (e *Engine) Revoke(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i, p := range e.prefs {
		if p.ID == id {
			e.prefs = append(e.prefs[:i], e.prefs[i+1:]...)
			return true
		}
	}
	return false
}

// Records returns the audit trail in decision order.
func (e *Engine) Records() []Record {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]Record, 0, len(e.records))
	for _, r := range e.records {
		out = append(out, *r)
	}
	return out
}

// gainedCapabilities reports whether the session now has any capability it did
// not have when a preference was taught.
func gainedCapabilities(sess *domain.Session, at []domain.CapabilityName) bool {
	known := make(map[domain.CapabilityName]bool, len(at))
	for _, c := range at {
		known[c] = true
	}
	for _, c := range sess.Capabilities.ActiveNames() {
		if !known[c] {
			return true
		}
	}
	return false
}
