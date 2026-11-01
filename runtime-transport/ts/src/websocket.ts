import {
  createEnvelope,
  eventTerminalState,
  type RuntimeEnvelope,
  type RuntimeRoute,
  type Subscription,
  type TransportStrategy,
} from "./index";
import {
  decodeJSONRuntimeEnvelope,
  encodeJSONRuntimeEnvelope,
  encodeRuntimeEnvelope,
} from "./binaryEnvelope";
import {
  compressRuntimeBytes,
  decodeRuntimeBinaryEnvelope,
  encodeRuntimeBinaryFrame,
  supportedRuntimeCompressionEncodings,
  type RuntimeCompressionOptions,
} from "./compression";

// WebSocket.send accepts only views over an ArrayBuffer, not a SharedArrayBuffer.
// A view over shared memory is copied into a new ArrayBuffer first.
const asWebSocketBinaryPayload = (bytes: Uint8Array): Uint8Array<ArrayBuffer> => {
  if (bytes.buffer instanceof ArrayBuffer) {
    return bytes as Uint8Array<ArrayBuffer>;
  }
  return new Uint8Array(bytes);
};

type WebSocketTransportOptions = {
  url: string;
  preferBinary?: boolean;
  protocols?: string | string[];
  compression?: RuntimeCompressionOptions;
  createSocket?: (url: string, protocols?: string | string[]) => WebSocket;
  onEnvelope?: (envelope: RuntimeEnvelope<unknown>) => void;
  readyWhenEnvelope?: (envelope: RuntimeEnvelope<unknown>) => boolean;
  /**
   * Re-authentication handshake run on every (re)connect, before onReady, the
   * re-subscribe step, and before the connect promise resolves (so the first
   * outgoing frames and any drained offline queue carry freshly established
   * connection auth). Use this to re-assert the current session/context after a
   * socket drop, closing the window where a fresh connection serves frames as
   * unauthenticated. A throw is treated as a readiness failure and triggers
   * reconnect/backoff.
   */
  authenticate?: (session: WebSocketReadySession) => Promise<void>;
  onReady?: (session: WebSocketReadySession) => Promise<void>;
  reconnect?: {
    enabled?: boolean;
    maxAttempts?: number;
    baseDelayMs?: number;
    maxDelayMs?: number;
    /**
     * Randomization factor in [0, 1] applied to exponential reconnect backoff
     * to prevent synchronized thundering-herd reconnect storms.
     * A factor of 1.0 corresponds to full jitter in [0, cap]; default 0.5
     * draws uniformly from [cap * 0.5, cap] (equal jitter).
     */
    jitterFactor?: number;
    shouldReconnect?: (close: { code: number; reason: string; wasClean: boolean }) => boolean;
  };
  health?: {
    connectTimeoutMs?: number;
    readyTimeoutMs?: number;
  };
};

type PendingRequest = {
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
};

export type WebSocketReadySession = {
  socket: WebSocket;
  readyEnvelope: RuntimeEnvelope<unknown> | null;
  dispatch: <TPayload>(envelope: RuntimeEnvelope<TPayload>, signal?: AbortSignal) => Promise<unknown>;
};

export type WebSocketTransport = TransportStrategy & {
  close: (code?: number, reason?: string) => void;
  isConnected: () => boolean;
  getConnectionState: () => "idle" | "connecting" | "open" | "reconnecting" | "closed";
};

export const createWebSocketTransport = (options: WebSocketTransportOptions): WebSocketTransport => {
  const pending = new Map<string, PendingRequest>();
  const preferBinary = options.preferBinary ?? true;
  const reconnect = {
    enabled: options.reconnect?.enabled ?? true,
    maxAttempts: Math.max(0, options.reconnect?.maxAttempts ?? 4),
    baseDelayMs: Math.max(25, options.reconnect?.baseDelayMs ?? 250),
    maxDelayMs: Math.max(100, options.reconnect?.maxDelayMs ?? 4000),
    jitterFactor: Math.min(1, Math.max(0, options.reconnect?.jitterFactor ?? 0.5)),
    shouldReconnect:
      options.reconnect?.shouldReconnect ??
      ((close: { code: number; reason: string; wasClean: boolean }) => close.code !== 1000 && close.code !== 1001),
  };
  const health = {
    connectTimeoutMs: Math.max(250, options.health?.connectTimeoutMs ?? 3000),
    readyTimeoutMs: Math.max(250, options.health?.readyTimeoutMs ?? 3000),
  };
  const createSocket =
    options.createSocket ??
    ((url: string, protocols?: string | string[]) => new WebSocket(url, protocols));

  let socket: WebSocket | null = null;
  let connectPromise: Promise<WebSocket> | null = null;
  // Aborts the in-flight connect. close() calls it because `socket` is null until readiness.
  let abortConnect: ((error: Error) => void) | null = null;
  let reconnectAttempts = 0;
  let reconnectTimer: ReturnType<typeof globalThis.setTimeout> | null = null;
  let manualClose = false;
  let connectionState: "idle" | "connecting" | "open" | "reconnecting" | "closed" = "idle";
  const activePatterns = new Map<string, number>();

  const clearReconnectTimer = () => {
    if (reconnectTimer !== null) {
      globalThis.clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
  };

  const rejectPending = (message: string) => {
    for (const [correlationId, request] of pending.entries()) {
      pending.delete(correlationId);
      request.reject(new Error(message));
    }
  };

  const scheduleReconnect = (close: { code: number; reason: string; wasClean: boolean }) => {
    if (manualClose || !reconnect.enabled || !reconnect.shouldReconnect(close)) {
      connectionState = "closed";
      return;
    }
    if (reconnectTimer !== null) {
      return;
    }
    if (reconnectAttempts >= reconnect.maxAttempts) {
      connectionState = "closed";
      return;
    }

    const exp = Math.min(reconnect.maxDelayMs, reconnect.baseDelayMs * 2 ** reconnectAttempts);
    const spread = exp * reconnect.jitterFactor;
    const delay = exp - spread + Math.random() * spread;
    reconnectAttempts += 1;
    connectionState = "reconnecting";
    reconnectTimer = globalThis.setTimeout(() => {
      reconnectTimer = null;
      void connect().catch(() => undefined);
    }, delay);
  };

  const connect = async (): Promise<WebSocket> => {
    if (socket && socket.readyState === WebSocket.OPEN) {
      return socket;
    }
    if (connectPromise) {
      return connectPromise;
    }
    manualClose = false;
    // The reconnect timer keeps "reconnecting". Any other connect is caller-started and gets a full retry budget.
    if (connectionState !== "reconnecting") {
      reconnectAttempts = 0;
    }
    connectionState = connectionState === "reconnecting" ? "reconnecting" : "connecting";
    const attempt: Promise<WebSocket> = new Promise<WebSocket>((resolve, reject) => {
      const url = withFormat(options.url, preferBinary ? "binary" : "json", options.compression);
      const next = createSocket(url, options.protocols);
      let ready = false;
      let settled = false;
      const readyTimeoutId = globalThis.setTimeout(() => {
        settleReject(new Error("websocket transport readiness timed out"), {
          code: 1006,
          reason: "readiness_timeout",
          wasClean: false,
        });
      }, Math.max(1, options.readyWhenEnvelope ? health.readyTimeoutMs : health.connectTimeoutMs));

      const settleReject = (error: Error, close: { code: number; reason: string; wasClean: boolean }) => {
        if (settled) {
          return;
        }
        settled = true;
        if (abortConnect === abort) {
          abortConnect = null;
        }
        globalThis.clearTimeout(readyTimeoutId);
        reject(error);
        if (next.readyState === WebSocket.CONNECTING || next.readyState === WebSocket.OPEN) {
          try {
            next.close();
          } catch {
            // Ignore close failures; reconnect scheduling is authoritative.
          }
        }
        scheduleReconnect(close);
      };
      const abort = (error: Error) => {
        settleReject(error, { code: 1000, reason: "client_closed", wasClean: true });
      };
      abortConnect = abort;

      next.binaryType = "arraybuffer";
      next.onopen = () => {
        if (options.readyWhenEnvelope) {
          return;
        }
        resolveReady(next, null).catch((error) =>
          settleReject(error instanceof Error ? error : new Error("websocket transport readiness failed"), {
            code: 1006,
            reason: "ready_handler_failed",
            wasClean: false,
          })
        );
      };
      next.onerror = () => {
        settleReject(new Error("websocket transport connection failed"), {
          code: 1006,
          reason: "connection_failed",
          wasClean: false,
        });
      };
      next.onclose = (event) => {
        if (socket === next) {
          socket = null;
        }
        const close = {
          code: event.code,
          reason: event.reason,
          wasClean: event.wasClean,
        };
        rejectPending(close.reason ? `websocket transport closed (${close.code}: ${close.reason})` : `websocket transport closed (${close.code})`);
        if (!settled) {
          settleReject(
            new Error(close.reason ? `websocket transport closed (${close.code}: ${close.reason})` : `websocket transport closed (${close.code})`),
            close
          );
          return;
        }
        scheduleReconnect(close);
      };
      // Decoding is async, and a compressed frame takes longer than a raw envelope. Decode
      // starts at once, but delivery waits for every earlier message, so arrival order holds.
      let delivered: Promise<void> = Promise.resolve();
      next.onmessage = (event) => {
        const decoding = decodeEnvelopeMessage(event.data);
        delivered = delivered
          .then(() => decoding)
          .then((envelope) => handleInbound(envelope))
          .catch(() => undefined);
      };
      const handleInbound = async (envelope: RuntimeEnvelope<unknown> | null) => {
        if (!envelope) {
          return;
        }
        if (!ready && options.readyWhenEnvelope?.(envelope)) {
          try {
            await resolveReady(next, envelope);
          } catch (error) {
            reject(error instanceof Error ? error : new Error("websocket transport readiness failed"));
            next.close();
          }
          return;
        }
        const correlationId = envelope.metadata.correlationId;
        const terminal = eventTerminalState(envelope.eventType);
        if (correlationId && (terminal === "success" || terminal === "failed")) {
          const request = pending.get(correlationId);
          if (request) {
            pending.delete(correlationId);
            if (terminal === "failed") {
              request.reject(new Error(JSON.stringify(envelope.payload)));
              return;
            }
            request.resolve(envelope.payload);
            return;
          }
        }
        if (envelope.eventType.endsWith(":requested")) {
          for (const sub of subscribers) {
            if (sub.pattern === "*" || sub.pattern === envelope.eventType) {
              sub.callback(envelope);
            }
          }
        }

        options.onEnvelope?.(envelope);
      };

      async function resolveReady(activeSocket: WebSocket, readyEnvelope: RuntimeEnvelope<unknown> | null) {
        if (ready || settled) {
          return;
        }
        const session: WebSocketReadySession = {
          socket: activeSocket,
          readyEnvelope,
          dispatch: <TPayload>(envelope: RuntimeEnvelope<TPayload>, signal?: AbortSignal) =>
            dispatchOnSocket(activeSocket, envelope, signal ?? new AbortController().signal),
        };
        // Re-authentication runs first so the connection's session/context is
        // re-established before any app readiness logic, re-subscribes, or
        // queued frames go out over a freshly (re)opened socket.
        if (options.authenticate) {
          await options.authenticate(session);
        }
        // close() can abort the attempt while a hook awaits. Then the socket must not be adopted.
        if (settled) {
          return;
        }
        if (options.onReady) {
          await options.onReady(session);
        }
        if (settled) {
          return;
        }
        ready = true;
        settled = true;
        if (abortConnect === abort) {
          abortConnect = null;
        }
        socket = activeSocket;
        reconnectAttempts = 0;
        connectionState = "open";
        globalThis.clearTimeout(readyTimeoutId);

        // Re-subscribe to all active patterns
        for (const pattern of activePatterns.keys()) {
          const envelope = createEnvelope({
            eventType: "system:websocket_subscribe:v1:requested",
            payload: { pattern },
          });
          void dispatchOnSocket(activeSocket, envelope, new AbortController().signal).catch(() => undefined);
        }

        resolve(activeSocket);
      }
    }).finally(() => {
      // Clear only this attempt. A connect() after close() can already own connectPromise.
      if (connectPromise === attempt) {
        connectPromise = null;
      }
    });
    connectPromise = attempt;
    return attempt;
  };

  const subscribers = new Set<{
    pattern: string;
    callback: (envelope: RuntimeEnvelope<unknown>) => void;
  }>();

  return {
    kind: "ws",
    async dispatch<TPayload>(envelope: RuntimeEnvelope<TPayload>, _route: RuntimeRoute, signal: AbortSignal): Promise<unknown> {
      const activeSocket = await connectWithAbort(connect, signal);
      return dispatchOnSocket(activeSocket, envelope, signal);
    },
    async subscribe(
      pattern: string,
      callback: (envelope: RuntimeEnvelope<unknown>) => void
    ): Promise<Subscription> {
      const sub = { pattern, callback };
      subscribers.add(sub);

      const count = activePatterns.get(pattern) ?? 0;
      activePatterns.set(pattern, count + 1);

      if (count === 0) {
        const alreadyConnected = socket?.readyState === WebSocket.OPEN;
        const activeSocket = await connect();
        // Initial/reconnecting connections replay activePatterns in
        // resolveReady. Only an already-open session needs an explicit send.
        if (alreadyConnected) {
          const envelope = createEnvelope({
            eventType: "system:websocket_subscribe:v1:requested",
            payload: { pattern },
          });
          await dispatchOnSocket(activeSocket, envelope, new AbortController().signal);
        }
      }

      return {
        unsubscribe: () => {
          subscribers.delete(sub);
          const currentCount = activePatterns.get(pattern) ?? 0;
          if (currentCount <= 1) {
            activePatterns.delete(pattern);
            if (socket?.readyState === WebSocket.OPEN) {
              const envelope = createEnvelope({
                eventType: "system:websocket_unsubscribe:v1:requested",
                payload: { pattern },
              });
              void dispatchOnSocket(socket, envelope, new AbortController().signal).catch(() => undefined);
            }
          } else {
            activePatterns.set(pattern, currentCount - 1);
          }
        },
      };
    },
    close(code = 1000, reason = "client_closed") {
      manualClose = true;
      connectionState = "closed";
      clearReconnectTimer();
      const abortInFlight = abortConnect;
      abortConnect = null;
      connectPromise = null;
      abortInFlight?.(new Error("websocket transport closed"));
      if (socket) {
        socket.close(code, reason);
        socket = null;
      }
      rejectPending("websocket transport closed");
      pending.clear();
      subscribers.clear();
      // Patterns count subscribers. Without the subscribers, a later connect must not replay them.
      activePatterns.clear();
    },
    isConnected() {
      return socket?.readyState === WebSocket.OPEN;
    },
    getConnectionState() {
      return connectionState;
    },
  };

  function dispatchOnSocket<TPayload>(
    activeSocket: WebSocket,
    envelope: RuntimeEnvelope<TPayload>,
    signal: AbortSignal
  ): Promise<unknown> {
    if (signal.aborted) {
      return Promise.reject(new Error("websocket dispatch aborted"));
    }
    if (activeSocket.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error("websocket transport is not connected"));
    }
    return new Promise<unknown>((resolve, reject) => {
      const cleanup = () => signal.removeEventListener("abort", abort);
      const abort = () => {
        pending.delete(envelope.metadata.correlationId);
        cleanup();
        reject(new Error("websocket dispatch aborted"));
      };
      pending.set(envelope.metadata.correlationId, {
        resolve: (value) => {
          cleanup();
          resolve(value);
        },
        reject: (error) => {
          cleanup();
          reject(error);
        },
      });
      signal.addEventListener(
        "abort",
        abort,
        { once: true }
      );
      void (async () => {
        try {
          if (preferBinary) {
            const compressed = await compressRuntimeBytes(encodeRuntimeEnvelope(envelope), options.compression);
            activeSocket.send(asWebSocketBinaryPayload(encodeRuntimeBinaryFrame(compressed)));
          } else {
            activeSocket.send(encodeJSONRuntimeEnvelope(envelope));
          }
        } catch (error) {
          pending.delete(envelope.metadata.correlationId);
          cleanup();
          reject(error instanceof Error ? error : new Error("websocket dispatch failed"));
        }
      })();
    });
  }
};

const withFormat = (url: string, format: "binary" | "json", compression?: RuntimeCompressionOptions): string => {
  const parsed = new URL(url, typeof window !== "undefined" ? window.location.origin : "http://localhost");
  parsed.searchParams.set("format", format);
  // Advertise by default, as the HTTP transport compresses by default. The server frames
  // only encodings listed here, and a browser without DecompressionStream lists only identity.
  if (format === "binary" && compression?.enabled !== false) {
    parsed.searchParams.set("compression", supportedRuntimeCompressionEncodings().join(","));
  }
  return parsed.toString();
};

const decodeEnvelopeMessage = async (data: Blob | ArrayBuffer | string): Promise<RuntimeEnvelope<unknown> | null> => {
  if (typeof data === "string") {
    return decodeJSONRuntimeEnvelope(data);
  }
  if (data instanceof ArrayBuffer) {
    return decodeRuntimeBinaryEnvelope(new Uint8Array(data));
  }
  if (typeof Blob !== "undefined" && data instanceof Blob) {
    return decodeRuntimeBinaryEnvelope(new Uint8Array(await data.arrayBuffer()));
  }
  return null;
};

const connectWithAbort = async (connect: () => Promise<WebSocket>, signal: AbortSignal): Promise<WebSocket> => {
  if (signal.aborted) {
    throw new Error("websocket dispatch aborted");
  }
  return new Promise<WebSocket>((resolve, reject) => {
    const abort = () => reject(new Error("websocket dispatch aborted"));
    signal.addEventListener("abort", abort, { once: true });
    connect()
      .then(resolve, reject)
      .finally(() => {
        signal.removeEventListener("abort", abort);
      });
  });
};
