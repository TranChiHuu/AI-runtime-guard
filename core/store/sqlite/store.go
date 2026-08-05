// Package sqlite provides durable local storage.
//
// SQLite is durability and history, not the working set: live sessions stay in
// memory and the decision path never waits on disk (docs/ARCHITECTURE.md §5).
// Writes land asynchronously; the guarantee that matters is that no decision is
// lost, not that it is fsynced before the agent proceeds.
//
// Nothing here ever stores a secret value. That is enforced at this boundary
// rather than left to callers (Article IX).
package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, so `go build` stays trivial

	"github.com/airuntimeguard/core/domain"
	"github.com/airuntimeguard/core/engine/audit"
)

const schema = `
CREATE TABLE IF NOT EXISTS signals (
  id          TEXT NOT NULL,
  session_id  TEXT NOT NULL,
  seq         INTEGER NOT NULL,
  observed_at TEXT NOT NULL,
  agent       TEXT NOT NULL,
  phase       INTEGER NOT NULL,
  kind        INTEGER NOT NULL,
  actor_type  INTEGER NOT NULL,
  actor_name  TEXT,
  target_type INTEGER NOT NULL,
  target      TEXT,
  scope       TEXT,
  -- shape and count only. The value never reaches this table.
  secret_shape TEXT,
  secret_count INTEGER,
  attributes  TEXT,
  -- Keyed by (session, id), not id alone. Signal ids are only required to be
  -- unique within a session, and a global key would let one session's signal
  -- silently swallow another's -- understating exactly the sessions that
  -- matter most.
  PRIMARY KEY (session_id, id)
);
CREATE INDEX IF NOT EXISTS signals_by_session ON signals(session_id, seq);

CREATE TABLE IF NOT EXISTS decisions (
  id          TEXT PRIMARY KEY,
  session_id  TEXT NOT NULL,
  signal_id   TEXT NOT NULL,
  action      INTEGER NOT NULL,
  score       INTEGER NOT NULL,
  confidence  REAL NOT NULL,
  config_version TEXT NOT NULL,
  decided_at  TEXT NOT NULL,
  latency_us  INTEGER NOT NULL,
  explanation TEXT NOT NULL,
  factors     TEXT NOT NULL,
  policies    TEXT,
  prompt_id   TEXT,
  resolution  TEXT,
  -- A question that was auto-answered because nobody could be asked. Often
  -- lands on ALLOW, so without this column it is invisible in a report.
  suppressed  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS decisions_by_session ON decisions(session_id, decided_at);

CREATE TABLE IF NOT EXISTS sessions (
  id          TEXT PRIMARY KEY,
  agent       TEXT NOT NULL,
  started_at  TEXT NOT NULL,
  ended_at    TEXT,
  state       INTEGER NOT NULL,
  score       INTEGER NOT NULL,
  signal_count INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS preferences (
  id          TEXT PRIMARY KEY,
  kind        INTEGER NOT NULL,
  scope       TEXT,
  destination TEXT,
  required_caps TEXT,
  ceiling     INTEGER NOT NULL,
  taught_by   TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
`

type Store struct {
	db *sql.DB
}

// Open creates or opens the database in dir.
func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, "guard.db")

	// WAL so `guard status` and the dashboard can read while the decision path
	// writes, without either blocking the other.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// PutSignal appends a signal. This table is the replay source, so it is
// append-only and never updated in place.
func (s *Store) PutSignal(sig domain.Signal) error {
	attrs, _ := json.Marshal(scrub(sig.Attributes))

	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO signals
		(id, session_id, seq, observed_at, agent, phase, kind, actor_type, actor_name,
		 target_type, target, scope, secret_shape, secret_count, attributes)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sig.ID, sig.SessionID, sig.Seq, ts(sig.ObservedAt), sig.Agent,
		int(sig.Phase), int(sig.Kind), int(sig.Actor.Type), sig.Actor.Name,
		int(sig.Target.Type), sig.Target.Value, string(sig.Target.Scope),
		string(sig.SecretShape), sig.SecretCount, string(attrs))
	return err
}

// PutDecision appends a decision with its full explanation. The explanation is
// stored, not regenerated: an audit record must show what the developer was
// actually told, even after the risk model changes.
func (s *Store) PutDecision(d domain.Decision) error {
	explanation, _ := json.Marshal(d.Explanation)
	factors, _ := json.Marshal(d.Risk.Factors)
	policies, _ := json.Marshal(d.Policies)

	promptID := ""
	if d.Interaction != nil {
		promptID = d.Interaction.PromptID
	}

	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO decisions
		(id, session_id, signal_id, action, score, confidence, config_version,
		 decided_at, latency_us, explanation, factors, policies, prompt_id, suppressed)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.SessionID, d.SignalID, int(d.Action), d.Risk.Score, d.Risk.Confidence,
		d.Risk.ConfigVersion, ts(d.DecidedAt), d.Latency.Microseconds(),
		string(explanation), string(factors), string(policies), promptID, d.Suppressed)
	return err
}

// PutResolution attaches an answer to the decision that asked for it.
func (s *Store) PutResolution(res domain.Resolution) error {
	blob, _ := json.Marshal(res)
	_, err := s.db.Exec(`UPDATE decisions SET resolution = ? WHERE prompt_id = ?`,
		string(blob), res.PromptID)
	return err
}

func (s *Store) PutSession(sess *domain.Session) error {
	var endedAt any
	if sess.Ended {
		endedAt = ts(sess.LastSignalAt)
	}

	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO sessions
		(id, agent, started_at, ended_at, state, score, signal_count)
		VALUES (?,?,?,?,?,?,?)`,
		sess.ID, sess.Agent, ts(sess.StartedAt), endedAt,
		int(sess.State), sess.Risk.Score, sess.SignalCount)
	return err
}

func (s *Store) PutPreference(p audit.Preference) error {
	caps, _ := json.Marshal(p.RequiredCaps)
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO preferences
		(id, kind, scope, destination, required_caps, ceiling, taught_by, created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		p.ID, int(p.Kind), string(p.Scope), p.Destination, string(caps),
		int(p.Ceiling), p.TaughtBy, ts(p.CreatedAt))
	return err
}

func (s *Store) DeletePreference(id string) error {
	_, err := s.db.Exec(`DELETE FROM preferences WHERE id = ?`, id)
	return err
}

// Signals returns a session's signals in sequence order — the input to replay.
func (s *Store) Signals(sessionID string) ([]domain.Signal, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, seq, observed_at, agent, phase, kind, actor_type,
		       actor_name, target_type, target, scope, secret_shape, secret_count, attributes
		FROM signals WHERE session_id = ? ORDER BY seq, observed_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Signal
	for rows.Next() {
		var (
			sig                             domain.Signal
			observedAt, scope, shape, attrs string
			phase, kind, actorT, targetT    int
			actorName, target               sql.NullString
		)
		if err := rows.Scan(&sig.ID, &sig.SessionID, &sig.Seq, &observedAt, &sig.Agent,
			&phase, &kind, &actorT, &actorName, &targetT, &target, &scope,
			&shape, &sig.SecretCount, &attrs); err != nil {
			return nil, err
		}

		sig.ObservedAt, _ = time.Parse(time.RFC3339Nano, observedAt)
		sig.Phase = domain.Phase(phase)
		sig.Kind = domain.Kind(kind)
		sig.Actor = domain.Actor{Type: domain.ActorType(actorT), Name: actorName.String}
		sig.Target = domain.Target{
			Type:  domain.TargetType(targetT),
			Value: target.String,
			Scope: domain.Scope(scope),
		}
		sig.SecretShape = domain.SecretShape(shape)
		_ = json.Unmarshal([]byte(attrs), &sig.Attributes)

		out = append(out, sig)
	}
	return out, rows.Err()
}

// SessionIDs lists recorded sessions, most recent first.
func (s *Store) SessionIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM sessions ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DecisionRow is a decision as stored, for `guard report`.
type DecisionRow struct {
	ID          string
	SessionID   string
	SignalID    string
	Action      domain.Action
	Score       int
	DecidedAt   time.Time
	Explanation domain.Explanation
	Resolution  *domain.Resolution
	Suppressed  bool
}

func (s *Store) Decisions(sessionID string) ([]DecisionRow, error) {
	query := `SELECT id, session_id, signal_id, action, score, decided_at, explanation,
	                 resolution, suppressed
	          FROM decisions`
	args := []any{}
	if sessionID != "" {
		query += ` WHERE session_id = ?`
		args = append(args, sessionID)
	}
	query += ` ORDER BY decided_at`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DecisionRow
	for rows.Next() {
		var (
			r           DecisionRow
			action      int
			decidedAt   string
			explanation string
			resolution  sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.SessionID, &r.SignalID, &action, &r.Score, &decidedAt,
			&explanation, &resolution, &r.Suppressed); err != nil {
			return nil, err
		}

		r.Action = domain.Action(action)
		r.DecidedAt, _ = time.Parse(time.RFC3339Nano, decidedAt)
		_ = json.Unmarshal([]byte(explanation), &r.Explanation)
		if resolution.Valid {
			var res domain.Resolution
			if json.Unmarshal([]byte(resolution.String), &res) == nil {
				r.Resolution = &res
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Purge deletes everything. The developer must be able to remove all stored
// data with one documented command (Article IX).
func (s *Store) Purge() error {
	for _, t := range []string{"signals", "decisions", "sessions", "preferences"} {
		if _, err := s.db.Exec("DELETE FROM " + t); err != nil {
			return err
		}
	}
	return nil
}

// Counts reports row totals, for `guard doctor`.
func (s *Store) Counts() (map[string]int, error) {
	out := map[string]int{}
	for _, t := range []string{"signals", "decisions", "sessions", "preferences"} {
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + t).Scan(&n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, nil
}

// secretish names attribute keys that must never be persisted, whatever an
// adapter puts in them. Enforced here rather than trusted upstream: this is the
// last boundary before data hits the disk.
var secretish = map[string]bool{
	"content": true, "value": true, "body": true, "secret": true,
	"token": true, "password": true, "key": true, "env": true,
	"stdout": true, "stderr": true, "result": true,
}

func scrub(attrs map[string]any) map[string]any {
	if attrs == nil {
		return nil
	}
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		if secretish[k] {
			out[k] = "[redacted]"
			continue
		}
		out[k] = v
	}
	return out
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
