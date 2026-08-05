import { test } from 'node:test';
import assert from 'node:assert/strict';

import { resolve, type Channel } from './resolve.js';
import {
  Action,
  ChannelHint,
  ResolutionSource,
  type Decision,
  type Resolution,
} from './types.js';

function askDecision(overrides: Partial<Decision['interaction']> = {}): Decision {
  return {
    id: 'd1',
    sessionId: 's1',
    signalId: 'sig-d',
    action: Action.Ask,
    explanation: {
      summary: 'Agent is about to POST to an unrecognized host',
      what: 'Outbound request to api.unknown-host.example',
      why: 'This session read 3 secrets and this host is unrecognized',
      evidence: ['sig-b', 'sig-c'],
      guidance: 'Allow once, or add the host to this workspace allowlist',
    },
    interaction: {
      promptId: 'p1',
      channelHint: ChannelHint.Inline,
      headlessDefault: Action.Block,
      timeoutMs: 50,
      options: [
        { id: 'once', label: 'Allow once', effect: Action.Allow, learns: false },
        { id: 'always', label: 'Always allow', effect: Action.Allow, learns: true },
        { id: 'deny', label: 'Deny', effect: Action.Block, learns: false },
      ],
      ...overrides,
    },
  };
}

function recorder() {
  const seen: Resolution[] = [];
  return { seen, report: async (r: Resolution) => void seen.push(r) };
}

function channel(name: string, opts: { available?: boolean; answer?: () => Promise<string | null> }): Channel {
  return {
    name,
    available: () => opts.available ?? true,
    prompt: opts.answer ?? (async () => null),
  };
}

test('prefers the first available channel', async () => {
  const { seen, report } = recorder();
  const calls: string[] = [];

  const native = channel('native', {
    available: false,
    answer: async () => {
      calls.push('native');
      return 'once';
    },
  });
  const tty = channel('tty', {
    answer: async () => {
      calls.push('tty');
      return 'once';
    },
  });

  const result = await resolve(askDecision(), [native, tty], report);

  assert.deepEqual(calls, ['tty'], 'unavailable channel must not be prompted');
  assert.equal(result.action, Action.Allow);
  assert.equal(result.source, ResolutionSource.Human);
  assert.equal(seen[0]?.optionId, 'once');
  assert.equal(seen[0]?.channel, 'tty');
});

test('applies the headless default when no channel can reach a human', async () => {
  const { seen, report } = recorder();

  const result = await resolve(askDecision(), [channel('native', { available: false })], report);

  assert.equal(result.action, Action.Block, 'must apply the Brain default, not invent one');
  assert.equal(result.source, ResolutionSource.Headless);
  assert.equal(seen[0]?.source, ResolutionSource.Headless, 'headless runs must still be reported');
});

test('an unanswered prompt times out to the headless default and is reported', async () => {
  const { seen, report } = recorder();

  const slow = channel('tty', {
    answer: () => new Promise((r) => setTimeout(() => r('once'), 500)),
  });

  const result = await resolve(askDecision({ timeoutMs: 20 }), [slow], report);

  assert.equal(result.action, Action.Block);
  assert.equal(result.source, ResolutionSource.Timeout);
  assert.equal(seen.length, 1, 'a timeout is never silently dropped');
});

test('a broken channel is an availability failure, not a verdict', async () => {
  const { seen, report } = recorder();

  const broken = channel('native', {
    answer: async () => {
      throw new Error('no tty');
    },
  });

  const result = await resolve(askDecision(), [broken], report);

  assert.equal(result.action, Action.Block);
  assert.equal(result.source, ResolutionSource.AdapterFailure);
  assert.equal(seen[0]?.source, ResolutionSource.AdapterFailure);
});

test('an option we never offered is treated as no answer', async () => {
  const { report } = recorder();

  const rogue = channel('native', { answer: async () => 'something-else' });
  const result = await resolve(askDecision(), [rogue], report);

  assert.equal(result.action, Action.Block, 'guessing the intent would be the adapter deciding');
  assert.equal(result.source, ResolutionSource.AdapterFailure);
});

test('PAUSE never prompts inline', async () => {
  const { seen, report } = recorder();
  let prompted = false;

  const any = channel('tty', {
    answer: async () => {
      prompted = true;
      return 'once';
    },
  });

  const decision = askDecision({ channelHint: ChannelHint.OutOfBand });
  decision.action = Action.Pause;

  const result = await resolve(decision, [any], report);

  assert.equal(prompted, false, 'PAUSE is resolved via guard, not inline');
  assert.equal(result.action, Action.Pause);
  assert.equal(seen.length, 0);
});

test('a decision needing no human passes straight through', async () => {
  const { report } = recorder();

  const decision: Decision = { ...askDecision(), action: Action.Allow };
  delete decision.interaction;

  const result = await resolve(decision, [], report);
  assert.equal(result.action, Action.Allow);
});

test('a learning choice is reported so the Brain can create the preference', async () => {
  const { seen, report } = recorder();

  const result = await resolve(askDecision(), [channel('native', { answer: async () => 'always' })], report);

  assert.equal(result.action, Action.Allow);
  assert.equal(seen[0]?.optionId, 'always');
  assert.equal(seen[0]?.source, ResolutionSource.Human);
});

test('a failed report still returns a verdict to the agent', async () => {
  const failing = async () => {
    throw new Error('daemon died');
  };

  const result = await resolve(askDecision(), [channel('native', { available: false })], failing);

  assert.equal(result.action, Action.Block, 'losing the audit record must not hang the agent');
});
