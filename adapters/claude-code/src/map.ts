/**
 * Claude Code hook event -> normalized Signal.
 *
 * This file is the entire platform-specific part of the adapter. It maps and
 * enriches; it never judges. There is no risk table here, no allowlist, and no
 * security-relevant conditional — every `if` below answers "what is this",
 * never "how bad is this" (Article III).
 */

import { Kind, Phase, Scope, TargetType, type Signal } from '@airuntimeguard/adapter-shared';

/** The subset of Claude Code's hook payload this adapter reads. */
export interface HookEvent {
  session_id?: string;
  hook_event_name?: string;
  tool_name?: string;
  tool_input?: Record<string, unknown>;
  cwd?: string;
}

/** Tool name -> what kind of thing it does. Naming, not judgment. */
const TOOL_KINDS: Record<string, Kind> = {
  Read: Kind.FileRead,
  Write: Kind.FileWrite,
  Edit: Kind.FileWrite,
  NotebookEdit: Kind.FileWrite,
  Bash: Kind.ShellExec,
  WebFetch: Kind.ContextIngest,
  WebSearch: Kind.ContextIngest,
  Glob: Kind.FileRead,
  Grep: Kind.FileRead,
};

/**
 * Filename patterns that indicate secret-shaped content.
 *
 * The adapter reports the *shape*, never the value: the Brain reasons about
 * shape and count and the secret itself never leaves this process (Article IX).
 */
const SECRET_SHAPES: Array<[RegExp, string]> = [
  [/(^|\/)\.env(\.|$)/, 'env-file'],
  [/(^|\/)\.ssh\//, 'private-key'],
  [/(^|\/)id_(rsa|ed25519|ecdsa)$/, 'private-key'],
  [/\.pem$/, 'private-key'],
  [/(^|\/)\.aws\/credentials$/, 'credential-store'],
  [/(^|\/)\.netrc$/, 'credential-store'],
  [/(^|\/)\.npmrc$/, 'token'],
  [/(^|\/)\.git-credentials$/, 'credential-store'],
];

/** Git subcommands, so the Brain can decide which ones mutate. */
const GIT_OPS = [
  'push',
  'commit',
  'remote-add',
  'remote-set',
  'tag',
  'reset-hard',
  'force-push',
];

export interface MapOptions {
  /** Workspace root, used to compute Target.scope. */
  cwd: string;
  home: string;
  seq: number;
  now: Date;
}

/**
 * Converts a hook event into a Signal, or null if the event carries nothing
 * worth observing.
 */
export function toSignal(event: HookEvent, opts: MapOptions): Signal | null {
  const tool = event.tool_name ?? '';
  const input = event.tool_input ?? {};

  const kind = TOOL_KINDS[tool] ?? Kind.ToolCall;
  const phase = event.hook_event_name === 'PostToolUse' ? Phase.Post : Phase.Pre;

  const base = {
    id: `${event.session_id ?? 'unknown'}-${opts.seq}`,
    sessionId: event.session_id ?? '',
    agent: 'claude-code',
    seq: opts.seq,
    observedAt: opts.now.toISOString(),
    phase,
    kind,
    actor: { name: tool },
    attributes: {} as Record<string, unknown>,
  };

  // Bash is the interesting case: one tool that can be a shell command, a git
  // mutation, or a network call. Classifying which is descriptive work only the
  // adapter can do, because only it sees the raw command line.
  if (kind === Kind.ShellExec) {
    const command = String(input['command'] ?? '');
    const host = outboundHost(command);
    const gitOp = gitOperation(command);

    if (host) {
      return {
        ...base,
        kind: Kind.Network,
        target: { type: TargetType.Host, value: host, scope: Scope.External },
        attributes: { command_shape: 'network' },
      };
    }
    if (gitOp) {
      return {
        ...base,
        kind: Kind.Git,
        target: { type: TargetType.Repo, value: 'local', scope: Scope.Repo },
        attributes: { git_op: gitOp },
      };
    }
    return {
      ...base,
      target: { type: TargetType.Command, value: command, scope: Scope.System },
    };
  }

  if (kind === Kind.ContextIngest) {
    const url = String(input['url'] ?? input['query'] ?? '');
    return {
      ...base,
      target: { type: TargetType.Resource, value: url, scope: Scope.External },
    };
  }

  const path = String(input['file_path'] ?? input['path'] ?? input['pattern'] ?? '');
  if (!path) {
    // A tool we do not model. Report it as an opaque tool call rather than
    // dropping it: the Brain counts signals, and silently discarding some would
    // understate every session.
    return { ...base, target: { type: TargetType.None, value: '', scope: Scope.Unknown } };
  }

  const shape = secretShape(path);
  return {
    ...base,
    target: { type: TargetType.Path, value: path, scope: scopeOf(path, opts) },
    attributes: shape ? { secret_shape: shape, secret_count: 1 } : {},
  };
}

/**
 * Where a path lives relative to the workspace. Descriptive, never a judgment:
 * the adapter says where something is, the Brain decides what that means.
 */
export function scopeOf(path: string, opts: Pick<MapOptions, 'cwd' | 'home'>): Scope {
  if (!path.startsWith('/')) return Scope.Repo; // relative paths are workspace-relative
  if (path.startsWith(opts.cwd)) return Scope.Repo;
  if (path.startsWith(opts.home)) return Scope.Home;
  return Scope.System;
}

export function secretShape(path: string): string | null {
  for (const [pattern, shape] of SECRET_SHAPES) {
    if (pattern.test(path)) return shape;
  }
  return null;
}

/**
 * Extracts an outbound host from a shell command.
 *
 * Deliberately simple: it recognizes a URL or an explicit host argument. It is
 * not a shell parser and does not try to be — a command it cannot classify
 * stays a plain SHELL_EXEC, which the Brain already treats as a capability.
 *
 * ponytail: regex, not a shell parser. Upgrade when a real evasion case shows
 * up in recorded sessions rather than in imagination.
 */
export function outboundHost(command: string): string | null {
  const url = command.match(/https?:\/\/([^\s/'"|)]+)/i);
  if (url?.[1]) return url[1].toLowerCase();

  // Transfer tools take local paths and remote targets on the same line, and a
  // filename like `dump.tar` is indistinguishable from a hostname by shape
  // alone. Anchor on the syntax that only a remote target has — `user@host` or
  // `host:path` — rather than guessing from the dot.
  const userAtHost = command.match(/[\w.-]+@([\w.-]+\.[a-z]{2,})(?::|\s|$)/i);
  if (userAtHost?.[1]) return userAtHost[1].toLowerCase();

  const hostColonPath = command.match(/(?:^|\s)([\w.-]+\.[a-z]{2,}):\S*/i);
  if (hostColonPath?.[1]) return hostColonPath[1].toLowerCase();

  // curl/wget/nc/ssh take a single destination, so a bare hostname argument is
  // unambiguous here in a way it is not for scp and rsync.
  const single = command.match(/\b(?:curl|wget|nc|ncat|ssh)\s+(?:-\S+\s+)*([\w.-]+\.[a-z]{2,})/i);
  if (single?.[1]) return single[1].toLowerCase();

  return null;
}

/** Names the git subcommand, if this is one. The Brain decides which mutate. */
export function gitOperation(command: string): string | null {
  if (!/\bgit\b/.test(command)) return null;

  if (/\bpush\b.*(--force|-f\b)/.test(command)) return 'force-push';
  if (/\breset\b.*--hard/.test(command)) return 'reset-hard';
  if (/\bremote\s+add\b/.test(command)) return 'remote-add';
  if (/\bremote\s+set-url\b/.test(command)) return 'remote-set';

  for (const op of GIT_OPS) {
    if (new RegExp(`\\b${op}\\b`).test(command)) return op;
  }
  return 'read';
}
