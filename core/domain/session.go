package domain

import "time"

// SafetyState is what the developer sees. It is derived from risk and decision
// history — never an independent input to risk.
type SafetyState uint8

const (
	StateUnspecified SafetyState = iota
	StateSafe
	StateWatching
	StateWarning
	StateCritical
	StateIntervention
)

func (s SafetyState) String() string {
	switch s {
	case StateSafe:
		return "SAFE"
	case StateWatching:
		return "WATCHING"
	case StateWarning:
		return "WARNING"
	case StateCritical:
		return "CRITICAL"
	case StateIntervention:
		return "INTERVENTION"
	default:
		return "UNSPECIFIED"
	}
}

// CapabilityName identifies a latched fact about a session.
type CapabilityName string

const (
	CapSecretAccess       CapabilityName = "secret_access"
	CapFilesystemRead     CapabilityName = "filesystem_read"
	CapFilesystemWrite    CapabilityName = "filesystem_write"
	CapShellExec          CapabilityName = "shell_exec"
	CapOutboundNetwork    CapabilityName = "outbound_network"
	CapDataEgress         CapabilityName = "data_egress"
	CapGitWrite           CapabilityName = "git_write"
	CapUntrustedContext   CapabilityName = "untrusted_context"
	CapCredentialMaterial CapabilityName = "credential_material"
)

// AllCapabilities is the declared set, in a stable order so that explanations
// and audit records read the same way every time.
var AllCapabilities = []CapabilityName{
	CapSecretAccess,
	CapFilesystemRead,
	CapFilesystemWrite,
	CapShellExec,
	CapOutboundNetwork,
	CapDataEgress,
	CapGitWrite,
	CapUntrustedContext,
	CapCredentialMaterial,
}

// maxEvidence caps how many signal ids a capability retains. Evidence exists to
// explain a decision, not to be a second copy of the timeline; a handful of
// signals is enough for a human to follow, and the cap is what keeps a live
// session's memory flat regardless of how long it runs.
const maxEvidence = 8

// Capability is a latched fact. Once active it stays active for the life of the
// session: a session that read .env an hour ago is still a session that has
// seen secrets.
type Capability struct {
	Active    bool
	FirstSeen time.Time
	Count     int
	Evidence  []string // signal ids, capped at maxEvidence
}

// Capabilities is the compressed "what has this session already done" — the
// thing that makes a later signal dangerous.
type Capabilities map[CapabilityName]*Capability

// Active reports whether a capability has been latched.
func (c Capabilities) Active(name CapabilityName) bool {
	entry, ok := c[name]
	return ok && entry.Active
}

// ActiveNames returns the latched capabilities in AllCapabilities order.
func (c Capabilities) ActiveNames() []CapabilityName {
	var out []CapabilityName
	for _, name := range AllCapabilities {
		if c.Active(name) {
			out = append(out, name)
		}
	}
	return out
}

// Latch records an observation of a capability. It is monotonic: a capability
// never un-latches, it only accumulates evidence.
func (c Capabilities) Latch(name CapabilityName, sig Signal) {
	entry, ok := c[name]
	if !ok {
		entry = &Capability{FirstSeen: sig.ObservedAt}
		c[name] = entry
	}
	if !entry.Active {
		entry.Active = true
		entry.FirstSeen = sig.ObservedAt
	}
	entry.Count++
	if len(entry.Evidence) < maxEvidence {
		entry.Evidence = append(entry.Evidence, sig.ID)
	}
}

// Session is the accumulated understanding of one agent run. It is the primary
// unit of reasoning (Article VI): a single event is rarely dangerous, the
// session tells the real story.
type Session struct {
	ID           string
	Agent        string
	StartedAt    time.Time
	LastSignalAt time.Time
	State        SafetyState
	Capabilities Capabilities
	Risk         Risk
	SignalCount  uint64

	// Ended marks a session that has received SESSION_END. Capabilities reset
	// only here.
	Ended bool

	// lastSeq tracks ordering so lost signals can be reported rather than
	// silently absorbed.
	lastSeq uint64
	// gaps counts detected sequence discontinuities. Surfaced in reports: a
	// session with lost signals is a session whose risk is understated, and the
	// developer deserves to know that.
	gaps uint64
}

// NewSession creates a session in its initial, unremarkable state.
func NewSession(id, agent string, at time.Time) *Session {
	return &Session{
		ID:           id,
		Agent:        agent,
		StartedAt:    at,
		LastSignalAt: at,
		State:        StateSafe,
		Capabilities: Capabilities{},
	}
}

// Gaps reports how many sequence discontinuities were detected.
func (s *Session) Gaps() uint64 { return s.gaps }

// Observe folds a signal's bookkeeping into the session: counts, clocks, and
// ordering. Capability derivation is the session engine's job, not the model's.
func (s *Session) Observe(sig Signal) {
	s.SignalCount++

	// Seq is monotonic per session. A jump means signals were lost in
	// transport, which means the session's risk is understated — record it
	// rather than absorbing it silently. Seq 0 means the adapter does not
	// number its signals, which is allowed.
	if sig.Seq > 0 {
		if s.lastSeq > 0 && sig.Seq > s.lastSeq+1 {
			s.gaps += sig.Seq - s.lastSeq - 1
		}
		if sig.Seq > s.lastSeq {
			s.lastSeq = sig.Seq
		}
	}

	// Clocks come from adapters on possibly different machines-worth of skew,
	// so never let the session's notion of "now" run backwards.
	if sig.ObservedAt.After(s.LastSignalAt) {
		s.LastSignalAt = sig.ObservedAt
	}
}

// End closes a session. Capabilities reset only here.
func (s *Session) End(at time.Time) {
	s.Ended = true
	if at.After(s.LastSignalAt) {
		s.LastSignalAt = at
	}
}

// Escalate raises the safety state. State moves up immediately and never falls
// on a single observation, so a session cannot flicker its way out of scrutiny.
// Lowering it is a deliberate, separate act (see Relax).
func (s *Session) Escalate(to SafetyState) {
	if to > s.State {
		s.State = to
	}
}

// Relax lowers the safety state. Callers must have established that risk has
// been sustainably low — this is not the inverse of Escalate and must not be
// driven by a single quiet signal.
func (s *Session) Relax(to SafetyState) {
	if to < s.State {
		s.State = to
	}
}
