# Runtime Model

The domain model of the Runtime Brain: what a signal is, what a session is, how
risk is computed, and how a decision is produced. This is the contract every
engine and adapter is written against.

Governed by `CONSTITUTION.md`. Structure and boundaries in `ARCHITECTURE.md`.

---

## 1. The Loop

```
Observe  →  Understand  →  Predict  →  Intervene  →  Learn
```

| Step | Engine | Input | Output |
|---|---|---|---|
| Observe | Session Engine | Signal | Session (updated) |
| Understand | Context Engine | Session | Context |
| Predict | Risk Engine | Session + Context | Risk |
| Intervene | Policy + Decision Engine | Risk + Policy | Decision |
| Learn | Decision + Audit Engine | Outcome | Preference, Record |

Every engine is a pure function of its inputs plus explicitly injected state. No
engine reads the clock, the filesystem, or the network directly — all of that is
passed in. This is what makes sessions replayable.

---

## 2. Signal

A Signal is one normalized observation. It is the *only* thing adapters produce.

```
Signal
  id            string        ULID, adapter-generated, unique
  session_id    string        correlates signals into one session
  agent         string        opaque label: "claude-code", "codex", ...
  seq           uint64        monotonic per session; gaps mean lost signals
  observed_at   timestamp     adapter's clock, UTC
  phase         Phase         PRE | POST
  kind          Kind          see below
  actor         Actor         what is acting
  target        Target        what is acted upon
  attributes    map[str]any   kind-specific, declared per kind
  raw_ref       string        opaque adapter-local handle for replay; optional
```

`phase` matters: `PRE` signals can be intervened on (the action has not
happened). `POST` signals are observations only — they update session state and
inform *future* decisions, but nothing can be blocked retroactively.

### 2.1 Kind

Kinds are a closed set in the core. Adding one is a versioned contract change
(Article XII), not an adapter decision.

```
PROMPT          user or system instruction entering the agent
TOOL_CALL       agent invoking any tool
TOOL_RESULT     result returned to the agent
FILE_READ       filesystem read
FILE_WRITE      filesystem write, create, or delete
SHELL_EXEC      shell command execution
NETWORK         outbound connection attempt
GIT             git operation (commit, push, remote change, ...)
MCP             MCP server call
CONTEXT_INGEST  external content entering context (fetched page, file, tool output)
SESSION_START   session lifecycle
SESSION_END     session lifecycle
```

### 2.2 Actor and Target

```
Actor
  type   AGENT | USER | TOOL | MCP_SERVER
  name   string          tool name, server name; opaque to the Brain

Target
  type   PATH | COMMAND | HOST | REPO | RESOURCE | NONE
  value  string          normalized: paths absolute, hosts lowercased
  scope  string          optional grouping: "home", "repo", "system", "external"
```

`scope` is computed by the adapter from platform-local knowledge (where the
workspace root is), because only the adapter can know it. It is descriptive, not
a judgment — the adapter says *where* something is, never *whether it is safe*.

### 2.3 Supervision

```
supervision   UNKNOWN | SUPERVISED | UNATTENDED
```

Whether a human is positioned to answer a question right now. `UNKNOWN` counts
as supervised: assuming nobody is watching would silently disable prompting for
every adapter that omits the field.

An `UNATTENDED` session is still fully protected — it just cannot be asked. The
Decision Engine collapses `ASK` to the headless default rather than issuing a
prompt that would time out into the same answer (§7.3). `PAUSE` and `BLOCK` are
unaffected; they take effect without an answer.

### 2.4 Secrets

Adapters MUST NOT transmit secret values. When a signal touches something
secret-shaped, the adapter sets:

```
attributes.secret_shape   = "env-file" | "private-key" | "token" | "credential-store"
attributes.secret_count   = int
```

and omits the content. The Brain reasons about shape and count, never value
(Article IX).

---

## 3. Session

The Session is the accumulated understanding of one agent run. It is the primary
unit of reasoning (Article VI).

```
Session
  id              string
  agent           string
  started_at      timestamp
  last_signal_at  timestamp
  state           SafetyState
  capabilities    Capabilities
  timeline        []SignalRef      bounded; see 3.3
  risk            Risk             current
  decisions       []DecisionRef
  learned         []PreferenceRef  applied this session
```

### 3.1 Capabilities

Capabilities are the compressed "what has this session already done" — the thing
that makes a later signal dangerous. Each is a latched fact with evidence.

```
Capabilities
  secret_access        Capability   read anything secret-shaped
  filesystem_read      Capability   read outside declared workspace
  filesystem_write     Capability   any write
  shell_exec           Capability   any shell execution
  outbound_network     Capability   any outbound connection
  git_write            Capability   commit, push, remote mutation
  untrusted_context    Capability   ingested content from outside the workspace
  credential_material  Capability   private keys, tokens, keychain

Capability
  active      bool
  first_seen  timestamp
  count       int
  evidence    []SignalRef    the signals that set it, capped
```

Capabilities **latch**: once true in a session, they stay true. A session that
read `.env` an hour ago is still a session that has seen secrets. They reset only
on `SESSION_END`.

### 3.2 Safety State

```
SAFE  →  WATCHING  →  WARNING  →  CRITICAL  →  INTERVENTION
```

State is derived from risk score and decision history — it is a *display and
sequencing* concept, not an independent input to risk. It moves up immediately
and decays down only on sustained low risk, so a session cannot flicker its way
out of scrutiny.

| State | Meaning | Typical effect |
|---|---|---|
| SAFE | nothing notable | silent |
| WATCHING | risky capability present, no combination | silent, visible in `guard status` |
| WARNING | risky combination present | NOTIFY |
| CRITICAL | high risk, irreversible action plausible | ASK / PAUSE |
| INTERVENTION | a decision above ALLOW is in force | ASK / PAUSE / BLOCK |

### 3.3 Bounds

Sessions are unbounded in principle and bounded in practice. The timeline keeps
the last N signals in memory plus all signals referenced as capability evidence;
everything else is durable in SQLite and loaded only for reporting and replay.
Memory per live session is capped and MUST NOT grow with session length.

---

## 4. Context

The Context Engine answers "what is this session *doing*, and in what
surroundings" — the interpretation layer between raw state and risk.

```
Context
  workspace_trust   TRUSTED | UNKNOWN | UNTRUSTED
  intent            []IntentTag       inferred coarse activity
  sensitivity       LOW | MEDIUM | HIGH   of the resources touched
  novelty           float 0..1        how unlike this developer's norm
  destination_trust map[host]Trust    per outbound target
```

Intent tags are coarse and few (`build`, `test`, `refactor`, `deploy`,
`investigate`, `exfiltrate-shaped`). They are hints that modulate risk, never
decisions on their own. Context is derived, never persisted as truth — it is
recomputed from session state so a replay produces identical results.

---

## 5. Risk

Risk is a continuous, recomputed, itemized score. Never a rule match.

```
Risk
  score       int 0..100
  confidence  float 0..1
  factors     []Factor
  computed_at timestamp

Factor
  name         string     stable identifier, e.g. "secret_access"
  contribution int        points added
  evidence     []SignalRef
  description  string     human sentence
```

### 5.1 How it is computed

```
score = clamp(0, 100, Σ base(factor) + Σ combination_bonus(factors) − Σ mitigation)
```

Three deliberate properties:

1. **Additive and itemized.** Every point on the board traces to a named factor
   with evidence. Article V is satisfied by construction, not by writing prose
   after the fact.
2. **Combinations dominate.** Individual capabilities score low. Their
   *co-occurrence* scores high. `secret_access` alone is mild;
   `secret_access + outbound_network + untrusted_destination` is the actual
   threat. Combination bonuses are where the intelligence lives.
3. **Confidence is separate from score.** Score says *how bad if real*.
   Confidence says *how sure we are*. Both gate intervention (Article VII) —
   high score with low confidence NOTIFYs; it does not BLOCK.

Weights are data, not code — see `ARCHITECTURE.md` §6.

### 5.2 Mitigations

Risk goes down as well as up. Signals that reduce it: destination on the
workspace's declared allowlist, prior explicit approval of this exact shape,
action reversible (write to a tracked file in a clean repo), developer actively
present and responding.

A model that only accumulates risk eventually flags every long session. Decay and
mitigation are what keep the tool usable past hour two.

---

## 6. Policy

Policy expresses developer and organization intent. It constrains the Decision
Engine; it does not compute risk.

```
Policy
  id          string
  scope       GLOBAL | WORKSPACE | SESSION
  match       Matcher       over signal kind, target, capabilities, risk band
  effect      Effect        floor and/or ceiling on the intervention ladder
  reason      string        surfaced verbatim in the explanation
  source      string        where it came from, for the audit trail
```

Policies set **floors and ceilings**, not verdicts:

- floor: "never go below ASK for pushes to remote `production`"
- ceiling: "never exceed NOTIFY inside this scratch workspace"

This is the mechanism that keeps `IF tool == curl THEN deny` out of the system.
Policy shapes the range; risk and confidence choose within it.

Conflicts resolve most-specific-scope-wins, then most-restrictive-floor-wins. The
resolution path is recorded in the decision.

---

## 7. Decision

```
Decision
  id            string
  session_id    string
  signal_id     string        the signal being decided
  action        ALLOW | NOTIFY | ASK | PAUSE | BLOCK
  risk          Risk          snapshot at decision time
  policies      []PolicyRef   applied, in resolution order
  explanation   Explanation
  options       []Option      what the developer may choose
  decided_at    timestamp
  latency_us    int
```

### 7.1 Explanation

Mandatory. Mirrors Article V one-to-one:

```
Explanation
  what      string        "Agent is about to POST to api.unknown-host.com"
  why       string        "This session read 3 secrets and this host is unrecognized"
  evidence  []SignalRef   the actual prior signals, with timestamps
  risk      RiskSummary   score, band, top contributing factors
  guidance  string        "Allow once, or add the host to this workspace's allowlist"
```

### 7.2 Choosing the action

```
1. Risk + confidence propose a rung on the ladder.
2. Policy floors and ceilings clamp it.
3. Learned preferences may lower it — never below a policy floor.
4. Irreversibility gate: only irreversible actions may reach BLOCK.
5. If the explanation cannot be built, degrade to fail-open (Article V).
```

Irreversible = cannot be undone from the developer's machine: outbound network
send, `git push`, force delete, credential write, external API mutation. A file
write in a clean git tree is reversible and MUST NOT be blocked.

### 7.3 Interaction

`ASK` and `PAUSE` need a human answer. A human takes seconds; the Brain's budget
is 50 ms (§10). Therefore **`Decide` never waits for a human.** It returns a
verdict plus everything needed to obtain the answer:

```
Interaction
  prompt_id         string        correlates the answer back
  channel_hint      INLINE | OUT_OF_BAND
  headless_default  Action        what to do if no human is reachable
  timeout_ms        int           how long the adapter should wait
  options           []Option
```

```
Option
  id      string
  label   string        "Allow once"
  effect  Action        the action this choice resolves to
  learns  bool          whether choosing it creates a Preference (§8)
```

`headless_default` is a judgment — what is safe when nobody is watching — so the
Brain decides it and the adapter merely applies it. This is what keeps the
adapter thin (Article III) even though it owns the prompt.

The answer returns via `Resolve(prompt_id, option_id)`. The Brain then applies
the effect, records it in the audit trail, and — if `learns` — creates a
Preference. An unanswered prompt resolves to `headless_default` on timeout and is
recorded as such, never silently dropped.

### 7.4 ASK versus PAUSE

They differ in channel and blocking, not merely in severity:

| | ASK | PAUSE |
|---|---|---|
| Agent | blocked inline, waiting | stopped; the call is refused |
| Channel | `INLINE` — the agent's own turn | `OUT_OF_BAND` — `guard` |
| Resolution | answer within `timeout_ms` | `guard resume` / `guard deny`, no deadline |
| Use when | one call needs a yes/no now | the whole session needs a look |

The Brain's 50 ms budget covers only producing the decision. Time the developer
spends thinking is not the Brain's latency and is excluded from §10.

---

## 8. Learning

```
Preference
  id          string
  scope       GLOBAL | WORKSPACE
  match       Matcher        the specific shape approved
  effect      lowers ceiling to ALLOW | NOTIFY
  taught_by   DecisionRef    the choice that created it
  created_at  timestamp
  expires_at  timestamp?     optional
```

`Always allow` creates a Preference matching the *specific shape* — this tool,
this target scope, this capability set — not the tool in general. Preferences
never override policy floors, are listable and revocable by CLI, and every
application is recorded in the decision (Article XI).

---

## 9. Audit

Every decision is durably recorded before it is enforced, with the session state
that produced it. The audit record is what makes `guard report` and Runtime
Replay possible, and it is the ground truth for tuning weights.

Records are append-only and local. They contain signal references and metadata —
never secret values.

---

## 10. Timing Budget

Hard limits (Article VIII), measured adapter-side round trip:

| Path | p50 | p99 | Hard ceiling |
|---|---|---|---|
| PRE decision (ALLOW expected) | < 5 ms | < 20 ms | 50 ms |
| PRE decision (escalated) | < 15 ms | < 40 ms | 50 ms |
| POST observation (fire-and-forget) | — | — | non-blocking |

At the ceiling the adapter stops waiting and fails open with a warning. POST
signals never block the agent. Anything that cannot meet this budget — enrichment
lookups, novelty modeling, report generation — runs off the decision path and
lands in the *next* decision, not this one.

---

## 11. Replay Invariant

Given the same ordered signals, the same policies, and the same preferences, the
Brain MUST produce byte-identical decisions.

This is why engines take time as input, why Context is derived rather than
stored, and why nothing in the decision path touches the network. Replay is not
a feature built later; it is a property preserved from the first commit, and it
is how every risk-model change gets validated against real recorded sessions
before it ships.
