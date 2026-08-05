import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Band, type RiskSummary } from '@airuntimeguard/adapter-shared';

import { score, band, line, block } from './render.js';

const risk = (n: number, b: Band = Band.Warning): RiskSummary => ({
  score: n,
  band: b,
  topFactors: [{ name: 'secret_access', contribution: 18, description: 'read something secret-shaped' }],
});

// Graded, not always-red: a score red at 25 and red at 95 has told the
// developer nothing, and after a week of red they stop seeing it.
test('colour is graded by severity, not constant', () => {
  assert.match(score(risk(95)), /\x1b\[1;31m/, 'critical should be bold red');
  assert.match(score(risk(60)), /\x1b\[31m/, 'warning should be red');
  assert.match(score(risk(30)), /\x1b\[33m/, 'watching should be yellow');
  assert.match(score(risk(5)), /\x1b\[2m/, 'safe should be dim');
});

test('every rendered score carries the number itself', () => {
  for (const n of [0, 43, 87, 100]) {
    assert.match(score(risk(n)), new RegExp(`${n}/100`));
  }
});

test('NO_COLOR strips escapes but keeps the score', () => {
  process.env['NO_COLOR'] = '1';
  try {
    const out = score(risk(87));
    assert.doesNotMatch(out, /\x1b/, 'a display preference must be honoured');
    assert.match(out, /87\/100/, 'the number must survive losing its colour');
  } finally {
    delete process.env['NO_COLOR'];
  }
});

test('a missing risk renders nothing rather than a fake zero', () => {
  assert.equal(score(undefined), '');
  assert.equal(line(undefined, 'something happened'), 'something happened');
});

test('the block names the factors, so the number is never unexplained', () => {
  const out = block(risk(87, Band.Critical), 'why this happened', 'what to do');

  assert.match(out, /87\/100/);
  assert.match(out, /CRITICAL/);
  assert.match(out, /why this happened/);
  assert.match(out, /read something secret-shaped/, 'an unexplained score is a black box');
  assert.match(out, /what to do/);
});

test('band names follow the score, not the caller', () => {
  assert.equal(band(risk(90, Band.Critical)), 'CRITICAL');
  assert.equal(band(risk(60, Band.Warning)), 'WARNING');
  assert.equal(band(undefined), 'SAFE');
});
