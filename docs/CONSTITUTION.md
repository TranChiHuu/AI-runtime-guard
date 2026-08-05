# Constitution

Non-negotiable rules for AI Runtime Guard. Everything in this file overrides
convenience, deadlines, and cleverness. If a change violates an article here,
the change is wrong — not the article.

Amendments require an explicit decision recorded in this file's history. Nothing
in `RUNTIME_MODEL.md` or `ARCHITECTURE.md` may contradict this document.

---

## Article I — Protection Is Local

Every runtime decision is made on the developer's machine, by the Runtime Brain,
with no network call in the decision path.

- The Brain MUST reach a decision without contacting any remote service.
- Cloud sync, telemetry, and remote dashboards are OPTIONAL and always
  out-of-band. They observe decisions; they never make them.
- A machine with no network MUST be exactly as protected as one online.

If a feature cannot work offline, it is not part of the decision path.

---

## Article II — Runtime Brain Is The Product

All behavior that decides *what is risky* and *what to do about it* lives in the
Go core. Nowhere else.

- Adapters normalize and transport. They MUST NOT score, judge, filter, or
  decide.
- The CLI presents and configures. It MUST NOT decide.
- The dashboard visualizes. It MUST NOT decide.
- If an adapter needs new logic, the logic belongs in the Brain and the adapter
  gets a new signal field.

Test: delete every adapter and the dashboard. The Brain must still be a complete,
testable safety system.

---

## Article III — Adapters Are Thin

An adapter's entire job is: receive a platform-native event, map it to the
normalized signal schema, send it over the Runtime API, apply the returned
verdict in the platform's own idiom.

Adapters MUST NOT:

- compute or adjust risk
- hold session state beyond the identifiers needed to correlate a call
- interpret policy
- decide to allow, deny, or prompt
- special-case a path, tool, or command name

An adapter that contains an `if` on a security-relevant value has a bug.

---

## Article IV — The Brain Is Platform-Independent

The Go core MUST NOT know that Claude Code, Codex, Gemini, Cursor, or MCP exist,
beyond an opaque `agent` label carried for reporting.

- No platform names in engine logic.
- No platform-shaped fields in the core domain model.
- Adding a new AI platform MUST require zero changes to the Brain.

---

## Article V — Every Decision Is Explainable

No decision may be emitted without a complete, human-readable explanation
attached. The explanation is part of the decision, not a logging afterthought.

Every decision MUST answer:

1. **What happened** — the triggering signal
2. **Why** — the session state and rules that produced this outcome
3. **What evidence** — the concrete prior signals that contributed
4. **What risk** — the score and the named factors that built it
5. **What to do** — the actions available to the developer

A decision the Brain cannot explain MUST NOT be enforced. If explanation
generation fails, the decision degrades to the fail-open behavior in Article VII.

Black-box scoring — including any model whose contribution cannot be itemized in
the evidence trail — is prohibited in the decision path.

---

## Article VI — Session Over Event

Risk is a property of a session, not of a single call. Engines reason over
accumulated session state. A rule that inspects only the current signal, with no
reference to session state, belongs in a linter, not here.

---

## Article VII — Intervene At The Lowest Sufficient Level

The intervention ladder is ordered and MUST be climbed, not jumped:

`ALLOW → NOTIFY → ASK → PAUSE → BLOCK`

- BLOCK is the last resort, reserved for irreversible actions at high confidence.
- Confidence gates escalation. Low confidence MUST NOT produce ASK or above.
- False interruptions cost more trust than they save risk. When uncertain,
  choose the rung below.

**Availability failure is not a security event.** If the Brain is unreachable,
times out, or crashes, the adapter fails **open** by default — the agent proceeds
and the developer is loudly warned that protection is off. A dead guard MUST NOT
brick a developer's agent.

Fail-closed is available as explicit opt-in configuration for environments that
require it. The default is not a claim that fail-open is safer; it is a claim
that a safety tool which breaks the workflow gets uninstalled, and an
uninstalled guard protects nobody.

---

## Article VIII — The Decision Path Is Fast

The Brain answers within a budget tight enough that developers do not feel it.
Targets in `RUNTIME_MODEL.md`; the rule here is that they are hard limits, not
aspirations.

- Exceeding the budget is a bug of the same severity as a wrong decision.
- Work that cannot fit the budget moves off the decision path (async enrichment,
  post-hoc analysis), never into it.

---

## Article IX — Runtime Data Belongs To The Developer

- All session data is stored locally, under the developer's control.
- Nothing leaves the machine without explicit, specific, revocable consent.
- Secret *values* are never persisted. Only the fact of access, its shape, and
  its location.
- The developer can inspect and delete everything the system has stored, with a
  single documented command.

---

## Article X — APIs Before UI

Every capability is reachable through the Runtime API and the CLI before any
pixel is drawn. The dashboard is a client of the same surface everything else
uses. No capability exists only in a UI.

---

## Article XI — Learning Never Silently Weakens Protection

Learned preferences ("always allow this") are scoped, inspectable, and
reversible.

- Every learned rule records what taught it, when, and in which session.
- Learned rules MUST be listable and revocable via CLI.
- Learning MAY lower friction for a specific, matched situation. It MUST NOT
  broaden into unrelated contexts, and it MUST NOT disable a category of
  detection.

---

## Article XII — Extension Without Modification

New platforms, new signal types, new risk factors, and new policies are added by
extending declared contracts — not by editing engine internals.

If supporting a new case requires a new branch inside an engine, the contract was
wrong. Fix the contract.
