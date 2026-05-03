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

const asWebSocketBinaryPayload = (bytes: Uint8Array): ArrayBuffer | ArrayBufferView => {
  if (bytes.buffer instanceof ArrayBuffer) {
    return bytes as unknown as ArrayBufferView;
  }
  const copy = Uint8Array.from(bytes);
  return copy.buffer as ArrayBuffer;
};

type WebSocketTransportOptions = {
  url: string;
  preferBinary?: boolean;
  protocols?: string | string[];
  compression?: RuntimeCompressionOptions;
  createSocket?: (url: string, protocols?: string | string[]) => WebSocket;
  onEnvelope?: (envelope: RuntimeEnvelope<unknown>) => void;
  readyWhenEnvelope?: (envelope: RuntimeEnvelope<unknown>) => boolean;
  onReady?: (session: WebSocketReadySession) => Promise<void>;
  reconnect?: {
    enabled?: boolean;
    maxAttempts?: number;
    baseDelayMs?: number;
    maxDelayMs?: number;
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

    const delay = Math.min(reconnect.maxDelayMs, reconnect.baseDelayMs * 2 ** reconnectAttempts);
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
    connectionState = connectionState === "reconnecting" ? "reconnecting" : "connecting";
    connectPromise = new Promise<WebSocket>((resolve, reject) => {
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
      next.onmessage = async (event) => {
        const envelope = await decodeEnvelopeMessage(event.data);
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
        if (options.onReady) {
          await options.onReady({
            socket: activeSocket,
            readyEnvelope,
            dispatch: <TPayload>(envelope: RuntimeEnvelope<TPayload>, signal?: AbortSignal) =>
              dispatchOnSocket(activeSocket, envelope, signal ?? new AbortController().signal),
          });
        }
        ready = true;
        settled = true;
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
          void dispatchOnSocket(activeSocket, envelope, new AbortController().signal);
        }

        resolve(activeSocket);
      }
    }).finally(() => {
      connectPromise = null;
    });
    return connectPromise;
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
        const activeSocket = await connect();
        const envelope = createEnvelope({
          eventType: "system:websocket_subscribe:v1:requested",
          payload: { pattern },
        });
        await dispatchOnSocket(activeSocket, envelope, new AbortController().signal);
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
              void dispatchOnSocket(socket, envelope, new AbortController().signal);
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
      if (socket) {
        socket.close(code, reason);
        socket = null;
      }
      rejectPending("websocket transport closed");
      pending.clear();
      subscribers.clear();
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
  if (format === "binary" && compression?.enabled) {
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
