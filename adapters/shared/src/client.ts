/**
 * Transport to the Runtime Brain.
 *
 * gRPC over a Unix domain socket: local-first means no listening port, so
 * filesystem permissions are the access control (docs/ARCHITECTURE.md §3).
 *
 * The .proto is loaded at runtime rather than code-generated. One less build
 * step for every adapter, and the proto stays the single source of truth on
 * both sides of the wire.
 */

import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import { join, resolve as resolvePath } from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  Action,
  ResolutionSource,
  type Decision,
  type Resolution,
  type Signal,
} from './types.js';

const require = createRequire(import.meta.url);

/** Where the daemon listens. Mirrors server.SocketPath() in the Go core. */
export function socketPath(): string {
  if (process.env['GUARD_SOCKET']) return process.env['GUARD_SOCKET'];
  return join(runtimeDir(), 'guard.sock');
}

export function runtimeDir(): string {
  if (process.env['GUARD_HOME']) return process.env['GUARD_HOME'];
  if (process.env['XDG_STATE_HOME']) {
    return join(process.env['XDG_STATE_HOME'], 'ai-runtime-guard');
  }
  return join(homedir(), '.local', 'state', 'ai-runtime-guard');
}

function protoPath(): string {
  if (process.env['GUARD_PROTO']) return process.env['GUARD_PROTO'];
  const here = fileURLToPath(new URL('.', import.meta.url));
  return resolvePath(here, '../../../proto/runtime/v1/runtime.proto');
}

export interface ClientOptions {
  /**
   * Deadline for a PRE decision. At the ceiling the client stops waiting and
   * fails open: a dead guard must not brick a developer's agent (Article VII).
   */
  timeoutMs?: number;
  socket?: string;
}

export class RuntimeClient {
  private stub: any;
  private readonly timeoutMs: number;

  constructor(opts: ClientOptions = {}) {
    this.timeoutMs = opts.timeoutMs ?? 50;

    const grpc = require('@grpc/grpc-js');
    const loader = require('@grpc/proto-loader');

    const def = loader.loadSync(protoPath(), {
      keepCase: false,
      longs: Number,
      enums: Number,
      defaults: true,
      oneofs: true,
      includeDirs: [resolvePath(fileURLToPath(new URL('.', import.meta.url)), '../../../proto')],
    });

    const pkg = grpc.loadPackageDefinition(def) as any;
    this.stub = new pkg.runtime.v1.Runtime(
      `unix://${opts.socket ?? socketPath()}`,
      grpc.credentials.createInsecure(),
    );
  }

  /**
   * Asks the Brain for a verdict.
   *
   * Returns null when the Brain cannot be reached in time. The caller must
   * treat null as fail-open: an availability failure is not a security event.
   */
  async decide(signal: Signal): Promise<Decision | null> {
    return new Promise((resolveP) => {
      const deadline = new Date(Date.now() + this.timeoutMs);

      this.stub.Decide(
        { signal: toWire(signal), apiVersion: 'runtime.v1' },
        { deadline },
        (err: unknown, decision: Decision) => resolveP(err ? null : decision),
      );
    });
  }

  /** Reports a human's answer, or the reason there wasn't one. */
  async resolve(resolution: Resolution): Promise<Action | null> {
    return new Promise((resolveP) => {
      // A resolution may follow a human taking their time, so it is not bound
      // by the decision budget — but it must still not hang forever.
      const deadline = new Date(Date.now() + 5000);

      this.stub.Resolve(
        {
          promptId: resolution.promptId,
          optionId: resolution.optionId,
          source: resolution.source,
          channel: resolution.channel,
        },
        { deadline },
        (err: unknown, ack: { applied: Action }) => resolveP(err ? null : ack.applied),
      );
    });
  }

  close(): void {
    this.stub?.close?.();
  }
}

/** Lifts typed fields back into the attributes map the proto carries. */
function toWire(s: Signal): Record<string, unknown> {
  return {
    id: s.id,
    sessionId: s.sessionId,
    agent: s.agent,
    seq: s.seq,
    observedAt: { seconds: Math.floor(Date.parse(s.observedAt) / 1000), nanos: 0 },
    phase: s.phase,
    kind: s.kind,
    actor: { type: 1, name: s.actor.name },
    target: { type: s.target.type, value: s.target.value, scope: s.target.scope },
    // supervision rides in attributes so the proto stays open to new lifecycle
    // facts without a schema bump; the server lifts it into a typed field.
    attributes: {
      fields: toStruct({
        ...s.attributes,
        supervision: s.supervision,
        transfer: s.transfer ?? 0,
      }),
    },
  };
}

function toStruct(attrs: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(attrs ?? {})) {
    if (typeof v === 'string') out[k] = { stringValue: v };
    else if (typeof v === 'number') out[k] = { numberValue: v };
    else if (typeof v === 'boolean') out[k] = { boolValue: v };
  }
  return out;
}

/** The resolution to report when the Brain was never reachable. */
export function headlessResolution(promptId: string): Resolution {
  return {
    promptId,
    optionId: '',
    source: ResolutionSource.Headless,
    channel: 'none',
  };
}
