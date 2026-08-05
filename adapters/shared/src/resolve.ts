/**
 * Obtaining a human's answer to an ASK or PAUSE.
 *
 * The Brain never waits for a person: its budget is milliseconds and a human
 * takes seconds. `Decide` returns immediately with an `Interaction` attached,
 * and this module gets the answer over whatever channel the platform offers.
 *
 * Choosing a channel is platform-local knowledge, which is exactly why it lives
 * in the adapter. Nothing here decides an outcome — it renders a decision the
 * Brain already made and transports the answer back. See docs/ARCHITECTURE.md
 * §2.1 and docs/RUNTIME_MODEL.md §7.3.
 */

import {
  Action,
  ChannelHint,
  ResolutionSource,
  type Decision,
  type Interaction,
  type Resolution,
} from './types.js';

/**
 * A way to reach the developer. Adapters register the channels their platform
 * actually supports, in preference order.
 */
export interface Channel {
  /** Diagnostic name recorded on the resolution: "native", "tty", "guard". */
  readonly name: string;

  /** Whether this channel can reach a human right now. */
  available(interaction: Interaction): boolean;

  /**
   * Present the decision and return the chosen option id, or null if the
   * developer could not be reached. Implementations must honour
   * `interaction.timeoutMs`; a channel that hangs is worse than one that fails.
   */
  prompt(decision: Decision, interaction: Interaction): Promise<string | null>;
}

/** Reports the resolution back to the Brain. */
export type Reporter = (resolution: Resolution) => Promise<void>;

export interface ResolveResult {
  /** The action to actually apply. */
  action: Action;
  source: ResolutionSource;
  channel: string;
}

/**
 * Resolves a decision that needs a human.
 *
 * Every path ends in a report — human answer, timeout, headless, or a broken
 * channel — so the Brain always learns what really happened instead of silently
 * dropping unanswered prompts.
 */
export async function resolve(
  decision: Decision,
  channels: Channel[],
  report: Reporter,
): Promise<ResolveResult> {
  const interaction = decision.interaction;
  if (!interaction) {
    return { action: decision.action, source: ResolutionSource.Unspecified, channel: '' };
  }

  // PAUSE never prompts inline. The call is refused with the Brain's reason and
  // the pending prompt is served to `guard` from the daemon's queue, so there
  // is nothing to wait for here.
  if (interaction.channelHint === ChannelHint.OutOfBand) {
    return { action: decision.action, source: ResolutionSource.Unspecified, channel: 'guard' };
  }

  const channel = channels.find((c) => c.available(interaction));
  if (!channel) {
    return finish(interaction, interaction.headlessDefault, ResolutionSource.Headless, 'none', report);
  }

  let chosen: string | null;
  try {
    chosen = await withTimeout(channel.prompt(decision, interaction), interaction.timeoutMs);
  } catch {
    // A broken prompt channel is an availability failure, not a security
    // event: fall back to the Brain's default rather than inventing a verdict.
    return finish(interaction, interaction.headlessDefault, ResolutionSource.AdapterFailure, channel.name, report);
  }

  if (chosen === null) {
    return finish(interaction, interaction.headlessDefault, ResolutionSource.Timeout, channel.name, report);
  }

  const option = interaction.options.find((o) => o.id === chosen);
  if (!option) {
    // The channel returned something we never offered. Treat it as no answer:
    // guessing which option was meant would be the adapter deciding.
    return finish(interaction, interaction.headlessDefault, ResolutionSource.AdapterFailure, channel.name, report);
  }

  await report({
    promptId: interaction.promptId,
    optionId: option.id,
    source: ResolutionSource.Human,
    channel: channel.name,
  });
  return { action: option.effect, source: ResolutionSource.Human, channel: channel.name };
}

async function finish(
  interaction: Interaction,
  action: Action,
  source: ResolutionSource,
  channel: string,
  report: Reporter,
): Promise<ResolveResult> {
  // Reporting is best-effort: if the daemon died between Decide and Resolve we
  // still have to return a verdict to the agent. Losing the audit record is bad;
  // hanging the developer's agent over it is worse.
  await report({ promptId: interaction.promptId, optionId: '', source, channel }).catch(() => {});
  return { action, source, channel };
}

/** Resolves to null on timeout rather than rejecting — a timeout is an expected outcome. */
function withTimeout<T>(p: Promise<T>, ms: number): Promise<T | null> {
  if (!(ms > 0)) return p;
  return new Promise((resolveP, rejectP) => {
    const timer = setTimeout(() => resolveP(null), ms);
    p.then(
      (v) => {
        clearTimeout(timer);
        resolveP(v);
      },
      (e) => {
        clearTimeout(timer);
        rejectP(e);
      },
    );
  });
}
