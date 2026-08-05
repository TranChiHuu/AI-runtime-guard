#!/bin/sh
# End-to-end check: start the daemon, drive it over the real socket, verify the
# verdicts and the audit trail.
#
# This asserts on behaviour a unit test cannot reach — the gRPC boundary, the
# SQLite writes, and the replay invariant — so it fails loudly rather than
# printing output nobody reads.
set -eu

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "  ok  $*"; }

echo "=== AI Runtime Guard — end to end ==="
echo

guard up &
DAEMON=$!
trap 'kill $DAEMON 2>/dev/null || true' EXIT

# Wait for the socket rather than sleeping a guessed interval.
i=0
while [ ! -S "${GUARD_HOME}/guard.sock" ]; do
  i=$((i + 1))
  [ "$i" -gt 100 ] && fail "daemon never opened its socket"
  sleep 0.1
done
pass "daemon listening on ${GUARD_HOME}/guard.sock"

guard doctor | grep -q "Protection is ON" || fail "doctor does not report protection ON"
pass "doctor reports protection ON"

OUT=$(guard simulate)
echo "$OUT"
echo

# --- the claims that matter -------------------------------------------------

echo "=== assertions ==="

echo "$OUT" | grep -q "PAUSE.*collect.unknown-host.example" \
  || fail "exfiltration chain did not reach PAUSE"
pass "exfiltration chain pauses on the outbound send"

echo "$OUT" | grep -q "PAUSE.*Git push" \
  || fail "injected git push did not reach PAUSE"
pass "prompt injection followed by a push pauses"

# The benign session must stay completely silent. A guard that narrates ordinary
# work is a guard developers turn off.
echo "$OUT" | sed -n '/Benign session/,/^Exfiltration/p' | grep -qE "ASK|PAUSE|BLOCK" \
  && fail "benign session produced an intervention"
pass "benign session stays silent"

# Same action, allowlisted destination: the mitigation must actually mitigate.
echo "$OUT" | sed -n '/allowlisted/,$p' | grep -qE "PAUSE|BLOCK" \
  && fail "allowlisted destination still escalated"
pass "allowlisted destination does not escalate"

echo "$OUT" | grep -q "headless default: BLOCK" \
  || fail "escalated interaction has no BLOCK headless default"
pass "escalated prompts default to BLOCK when no human is reachable"

echo "$OUT" | grep -q "answered: Allow once" \
  || fail "Resolve round trip did not complete"
pass "Resolve round trip completes"

# An unattended session must never be asked: the prompt would time out, the
# default would apply anyway, and the developer would have been interrupted by a
# question that decided nothing.
echo "$OUT" | sed -n '/Unattended session/,/^Same upload/p' | grep -q "ASK" \
  && fail "unattended session was asked a question nobody could answer"
pass "unattended session is decided, not asked"

echo "$OUT" | grep -q "No human was available to ask" \
  || fail "unattended decision does not say why it was not asked"
pass "unattended decisions explain that nobody was asked"

# --- durability -------------------------------------------------------------

guard report | grep -q "why:" || fail "audit trail has no explanations"
pass "audit trail persisted with explanations"

SESSION=$(guard replay 2>&1 | grep "sim-exfil" | head -1 | tr -d ' ')
[ -n "$SESSION" ] || fail "no recorded session found for replay"

REPLAY=$(guard replay "$SESSION")
echo "$REPLAY"
echo "$REPLAY" | grep -q "replay identical" \
  || fail "replay was not deterministic — the replay invariant is broken"
pass "replay reproduces every decision exactly"

# --- privacy ----------------------------------------------------------------
# Article IX: secret values must never reach the database.
if command -v strings >/dev/null 2>&1; then
  strings "${GUARD_HOME}/guard.db" 2>/dev/null | grep -q "BEGIN.*PRIVATE KEY" \
    && fail "key material found in the database"
fi
pass "no secret values in the database"

echo
echo "=== all end-to-end checks passed ==="
