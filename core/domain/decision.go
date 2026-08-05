package domain

import (
	"errors"
	"time"
)

// Action is the intervention ladder. It is ordered, and it is climbed rather
// than jumped: blocking is the last resort, and a pause is usually the better
// developer experience (Article VII).
type Action uint8

const (
	ActionUnspecified Action = iota
	ActionAllow
	ActionNotify
	ActionAsk
	ActionPause
	ActionBlock
)

func (a Action) String() string {
	switch a {
	case ActionAllow:
		return "ALLOW"
	case ActionNotify:
		return "NOTIFY"
	case ActionAsk:
		return "ASK"
	case ActionPause:
		return "PAUSE"
	case ActionBlock:
		return "BLOCK"
	default:
		return "UNSPECIFIED"
	}
}

// NeedsHuman reports whether this action cannot complete without a person.
func (a Action) NeedsHuman() bool { return a == ActionAsk || a == ActionPause }

// Clamp constrains an action between a policy floor and ceiling. Policies set
// ranges, not verdicts — this is the mechanism that keeps
// "IF tool == curl THEN deny" structurally impossible.
//
// The ceiling is applied first so the floor has the last word. When policies
// genuinely conflict — one says "never below ASK", another says "never above
// NOTIFY" — the more restrictive one wins, because the cost of an unnecessary
// prompt is smaller than the cost of a missed one.
func (a Action) Clamp(floor, ceiling Action) Action {
	if ceiling != ActionUnspecified && a > ceiling {
		a = ceiling
	}
	if floor != ActionUnspecified && a < floor {
		a = floor
	}
	return a
}

// Explanation is mandatory on every decision and mirrors Article V one-to-one.
// A decision the Brain cannot explain must not be enforced.
type Explanation struct {
	// Summary is one line, for prompt channels with tight text budgets.
	Summary  string
	What     string   // the triggering signal
	Why      string   // the session state and rules that produced this
	Evidence []string // prior signal ids that actually contributed
	Risk     Risk
	Guidance string // what the developer should do
}

// ErrIncompleteExplanation is returned when a decision cannot account for
// itself. Callers must degrade to fail-open rather than enforce it.
var ErrIncompleteExplanation = errors.New("domain: decision lacks a complete explanation")

// Validate enforces Article V. Evidence is not required — the very first signal
// of a session can be risky with no history behind it — but a decision that
// cannot say what happened, why, and what to do about it is a black box.
func (e Explanation) Validate() error {
	if e.Summary == "" || e.What == "" || e.Why == "" || e.Guidance == "" {
		return ErrIncompleteExplanation
	}
	return nil
}

// ChannelHint tells the adapter how to reach the human.
type ChannelHint uint8

const (
	ChannelUnspecified ChannelHint = iota
	// ChannelInline blocks the agent's turn: the answer is needed now.
	ChannelInline
	// ChannelOutOfBand resolves via the guard CLI with no deadline.
	ChannelOutOfBand
)

// Option is one choice offered to the developer.
type Option struct {
	ID     string
	Label  string
	Effect Action
	// Learns marks choices that create a Preference. Scoped to the specific
	// shape approved, never to the tool in general (Article XI).
	Learns bool
}

// Interaction carries everything an adapter needs to obtain a human answer.
//
// The Brain never waits for a person: its budget is milliseconds and a human
// takes seconds. Decide returns immediately with this attached, the adapter
// gets the answer over whatever channel its platform offers, and the answer
// comes back through Resolve.
type Interaction struct {
	PromptID    string
	ChannelHint ChannelHint
	// HeadlessDefault is what to do when no human is reachable. It is a
	// judgment about what is safe when nobody is watching, so the Brain decides
	// it and the adapter merely applies it — this is what lets an adapter own
	// the prompt while staying thin.
	HeadlessDefault Action
	Timeout         time.Duration
	Options         []Option
}

// Option finds an offered option by id.
func (i Interaction) Option(id string) (Option, bool) {
	for _, o := range i.Options {
		if o.ID == id {
			return o, true
		}
	}
	return Option{}, false
}

// Decision is the Brain's verdict on one signal.
type Decision struct {
	ID          string
	SessionID   string
	SignalID    string
	Action      Action
	Risk        Risk
	Policies    []string // applied, in resolution order
	Explanation Explanation
	// Interaction is nil unless a human answer is needed.
	Interaction *Interaction
	DecidedAt   time.Time
	Latency     time.Duration
}

// Validate enforces the invariants that must hold before a decision is
// enforced. The server calls this on every outbound decision.
func (d Decision) Validate() error {
	if d.Action == ActionUnspecified {
		return errors.New("domain: decision has no action")
	}
	if err := d.Explanation.Validate(); err != nil {
		return err
	}
	if d.Action.NeedsHuman() && d.Interaction == nil {
		return errors.New("domain: " + d.Action.String() + " requires an interaction")
	}
	if d.Interaction != nil && d.Interaction.HeadlessDefault == ActionUnspecified {
		return errors.New("domain: interaction requires a headless default")
	}
	return nil
}

// ResolutionSource records how an interaction actually ended. Every prompt ends
// here — including timeouts and headless runs — so the Brain always learns what
// really happened rather than silently dropping unanswered prompts.
type ResolutionSource uint8

const (
	ResolutionUnspecified ResolutionSource = iota
	ResolutionHuman
	ResolutionTimeout
	ResolutionHeadless
	ResolutionAdapterFailure
)

// Resolution is a human's answer, or the reason there wasn't one.
type Resolution struct {
	PromptID string
	OptionID string // empty unless Source is ResolutionHuman
	Source   ResolutionSource
	Channel  string // "native", "tty", "guard" — diagnostics only
	At       time.Time
}
