/**
 * Claude Code hook event -> normalized Signal.
 *
 * This file is the entire platform-specific part of the adapter. It maps and
 * enriches; it never judges. There is no risk table here, no allowlist, and no
 * security-relevant conditional — every `if` below answers "what is this",
 * never "how bad is this" (Article III).
 *
 * Verified against the hook reference at https://code.claude.com/docs/en/hooks.
 */

import {
  Kind,
  Phase,
  Scope,
  Supervision,
  TargetType,
  Transfer,
  type Signal,
} from '@airuntimeguard/adapter-shared';

/**
 * The subset of Claude Code's hook payload this adapter reads.
 *
 * Field names are the host's, verbatim, and were confirmed against payloads
 * captured from a live session — not from the reference alone, which differs
 * from reality in two places (see `exit_reason` and `tool_response` below).
 *
 * `tool_response` is deliberately absent from this interface. A captured
 * PostToolUse for `Read` carries the file's entire content inside it, and a
 * `Bash` one carries full stdout. Never naming the field is the cheapest way to
 * guarantee it cannot reach the wire (Article IX).
 */
export interface HookEvent {
  session_id?: string;
  hook_event_name?: string;
  cwd?: string;

  /** Stable per-call id. Used as the signal id, so no counter is needed. */
  tool_use_id?: string;
  tool_name?: string;
  tool_input?: Record<string, unknown>;

  /** default | plan | acceptEdits | auto | dontAsk | bypassPermissions */
  permission_mode?: string;

  /** Present only for subagent calls. */
  agent_id?: string;
  agent_type?: string;

  /** UserPromptSubmit */
  prompt?: string;
  prompt_id?: string;

  /**
   * SessionEnd.
   *
   * The published reference calls this `exit_reason`; a live session sends
   * `reason`. Both are read, because being wrong here means capabilities never
   * reset and every session on the machine stays permanently marked as one that
   * once touched a secret.
   */
  reason?: string;
  exit_reason?: string;
  /** SessionStart */
  source?: string;
}

/**
 * Permission modes in which nobody will answer a prompt.
 *
 * `bypassPermissions` and `dontAsk` are the developer explicitly saying "stop
 * asking me"; `auto` hands decisions to the classifier. Asking in any of these
 * produces an interruption that decides nothing, so the Brain is told and
 * decides what to do about it.
 */
const UNATTENDED_MODES = new Set(['bypassPermissions', 'dontAsk', 'auto']);

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
 * shape and count and the secret itself never leaves this process.
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

/**
 * Shell commands that read file contents.
 *
 * The shell is a second, parallel filesystem API. `cat /repo/.env` is the same
 * act as the Read tool performing it, and a guard that only watches the tool
 * sees a session that never touched a secret — while the agent holds it.
 */
const READING_COMMANDS =
  /\b(cat|head|tail|less|more|strings|xxd|od|base64|awk|sed|grep|rg|cp|mv|tar|zip)\b/;

/**
 * Locations whose contents execute later, without anyone invoking them.
 *
 * A write here outlives the session: it is the difference between an agent
 * doing something now and an agent arranging for something to happen every time
 * the developer commits, opens a shell, or logs in.
 */
const PERSISTENCE_PATHS = [
  /\/\.git\/hooks\//,
  /\/\.(bash|zsh)rc$/,
  /\/\.(bash_profile|zprofile|profile)$/,
  /\/\.config\/systemd\//,
  /\/Library\/LaunchAgents\//,
  /\/etc\/cron/,
  /\/\.claude\/settings/,
  /\/\.vscode\/tasks\.json$/,
];

/** Git subcommands, so the Brain can decide which ones mutate. */
const GIT_OPS = ['push', 'commit', 'remote-add', 'remote-set', 'tag', 'reset-hard', 'force-push'];

/**
 * Hook events that carry no tool call but do mark session lifecycle.
 *
 * SESSION_END matters more than it looks: capabilities latch for the life of a
 * session and reset only here. Without it, a machine accumulates sessions that
 * are permanently "the session that once read a secret".
 */
const LIFECYCLE_KINDS: Record<string, Kind> = {
  SessionStart: Kind.SessionStart,
  SessionEnd: Kind.SessionEnd,
  UserPromptSubmit: Kind.Prompt,
};

/**
 * Hook events that describe something that already happened.
 *
 * Session lifecycle belongs here: a session ending cannot be prevented, and
 * marking it PRE made the Brain issue an ASK asking permission for something
 * that had already occurred. UserPromptSubmit stays PRE because the host really
 * can block a prompt before the model sees it.
 */
const POST_EVENTS = new Set([
  'PostToolUse',
  'PostToolUseFailure',
  'SessionStart',
  'SessionEnd',
]);

export interface MapOptions {
  cwd: string;
  home: string;
  now: Date;
  /** Fallback signal id for events with no tool_use_id. */
  fallbackId: string;
}

/**
 * Converts a hook event into a Signal, or null if the event carries nothing
 * worth observing.
 */
export function toSignal(event: HookEvent, opts: MapOptions): Signal | null {
  const tool = event.tool_name ?? '';
  const input = event.tool_input ?? {};
  const hookName = event.hook_event_name ?? '';

  const base = {
    // The host already assigns a unique id per tool call. Using it means no
    // counter, no state file, and an id that survives a crashed hook process.
    id: event.tool_use_id ?? event.prompt_id ?? opts.fallbackId,
    sessionId: event.session_id ?? '',
    agent: 'claude-code',
    seq: 0,
    observedAt: opts.now.toISOString(),
    phase: POST_EVENTS.has(hookName) ? Phase.Post : Phase.Pre,
    supervision: supervisionOf(event.permission_mode),
    actor: { name: tool || hookName },
    attributes: {} as Record<string, unknown>,
  };

  // A subagent acts inside the same session. Recording which one keeps a
  // report readable when several run in parallel.
  if (event.agent_type) {
    base.attributes['agent_type'] = event.agent_type;
  }
  // A failed tool call did not necessarily do what it asked to do. The Brain
  // decides what that means; the adapter only reports it.
  if (hookName === 'PostToolUseFailure') {
    base.attributes['failed'] = true;
  }

  const lifecycle = LIFECYCLE_KINDS[hookName];
  if (lifecycle !== undefined) {
    return {
      ...base,
      kind: lifecycle,
      target: { type: TargetType.None, value: '', scope: Scope.Unknown },
      attributes: {
        ...base.attributes,
        // The prompt text itself is never forwarded — only its size, which is
        // enough to notice an unusually large injected instruction.
        ...(event.prompt ? { prompt_length: event.prompt.length } : {}),
        ...(exitReason(event) ? { exit_reason: exitReason(event) } : {}),
        ...(event.source ? { source: event.source } : {}),
      },
    };
  }

  const kind = TOOL_KINDS[tool] ?? Kind.ToolCall;

  // Bash is the interesting case: one tool that can be a shell command, a git
  // mutation, or a network call. Classifying which is descriptive work only the
  // adapter can do, because only it sees the raw command line.
  if (kind === Kind.ShellExec) {
    const command = String(input['command'] ?? '');
    const host = outboundHost(command);
    const gitOp = gitOperation(command);

    // A shell command can read a secret just as well as the Read tool can.
    // Report the shape here too, or `cat .env` looks like an ordinary command
    // and the session appears never to have touched a secret.
    const shellSecret = shellSecretShape(command);
    if (shellSecret) {
      base.attributes['secret_shape'] = shellSecret;
      base.attributes['secret_count'] = 1;
    }

    if (host) {
      return {
        ...base,
        kind: Kind.Network,
        target: { type: TargetType.Host, value: host, scope: Scope.External },
        transfer: transferDirection(command),
        attributes: {
          ...base.attributes,
          command_shape: isRemoteShell(command) ? 'remote_shell' : 'network',
        },
      };
    }
    if (gitOp) {
      return {
        ...base,
        kind: Kind.Git,
        target: { type: TargetType.Repo, value: 'local', scope: Scope.Repo },
        attributes: { ...base.attributes, git_op: gitOp },
      };
    }
    return {
      ...base,
      kind,
      target: { type: TargetType.Command, value: command, scope: Scope.System },
    };
  }

  if (kind === Kind.ContextIngest) {
    const url = String(input['url'] ?? input['query'] ?? '');
    return {
      ...base,
      kind,
      target: { type: TargetType.Resource, value: url, scope: Scope.External },
    };
  }

  const path = String(input['file_path'] ?? input['path'] ?? input['pattern'] ?? '');
  if (!path) {
    // A tool we do not model. Report it as an opaque tool call rather than
    // dropping it: the Brain counts signals, and silently discarding some would
    // understate every session.
    return {
      ...base,
      kind,
      target: { type: TargetType.None, value: '', scope: Scope.Unknown },
    };
  }

  const shape = secretShape(path);
  const attributes = { ...base.attributes };
  if (shape) {
    attributes['secret_shape'] = shape;
    attributes['secret_count'] = 1;
  }
  if (kind === Kind.FileWrite && isPersistencePath(path)) {
    attributes['write_shape'] = 'persistence';
  }

  return {
    ...base,
    kind,
    target: { type: TargetType.Path, value: path, scope: scopeOf(path, opts) },
    attributes,
  };
}

/**
 * Which way data moves in a network command.
 *
 * Reaching a host and sending data to it are different facts. `npm install`
 * after reading a config file is the single most common benign sequence an
 * agent produces; scoring it like an upload is how a guard teaches people to
 * ignore it.
 *
 * Only explicit data-carrying syntax counts. Anything ambiguous stays Unknown,
 * which does not latch egress — guessing here would reintroduce the false
 * positive this function exists to remove.
 *
 * ponytail: flag matching, not a shell parser. Remaining blind spot, which fails
 * toward Unknown rather than toward a wrong answer: a bare pipe into a network
 * tool is caught by the tool's own flags, not by the pipe. Close it when a
 * recorded session shows one, not before.
 */
export function transferDirection(command: string): Transfer {
  // A shell opening /dev/tcp is a two-way channel; treat it as a send, because
  // that is the half that matters.
  if (/\/dev\/(?:tcp|udp)\//.test(command)) return Transfer.Egress;

  // Command substitution inside a URL means computed data is being placed in
  // the request itself. There is no body to inspect, which is exactly the
  // point of the technique.
  if (/https?:\/\/[^\s'"]*(\$\(|`|\$\{)/.test(command)) return Transfer.Egress;

  // A long opaque query value is a payload wearing a URL.
  const query = command.match(/https?:\/\/[^\s'"]*\?([^\s'"]+)/i);
  if (query?.[1] && query[1].split('&').some((kv) => (kv.split('=')[1] ?? '').length > 64)) {
    return Transfer.Egress;
  }

  // curl: any flag that attaches a body or a file.
  if (/\bcurl\b/.test(command)) {
    if (/(^|\s)(-d|-F|-T|--data|--data-raw|--data-binary|--data-urlencode|--form|--upload-file|--json)(\s|=)/.test(command)) {
      return Transfer.Egress;
    }
    return Transfer.Inbound;
  }

  // wget is a downloader unless explicitly told to post a body.
  if (/\bwget\b/.test(command)) {
    return /--(post-data|post-file|body-data|body-file)/.test(command)
      ? Transfer.Egress
      : Transfer.Inbound;
  }

  // scp and rsync take direction from argument order: the remote target is the
  // one with a colon, and whether it comes last decides which way data flows.
  if (/\b(scp|rsync)\b/.test(command)) {
    const args = command.split(/\s+/).filter((a) => !a.startsWith('-')).slice(1);
    const last = args[args.length - 1] ?? '';
    const firstIsRemote = args.slice(0, -1).some((a) => /:/.test(a));
    if (/:/.test(last)) return Transfer.Egress;
    if (firstIsRemote) return Transfer.Inbound;
    return Transfer.Unknown;
  }

  // A redirect into netcat is a send.
  if (/\b(nc|ncat)\b/.test(command)) {
    return /<\s*\S/.test(command) ? Transfer.Egress : Transfer.Unknown;
  }

  return Transfer.Unknown;
}

/** Reads whichever spelling of the session-end reason the host sent. */
function exitReason(event: HookEvent): string | undefined {
  return event.reason ?? event.exit_reason;
}

/**
 * Normalizes the host's permission mode into "can a human answer right now".
 *
 * The mapping lives here rather than in the Brain: `bypassPermissions` is a
 * Claude Code word, and the Brain must not learn platform vocabulary
 * (Article IV).
 */
export function supervisionOf(mode: string | undefined): Supervision {
  if (!mode) return Supervision.Unknown;
  return UNATTENDED_MODES.has(mode) ? Supervision.Unattended : Supervision.Supervised;
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

/**
 * The secret shape a shell command touches, if any.
 *
 * Only fires for commands that actually read content: `rm /repo/.env` destroys
 * a secret but does not learn it, and treating the two alike would put a
 * cleanup script in the same bucket as a theft.
 */
export function shellSecretShape(command: string): string | null {
  if (!READING_COMMANDS.test(command)) {
    // `env` and `printenv` read no file but dump every secret the process was
    // handed, which is where credentials increasingly live.
    return /\b(env|printenv)\b/.test(command) ? 'env-file' : null;
  }

  for (const token of command.split(/[\s'"<>|;&]+/)) {
    const shape = secretShape(token);
    if (shape) return shape;
  }
  return null;
}

/**
 * Whether a command wires a shell to a remote socket.
 *
 * This is not data leaving — it is control arriving. Scoring it as egress
 * undersells it: exfiltration loses whatever the session had, a reverse shell
 * loses everything the machine will ever have.
 */
export function isRemoteShell(command: string): boolean {
  if (/\/dev\/(?:tcp|udp)\/[\w.-]+\/\d+/.test(command) && /\b(bash|sh|zsh)\b/.test(command)) {
    return true;
  }
  // nc -e / --exec hands a shell to whoever connects.
  if (/\b(nc|ncat)\b[^\n]*\s(-e|--exec|-c)\s/.test(command)) return true;
  // The classic FIFO round-trip, which uses no flag that looks dangerous.
  if (/mkfifo[^\n]*\|[^\n]*\b(nc|ncat)\b/.test(command)) return true;
  return false;
}

/** Whether a write lands somewhere that executes later. */
export function isPersistencePath(path: string): boolean {
  return PERSISTENCE_PATHS.some((p) => p.test(path));
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
 * Deliberately simple: it recognizes a URL or an explicit remote target. It is
 * not a shell parser and does not try to be — a command it cannot classify
 * stays a plain SHELL_EXEC, which the Brain already treats as a capability.
 *
 * ponytail: regex, not a shell parser. Upgrade when a real evasion case shows
 * up in recorded sessions rather than in imagination.
 */
export function outboundHost(command: string): string | null {
  // Bash's /dev/tcp is a network socket wearing a filename. It is how a reverse
  // shell is opened without any tool that looks like networking.
  const devSocket = command.match(/\/dev\/(?:tcp|udp)\/([\w.-]+)\/\d+/i);
  if (devSocket?.[1]) return devSocket[1].toLowerCase();

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
