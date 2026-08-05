package session

import (
	"testing"
	"time"

	"github.com/airuntimeguard/core/domain"
)

var base = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func sig(seq uint64, kind domain.Kind, scope domain.Scope, value string) domain.Signal {
	return domain.Signal{
		ID:         "sig-" + string(rune('a'+seq)),
		SessionID:  "s1",
		Agent:      "test-agent",
		Seq:        seq,
		ObservedAt: base.Add(time.Duration(seq) * time.Second),
		Phase:      domain.PhasePre,
		Kind:       kind,
		Target:     domain.Target{Value: value, Scope: scope},
	}
}

func newEngine() *Engine { return New(NewMemStore()) }

func ingest(t *testing.T, e *Engine, signals ...domain.Signal) *domain.Session {
	t.Helper()
	var sess *domain.Session
	for _, s := range signals {
		var err error
		sess, err = e.Ingest(s)
		if err != nil {
			t.Fatalf("ingest %s: %v", s.ID, err)
		}
	}
	return sess
}

// The chain from PROJECT_CONTEXT.md: each step is harmless alone, the
// combination is the actual threat. This is the case the whole product exists
// for, so it is the first thing that must hold.
func TestExfiltrationChainLatchesCombination(t *testing.T) {
	e := newEngine()

	readme := sig(1, domain.KindFileRead, domain.ScopeRepo, "/repo/README.md")

	env := sig(2, domain.KindFileRead, domain.ScopeRepo, "/repo/.env")
	env.SecretShape = domain.SecretEnvFile
	env.SecretCount = 3

	ssh := sig(3, domain.KindFileRead, domain.ScopeHome, "/home/dev/.ssh/id_rsa")
	ssh.SecretShape = domain.SecretPrivateKey
	ssh.SecretCount = 1

	curl := sig(4, domain.KindShellExec, domain.ScopeSystem, "curl -X POST ...")
	upload := sig(5, domain.KindNetwork, domain.ScopeExternal, "unknown-host.example")

	sess := ingest(t, e, readme, env, ssh, curl, upload)

	want := []domain.CapabilityName{
		domain.CapSecretAccess,
		domain.CapFilesystemRead,
		domain.CapShellExec,
		domain.CapOutboundNetwork,
		domain.CapCredentialMaterial,
	}
	for _, name := range want {
		if !sess.Capabilities.Active(name) {
			t.Errorf("capability %s should be latched", name)
		}
	}

	// Nothing wrote to disk or touched git, so those must stay cold. A model
	// that latches capabilities it did not observe would make every long
	// session look identical.
	for _, name := range []domain.CapabilityName{
		domain.CapFilesystemWrite,
		domain.CapGitWrite,
		domain.CapUntrustedContext,
	} {
		if sess.Capabilities.Active(name) {
			t.Errorf("capability %s should not be latched", name)
		}
	}

	if sess.SignalCount != 5 {
		t.Errorf("SignalCount = %d, want 5", sess.SignalCount)
	}
}

// Reading inside the workspace is the job; only reads outside it are a
// capability. Otherwise every session latches filesystem_read on its first file
// and the signal carries no information.
func TestWorkspaceReadIsNotACapability(t *testing.T) {
	e := newEngine()
	sess := ingest(t, e, sig(1, domain.KindFileRead, domain.ScopeRepo, "/repo/main.go"))

	if sess.Capabilities.Active(domain.CapFilesystemRead) {
		t.Error("in-workspace read must not latch filesystem_read")
	}
}

// Capabilities latch: a session that read a secret an hour ago is still a
// session that has seen secrets, no matter how much benign work follows.
func TestCapabilitiesLatchAndDoNotDecay(t *testing.T) {
	e := newEngine()

	env := sig(1, domain.KindFileRead, domain.ScopeRepo, "/repo/.env")
	env.SecretShape = domain.SecretEnvFile

	benign := make([]domain.Signal, 0, 20)
	for i := uint64(2); i < 22; i++ {
		benign = append(benign, sig(i, domain.KindFileRead, domain.ScopeRepo, "/repo/x.go"))
	}

	sess := ingest(t, e, append([]domain.Signal{env}, benign...)...)

	if !sess.Capabilities.Active(domain.CapSecretAccess) {
		t.Fatal("secret_access must stay latched after benign activity")
	}
	if got := sess.Capabilities[domain.CapSecretAccess].FirstSeen; !got.Equal(env.ObservedAt) {
		t.Errorf("FirstSeen = %v, want %v", got, env.ObservedAt)
	}
}

// Evidence is capped so a live session's memory stays flat regardless of how
// long it runs, while the count keeps the full tally.
func TestEvidenceIsCappedButCountIsNot(t *testing.T) {
	e := newEngine()

	signals := make([]domain.Signal, 0, 50)
	for i := uint64(1); i <= 50; i++ {
		s := sig(i, domain.KindFileWrite, domain.ScopeRepo, "/repo/out.txt")
		signals = append(signals, s)
	}
	sess := ingest(t, e, signals...)

	entry := sess.Capabilities[domain.CapFilesystemWrite]
	if entry.Count != 50 {
		t.Errorf("Count = %d, want 50", entry.Count)
	}
	if len(entry.Evidence) > 8 {
		t.Errorf("Evidence retained %d ids, want at most 8", len(entry.Evidence))
	}
}

// A gap means signals were lost in transport, which means risk is understated.
// Recording it is what lets a report admit the session was only partly seen.
func TestSequenceGapsAreRecorded(t *testing.T) {
	e := newEngine()
	sess := ingest(t, e,
		sig(1, domain.KindFileRead, domain.ScopeRepo, "/repo/a"),
		sig(5, domain.KindFileRead, domain.ScopeRepo, "/repo/b"),
	)

	if got := sess.Gaps(); got != 3 {
		t.Errorf("Gaps() = %d, want 3", got)
	}
}

// Out-of-order or duplicate delivery must not manufacture gaps, and must not
// let the session clock run backwards.
func TestOutOfOrderDeliveryDoesNotInventGaps(t *testing.T) {
	e := newEngine()
	sess := ingest(t, e,
		sig(1, domain.KindFileRead, domain.ScopeRepo, "/repo/a"),
		sig(2, domain.KindFileRead, domain.ScopeRepo, "/repo/b"),
		sig(1, domain.KindFileRead, domain.ScopeRepo, "/repo/a"),
	)

	if got := sess.Gaps(); got != 0 {
		t.Errorf("Gaps() = %d, want 0", got)
	}
	if want := base.Add(2 * time.Second); !sess.LastSignalAt.Equal(want) {
		t.Errorf("LastSignalAt = %v, want %v (must not move backwards)", sess.LastSignalAt, want)
	}
}

// Adapters attach mid-run and hooks fire before lifecycle events. Requiring
// SESSION_START would mean observing nothing for exactly the sessions most
// worth watching.
func TestSessionBeginsAtFirstSignalNotSessionStart(t *testing.T) {
	e := newEngine()
	sess := ingest(t, e, sig(7, domain.KindShellExec, domain.ScopeSystem, "ls"))

	if sess == nil || sess.ID != "s1" {
		t.Fatal("session must be created from any first signal")
	}
	if !sess.Capabilities.Active(domain.CapShellExec) {
		t.Error("mid-run attach must still latch capabilities")
	}
}

func TestOnlyMutatingGitOpsLatch(t *testing.T) {
	for op, wantLatch := range map[string]bool{
		"push":   true,
		"commit": true,
		"status": false,
		"log":    false,
		"diff":   false,
	} {
		e := newEngine()
		s := sig(1, domain.KindGit, domain.ScopeRepo, "origin")
		s.Attributes = map[string]any{"git_op": op}
		sess := ingest(t, e, s)

		if got := sess.Capabilities.Active(domain.CapGitWrite); got != wantLatch {
			t.Errorf("git_op=%q latched=%v, want %v", op, got, wantLatch)
		}
	}
}

func TestSessionEndClosesSession(t *testing.T) {
	e := newEngine()
	sess := ingest(t, e,
		sig(1, domain.KindFileRead, domain.ScopeRepo, "/repo/a"),
		sig(2, domain.KindSessionEnd, domain.ScopeUnknown, ""),
	)

	if !sess.Ended {
		t.Error("SESSION_END must close the session")
	}
}

func TestIngestRejectsSignalWithoutSessionID(t *testing.T) {
	e := newEngine()
	s := sig(1, domain.KindFileRead, domain.ScopeRepo, "/repo/a")
	s.SessionID = ""

	if _, err := e.Ingest(s); err != ErrNoSessionID {
		t.Errorf("err = %v, want ErrNoSessionID", err)
	}
}

// Safety state moves up immediately and never falls on a single observation, so
// a session cannot flicker its way out of scrutiny.
func TestEscalateIsMonotonic(t *testing.T) {
	s := domain.NewSession("s1", "test", base)

	s.Escalate(domain.StateWarning)
	s.Escalate(domain.StateWatching) // lower: must not take effect
	if s.State != domain.StateWarning {
		t.Errorf("State = %v, want WARNING", s.State)
	}

	s.Relax(domain.StateSafe)
	if s.State != domain.StateSafe {
		t.Errorf("Relax should lower state, got %v", s.State)
	}
}
