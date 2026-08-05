/**
 * How this platform reaches a human.
 *
 * Channels render a decision the Brain already made and carry the answer back.
 * They never choose an outcome — an option a channel returns is one the Brain
 * offered, and anything else is treated as no answer at all.
 */

import { openSync, readSync, closeSync } from 'node:fs';

import type { Channel, Decision, Interaction } from '@airuntimeguard/adapter-shared';

/**
 * A direct prompt on the controlling terminal.
 *
 * Used only where the host offers no permission UI of its own. Raw output
 * fighting an agent's TUI is worse than it sounds, so this is the fallback, not
 * the default — see `index.ts` for why the native path does not appear here.
 */
export function ttyChannel(): Channel {
  return {
    name: 'tty',

    available(): boolean {
      try {
        const fd = openSync('/dev/tty', 'r');
        closeSync(fd);
        return true;
      } catch {
        return false;
      }
    },

    async prompt(decision: Decision, interaction: Interaction): Promise<string | null> {
      const e = decision.explanation;

      const lines = [
        '',
        `  AI Runtime Guard — ${e.summary}`,
        `  ${e.why}`,
        `  ${e.guidance}`,
        '',
        ...interaction.options.map((o, i) => `    ${i + 1}) ${o.label}`),
        '',
        '  choose: ',
      ];
      process.stderr.write(lines.join('\n'));

      const answer = readLineFromTTY();
      if (answer === null) return null;

      const index = Number(answer.trim()) - 1;
      const option = interaction.options[index];
      return option ? option.id : null;
    },
  };
}

/**
 * Reads one line from the controlling terminal.
 *
 * Synchronous and deliberately small: the hook process exists only to answer
 * this question, and pulling in a readline stack to read a single digit would
 * be more moving parts than the job needs.
 */
function readLineFromTTY(): string | null {
  let fd: number | null = null;
  try {
    fd = openSync('/dev/tty', 'r');
    const buf = Buffer.alloc(64);
    const read = readSync(fd, buf, 0, buf.length, null);
    return buf.subarray(0, read).toString('utf8');
  } catch {
    return null;
  } finally {
    if (fd !== null) {
      try {
        closeSync(fd);
      } catch {
        /* the prompt already succeeded or already failed */
      }
    }
  }
}
