/**
 * How a verdict looks in the developer's terminal.
 *
 * Presentation only. Every number and sentence here came from the Brain — this
 * file chooses colour and layout, never severity (Article III).
 */

import { Band, type RiskSummary } from '@airuntimeguard/adapter-shared';

const RESET = '\x1b[0m';
const BOLD_RED = '\x1b[1;31m';
const RED = '\x1b[31m';
const YELLOW = '\x1b[33m';
const DIM = '\x1b[2m';

/**
 * Whether to emit colour.
 *
 * NO_COLOR is honoured because it is the one convention every terminal tool
 * agrees on, and a guard that ignores a developer's display settings has
 * already started being the thing people turn off.
 */
function colourEnabled(): boolean {
  if (process.env['NO_COLOR']) return false;
  if (process.env['GUARD_COLOR'] === '0') return false;
  return true;
}

function paint(text: string, colour: string): string {
  return colourEnabled() ? `${colour}${text}${RESET}` : text;
}

/**
 * The score, coloured by how bad it is.
 *
 * Graded rather than always-red on purpose: a score that is red at 25 and red
 * at 95 has told the developer nothing, and after a week of red they stop
 * seeing it. Red is reserved for scores that earned it.
 */
export function score(risk: RiskSummary | undefined): string {
  if (!risk) return '';

  const value = risk.score ?? 0;
  const text = `${value}/100`;

  if (value >= 80) return paint(`■ ${text}`, BOLD_RED);
  if (value >= 50) return paint(`■ ${text}`, RED);
  if (value >= 20) return paint(`■ ${text}`, YELLOW);
  return paint(`■ ${text}`, DIM);
}

/** Band name, for the one-line form. */
export function band(risk: RiskSummary | undefined): string {
  switch (risk?.band) {
    case Band.Critical:
    case Band.Intervention:
      return 'CRITICAL';
    case Band.Warning:
      return 'WARNING';
    case Band.Watching:
      return 'WATCHING';
    default:
      return 'SAFE';
  }
}

/** The single line shown for a NOTIFY: score, then what happened. */
export function line(risk: RiskSummary | undefined, summary: string): string {
  const s = score(risk);
  return s ? `${s}  ${summary}` : summary;
}

/**
 * The block shown when the developer is asked to decide.
 *
 * The top factors are listed because "why 87" is the question a developer
 * actually has, and an unexplained number is the black box Article V forbids.
 */
export function block(risk: RiskSummary | undefined, why: string, guidance: string): string {
  const parts = [`${score(risk)}  ${band(risk)}`, '', why];

  const factors = risk?.topFactors ?? [];
  if (factors.length > 0) {
    parts.push('');
    for (const f of factors) {
      parts.push(`  +${String(f.contribution).padEnd(3)} ${f.description}`);
    }
  }

  parts.push('', guidance);
  return parts.join('\n');
}
