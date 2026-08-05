// Package brain wires the engines into the runtime loop.
//
// It is the only place that knows the engines form a pipeline — the engines
// themselves do not know they are in one, which is what keeps each of them
// independently testable and replaceable (docs/ARCHITECTURE.md §4).
//
// Observe → Understand → Predict → Intervene → Learn
package brain

import (
	"errors"
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

// Sink is durable storage. Declared here as an interface so the Brain depends
// on no storage implementation, and so a replay can run with no sink at all.
type Sink interface {
	PutSignal(domain.Signal) error
	PutDecision(domain.Decision) error
	PutSession(*domain.Session) error
	PutResolution(domain.Resolution) error
	PutPreference(audit.Preference) error
}

type Options struct {
	Config    *config.Config
	Workspace ctxengine.Workspace
	Policies  []policy.Policy
	Clock     Clock
	// NewID must be deterministic under replay. The default is a monotonic
	// counter for exactly that reason: a UUID would make every replay differ.
	NewID func() string
	// Sink is optional. When set, writes happen off the decision path.
	Sink Sink
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

	// writes carries durable work off the decision path. Disk latency must
	// never sit inside the 50 ms budget (docs/RUNTIME_MODEL.md §10).
	writes chan func(Sink)
	sink   Sink
	done   chan struct{}
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

	b := &Brain{
		cfg:        o.Config,
		sessions:   session.New(session.NewMemStore()),
		context:    ctxengine.New(o.Workspace),
		risk:       risk.New(o.Config),
		policy:     policy.New(o.Policies),
		decision:   decision.New(o.Config, auditEngine, o.NewID),
		audit:      auditEngine,
		clock:      o.Clock,
		lastSignal: map[string]domain.Signal{},
		sink:       o.Sink,
		done:       make(chan struct{}),
	}

	if o.Sink != nil {
		// Buffered so a slow disk cannot back-pressure into the decision path.
		// If the buffer fills we drop the write and keep deciding: losing an
		// audit row is bad, stalling an agent behind fsync is worse.
		b.writes = make(chan func(Sink), 1024)
		go b.drain()
	}

	return b
}

func (b *Brain) drain() {
	defer close(b.done)
	for w := range b.writes {
		w(b.sink)
	}
}

// persist queues durable work. Non-blocking by design.
func (b *Brain) persist(w func(Sink)) {
	if b.writes == nil {
		return
	}
	select {
	case b.writes <- w:
	default:
	}
}

// Close flushes pending writes. Called on daemon shutdown; the demo and tests
// use it to make assertions about what actually landed on disk.
func (b *Brain) Close() {
	if b.writes == nil {
		return
	}
	close(b.writes)
	<-b.done
	b.writes = nil
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

	b.persist(func(s Sink) {
		_ = s.PutSignal(sig)
		_ = s.PutDecision(d)
		_ = s.PutSession(sess)
	})

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

	b.persist(func(st Sink) {
		_ = st.PutSignal(sig)
		_ = st.PutSession(sess)
	})
	return nil
}

// ErrUnknownPrompt means the prompt was never issued, or was already resolved.
// Resolving twice is not an error worth crashing over, but it must not silently
// look like a fresh answer either.
var ErrUnknownPrompt = errors.New("brain: unknown or already-resolved prompt")

// ResolvePrompt applies a human's answer — or the reason there wasn't one.
//
// It takes only the prompt id because that is all an adapter reliably has: the
// hook process that renders a prompt may not be the one that received the
// decision. The Brain owns the mapping back to the session.
func (b *Brain) ResolvePrompt(res domain.Resolution) (domain.Action, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sig, ok := b.lastSignal[res.PromptID]
	if !ok {
		return domain.ActionUnspecified, ErrUnknownPrompt
	}

	sess, ok := b.sessions.Get(sig.SessionID)
	if !ok {
		return domain.ActionUnspecified, session.ErrNoSessionID
	}

	action, pref := b.audit.Resolve(sess, res)

	if pref != nil {
		// Bind the preference to the shape that was actually approved, so it
		// can never widen beyond it.
		dest := ""
		if sig.Target.Type == domain.TargetHost {
			dest = sig.Target.Value
		}
		b.audit.Bind(pref.ID, sig, dest)

		saved := *pref
		saved.Kind, saved.Scope, saved.Destination = sig.Kind, sig.Target.Scope, dest
		b.persist(func(st Sink) { _ = st.PutPreference(saved) })
	}
	delete(b.lastSignal, res.PromptID)

	b.persist(func(st Sink) { _ = st.PutResolution(res) })

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
