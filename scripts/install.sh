#!/bin/sh
# Build AI Runtime Guard and print how to wire it into Claude Code.
#
# Deliberately does not register the hook. A hook applies to every session in a
# project, so that is the developer's decision to make explicitly -- and a
# security tool that installs itself without asking has picked a strange place
# to start earning trust.
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="${GUARD_BIN_DIR:-$HOME/.local/bin}"

need() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "missing: $1 ($2)" >&2
        exit 1
    }
}

need go "https://go.dev/dl/ — 1.23 or newer"
need node "https://nodejs.org — 20 or newer"
need pnpm "npm install -g pnpm"

echo "Building the Runtime Brain..."
(cd "$ROOT/core" && go build -o "$ROOT/guard" ./cmd/guard)

echo "Building the Claude Code adapter..."
(cd "$ROOT" && pnpm install --frozen-lockfile >/dev/null && pnpm -r build >/dev/null)

mkdir -p "$BIN"
cp "$ROOT/guard" "$BIN/guard"

cat <<EOF

Installed: $BIN/guard

  1. Put $BIN on your PATH if it is not already.
  2. Start the daemon:            guard up
  3. Check it:                    guard doctor
  4. See it decide:               guard simulate

To protect a project with Claude Code, add this to that project's
.claude/settings.json -- the hook runs for every session in the project, so
add it where you actually want protection:

{
  "hooks": {
    "PreToolUse":  [{ "hooks": [{ "type": "command", "command": "$ROOT/.claude/hooks/guard.sh" }] }],
    "PostToolUse": [{ "hooks": [{ "type": "command", "command": "$ROOT/.claude/hooks/guard.sh" }] }],
    "SessionEnd":  [{ "hooks": [{ "type": "command", "command": "$ROOT/.claude/hooks/guard.sh" }] }]
  }
}

Hooks are read when a session starts, so this does not apply to a session that
is already running. Start a new one.

Nothing leaves your machine, and no port is opened: the daemon listens on a
Unix socket under \$GUARD_HOME (default ~/.local/state/ai-runtime-guard).
EOF
