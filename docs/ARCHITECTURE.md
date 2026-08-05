# Architecture

How AI Runtime Guard is structured, where each boundary sits, and what may cross
it. Governed by `CONSTITUTION.md`; the domain it implements is
`RUNTIME_MODEL.md`.

---

## 1. Layers

```
                AI Agent
                   │
       Claude · Codex · Gemini · Cursor · MCP
                   │
       TypeScript Adapters (Sensors)          ← thin, platform-specific
                   │
           Runtime API (gRPC)                 ← the only boundary that matters
                   │
                   ▼
     ┌──────────────────────────────┐
     │       Runtime Brain          │         ← Go, platform-independent
     │         (Go Core)            │
     ├──────────────────────────────┤
     │  Session Engine              │
     │  Context Engine              │
     │  Risk Engine                 │
     │  Policy Engine               │
     │  Decision Engine             │
     │  Audit Engine                │
     └──────────────────────────────┘
                   │
        ┌──────────┴──────────┐
        ▼                     ▼
   Local SQLite         Local Dashboard
```

One rule explains the whole picture: **everything above the Runtime API
translates, everything below decides.**

---

## 2. Adapters (TypeScript)

One package per platform. Each adapter:

1. Hooks into its platform's native extension point (Claude Code hooks, MCP
   middleware, Codex/Gemini equivalents).
2. Maps the platform event to a `Signal` (`RUNTIME_MODEL.md` §2).
3. Calls the Runtime API.
4. Translates the returned `Decision.action` into the platform's own idiom
   (exit code, hook response, permission prompt).

That is the entire job. An adapter has no policy file, no risk table, no
allowlist, and no security-relevant conditional (Article III).

**Shared runtime.** Transport, retry, timeout, fail-open handling, and the
generated gRPC client live in one shared TS package. A per-platform adapter is
then a mapping table plus a verdict translator — small enough to review in one
sitting. New platform = new mapping, zero Go changes (Article IV).

**Signal enrichment is allowed; judgment is not.** An adapter may compute
`Target.scope` from the workspace root, or set `secret_shape` from a filename
pattern, because only it has that local knowledge. It reports *what something
is*. It never reports *how bad it is*.

### 2.1 Resolution channels

When a decision carries an `Interaction` (`RUNTIME_MODEL.md` §7.3), the adapter
obtains the human's answer. Which channel to use is platform-local knowledge —
which is precisely why this lives in the adapter and not the Brain. The adapter
renders and transports; it never chooses the outcome.

Channels, in preference order for `INLINE`:

1. **Native platform prompt.** Translate `ASK` into the platform's own
   permission UI (Claude Code's tool-permission response, an MCP elicitation).
   Best UX by far: it looks like the tool the developer already uses, and it
   costs us no TUI. The explanation must fit whatever text the platform allows,
   so `Explanation` carries both a one-line summary and the full detail.
2. **Controlling TTY.** Open `/dev/tty` directly and prompt. Used where the
   platform offers no native prompt but a terminal exists. Not the default —
   raw output fighting an agent's own TUI is worse than it sounds.
3. **Headless.** No human reachable: apply `headless_default` immediately and
   record that no human answered.

`OUT_OF_BAND` (`PAUSE`) never prompts inline. The adapter refuses the call with
the Brain's reason, and the pending prompt is served to `guard status` /
`guard resume` and the dashboard from the daemon's pending queue.

Every path ends in `Resolve` — including timeout and headless. The Brain always
learns what actually happened.

---

## 3. Runtime API (gRPC over Unix Domain Socket)

Transport is gRPC over a **Unix domain socket**, not TCP.

Local-first means no listening port: nothing to firewall, nothing another
process on the network can reach, and filesystem permissions become the access
control. The socket lives under the runtime directory (§5) with owner-only
permissions.

```protobuf
service Runtime {
  // PRE-phase: blocking, budgeted, returns a verdict.
  rpc Decide(Signal) returns (Decision);

  // POST-phase: fire-and-forget observation. Never blocks the agent.
  rpc Observe(stream Signal) returns (ObserveAck);

  // Session and status surfaces used by CLI and dashboard alike.
  rpc GetSession(SessionRequest) returns (Session);
  rpc WatchSession(SessionRequest) returns (stream SessionUpdate);
  rpc Resolve(ResolveRequest) returns (ResolveAck);   // developer's answer to ASK/PAUSE
  rpc Health(HealthRequest) returns (HealthResponse);
}
```

The `.proto` files are the single source of truth. Go server stubs and the TS
client are both generated; neither side hand-writes the wire types.

### 3.1 The connection-cost problem

Claude Code hooks are short-lived processes. Paying TLS-free-but-still-real gRPC
connection setup on every tool call would blow the 50 ms budget
(`RUNTIME_MODEL.md` §10) before any thinking happens.

Mitigation, in order of preference:

1. **Persistent sidecar.** `guard up` starts the daemon; the adapter keeps a
   warm connection for the life of the agent process where the platform allows a
   long-lived host process.
2. **Short-lived hook path.** Where the platform gives us only a per-call
   process, the hook connects to the already-warm UDS. A UDS connect plus one
   unary call is sub-millisecond; the cost is process startup, which is the
   platform's, not ours.
3. **Budget enforcement.** The client enforces the deadline itself and fails open
   (Article VII) rather than letting the agent hang.

This is measured before the first adapter ships. If Node process startup alone
eats the budget on any platform, the answer is a small native launcher for that
platform's hook — not moving decisions out of the Brain.

### 3.2 Versioning

The API is versioned (`runtime.v1`). Adapters declare the version they speak.
The daemon supports the current and previous major version so a stale adapter
degrades gracefully instead of failing closed on an upgrade.

---

## 4. Runtime Brain (Go)

Six engines, one direction of flow, no cycles.

```
Signal ──▶ Session ──▶ Context ──▶ Risk ──▶ Policy ──▶ Decision ──▶ verdict
              │           │          │         │          │
              └───────────┴──────────┴─────────┴──────────┴──▶ Audit
```

| Engine | Owns | May not |
|---|---|---|
| **Session** | signal ingestion, ordering, capability latching, lifecycle | interpret meaning |
| **Context** | trust, intent, sensitivity, novelty | assign scores |
| **Risk** | factors, weights, combinations, confidence | know about policy or actions |
| **Policy** | matching, scope resolution, floors and ceilings | compute risk |
| **Decision** | ladder selection, irreversibility gate, explanation, preferences | recompute risk |
| **Audit** | durable append-only record, replay source | influence any decision |

Each engine is a package with a narrow interface, a pure core, and injected
dependencies (clock, store, config). Purity is not an aesthetic preference — it
is what the replay invariant (`RUNTIME_MODEL.md` §11) requires.

**Dependency rule.** Engines depend on the domain model and on interfaces they
declare themselves. No engine imports another engine's package. The daemon wires
them together; the engines do not know they are in a pipeline.

**No platform strings.** `agent` is an opaque label carried for reporting.
`grep -ri "claude\|codex\|gemini\|cursor"` over the engine packages returns
nothing but that field's doc comment. This is worth an actual CI check.

---

## 5. Storage (SQLite)

One local SQLite database under the runtime directory
(`$XDG_STATE_HOME/ai-runtime-guard` or the platform equivalent), owner-only.

```
sessions      lifecycle, agent, final state
signals       append-only, the replay source
decisions     verdict + full explanation + risk snapshot
policies      developer- and org-authored, with source
preferences   learned, scoped, revocable
```

Choices that follow from the model:

- **WAL mode.** The dashboard and `guard status` read while the decision path
  writes, without blocking it.
- **Writes off the hot path.** `Decide` returns after the in-memory decision;
  the durable write is committed by the Audit Engine asynchronously. The
  ordering guarantee that matters is that no decision is *lost*, not that it is
  *fsynced before* the agent proceeds — that would put disk latency inside a
  50 ms budget.
- **Live sessions are in-memory.** SQLite is durability and history, not the
  working set.
- **No secret values, ever** (Article IX). This is enforced at the store
  boundary, not left to callers.
- **Retention is bounded and configurable**, with a documented command to
  inspect and purge everything.

---

## 6. Configuration

Risk weights, combination bonuses, thresholds, and ladder gates are **data, not
code** — versioned files shipped with the daemon and overridable locally.

Tuning the model must not require a Go release, and a weight change must be
reviewable as a diff and validatable by replaying recorded sessions
(`RUNTIME_MODEL.md` §11). Config is loaded once at startup and its version is
stamped into every decision, so an audit record always says which model produced
it.

Policies are authored separately (workspace-level file, checked into the repo if
the team wants it shared) and are hot-reloadable.

---

## 7. CLI

The CLI is the primary interface (Article X). It is a gRPC client — it has no
privileged path into the Brain.

```
guard up        start the daemon
guard status    current sessions, states, live risk
guard doctor    verify adapters, socket, permissions, config
guard report    session history, decisions, explanations
guard policy    list, add, validate
guard learned   list, revoke preferences
guard replay    re-run a recorded session against current config
```

`guard replay` is not a convenience command. It is how any change to the risk
model gets validated before release, and it is the seed of Runtime Replay as a
product.

---

## 8. Dashboard

A local read-mostly client of the same gRPC surface, served on localhost, off by
default. It renders `WatchSession` streams and audit history.

It contains no risk logic, no policy interpretation, and no decision rendering
that isn't already in the `Explanation` the Brain produced. If the dashboard
needs to compute something to display it, that computation belongs in the Brain.

---

## 9. Repository Layout

```
proto/                  .proto definitions — source of truth
core/                   Go module: the Runtime Brain
  domain/               Signal, Session, Risk, Decision — no dependencies
  engine/
    session/ context/ risk/ policy/ decision/ audit/
  store/                SQLite implementation of store interfaces
  config/               weights, thresholds, loading, versioning
  server/               gRPC service, wire<->domain conversion
  store/sqlite/         durable local storage
  brain/                wires the engines into the loop
  cmd/guard/            the guard CLI and daemon
  cmd/demo/             the Brain's reasoning without a daemon
adapters/               TypeScript workspace
  shared/               transport, retry, fail-open, generated client
  claude-code/ codex/ gemini/ cursor/ mcp/
dashboard/              TypeScript: local UI
docs/
scripts/e2e.sh          end-to-end check: daemon, gRPC, storage, replay
Dockerfile              reproducible build and test
docker-compose.yml      docker compose run --rm test | e2e | demo
```

`core/domain` importing nothing but the standard library is the structural
statement of Article IV.

The CLI lives in `core/cmd/guard` rather than a top-level module: it is a plain
gRPC client with no privileged path into the Brain, and a second Go module would
buy a `replace` directive and nothing else.

---

## 10. Build Order

Each step is independently useful and testable:

1. `proto/` + `core/domain` — the contract and the model
2. Session Engine + in-memory store — sessions and capability latching
3. Risk Engine with config-driven weights — scores with itemized factors
4. Decision Engine + Explanation — verdicts that can explain themselves
5. gRPC server + `guard status` — the Brain observable from outside
6. SQLite store + Audit + `guard replay` — durability and the replay invariant
7. First adapter (Claude Code) — the loop closes end to end
8. Policy Engine, Context Engine, learning
9. Dashboard

Note what comes last: the UI. And note that `guard replay` (6) lands before the
first real adapter (7) — from the moment real signals arrive, every model change
is validated against recorded reality rather than intuition.

---

## 11. Architectural Fitness Checks

Enforced in CI, because these are the invariants that quietly rot:

- `core/domain` imports nothing outside the standard library
- no engine package imports another engine package
- no platform name appears in `core/engine/**`
- no adapter package contains a security-relevant conditional (reviewed; lint
  where mechanizable)
- every `Decision` returned by the server carries a complete `Explanation`
- replaying `testdata/sessions/**` produces byte-identical decisions
