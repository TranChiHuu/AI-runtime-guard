// Package decision implements the Intervene step: it turns a risk score into a
// verdict a developer can act on and understand.
//
// It recomputes no risk. Its job is choosing a rung on the ladder, applying
// policy bounds and learned preferences, gating irreversibility, and — above
// all — producing an explanation. A decision the Brain cannot explain must not
// be enforced (Article V).
package decision

import (
	"fmt"
	"strings"
	"time"

	"github.com/airuntimeguard/core/config"
	"github.com/airuntimeguard/core/domain"
	"github.com/airuntimeguard/core/engine/policy"
)

// Preferences supplies what the developer has already taught the system.
// Declared here rather than imported so this engine depends on no other engine.
type Preferences interface {
	// Ceiling returns the highest action allowed for this situation by a
	// learned preference, or ActionUnspecified if none applies.
	Ceiling(sess *domain.Session, sig domain.Signal) (domain.Action, string, bool)
}

// IDGen supplies decision and prompt identifiers. Injected because a replay
// must reproduce them exactly.
type IDGen func() string

type Engine struct {
	cfg   *config.Config
	prefs Preferences
	newID IDGen
}

func New(cfg *config.Config, prefs Preferences, newID IDGen) *Engine {
	return &Engine{cfg: cfg, prefs: prefs, newID: newID}
}

// Decide produces the verdict for one signal.
//
// The order is fixed and each step is narrow:
//
//  1. risk and confidence propose a rung
//  2. policy floors and ceilings clamp it
//  3. learned preferences may lower it — never below a policy floor
//  4. the irreversibility gate caps anything reversible below PAUSE
//  5. the explanation is built; if it is incomplete, the decision fails open
func (e *Engine) Decide(
	sess *domain.Session,
	ctx domain.Context,
	sig domain.Signal,
	risk domain.Risk,
	bounds policy.Bounds,
	now time.Time,
) domain.Decision {
	proposed := e.propose(risk)

	// Policy bounds. The floor is applied last inside Clamp, so a conflict
	// resolves to the more restrictive rule.
	action := proposed.Clamp(bounds.Floor, bounds.Ceiling)

	// Learned preferences lower friction for a shape the developer already
	// approved. They may never breach a policy floor (Article XI).
	var prefReason string
	if ceiling, reason, ok := e.prefs.Ceiling(sess, sig); ok && ceiling < action {
		if ceiling >= bounds.Floor {
			action = ceiling
			prefReason = reason
		}
	}

	// Only irreversible actions may reach PAUSE or BLOCK. A file write in a
	// clean git tree can be undone, and interrupting the developer over
	// something they can simply revert spends trust for nothing.
	if action >= domain.ActionPause && !e.cfg.IsIrreversible(sig) {
		action = domain.ActionAsk
	}

	d := domain.Decision{
		ID:        e.newID(),
		SessionID: sess.ID,
		SignalID:  sig.ID,
		Action:    action,
		Risk:      risk,
		Policies:  bounds.Applied,
		DecidedAt: now,
	}
	d.Explanation = e.explain(sess, ctx, sig, risk, action, bounds, prefReason)

	if action.NeedsHuman() {
		d.Interaction = e.interaction(action, risk, sig)
	}

	// Article V, enforced rather than assumed: if we cannot account for a
	// decision, we do not get to enforce it. Degrade to the fail-open behavior
	// and say so, rather than silently shipping a black box.
	if err := d.Validate(); err != nil {
		return e.failOpen(sess, sig, risk, now, err)
	}
	return d
}

// propose picks a rung from score and confidence alone.
//
// Confidence gates escalation: a high score we are unsure about notifies, it
// never blocks. Score says how bad if real; confidence says how sure we are.
func (e *Engine) propose(risk domain.Risk) domain.Action {
	t := e.cfg.Thresholds

	switch {
	case risk.Score >= t.Escalate:
		if risk.Confidence >= t.MinConfidenceEscalate {
			return domain.ActionPause
		}
		if risk.Confidence >= t.MinConfidenceAsk {
			return domain.ActionAsk
		}
		return domain.ActionNotify

	case risk.Score >= t.Ask:
		if risk.Confidence >= t.MinConfidenceAsk {
			return domain.ActionAsk
		}
		return domain.ActionNotify

	case risk.Score >= t.Notify:
		return domain.ActionNotify

	default:
		return domain.ActionAllow
	}
}

// explain answers the five questions of Article V. It is built for every
// decision, including ALLOW — an audit trail of only the alarming decisions
// cannot show that the quiet ones were considered.
func (e *Engine) explain(
	sess *domain.Session,
	ctx domain.Context,
	sig domain.Signal,
	risk domain.Risk,
	action domain.Action,
	bounds policy.Bounds,
	prefReason string,
) domain.Explanation {
	what := describeSignal(sig)

	top := risk.TopFactors(3)
	why := describeWhy(sess, risk, top)
	if len(bounds.Reasons) > 0 {
		why += " Policy: " + strings.Join(bounds.Reasons, "; ") + "."
	}
	if prefReason != "" {
		why += " " + prefReason
	}
	if ctx.SignalsLost {
		// The developer deserves to know the picture is partial rather than
		// reading a confident-looking score built on missing data.
		why += fmt.Sprintf(" Note: %d signals were lost, so this session is only partly observed.", sess.Gaps())
	}

	return domain.Explanation{
		Summary:  summarize(action, sig, risk),
		What:     what,
		Why:      why,
		Evidence: evidenceOf(top),
		Risk:     risk,
		Guidance: guidance(action, ctx, sig),
	}
}

func describeSignal(sig domain.Signal) string {
	target := sig.Target.Value
	if target == "" {
		target = "(no target)"
	}
	switch sig.Kind {
	case domain.KindNetwork:
		return "Outbound connection to " + target
	case domain.KindShellExec:
		return "Shell command: " + target
	case domain.KindFileRead:
		return "Read " + target
	case domain.KindFileWrite:
		return "Write to " + target
	case domain.KindGit:
		return "Git " + sig.Attr("git_op") + " on " + target
	default:
		return sig.Kind.String() + " " + target
	}
}

func describeWhy(sess *domain.Session, risk domain.Risk, top []domain.Factor) string {
	if len(top) == 0 {
		return "Nothing in this session's history raises concern."
	}

	parts := make([]string, 0, len(top))
	for _, f := range top {
		parts = append(parts, f.Description)
	}

	return fmt.Sprintf(
		"This session has %s. Risk %d/100 (confidence %.0f%%) across %d signals.",
		joinNaturally(parts), risk.Score, risk.Confidence*100, sess.SignalCount,
	)
}

func summarize(action domain.Action, sig domain.Signal, risk domain.Risk) string {
	switch action {
	case domain.ActionAllow:
		return describeSignal(sig)
	case domain.ActionNotify:
		return fmt.Sprintf("Heads up: %s (risk %d)", describeSignal(sig), risk.Score)
	default:
		return fmt.Sprintf("%s — %s (risk %d)", action, describeSignal(sig), risk.Score)
	}
}

func guidance(action domain.Action, ctx domain.Context, sig domain.Signal) string {
	switch action {
	case domain.ActionAllow:
		return "No action needed."
	case domain.ActionNotify:
		return "Nothing to do. Run `guard status` if you want the full session picture."
	case domain.ActionPause, domain.ActionBlock:
		if ctx.Destination != "" {
			return fmt.Sprintf(
				"Session paused. Run `guard resume` to continue, or allowlist %s for this workspace if it is expected.",
				ctx.Destination)
		}
		return "Session paused. Run `guard status` to review, then `guard resume` or `guard deny`."
	default: // ASK
		if ctx.Destination != "" {
			return fmt.Sprintf("Allow once, or always allow %s for this workspace.", ctx.Destination)
		}
		return fmt.Sprintf("Allow once, or always allow this kind of %s.", strings.ToLower(sig.Kind.String()))
	}
}

// interaction supplies everything the adapter needs to reach a human. The Brain
// never waits for one: its budget is milliseconds and a person takes seconds.
func (e *Engine) interaction(action domain.Action, risk domain.Risk, sig domain.Signal) *domain.Interaction {
	escalated := risk.Score >= e.cfg.Thresholds.Escalate

	hint := domain.ChannelInline
	if action == domain.ActionPause {
		// PAUSE is a look at the whole session, not a yes/no on one call, so it
		// is resolved through guard with no deadline.
		hint = domain.ChannelOutOfBand
	}

	options := []domain.Option{
		{ID: "once", Label: "Allow once", Effect: domain.ActionAllow},
		{ID: "always", Label: "Always allow this", Effect: domain.ActionAllow, Learns: true},
		{ID: "deny", Label: "Deny", Effect: domain.ActionBlock},
	}
	// "Always allow" is not offered for irreversible actions at high risk.
	// Teaching a permanent exception in the middle of the most dangerous moment
	// of a session is how a safety tool talks itself into being useless.
	if escalated && e.cfg.IsIrreversible(sig) {
		options = []domain.Option{options[0], options[2]}
	}

	return &domain.Interaction{
		PromptID:        e.newID(),
		ChannelHint:     hint,
		HeadlessDefault: e.cfg.HeadlessDefault(escalated),
		Timeout:         e.cfg.Timeout(),
		Options:         options,
	}
}

// failOpen is the last resort when a decision cannot account for itself. An
// availability failure is not a security event (Article VII): the agent
// proceeds and the developer is told protection degraded.
func (e *Engine) failOpen(sess *domain.Session, sig domain.Signal, risk domain.Risk, now time.Time, cause error) domain.Decision {
	return domain.Decision{
		ID:        e.newID(),
		SessionID: sess.ID,
		SignalID:  sig.ID,
		Action:    domain.ActionNotify,
		Risk:      risk,
		DecidedAt: now,
		Explanation: domain.Explanation{
			Summary:  "Runtime Guard could not fully evaluate this action",
			What:     describeSignal(sig),
			Why:      "The decision could not be explained (" + cause.Error() + "), so it was not enforced.",
			Risk:     risk,
			Guidance: "This action was allowed. Run `guard doctor` — protection is degraded.",
		},
	}
}

func evidenceOf(factors []domain.Factor) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range factors {
		for _, id := range f.Evidence {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// joinNaturally renders a list the way a person would say it, because these
// strings are read by developers mid-task, not parsed.
func joinNaturally(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}
