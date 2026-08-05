// Package risk implements the Predict step.
//
// Risk is continuous, recomputed, and itemized — never a rule match. Every
// point on the board traces to a named factor with evidence, which is how
// Article V is satisfied by construction rather than by writing prose after the
// fact.
//
// The engine knows nothing about policy or actions: it says how bad and how
// sure, never what to do. See docs/ARCHITECTURE.md §4.
package risk

import (
	"time"

	"github.com/airuntimeguard/core/config"
	"github.com/airuntimeguard/core/domain"
)

type Engine struct {
	cfg *config.Config
}

func New(cfg *config.Config) *Engine { return &Engine{cfg: cfg} }

// Score computes the risk of a session at the moment of a signal.
//
// The shape is deliberate:
//
//	score = Σ capability weights + Σ combination bonuses − Σ mitigations
//
// Individual capabilities contribute little. Their co-occurrence is what
// scores: reading a secret is unremarkable, and reaching the network is
// unremarkable, but a session that has done both is the actual threat.
//
// `now` is passed in rather than read from the clock so that replaying a
// recorded session produces byte-identical results.
func (e *Engine) Score(sess *domain.Session, ctx domain.Context, sig domain.Signal, now time.Time) domain.Risk {
	var factors []domain.Factor
	total := 0

	// Latched capabilities. Ordered by AllCapabilities so explanations and
	// audit records read the same way every time.
	caps := sess.Capabilities
	for _, name := range domain.AllCapabilities {
		if !caps.Active(name) {
			continue
		}
		w, ok := e.cfg.Capabilities[name]
		if !ok {
			continue
		}
		total += w.Points
		factors = append(factors, domain.Factor{
			Name:         string(name),
			Contribution: w.Points,
			Evidence:     caps[name].Evidence,
			Description:  w.Description,
		})
	}

	// Combinations — where the intelligence lives.
	for _, comb := range e.cfg.Combinations {
		if !allActive(caps, comb.Requires) {
			continue
		}
		total += comb.Points
		factors = append(factors, domain.Factor{
			Name:         comb.Name,
			Contribution: comb.Points,
			Evidence:     combinedEvidence(caps, comb.Requires),
			Description:  comb.Description,
		})
	}

	// Mitigations. A model that only accumulates risk eventually flags every
	// long session; these are what keep the tool usable past hour two.
	for _, m := range e.mitigations(ctx, sig) {
		w, ok := e.cfg.Mitigations[m]
		if !ok {
			continue
		}
		total += w.Points // already negative
		factors = append(factors, domain.Factor{
			Name:         m,
			Contribution: w.Points,
			Evidence:     []string{sig.ID},
			Description:  w.Description,
		})
	}

	return domain.Risk{
		Score:         domain.Clamp(total),
		Confidence:    e.confidence(ctx, sig),
		Factors:       factors,
		ComputedAt:    now,
		ConfigVersion: e.cfg.Version,
	}
}

// mitigations names the reasons this situation is less alarming than its raw
// capability set suggests.
func (e *Engine) mitigations(ctx domain.Context, sig domain.Signal) []string {
	var out []string

	if ctx.DestinationTrust == domain.TrustTrusted {
		out = append(out, "allowlisted_destination")
	}
	if !e.cfg.IsIrreversible(sig) {
		out = append(out, "reversible_action")
	}
	if ctx.WorkspaceTrust == domain.TrustTrusted {
		out = append(out, "trusted_workspace")
	}

	return out
}

// confidence is how sure we are, deliberately separate from how bad it would
// be. Both gate intervention: a high score with low confidence notifies, it
// does not block.
//
// Confidence falls when our picture of the session is incomplete — not when the
// situation looks benign. Those are different questions.
func (e *Engine) confidence(ctx domain.Context, sig domain.Signal) float64 {
	c := e.cfg.Confidence
	score := c.Base

	if ctx.SignalsLost {
		// We know we missed signals, so we know the picture is partial.
		score -= c.PenaltySignalsLost
	}
	if sig.Target.Type != domain.TargetNone && sig.Target.Scope == domain.ScopeUnknown {
		// The adapter could not say where this target lives, so half the
		// capability rules could not fire correctly.
		score -= c.PenaltyUnknownScope
	}
	if sig.Kind == domain.KindNetwork && ctx.Destination == "" {
		score -= c.PenaltyUnknownDestination
	}

	if score < c.Floor {
		score = c.Floor
	}
	if score > 1 {
		score = 1
	}
	return score
}

func allActive(caps domain.Capabilities, names []domain.CapabilityName) bool {
	for _, n := range names {
		if !caps.Active(n) {
			return false
		}
	}
	return true
}

// combinedEvidence gathers the signals behind every leg of a combination, so a
// developer can see the actual chain rather than a verdict.
func combinedEvidence(caps domain.Capabilities, names []domain.CapabilityName) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		entry, ok := caps[n]
		if !ok {
			continue
		}
		for _, id := range entry.Evidence {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}
