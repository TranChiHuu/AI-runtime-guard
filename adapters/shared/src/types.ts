/**
 * Wire vocabulary shared by every adapter.
 *
 * Hand-written to mirror `proto/runtime/v1/runtime.proto` until the generated
 * client lands; the proto stays the source of truth and this file must follow
 * it, never the reverse.
 */

/**
 * The intervention ladder. Ordered — the numeric values matter, because
 * comparisons are how floors, ceilings, and gates are expressed.
 */
export enum Action {
  Unspecified = 0,
  Allow = 1,
  Notify = 2,
  Ask = 3,
  Pause = 4,
  Block = 5,
}

export enum ChannelHint {
  Unspecified = 0,
  /** ASK: the answer is needed now; the agent's turn blocks. */
  Inline = 1,
  /** PAUSE: resolved out-of-band via `guard`, no deadline. */
  OutOfBand = 2,
}

export enum ResolutionSource {
  Unspecified = 0,
  Human = 1,
  Timeout = 2,
  Headless = 3,
  AdapterFailure = 4,
}

export interface Option {
  id: string;
  label: string;
  effect: Action;
  /** Whether choosing this creates a Preference for the specific shape approved. */
  learns: boolean;
}

export interface Interaction {
  promptId: string;
  channelHint: ChannelHint;
  /**
   * What to do when no human is reachable. A judgment about what is safe when
   * nobody is watching, so the Brain decides it and the adapter only applies
   * it. This is what lets an adapter own the prompt while staying thin.
   */
  headlessDefault: Action;
  timeoutMs: number;
  options: Option[];
}

export interface Explanation {
  /** One line, for prompt channels with tight text budgets. */
  summary: string;
  what: string;
  why: string;
  evidence: string[];
  guidance: string;
}

export interface Decision {
  id: string;
  sessionId: string;
  signalId: string;
  action: Action;
  explanation: Explanation;
  /** Absent unless a human answer is needed. */
  interaction?: Interaction;
}

export interface Resolution {
  promptId: string;
  /** Empty unless source is Human. */
  optionId: string;
  source: ResolutionSource;
  /** Which channel was actually used — diagnostics only. */
  channel: string;
}
