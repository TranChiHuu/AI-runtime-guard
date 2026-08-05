# Hook harness

Two scripts, neither installed by default. Registering a hook affects every
session in this project, so opting in is deliberate.

## capture.sh — record what the host actually sends

The published hook reference and the running product disagree; the fixtures in
`testdata/hook-events/` came from here, and are what `live.test.ts` asserts
against. Regenerate them whenever Claude Code updates.

```jsonc
// .claude/settings.local.json
{
  "hooks": {
    "PreToolUse":  [{ "hooks": [{ "type": "command", "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/capture.sh" }] }],
    "PostToolUse": [{ "hooks": [{ "type": "command", "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/capture.sh" }] }],
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/capture.sh" }] }],
    "SessionEnd":  [{ "hooks": [{ "type": "command", "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/capture.sh" }] }]
  }
}
```

Writes to `$GUARD_CAPTURE` (default `/tmp/guard-capture.jsonl`). It exits 0 with
no stdout, so it cannot allow, deny, or perturb a session.

**Redact before committing a fixture.** A captured `PostToolUse` carries the
read file's entire content and the command's full stdout.

## guard.sh — the real adapter

Same registration, swapping `capture.sh` for `guard.sh`. Requires `guard up`.
Hooks are read at session start, so a newly registered hook does not apply to
the session that registered it — start a new one, or use `claude -p`.
