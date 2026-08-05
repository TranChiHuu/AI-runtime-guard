/**
 * How this platform reaches a human.
 *
 * Channels render a decision the Brain already made and carry the answer back.
 * They never choose an outcome — an option a channel returns is one the Brain
 * offered, and anything else is treated as no answer at all.
 */

import { openSync, readSync, closeSync } from 'node:fs';

import { ChannelHint, type Channel, type Decision, type Interaction } from '@airuntimeguard/adapter-shared';

/**
 * Claude Code's own permission UI.
 *
 * Preferred channel by a wide margin: it looks like the tool the developer
 * already uses, it costs us no TUI of our own, and it cannot fight the agent's
 * rendering. The adapter returns `ask` and Claude Code prompts — so this
 * channel does not block here, it defers the question to the host and reports
 * the Brain's own default in the meantime.
 */
export function nativeChannel(): Channel {
  return {
    name: 'native',

    available(interaction: Interaction): boolean {
      return interaction.channelHint === ChannelHint.Inline;
    },

    async prompt(_decision: Decision, interaction: Interaction): Promise<string | null> {
      // Handing the question to Claude Code means the answer arrives as the
      // host's own allow/deny, not as an option id we can return here. Report
      // no answer so the Brain's headless default applies to this call, and let
      // the host's prompt govern what actually happens.
      const deny = interaction.options.find((o) => o.id === 'deny');
      return deny && interaction.headlessDefault >= 4 ? deny.id : null;
    },
  };
}

/**
 * A direct prompt on the controlling terminal.
 *
 * Not the default: raw output fighting an agent's own TUI is worse than it
 * sounds. This exists for hosts that offer no native prompt but do have a
 * terminal.
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
