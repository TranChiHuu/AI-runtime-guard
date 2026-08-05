#!/usr/bin/env node
/**
 * Claude Code hook adapter.
 *
 * Reads a hook event on stdin, asks the Brain for a verdict, and translates the
 * verdict into Claude Code's own idiom. It decides nothing: every judgment in
 * this process came out of the Brain (Article III).
 *
 * Install as a PreToolUse / PostToolUse hook:
 *
 *   { "hooks": { "PreToolUse": [{ "hooks": [
 *       { "type": "command", "command": "guard-claude-code" } ] }] } }
 */

import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';

import {
  Action,
  ChannelHint,
  Phase,
  RuntimeClient,
  resolve as resolveInteraction,
  type Channel,
  type Decision,
} from '@airuntimeguard/adapter-shared';

import { toSignal, type HookEvent } from './map.js';
import { nativeChannel, ttyChannel } from './channels.js';

/**
 * Claude Code's hook response. `deny` blocks the call with a reason the agent
 * sees; `ask` hands the question to Claude Code's own permission UI.
 */
interface HookResponse {
  hookSpecificOutput?: {
    hookEventName: string;
    permissionDecision: 'allow' | 'deny' | 'ask';
    permissionDecisionReason: string;
  };
  systemMessage?: string;
}

async function main(): Promise<void> {
  const event = readEvent();
  if (!event) {
    // Unreadable input is our problem, not the developer's. Say nothing and let
    // the call through.
    process.exit(0);
  }

  const signal = toSignal(event, {
    cwd: event.cwd ?? process.cwd(),
    home: homedir(),
    seq: nextSeq(event.session_id ?? ''),
    now: new Date(),
  });

  if (!signal || !signal.sessionId) {
    process.exit(0);
  }

  const client = new RuntimeClient();
  let decision: Decision | null = null;

  try {
    decision = await client.decide(signal);
  } catch {
    decision = null;
  }

  // Fail open. A guard that breaks the workflow gets uninstalled, and an
  // uninstalled guard protects nobody (Article VII). The developer is told,
  // loudly, that protection is off.
  if (!decision) {
    emit({ systemMessage: 'AI Runtime Guard is not running — protection is OFF (`guard up`)' });
    client.close();
    process.exit(0);
  }

  // POST-phase signals are observations. Nothing can be blocked retroactively.
  if (signal.phase === Phase.Post) {
    client.close();
    process.exit(0);
  }

  const action = decision.interaction
    ? await obtainAnswer(client, decision)
    : decision.action;

  emit(translate(action, decision));
  client.close();
  process.exit(0);
}

/**
 * Gets the human's answer over the best channel this platform offers, then
 * reports it back. Choosing the channel is platform-local knowledge — the
 * reason this lives in the adapter and not the Brain.
 */
async function obtainAnswer(client: RuntimeClient, decision: Decision): Promise<Action> {
  const interaction = decision.interaction!;

  // PAUSE is resolved out-of-band through `guard`. Refuse the call with the
  // Brain's reason rather than prompting for something that needs a wider look.
  if (interaction.channelHint === ChannelHint.OutOfBand) {
    return decision.action;
  }

  const channels: Channel[] = [nativeChannel(), ttyChannel()];

  const result = await resolveInteraction(decision, channels, async (resolution) => {
    await client.resolve(resolution);
  });

  return result.action;
}

/** Translates a verdict into Claude Code's idiom. Presentation only. */
function translate(action: Action, decision: Decision): HookResponse {
  const e = decision.explanation;

  switch (action) {
    case Action.Allow:
      return {};

    case Action.Notify:
      return { systemMessage: `AI Runtime Guard: ${e.summary}` };

    case Action.Ask:
      return {
        hookSpecificOutput: {
          hookEventName: 'PreToolUse',
          permissionDecision: 'ask',
          permissionDecisionReason: `${e.why}\n\n${e.guidance}`,
        },
      };

    case Action.Pause:
    case Action.Block:
      return {
        hookSpecificOutput: {
          hookEventName: 'PreToolUse',
          permissionDecision: 'deny',
          permissionDecisionReason: `${e.why}\n\n${e.guidance}`,
        },
      };

    default:
      return {};
  }
}

function readEvent(): HookEvent | null {
  try {
    const raw = readFileSync(0, 'utf8');
    return raw.trim() ? (JSON.parse(raw) as HookEvent) : null;
  } catch {
    return null;
  }
}

function emit(response: HookResponse): void {
  if (Object.keys(response).length > 0) {
    process.stdout.write(JSON.stringify(response));
  }
}

/**
 * Per-session sequence numbers.
 *
 * Each hook invocation is a fresh process, so the counter lives in a file
 * beside the runtime state. A gap tells the Brain that signals were lost, which
 * lowers confidence rather than silently understating a session.
 */
function nextSeq(sessionId: string): number {
  if (!sessionId) return 0;

  const { readFileSync: read, writeFileSync: write, mkdirSync } = require('node:fs') as typeof import('node:fs');
  const { join } = require('node:path') as typeof import('node:path');
  const { runtimeDir } = require('@airuntimeguard/adapter-shared') as typeof import('@airuntimeguard/adapter-shared');

  const dir = join(runtimeDir(), 'seq');
  const file = join(dir, `${sessionId}.seq`);

  try {
    mkdirSync(dir, { recursive: true, mode: 0o700 });
    const current = Number(read(file, 'utf8')) || 0;
    const next = current + 1;
    write(file, String(next), { mode: 0o600 });
    return next;
  } catch {
    // Sequence numbers are diagnostics, not correctness. Seq 0 means "this
    // adapter does not number its signals", which the Brain already handles.
    return 0;
  }
}

void main();
