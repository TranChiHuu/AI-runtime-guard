// Package context implements the Understand step: it turns raw session state
// into an interpretation — what is this session doing, and in what
// surroundings.
//
// It owns trust, sensitivity, and novelty. It assigns no scores: that is the
// risk engine's job. See docs/ARCHITECTURE.md §4.
package context

import (
	"strings"

	"github.com/airuntimeguard/core/domain"
)

// Workspace is what the developer has declared about where they are working.
// It is configuration, not inference.
type Workspace struct {
	Trusted bool
	// AllowedHosts are destinations the developer has already sanctioned.
	// Matching is exact or a leading-dot suffix (".internal.example").
	AllowedHosts []string
}

type Engine struct {
	ws Workspace
}

func New(ws Workspace) *Engine { return &Engine{ws: ws} }

// Derive recomputes the context for a session and the signal being decided.
//
// Nothing is cached. Recomputing from state on every decision is what lets a
// replay produce identical results (docs/RUNTIME_MODEL.md §11) — a cached
// interpretation would make the outcome depend on arrival order of things that
// should not matter.
func (e *Engine) Derive(sess *domain.Session, sig domain.Signal) domain.Context {
	ctx := domain.Context{
		WorkspaceTrust: domain.TrustUnknown,
		Sensitivity:    domain.SensitivityLow,
		SignalsLost:    sess.Gaps() > 0,
		// Unknown counts as supervised: assuming nobody is watching would
		// silently disable prompting for every adapter that omits the field.
		Supervised: !sig.Supervision.Unattended(),
	}

	if e.ws.Trusted {
		ctx.WorkspaceTrust = domain.TrustTrusted
	}

	// Sensitivity follows what the session has actually touched, not what it is
	// about to do — a session that has already read key material stays
	// sensitive for the rest of its life.
	caps := sess.Capabilities
	switch {
	case caps.Active(domain.CapCredentialMaterial):
		ctx.Sensitivity = domain.SensitivityHigh
	case caps.Active(domain.CapSecretAccess):
		ctx.Sensitivity = domain.SensitivityMedium
	}

	if sig.Target.Type == domain.TargetHost && sig.Target.Value != "" {
		ctx.Destination = sig.Target.Value
		ctx.DestinationTrust = e.trustHost(sig.Target.Value)
	}

	return ctx
}

// trustHost resolves a destination against the workspace allowlist. Anything
// not explicitly allowed is unknown — never untrusted. Marking every
// unrecognized host as hostile would make the signal meaningless, since most
// unrecognized hosts are simply new.
func (e *Engine) trustHost(host string) domain.Trust {
	host = strings.ToLower(host)
	for _, allowed := range e.ws.AllowedHosts {
		allowed = strings.ToLower(allowed)
		if host == allowed {
			return domain.TrustTrusted
		}
		// A leading dot means "this domain and anything under it".
		if strings.HasPrefix(allowed, ".") && strings.HasSuffix(host, allowed) {
			return domain.TrustTrusted
		}
	}
	return domain.TrustUnknown
}
