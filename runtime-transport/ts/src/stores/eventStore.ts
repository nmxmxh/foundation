import { createStore } from "zustand/vanilla";

import { type PayloadEncoding, type RuntimeEnvelope, createEnvelope } from "../index";
import { normalizeRuntimeString, pickRuntimeSessionID } from "../runtimeMetadata";

export type RequestReplayPolicy = "auto" | "always" | "never";
export type RequestCoalescingPolicy = "auto" | "always" | "never";

export interface EventState {
  pendingRequests: Map<string, Promise<unknown>>;
  responseCache: Map<string, { data: unknown; timestamp: number }>;
  loadingStates: Record<string, number>;
  isLoading: boolean;
  status: "idle" | "loading" | "success" | "error";
  lastError: string | null;

  setIsLoading: (loading: boolean, key?: string) => void;
  clearLoadingState: (key?: string) => void;
  isLoadingKeyActive: (key: string) => boolean;
  setStatus: (status: "idle" | "loading" | "success" | "error", error?: string | null) => void;
  clearCache: () => void;
  reset: () => void;
}

export interface EmitEventOptions {
  cacheDurationMs?: number; // 0 means do not cache
  forceRefresh?: boolean;
  payloadEncoding?: PayloadEncoding;
  extra?: Record<string, unknown>;
  replayPolicy?: RequestReplayPolicy;
  coalescingPolicy?: RequestCoalescingPolicy;
  loadingKey?: string;
}

const DEFAULT_LOADING_KEY = "global";
const MUTATION_EVENT_PATTERN =
  /(?:[:_])(create|update|delete|upload|import|export|generate|process|sync|assign|set|post|put|patch|start|stop)/;

const isPlainObject = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object" && Object.getPrototypeOf(value) === Object.prototype;

const pickString = (source: Record<string, unknown>, ...keys: string[]): string => {
  for (const key of keys) {
    const raw = source[key];
    const normalized = normalizeRuntimeString(raw);
    if (normalized) {
      return normalized;
    }
  }
  return "";
};

const hashKey = (value: string): string => {
  let hash = 5381;
  for (let index = 0; index < value.length; index += 1) {
    hash = (hash * 33) ^ value.charCodeAt(index);
  }
  return (hash >>> 0).toString(36);
};

const hashBytes = (bytes: Uint8Array): string => {
  let hash = 5381;
  for (let index = 0; index < bytes.length; index += 1) {
    hash = (hash * 33) ^ bytes[index];
  }
  return (hash >>> 0).toString(36);
};

const toBinarySummary = (value: Uint8Array, type: string): Record<string, unknown> => ({
  __binaryType: type,
  byteLength: value.byteLength,
  hash: hashBytes(value),
});

const toStableValue = (value: unknown): unknown => {
  if (value instanceof Date) {
    return value.toISOString();
  }

  if (typeof File !== "undefined" && value instanceof File) {
    return {
      __file: true,
      name: value.name,
      size: value.size,
      type: value.type,
      lastModified: value.lastModified,
    };
  }

  if (typeof Blob !== "undefined" && value instanceof Blob) {
    return {
      __blob: true,
      size: value.size,
      type: value.type,
    };
  }

  if (ArrayBuffer.isView(value)) {
    return toBinarySummary(new Uint8Array(value.buffer, value.byteOffset, value.byteLength), value.constructor.name);
  }

  if (value instanceof ArrayBuffer) {
    return toBinarySummary(new Uint8Array(value), "ArrayBuffer");
  }

  if (Array.isArray(value)) {
    return value.map((item) => toStableValue(item));
  }

  if (isPlainObject(value)) {
    const sorted: Record<string, unknown> = {};
    Object.keys(value)
      .sort()
      .forEach((key) => {
        sorted[key] = toStableValue(value[key]);
      });
    return sorted;
  }

  return value;
};

export const stableStringify = (value: unknown): string => {
  try {
    return JSON.stringify(toStableValue(value ?? {}));
  } catch {
    return JSON.stringify(value ?? {});
  }
};

export const buildRequestFingerprint = (payload: unknown): string => hashKey(stableStringify(payload));

const buildMetadataFingerprint = (metadata: unknown): string => hashKey(stableStringify(metadata));

const toIdentityScopedMetadata = (metadata: unknown): Record<string, unknown> => {
  if (!isPlainObject(metadata)) {
    return {};
  }

  const root = metadata;
  const globalContext = isPlainObject(root.global_context)
    ? root.global_context
    : isPlainObject(root.globalContext)
      ? root.globalContext
      : {};

  const scoped: Record<string, unknown> = {};
  const sessionId = pickRuntimeSessionID(
    globalContext["session_id"],
    globalContext["sessionId"],
    root["session_id"],
    root["sessionId"]
  );
  const userId = pickString(globalContext, "user_id", "userId") || pickString(root, "user_id", "userId");
  const organizationId =
    pickString(globalContext, "organization_id", "organizationId") ||
    pickString(root, "organization_id", "organizationId");
  const roleId = pickString(globalContext, "role_id", "roleId") || pickString(root, "role_id", "roleId");

  if (sessionId) {
    scoped.session_id = sessionId;
  }
  if (userId) {
    scoped.user_id = userId;
  }
  if (organizationId) {
    scoped.organization_id = organizationId;
  }
  if (roleId) {
    scoped.role_id = roleId;
  }

  return scoped;
};

export const buildCommandLoadingKey = (scope: string, eventType: string, payload: unknown): string =>
  `${scope.trim() || "event"}:${eventType}:${buildRequestFingerprint(payload)}`;

export const buildInflightRequestKey = (scope: string, eventType: string, payload: unknown): string =>
  `${scope.trim() || "event"}:${eventType}:${buildRequestFingerprint(payload)}`;

export const normalizeLoadingKey = (value?: string): string => {
  const normalized = typeof value === "string" ? value.trim() : "";
  return normalized || DEFAULT_LOADING_KEY;
};

export const hasActiveLoadingState = (loadingStates: Record<string, number>): boolean =>
  Object.values(loadingStates).some((count) => count > 0);

export const setLoadingState = (
  loadingStates: Record<string, number>,
  loading: boolean,
  key?: string
): Record<string, number> => {
  const normalizedKey = normalizeLoadingKey(key);
  const nextLoadingStates = { ...loadingStates };
  const currentCount = nextLoadingStates[normalizedKey] || 0;

  if (loading) {
    nextLoadingStates[normalizedKey] = currentCount + 1;
  } else if (currentCount <= 1) {
    delete nextLoadingStates[normalizedKey];
  } else {
    nextLoadingStates[normalizedKey] = currentCount - 1;
  }

  return nextLoadingStates;
};

export const clearLoadingState = (loadingStates: Record<string, number>, key?: string): Record<string, number> => {
  if (typeof key === "string" && key.trim()) {
    const normalizedKey = normalizeLoadingKey(key);
    if (!(normalizedKey in loadingStates)) {
      return loadingStates;
    }

    const nextLoadingStates = { ...loadingStates };
    delete nextLoadingStates[normalizedKey];
    return nextLoadingStates;
  }

  return {};
};

export const isLoadingStateActive = (loadingStates: Record<string, number>, key: string): boolean => {
  const normalizedKey = normalizeLoadingKey(key);
  return (loadingStates[normalizedKey] || 0) > 0;
};

const normalizeEventType = (eventType: string): string => eventType.trim().toLowerCase();

export const isMutationRequestedEvent = (eventType: string): boolean => {
  const normalized = normalizeEventType(eventType);
  if (!normalized.endsWith(":requested")) {
    return false;
  }
  return MUTATION_EVENT_PATTERN.test(normalized.replace(/:requested$/, ""));
};

export const isReplayEligibleRequest = (eventType: string): boolean => {
  const normalized = normalizeEventType(eventType);
  if (!normalized.endsWith(":requested")) {
    return false;
  }
  if (/^(user:|auth:|identity:)/.test(normalized)) {
    return false;
  }
  return !isMutationRequestedEvent(normalized);
};

const shouldUseReplayCache = (
  eventType: string,
  policy: RequestReplayPolicy,
  forceRefresh: boolean
): boolean => {
  if (forceRefresh) {
    return false;
  }
  if (policy === "always") {
    return true;
  }
  if (policy === "never") {
    return false;
  }
  return isReplayEligibleRequest(eventType);
};

const shouldCoalesceRequest = (
  eventType: string,
  policy: RequestCoalescingPolicy,
  replayEnabled: boolean
): boolean => {
  if (policy === "always") {
    return true;
  }
  if (policy === "never") {
    return false;
  }
  return replayEnabled || isReplayEligibleRequest(eventType);
};

export const createEventStore = (
  dispatch: <TPayload>(envelope: RuntimeEnvelope<TPayload>) => Promise<unknown>,
  getMetadata?: () => Record<string, unknown>
) => {
  const pendingRequests = new Map<string, Promise<unknown>>();
  const responseCache = new Map<string, { data: unknown; timestamp: number }>();

  const store = createStore<EventState>((set, get) => ({
    pendingRequests,
    responseCache,
    loadingStates: {},
    isLoading: false,
    status: "idle",
    lastError: null,

    setIsLoading: (loading: boolean, key?: string) =>
      set((state) => {
        const loadingStates = setLoadingState(state.loadingStates, loading, key);
        return {
          loadingStates,
          isLoading: hasActiveLoadingState(loadingStates),
        };
      }),
    clearLoadingState: (key?: string) =>
      set((state) => {
        const loadingStates = clearLoadingState(state.loadingStates, key);
        return {
          loadingStates,
          isLoading: hasActiveLoadingState(loadingStates),
        };
      }),
    isLoadingKeyActive: (key: string) => isLoadingStateActive(get().loadingStates, key),
    setStatus: (status: "idle" | "loading" | "success" | "error", error: string | null = null) =>
      set({ status, lastError: error }),
    clearCache: () => {
      responseCache.clear();
      set({ responseCache });
    },
    reset: () => {
      pendingRequests.clear();
      responseCache.clear();
      set({
        status: "idle",
        lastError: null,
        isLoading: false,
        loadingStates: {},
      });
    },
  }));

  const computeDedupeKey = (eventType: string, payload: unknown, payloadEncoding: PayloadEncoding): string => {
    const contextKey = buildMetadataFingerprint(toIdentityScopedMetadata(getMetadata ? getMetadata() : {}));
    return `${eventType}:${payloadEncoding}:ctx:${contextKey}:payload:${buildRequestFingerprint(payload)}`;
  };

  const emitEvent = async <TPayload, TResponse = unknown>(
    eventType: string,
    payload: TPayload,
    options: EmitEventOptions = {}
  ): Promise<TResponse> => {
    const payloadEncoding = options.payloadEncoding ?? "json";
    const dedupeKey = computeDedupeKey(eventType, payload, payloadEncoding);
    const replayPolicy = options.replayPolicy ?? "auto";
    const coalescingPolicy = options.coalescingPolicy ?? "auto";
    const cacheDuration = options.cacheDurationMs ?? 0;
    const replayEnabled = cacheDuration > 0 && shouldUseReplayCache(eventType, replayPolicy, options.forceRefresh ?? false);
    const coalescingEnabled = shouldCoalesceRequest(eventType, coalescingPolicy, replayEnabled);

    if (replayEnabled) {
      const cached = responseCache.get(dedupeKey);
      if (cached && Date.now() - cached.timestamp < cacheDuration) {
        return cached.data as TResponse;
      }
    }

    if (coalescingEnabled) {
      const existing = pendingRequests.get(dedupeKey);
      if (existing) {
        return existing as Promise<TResponse>;
      }
    }

    store.getState().setStatus("loading");
    store.getState().setIsLoading(true, options.loadingKey);

    const extraMetadata = {
      ...(getMetadata ? getMetadata() : {}),
      ...(options.extra ?? {}),
    };
    const envelope = createEnvelope({
      eventType,
      payload,
      extra: extraMetadata,
    });
    if (payloadEncoding !== envelope.payloadEncoding) {
      envelope.payloadEncoding = payloadEncoding;
    }

    const dispatchPromise = dispatch(envelope)
      .then((res) => {
        if (replayEnabled) {
          responseCache.set(dedupeKey, { data: res, timestamp: Date.now() });
        }
        store.setState({ status: "success", lastError: null });
        return res;
      })
      .catch((err: unknown) => {
        const error = err instanceof Error ? err : new Error(String(err));
        store.setState({ status: "error", lastError: error.message });
        throw error;
      })
      .finally(() => {
        if (coalescingEnabled) {
          pendingRequests.delete(dedupeKey);
        }
        store.getState().setIsLoading(false, options.loadingKey);
      });

    if (coalescingEnabled) {
      pendingRequests.set(dedupeKey, dispatchPromise);
    }

    return dispatchPromise as Promise<TResponse>;
  };

  return {
    store,
    emitEvent,
    clearCache: () => {
      responseCache.clear();
    },
  };
};
