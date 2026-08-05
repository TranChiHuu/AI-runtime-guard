// Package session implements the Observe step: it turns a stream of signals
// into the accumulated understanding of a session.
//
// It owns signal ingestion, ordering, capability latching, and lifecycle. It
// does not interpret meaning, assign scores, or choose actions — those belong
// to the context, risk, and decision engines respectively. See
// docs/ARCHITECTURE.md §4.
//
// The engine imports no other engine and touches no clock, disk, or network of
// its own: everything it needs arrives as an argument. That is what makes the
// replay invariant (docs/RUNTIME_MODEL.md §11) hold.
package session

import (
	"errors"
	"sync"

	"github.com/airuntimeguard/core/domain"
)

// Store is the persistence the engine needs, declared here rather than imported
// so the engine depends on nothing but its own interface.
type Store interface {
	Load(sessionID string) (*domain.Session, bool)
	Save(*domain.Session)
	Delete(sessionID string)
	List() []*domain.Session
}

var ErrNoSessionID = errors.New("session: signal has no session id")

// Engine maintains live sessions.
type Engine struct {
	mu    sync.Mutex
	store Store
}

func New(store Store) *Engine {
	return &Engine{store: store}
}

// Ingest folds a signal into its session and returns the updated session.
//
// It is safe to call concurrently. The lock is engine-wide rather than
// per-session because ingestion is a handful of map writes and the decision
// path is dominated by everything downstream of it.
//
// ponytail: engine-wide lock. Move to per-session locks if a machine ever runs
// enough concurrent agents for this to show up in the latency budget.
func (e *Engine) Ingest(sig domain.Signal) (*domain.Session, error) {
	if sig.SessionID == "" {
		return nil, ErrNoSessionID
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	sess, ok := e.store.Load(sig.SessionID)
	if !ok {
		// A session begins at its first signal, not at SESSION_START. Adapters
		// attach mid-run, hooks fire before lifecycle events, and processes
		// die: requiring an opening event would mean silently observing
		// nothing for exactly the sessions most worth watching.
		sess = domain.NewSession(sig.SessionID, sig.Agent, sig.ObservedAt)
	}

	sess.Observe(sig)
	latch(sess, sig)

	if sig.Kind == domain.KindSessionEnd {
		sess.End(sig.ObservedAt)
	}

	e.store.Save(sess)
	return sess, nil
}

// Get returns a live session.
func (e *Engine) Get(id string) (*domain.Session, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.store.Load(id)
}

// List returns all live sessions.
func (e *Engine) List() []*domain.Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.store.List()
}

// latch derives which capabilities a signal proves the session now has.
//
// These rules answer only "what did this session become able to do", never "how
// bad is that" — scoring belongs to the risk engine. A signal may latch several
// capabilities, or none.
func latch(sess *domain.Session, sig domain.Signal) {
	caps := sess.Capabilities

	if sig.TouchesSecret() {
		caps.Latch(domain.CapSecretAccess, sig)
		switch sig.SecretShape {
		case domain.SecretPrivateKey, domain.SecretCredentialStore:
			// Key material is qualitatively different from a config secret: it
			// is reusable, long-lived, and rarely rotated after exposure.
			caps.Latch(domain.CapCredentialMaterial, sig)
		}
	}

	switch sig.Kind {
	case domain.KindFileRead:
		// Reading inside the workspace is the job. Reading outside it is the
		// capability worth remembering.
		if sig.Target.Scope != domain.ScopeRepo {
			caps.Latch(domain.CapFilesystemRead, sig)
		}

	case domain.KindFileWrite:
		caps.Latch(domain.CapFilesystemWrite, sig)

		// A write whose contents execute later outlives the session. It is the
		// difference between doing something now and arranging for something to
		// happen every time the developer commits or opens a shell.
		if sig.Attr("write_shape") == "persistence" {
			caps.Latch(domain.CapPersistence, sig)
		}

	case domain.KindShellExec:
		caps.Latch(domain.CapShellExec, sig)

	case domain.KindNetwork:
		if sig.Target.Scope == domain.ScopeExternal {
			caps.Latch(domain.CapOutboundNetwork, sig)

			// Reaching a host and sending data to it are separate facts.
			// Fetching a dependency is the most common benign outbound call
			// there is; latching egress for it would make every session that
			// runs a package manager look like an exfiltration attempt.
			if sig.SendsData() {
				caps.Latch(domain.CapDataEgress, sig)
			}

			// A shell wired to a socket is not data leaving, it is control
			// arriving. Exfiltration loses what the session had; this loses
			// everything the machine will ever have.
			if sig.Attr("command_shape") == "remote_shell" {
				caps.Latch(domain.CapRemoteControl, sig)
			}
		}

	case domain.KindGit:
		if isGitWrite(sig.Attr("git_op")) {
			caps.Latch(domain.CapGitWrite, sig)
		}

	case domain.KindContextIngest:
		// Content from outside the workspace can carry instructions. Once a
		// session has ingested any, every later action is potentially
		// attacker-influenced.
		if sig.Target.Scope == domain.ScopeExternal {
			caps.Latch(domain.CapUntrustedContext, sig)
		}
	}
}

// gitWriteOps are the operations that mutate history or push it somewhere.
// Adapters report the operation name; the engine decides what counts.
var gitWriteOps = map[string]bool{
	"push":       true,
	"commit":     true,
	"remote-add": true,
	"remote-set": true,
	"tag":        true,
	"reset-hard": true,
	"force-push": true,
}

func isGitWrite(op string) bool { return gitWriteOps[op] }
