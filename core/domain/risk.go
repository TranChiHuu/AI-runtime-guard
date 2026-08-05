package domain

import "time"

// Factor is one itemized contribution to a risk score. Every point on the board
// traces to a named factor with evidence — this is how Article V is satisfied
// by construction rather than by writing prose after the fact.
type Factor struct {
	Name         string
	Contribution int // positive for risk, negative for mitigation
	Evidence     []string
	Description  string
}

// Risk is continuous, recomputed, and itemized. Never a rule match.
type Risk struct {
	Score      int     // 0..100 — how bad if real
	Confidence float64 // 0..1  — how sure we are; deliberately separate
	Factors    []Factor
	ComputedAt time.Time
	// ConfigVersion stamps which weight set produced this score, so an audit
	// record always says which model made the call.
	ConfigVersion string
}

// Band maps a score onto the safety ladder. Presentation only: the band never
// feeds back into scoring.
func (r Risk) Band() SafetyState {
	switch {
	case r.Score >= 80:
		return StateCritical
	case r.Score >= 50:
		return StateWarning
	case r.Score >= 20:
		return StateWatching
	default:
		return StateSafe
	}
}

// TopFactors returns the n largest positive contributors, for explanations that
// have to fit in a sentence. Mitigations are excluded: when telling a developer
// why something looks dangerous, the reasons it looks less dangerous are noise.
func (r Risk) TopFactors(n int) []Factor {
	positive := make([]Factor, 0, len(r.Factors))
	for _, f := range r.Factors {
		if f.Contribution > 0 {
			positive = append(positive, f)
		}
	}
	// Selection sort by contribution; n is small and the slice is short, and a
	// stable hand-rolled pass keeps ties in factor-declaration order so
	// explanations read identically across replays.
	for i := 0; i < len(positive) && i < n; i++ {
		best := i
		for j := i + 1; j < len(positive); j++ {
			if positive[j].Contribution > positive[best].Contribution {
				best = j
			}
		}
		positive[i], positive[best] = positive[best], positive[i]
	}
	if len(positive) > n {
		positive = positive[:n]
	}
	return positive
}

// Clamp bounds a raw score to the 0..100 range.
func Clamp(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}
