// Package brain wires the engines into the runtime loop.
//
// It is the only place that knows the engines form a pipeline — the engines
// themselves do not know they are in one, which is what keeps each of them
// independently testable and replaceable (docs/ARCHITECTURE.md §4).
//
// Observe → Understand → Predict → Intervene → Learn
package brain

import (
	"sync"
	"time"

	"github.com/airuntimeguard/core/config"
	"github.com/airuntimeguard/core/domain"
	"github.com/airuntimeguard/core/engine/audit"
	ctxengine "github.com/airuntimeguard/core/engine/context"
	"github.com/airuntimeguard/core/engine/decision"
	"github.com/airuntimeguard/core/engine/policy"
	"github.com/airuntimeguard/core/engine/risk"
	"github.com/airuntimeguard/core/engine/session"
)

// Clock is injected so a replay reproduces timestamps exactly. Nothing below
// this line calls time.Now().
type Clock func() time.Time

type Options struct {
	Config    *config.Config
	Workspace ctxengine.Workspace
	Policies  []policy.Policy
	Clock     Clock
	// NewID must be deterministic under replay. The default is a monotonic
	// counter for exactly that reason: a UUID would make every replay differ.
	NewID func() string
}

type Brain struct {
	cfg *config.Config

	sessions *session.Engine
	context  *ctxengine.Engine
	risk     *risk.Engine
	policy   *policy.Engine
	decision *decision.Engine
	audit    *audit.Engine

	clock Clock

	// mu serializes Decide so that a session's state cannot change between
	// being scored and being decided on. Correctness before throughput: a
	// decision made against a stale session is a wrong decision.
	//
	// ponytail: global lock. Per-session locks if concurrent agents ever show
	// up in the latency budget.
	mu sync.Mutex

	// lastSignal remembers the signal behind each prompt, so a learned
	// preference can be bound to the shape that was actually approved.
	lastSignal map[string]domain.Signal
}

func New(o Options) *Brain {
	if o.Config == nil {
		o.Config = config.MustDefault()
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.NewID == nil {
		o.NewID = counter("d")
	}

	auditEngine := audit.New(o.NewID)

	return &Brain{
		cfg:        o.Config,
		sessions:   session.New(session.NewMemStore()),
		context:    ctxengine.New(o.Workspace),
		risk:       risk.New(o.Config),
		policy:     policy.New(o.Policies),
		decision:   decision.New(o.Config, auditEngine, o.NewID),
		audit:      auditEngine,
		clock:      o.Clock,
		lastSignal: map[string]domain.Signal{},
	}
}

// Decide runs the full loop for a PRE-phase signal and returns a verdict.
//
// POST-phase signals are observations only: nothing can be blocked
// retroactively, so they update state and return ALLOW without consuming the
// decision path.
func (b *Brain) Decide(sig domain.Signal) (domain.Decision, error) {
	start := b.clock()

	b.mu.Lock()
	defer b.mu.Unlock()

	// Observe
	sess, err := b.sessions.Ingest(sig)
	if err != nil {
		return domain.Decision{}, err
	}

	// Understand
	ctx := b.context.Derive(sess, sig)

	// Predict
	r := b.risk.Score(sess, ctx, sig, start)
	sess.Risk = r

	if sig.Phase == domain.PhasePost {
		sess.Escalate(r.Band())
		return domain.Decision{
			SessionID: sess.ID,
			SignalID:  sig.ID,
			Action:    domain.ActionAllow,
			Risk:      r,
		}, nil
	}

	// Intervene
	bounds := b.policy.Resolve(sess, sig, r)
	d := b.decision.Decide(sess, ctx, sig, r, bounds, start)
	d.Latency = b.clock().Sub(start)

	// The state the developer sees follows the risk band, except that an active
	// intervention outranks it: a session being asked about is, by definition,
	// under intervention.
	sess.Escalate(r.Band())
	if d.Action.NeedsHuman() {
		sess.Escalate(domain.StateIntervention)
	}

	// Learn — record before enforcing, so a crash between deciding and acting
	// still leaves a trace.
	b.audit.Record(d)
	if d.Interaction != nil {
		b.lastSignal[d.Interaction.PromptID] = sig
	}

	return d, nil
}

// Observe ingests a POST-phase signal. Fire-and-forget: it never blocks the
// agent and never returns a verdict.
func (b *Brain) Observe(sig domain.Signal) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	sess, err := b.sessions.Ingest(sig)
	if err != nil {
		return err
	}
	ctx := b.context.Derive(sess, sig)
	sess.Risk = b.risk.Score(sess, ctx, sig, b.clock())
	sess.Escalate(sess.Risk.Band())
	return nil
}

// Resolve applies a human's answer — or the reason there wasn't one.
func (b *Brain) Resolve(sessionID string, res domain.Resolution) (domain.Action, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sess, ok := b.sessions.Get(sessionID)
	if !ok {
		return domain.ActionUnspecified, session.ErrNoSessionID
	}

	action, pref := b.audit.Resolve(sess, res)

	if pref != nil {
		// Bind the preference to the shape that was actually approved, so it
		// can never widen beyond it.
		if sig, ok := b.lastSignal[res.PromptID]; ok {
			dest := ""
			if sig.Target.Type == domain.TargetHost {
				dest = sig.Target.Value
			}
			b.audit.Bind(pref.ID, sig, dest)
		}
	}
	delete(b.lastSignal, res.PromptID)

	// The intervention is over; the session falls back to what its risk says.
	if sess.State == domain.StateIntervention {
		sess.Relax(sess.Risk.Band())
	}

	return action, nil
}

func (b *Brain) Session(id string) (*domain.Session, bool) { return b.sessions.Get(id) }
func (b *Brain) Sessions() []*domain.Session               { return b.sessions.List() }
func (b *Brain) Audit() *audit.Engine                      { return b.audit }
func (b *Brain) ConfigVersion() string                     { return b.cfg.Version }

// counter produces deterministic ids. Replay compares decision records, so a
// random id would make every replay differ for reasons that have nothing to do
// with the risk model.
func counter(prefix string) func() string {
	var mu sync.Mutex
	n := 0
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return prefix + "-" + itoa(n)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
