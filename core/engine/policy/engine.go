// Package policy expresses developer and organization intent.
//
// Policies set floors and ceilings on the intervention ladder — never verdicts.
// This is the mechanism that keeps "IF tool == curl THEN deny" structurally
// impossible: policy shapes the range, risk and confidence choose within it.
//
// The engine computes no risk. See docs/RUNTIME_MODEL.md §6.
package policy

import (
	"sort"

	"github.com/airuntimeguard/core/domain"
)

type Scope uint8

const (
	ScopeGlobal Scope = iota
	ScopeWorkspace
	ScopeSession
)

// Matcher selects the situations a policy applies to. Every field is optional;
// an empty matcher matches everything, which is how a global baseline is
// written.
type Matcher struct {
	Kinds        []domain.Kind
	TargetScopes []domain.Scope
	// RequiresAll matches only when every listed capability is latched.
	RequiresAll []domain.CapabilityName
	MinScore    int
}

type Policy struct {
	ID    string
	Scope Scope
	Match Matcher
	// Floor and Ceiling bound the action. Unspecified means unbounded.
	Floor   domain.Action
	Ceiling domain.Action
	// Reason is surfaced verbatim in the explanation, so a developer sees the
	// intent in their own words rather than a policy id.
	Reason string
	Source string
}

func (m Matcher) matches(sess *domain.Session, sig domain.Signal, risk domain.Risk) bool {
	if len(m.Kinds) > 0 && !containsKind(m.Kinds, sig.Kind) {
		return false
	}
	if len(m.TargetScopes) > 0 && !containsScope(m.TargetScopes, sig.Target.Scope) {
		return false
	}
	for _, c := range m.RequiresAll {
		if !sess.Capabilities.Active(c) {
			return false
		}
	}
	if risk.Score < m.MinScore {
		return false
	}
	return true
}

type Engine struct {
	policies []Policy
}

func New(policies []Policy) *Engine {
	// Sort by scope so resolution order is stable and most-specific-last. The
	// decision record lists policies in this order, and replay compares those
	// records, so the ordering is part of the contract.
	sorted := append([]Policy(nil), policies...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Scope < sorted[j].Scope })
	return &Engine{policies: sorted}
}

// Bounds is the range a decision must land in.
type Bounds struct {
	Floor   domain.Action
	Ceiling domain.Action
	// Applied lists the matching policy ids in resolution order.
	Applied []string
	// Reasons are the human-readable intents behind the applied policies.
	Reasons []string
}

// Resolve computes the bounds for a situation.
//
// Conflicts resolve most-specific-scope-wins, then most-restrictive-wins: when
// one policy says "never below ASK" and another says "never above NOTIFY", the
// floor takes it. The cost of an unnecessary prompt is smaller than the cost of
// a missed one.
func (e *Engine) Resolve(sess *domain.Session, sig domain.Signal, risk domain.Risk) Bounds {
	var b Bounds

	for _, p := range e.policies {
		if !p.Match.matches(sess, sig, risk) {
			continue
		}
		b.Applied = append(b.Applied, p.ID)
		if p.Reason != "" {
			b.Reasons = append(b.Reasons, p.Reason)
		}

		// A later, more specific policy may only tighten what an earlier one
		// established. Otherwise a narrow permissive rule could quietly undo a
		// global protection, which is exactly the failure Article XI forbids.
		if p.Floor > b.Floor {
			b.Floor = p.Floor
		}
		if p.Ceiling != domain.ActionUnspecified {
			if b.Ceiling == domain.ActionUnspecified || p.Ceiling < b.Ceiling {
				b.Ceiling = p.Ceiling
			}
		}
	}

	return b
}

func containsKind(list []domain.Kind, k domain.Kind) bool {
	for _, v := range list {
		if v == k {
			return true
		}
	}
	return false
}

func containsScope(list []domain.Scope, s domain.Scope) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
