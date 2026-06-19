import type { RuntimeEnvelope } from "./index";

export type OfflineConflictResolution = "server-wins" | "client-wins" | "manual";

// Default metadata.extra keys that carry the bearer/session credential. These
// are stripped on enqueue so a token captured before a reconnect/rotation can
// never be replayed stale, and re-stamped fresh on drain.
export const DEFAULT_AUTH_TOKEN_EXTRA_KEYS = ["auth_token", "authToken"] as const;

export type OfflineQueueOptions = {
  maxQueueSize?: number;
  conflictResolution?: OfflineConflictResolution;
  /**
   * metadata.extra keys treated as the auth credential. Stripped on enqueue and
   * (the first key) re-stamped on drain. Defaults to DEFAULT_AUTH_TOKEN_EXTRA_KEYS.
   */
  authTokenExtraKeys?: readonly string[];
  /**
   * Resolves the CURRENT auth token at drain time. Called once per drained
   * entry so replays after a token rotation carry a valid credential instead of
   * the one captured when the request was first queued. Return an empty/falsy
   * value to leave the credential absent (the gateway can authorize an
   * already-authenticated connection without a per-frame token).
   */
  resolveAuthToken?: () => string | null | undefined;
};

export type OfflineQueueSnapshot = {
  size: number;
  capacity: number;
  attempts: number;
  oldestQueuedAt: string | null;
};

export type OfflineQueueEntry<TPayload = unknown> = {
  envelope: RuntimeEnvelope<TPayload>;
  queuedAt: string;
  attempts: number;
};

const cloneEnvelopeWithExtra = <TPayload>(
  envelope: RuntimeEnvelope<TPayload>,
  nextExtra: Record<string, unknown>
): RuntimeEnvelope<TPayload> => ({
  ...envelope,
  // Clone only the metadata + extra bag. Payload is left by reference because it
  // may be a non-cloneable binary buffer (Uint8Array).
  metadata: { ...envelope.metadata, extra: nextExtra },
});

export const createOfflineQueue = (options: OfflineQueueOptions = {}) => {
  const maxQueueSize = Math.max(1, options.maxQueueSize ?? 100);
  const authTokenKeys =
    options.authTokenExtraKeys && options.authTokenExtraKeys.length > 0
      ? options.authTokenExtraKeys
      : DEFAULT_AUTH_TOKEN_EXTRA_KEYS;
  const entries: OfflineQueueEntry[] = [];

  const stripAuth = (extra: Record<string, unknown> | undefined): Record<string, unknown> => {
    const next: Record<string, unknown> = { ...(extra ?? {}) };
    for (const key of authTokenKeys) {
      delete next[key];
    }
    return next;
  };

  const restampAuth = (extra: Record<string, unknown>): Record<string, unknown> => {
    if (!options.resolveAuthToken) {
      return extra;
    }
    const token = options.resolveAuthToken();
    if (!token) {
      return extra;
    }
    return { ...extra, [authTokenKeys[0]]: token };
  };

  return {
    enqueue<TPayload>(envelope: RuntimeEnvelope<TPayload>) {
      if (entries.length >= maxQueueSize) {
        throw new Error(`offline queue capacity exceeded: ${entries.length}/${maxQueueSize}`);
      }
      // Persist the request WITHOUT its (soon-to-be-stale) credential.
      const stored = cloneEnvelopeWithExtra(envelope, stripAuth(envelope.metadata?.extra));
      entries.push({ envelope: stored as RuntimeEnvelope, queuedAt: new Date().toISOString(), attempts: 0 });
    },
    drain(): OfflineQueueEntry[] {
      const drained = entries.splice(0, entries.length);
      // Re-stamp each replayed request with the current credential.
      return drained.map((entry) => ({
        ...entry,
        envelope: cloneEnvelopeWithExtra(entry.envelope, restampAuth(entry.envelope.metadata?.extra ?? {})),
      }));
    },
    snapshot(): OfflineQueueSnapshot {
      return {
        size: entries.length,
        capacity: maxQueueSize,
        attempts: entries.reduce((total, entry) => total + entry.attempts, 0),
        oldestQueuedAt: entries[0]?.queuedAt ?? null,
      };
    },
    size(): number {
      return entries.length;
    },
    conflictResolution(): OfflineConflictResolution {
      return options.conflictResolution ?? "server-wins";
    },
  };
};
