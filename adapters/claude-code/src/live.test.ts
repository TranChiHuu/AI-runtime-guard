/**
 * The mapper, run against payloads captured from a live Claude Code session.
 *
 * The published reference and the running product disagree in two places, so
 * these fixtures — not the docs — are the contract this adapter is written
 * against. Regenerate them by registering `.claude/hooks/capture.sh` and
 * running any session; see docs/ARCHITECTURE.md §2.2.
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

import { Kind, Phase, Supervision } from '@airuntimeguard/adapter-shared';

import { toSignal, type HookEvent } from './map.js';

const fixture = resolve(
  fileURLToPath(new URL('.', import.meta.url)),
  '../../../testdata/hook-events/claude-code.jsonl',
);

const events: HookEvent[] = readFileSync(fixture, 'utf8')
  .split('\n')
  .filter(Boolean)
  .map((l) => JSON.parse(l) as HookEvent);

const opts = {
  cwd: '/repo',
  home: '/home/dev',
  now: new Date('2026-01-01T12:00:00Z'),
  fallbackId: 'fallback',
};

function eventFor(name: string, tool?: string): HookEvent {
  const found = events.find(
    (e) => e.hook_event_name === name && (tool === undefined || e.tool_name === tool),
  );
  assert.ok(found, `fixture is missing ${name}${tool ? `/${tool}` : ''}`);
  return found;
}

test('every captured event maps to a signal', () => {
  for (const event of events) {
    const s = toSignal(event, opts);
    assert.ok(s, `${event.hook_event_name} produced no signal`);
    assert.ok(s.id, `${event.hook_event_name} produced no signal id`);
    assert.ok(s.sessionId, `${event.hook_event_name} produced no session id`);
  }
});

test('real tool_use_id values are used verbatim as signal ids', () => {
  const pre = eventFor('PreToolUse', 'Read');
  const s = toSignal(pre, opts);

  assert.match(s!.id, /^toolu_/, 'the host id should be carried through unchanged');
  assert.equal(s!.id, pre.tool_use_id);
});

test('a PreToolUse Read of a real payload maps to FILE_READ in the PRE phase', () => {
  const s = toSignal(eventFor('PreToolUse', 'Read'), opts);

  assert.equal(s!.kind, Kind.FileRead);
  assert.equal(s!.phase, Phase.Pre);
});

test('a real Bash call maps to SHELL_EXEC with the command', () => {
  const s = toSignal(eventFor('PreToolUse', 'Bash'), opts);

  assert.equal(s!.kind, Kind.ShellExec);
  assert.equal(s!.target.value, 'echo hello');
});

// The live payload sends `reason`; the published reference says `exit_reason`.
// Getting this wrong means capabilities never reset and every session on the
// machine stays permanently marked as one that once touched a secret.
test('SessionEnd is read from the field the host actually sends', () => {
  const event = eventFor('SessionEnd');
  assert.ok('reason' in event, 'fixture should carry the live spelling');

  const s = toSignal(event, opts);
  assert.equal(s!.kind, Kind.SessionEnd);
  assert.equal(s!.attributes['exit_reason'], 'other');
});

// A session ending cannot be prevented. Marking it PRE made the Brain issue an
// ASK for permission to do something that had already happened.
test('session lifecycle is observed, never decided on', () => {
  const s = toSignal(eventFor('SessionEnd'), opts);
  assert.equal(s!.phase, Phase.Post, 'SessionEnd must never be decidable');
});

test('the live permission_mode is carried through as supervision', () => {
  const s = toSignal(eventFor('PreToolUse', 'Read'), opts);
  assert.equal(s!.supervision, Supervision.Supervised, 'mode "default" means a human is there');
});

// tool_response carries the file's entire content for Read and full stdout for
// Bash. It must never appear in a signal, whatever else changes.
test('no mapped signal carries tool output', () => {
  for (const event of events) {
    const s = toSignal(event, opts);
    const serialized = JSON.stringify(s);

    assert.doesNotMatch(serialized, /tool_response/, `${event.hook_event_name} leaked tool_response`);
    assert.doesNotMatch(serialized, /packages:/, `${event.hook_event_name} leaked file content`);
    assert.doesNotMatch(serialized, /PROMPT-TEXT-REDACTED/, `${event.hook_event_name} leaked prompt text`);
  }
});
