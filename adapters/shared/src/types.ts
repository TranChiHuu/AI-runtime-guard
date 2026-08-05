/**
 * Wire vocabulary shared by every adapter.
 *
 * Hand-written to mirror `proto/runtime/v1/runtime.proto` until the generated
 * client lands; the proto stays the source of truth and this file must follow
 * it, never the reverse.
 */

export enum Phase {
  Unspecified = 0,
  /** The action has not happened yet, so it can still be intervened on. */
  Pre = 1,
  /** Observation only — nothing can be blocked retroactively. */
  Post = 2,
}

export enum Kind {
  Unspecified = 0,
  Prompt = 1,
  ToolCall = 2,
  ToolResult = 3,
  FileRead = 4,
  FileWrite = 5,
  ShellExec = 6,
  Network = 7,
  Git = 8,
  MCP = 9,
  ContextIngest = 10,
  SessionStart = 11,
  SessionEnd = 12,
}

export enum TargetType {
  Unspecified = 0,
  None = 1,
  Path = 2,
  Command = 3,
  Host = 4,
  Repo = 5,
  Resource = 6,
}

/**
 * Where a target lives. Computed by the adapter from workspace-local knowledge,
 * because only the adapter knows where the workspace root is. Descriptive,
 * never a judgment.
 */
export enum Scope {
  Unknown = '',
  Repo = 'repo',
  Home = 'home',
  System = 'system',
  External = 'external',
}

/**
 * Whether a human is positioned to answer a question right now.
 *
 * Every platform has some notion of "the user turned off prompting". The
 * concept is normalized so the Brain never learns platform vocabulary.
 */
export enum Supervision {
  /** The adapter did not say. Treated as supervised. */
  Unknown = 0,
  Supervised = 1,
  Unattended = 2,
}

export interface Target {
  type: TargetType;
  value: string;
  scope: Scope;
}

export interface Actor {
  name: string;
}

/** One normalized observation — the only thing adapters produce. */
export interface Signal {
  id: string;
  sessionId: string;
  agent: string;
  seq: number;
  observedAt: string;
  phase: Phase;
  kind: Kind;
  actor: Actor;
  target: Target;
  /** Whether a human could answer a prompt about this signal. */
  supervision: Supervision;
  /**
   * Kind-specific. Secret values must never appear here; use
   * `secret_shape` / `secret_count` instead.
   */
  attributes: Record<string, unknown>;
}

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
