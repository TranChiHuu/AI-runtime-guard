package domain

import (
	"errors"
	"testing"
	"time"
)

func goodExplanation() Explanation {
	return Explanation{
		Summary:  "Agent is about to POST to an unrecognized host",
		What:     "Outbound request to api.unknown-host.example",
		Why:      "This session read 3 secrets and this host is unrecognized",
		Evidence: []string{"sig-b", "sig-c"},
		Guidance: "Allow once, or add the host to this workspace's allowlist",
	}
}

// Article V: a decision the Brain cannot explain must not be enforced. Every
// field of the explanation is load-bearing, so drop each one in turn.
func TestDecisionWithoutCompleteExplanationIsInvalid(t *testing.T) {
	for name, mutate := range map[string]func(*Explanation){
		"no summary":  func(e *Explanation) { e.Summary = "" },
		"no what":     func(e *Explanation) { e.What = "" },
		"no why":      func(e *Explanation) { e.Why = "" },
		"no guidance": func(e *Explanation) { e.Guidance = "" },
	} {
		exp := goodExplanation()
		mutate(&exp)

		d := Decision{Action: ActionNotify, Explanation: exp}
		if err := d.Validate(); !errors.Is(err, ErrIncompleteExplanation) {
			t.Errorf("%s: err = %v, want ErrIncompleteExplanation", name, err)
		}
	}
}

// Evidence is not required: the first signal of a session can be risky with no
// history behind it.
func TestDecisionWithoutEvidenceIsStillValid(t *testing.T) {
	exp := goodExplanation()
	exp.Evidence = nil

	d := Decision{Action: ActionNotify, Explanation: exp, DecidedAt: time.Now()}
	if err := d.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

// ASK and PAUSE are meaningless without a way to reach a human, and an
// interaction without a headless default would hang forever when nobody is
// watching. Both are structural, so both are enforced here.
func TestHumanActionsRequireAResolvableInteraction(t *testing.T) {
	exp := goodExplanation()

	for _, action := range []Action{ActionAsk, ActionPause} {
		d := Decision{Action: action, Explanation: exp}
		if err := d.Validate(); err == nil {
			t.Errorf("%v without interaction should be invalid", action)
		}
	}

	d := Decision{
		Action:      ActionAsk,
		Explanation: exp,
		Interaction: &Interaction{PromptID: "p1", ChannelHint: ChannelInline},
	}
	if err := d.Validate(); err == nil {
		t.Error("interaction without a headless default should be invalid")
	}

	d.Interaction.HeadlessDefault = ActionBlock
	if err := d.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

// Policies set floors and ceilings, not verdicts. This is the mechanism that
// keeps "IF tool == curl THEN deny" structurally impossible.
func TestPolicyClampsRatherThanDecides(t *testing.T) {
	cases := []struct {
		name                     string
		proposed, floor, ceiling Action
		want                     Action
	}{
		{"floor raises", ActionNotify, ActionAsk, ActionUnspecified, ActionAsk},
		{"ceiling lowers", ActionBlock, ActionUnspecified, ActionNotify, ActionNotify},
		{"within range untouched", ActionAsk, ActionNotify, ActionBlock, ActionAsk},
		{"no policy untouched", ActionPause, ActionUnspecified, ActionUnspecified, ActionPause},
		{"floor wins over ceiling", ActionAllow, ActionAsk, ActionNotify, ActionAsk},
	}

	for _, c := range cases {
		if got := c.proposed.Clamp(c.floor, c.ceiling); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// The ladder must stay ordered — every gate in the decision engine compares
// actions numerically, so a reordering would silently invert them.
func TestLadderIsOrdered(t *testing.T) {
	ladder := []Action{ActionAllow, ActionNotify, ActionAsk, ActionPause, ActionBlock}
	for i := 1; i < len(ladder); i++ {
		if !(ladder[i-1] < ladder[i]) {
			t.Fatalf("%v must rank below %v", ladder[i-1], ladder[i])
		}
	}

	if ActionAllow.NeedsHuman() || ActionNotify.NeedsHuman() || ActionBlock.NeedsHuman() {
		t.Error("only ASK and PAUSE need a human")
	}
	if !ActionAsk.NeedsHuman() || !ActionPause.NeedsHuman() {
		t.Error("ASK and PAUSE must need a human")
	}
}

// Explanations feed prompts with tight text budgets, so top factors must be the
// largest contributors, must exclude mitigations, and must be stable across
// replays.
func TestTopFactorsExcludesMitigationsAndRanks(t *testing.T) {
	r := Risk{Factors: []Factor{
		{Name: "secret_access", Contribution: 30},
		{Name: "allowlisted_host", Contribution: -25},
		{Name: "outbound_network", Contribution: 45},
		{Name: "shell_exec", Contribution: 10},
	}}

	top := r.TopFactors(2)
	if len(top) != 2 {
		t.Fatalf("got %d factors, want 2", len(top))
	}
	if top[0].Name != "outbound_network" || top[1].Name != "secret_access" {
		t.Errorf("got %s, %s; want outbound_network, secret_access", top[0].Name, top[1].Name)
	}
}

func TestRiskBands(t *testing.T) {
	for score, want := range map[int]SafetyState{
		0: StateSafe, 19: StateSafe,
		20: StateWatching, 49: StateWatching,
		50: StateWarning, 79: StateWarning,
		80: StateCritical, 100: StateCritical,
	} {
		if got := (Risk{Score: score}).Band(); got != want {
			t.Errorf("score %d: band = %v, want %v", score, got, want)
		}
	}
}
