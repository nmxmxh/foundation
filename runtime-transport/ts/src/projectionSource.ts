// Concrete projection read-path source: the transport bridge between the server
// projection gateway (server-kit/go/projectiongw) and the frontend read model
// (frontend-kit's HermesProjectionSource -> adapter -> connectLiveProjection).
//
// This lives in runtime-transport because it owns the wire: it decodes the
// binary foundation.v1 ProjectionSnapshot over HTTP and the events.Envelope /
// RecordMutationBatch delta frames over WebSocket. frontend-kit stays
// transport-agnostic and only defines the HermesProjectionSource interface; the
// objects returned here are structurally that interface, so the scaffold passes
// this straight into createHermesProjectionAdapter without a type dependency
// from the transport layer up to the UI layer.

import {
  createRuntimeTransportProjectionCodec,
  type ProjectionProtoCodec,
} from "./projectionCodec";

// Structural mirrors of frontend-kit's read-model types. Defined locally so the
// transport layer does not import the higher UI layer; they meet frontend-kit's
// HermesProjectionSource structurally at the scaffold seam.
export type ProjectionScope = {
  tenantId: string;
  domain: string;
  collection: string;
};

export type ProjectionMutationOperation = "upsert" | "patch" | "delete";

export type ProjectionMutation = {
  operation: ProjectionMutationOperation;
  tenantId: string;
  domain: string;
  collection: string;
  recordId: string;
  version: number;
  fields?: Record<string, unknown>;
};

export type ProjectionLoadRequest = {
  scope: ProjectionScope;
  sinceWatermark?: string;
  limit?: number;
  signal?: AbortSignal;
};

export type { ProjectionProtoCodec };

// HermesProjectionSourceCompatible is a structural mirror of frontend-kit's
// HermesProjectionSource. The transport layer must not import the UI layer, so
// the contract is restated here and met by shape. Crucially the method return is
// the same phantom type frontend-kit declares: that is what makes the source
// assignable with no cast at any consumer (frontend-kit's type is "weak" — all
// optional — so a concrete return is not assignable to it; matching the phantom
// shape here moves the single unavoidable cast inside this module).
export type HermesProjectionSourceCompatible = {
  loadProjection?<TRecord extends Record<string, unknown>>(
    scope: ProjectionScope,
    request: ProjectionLoadRequest,
  ): Promise<{ _phantom?: TRecord }>;
  subscribeProjection?<TRecord extends Record<string, unknown>>(
    scope: ProjectionScope,
    listener: (event: unknown, record?: TRecord) => void,
  ): () => void;
};

// ProjectionLoadResult is the concrete shape loadProjection actually resolves to.
// It is what the adapter normalizes; exposed for callers that decode it directly.
export type ProjectionLoadResult = {
  mutations: ProjectionMutation[];
  sourceWatermark?: string;
  version?: number;
};

// PROJECTION_OPERATION enum values from foundation/v1/projection.proto.
const PROTO_OPERATION: Record<number, ProjectionMutationOperation> = {
  1: "upsert",
  2: "delete",
  3: "patch",
};

type WireScalarValue = {
  stringValue?: string;
  int64Value?: number | string;
  uint64Value?: number | string;
  doubleValue?: number;
  boolValue?: boolean;
  bytesValue?: Uint8Array;
};

type WireFieldValue = { name: string; value?: WireScalarValue };

type WireRecordMutation = {
  operation: number;
  domain: string;
  collection: string;
  organizationId: string;
  recordId: string;
  version: number | string;
  fields: WireFieldValue[];
};

const toNumber = (value: number | string | undefined): number => {
  if (typeof value === "number") return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return 0;
};

const scalarToValue = (scalar: WireScalarValue | undefined): unknown => {
  if (!scalar) return undefined;
  if (scalar.stringValue !== undefined) return scalar.stringValue;
  if (scalar.boolValue !== undefined) return scalar.boolValue;
  if (scalar.doubleValue !== undefined) return scalar.doubleValue;
  if (scalar.int64Value !== undefined) return toNumber(scalar.int64Value);
  if (scalar.uint64Value !== undefined) return toNumber(scalar.uint64Value);
  if (scalar.bytesValue !== undefined) return scalar.bytesValue;
  return undefined;
};

// A tenant-mismatch drop is indistinguishable from "no data" in the UI: the
// server scopes every record to the authenticated organization, so a runtime
// created with the wrong (or default placeholder) tenantId silently filters
// out every real record and the app renders empty despite full snapshots
// arriving. Warn once per scope/tenant pair so the misconfiguration is
// visible in the console instead of costing a debugging session.
const warnedTenantMismatches = new Set<string>();
const warnTenantMismatch = (scope: ProjectionScope, recordTenantId: string): void => {
  const key = `${scope.domain}/${scope.collection}:${scope.tenantId}!=${recordTenantId}`;
  if (warnedTenantMismatches.has(key)) return;
  warnedTenantMismatches.add(key);
  // eslint-disable-next-line no-console
  console.warn(
    `[runtime-transport] dropped projection record(s) for ${scope.domain}/${scope.collection}: ` +
      `record tenant "${recordTenantId}" does not match scope tenant "${scope.tenantId}". ` +
      `If the app looks empty, the runtime was likely created without the authenticated ` +
      `organization as its tenantId.`,
  );
};

// mapMutation projects a decoded proto mutation onto a scope-validated
// ProjectionMutation. Mutations with an unknown operation, an empty record id,
// or which fall outside the requested scope are dropped (returns undefined).
export const mapMutation = (
  mutation: WireRecordMutation,
  scope: ProjectionScope,
): ProjectionMutation | undefined => {
  const operation = PROTO_OPERATION[mutation.operation];
  if (!operation) return undefined;

  const tenantId = mutation.organizationId || scope.tenantId;
  const domain = mutation.domain || scope.domain;
  const collection = mutation.collection || scope.collection;
  if (tenantId !== scope.tenantId) {
    warnTenantMismatch(scope, tenantId);
    return undefined;
  }
  if (domain !== scope.domain || collection !== scope.collection) {
    return undefined;
  }
  if (!mutation.recordId) return undefined;

  const fields: Record<string, unknown> = {};
  for (const field of mutation.fields ?? []) {
    if (!field?.name) continue;
    const value = scalarToValue(field.value);
    if (value !== undefined) fields[field.name] = value;
  }

  return {
    operation,
    tenantId,
    domain,
    collection,
    recordId: mutation.recordId,
    version: toNumber(mutation.version),
    fields,
  };
};

const scopePath = (scope: ProjectionScope): string =>
  `${encodeURIComponent(scope.domain)}/${encodeURIComponent(scope.collection)}`;

// --- HTTP snapshot source -------------------------------------------------

export type HttpProjectionSourceConfig = {
  baseUrl: string;
  codec?: ProjectionProtoCodec;
  headers?: () => Record<string, string> | Promise<Record<string, string>>;
  fetch?: typeof fetch;
  // Safety bound on pages followed during a single backfill, so a pathological
  // scope cannot loop unboundedly. Defaults to 1000 pages.
  maxPages?: number;
};

const DEFAULT_MAX_PAGES = 1000;

const joinUrl = (base: string, path: string): string => `${base.replace(/\/+$/, "")}/${path}`;

export const createHttpProjectionSource = (
  config: HttpProjectionSourceConfig,
): Required<Pick<HermesProjectionSourceCompatible, "loadProjection">> => {
  const codec = config.codec ?? createRuntimeTransportProjectionCodec();
  const maxPages = Math.max(1, config.maxPages ?? DEFAULT_MAX_PAGES);

  const fetchPage = async (
    scope: ProjectionScope,
    request: ProjectionLoadRequest,
    cursor: string,
  ): Promise<ReturnType<ProjectionProtoCodec["decodeSnapshot"]>> => {
    const fetchImpl = config.fetch ?? fetch;
    const url = new URL(joinUrl(config.baseUrl, scopePath(scope)));
    if (request.sinceWatermark) url.searchParams.set("since", request.sinceWatermark);
    if (request.limit) url.searchParams.set("limit", String(request.limit));
    if (cursor) url.searchParams.set("cursor", cursor);

    const headers = (await config.headers?.()) ?? {};
    const response = await fetchImpl(url.toString(), {
      method: "GET",
      headers: { Accept: "application/x-protobuf", ...headers },
      signal: request.signal,
    });
    if (!response.ok) {
      throw new Error(`projection snapshot failed: ${response.status}`);
    }
    return codec.decodeSnapshot(new Uint8Array(await response.arrayBuffer()));
  };

  return {
    async loadProjection<TRecord extends Record<string, unknown>>(
      scope: ProjectionScope,
      request: ProjectionLoadRequest,
    ): Promise<{ _phantom?: TRecord }> {
      // Backfill a scope larger than the server limit by following the keyset
      // cursor. Each request stays O(limit) server-side; the client assembles
      // the complete set across bounded pages. The resume watermark for the live
      // delta stream is the first page's watermark (read at the newest epoch).
      const mutations: ProjectionMutation[] = [];
      let cursor = "";
      let sourceWatermark: string | undefined;
      let version: number | undefined;
      for (let page = 0; page < maxPages; page += 1) {
        const snapshot = await fetchPage(scope, request, cursor);
        if (page === 0) {
          sourceWatermark = snapshot.watermark || undefined;
          version = toNumber(snapshot.epoch);
        }
        for (const wire of snapshot.batch?.mutations ?? []) {
          const mutation = mapMutation(wire as WireRecordMutation, scope);
          if (mutation) mutations.push(mutation);
        }
        if (!snapshot.hasMore || !snapshot.nextCursor) break;
        cursor = snapshot.nextCursor;
      }
      const result: ProjectionLoadResult = { mutations, sourceWatermark, version };
      // The adapter normalizes this structurally; frontend-kit's source type uses
      // a phantom return, so the single bridging cast lives here.
      return result as unknown as { _phantom?: TRecord };
    },
  };
};

// --- WebSocket delta source ----------------------------------------------

export type ProjectionTransportPhase =
  | "connecting"
  | "live"
  | "reconnecting"
  | "degraded"
  | "closed";

export type ProjectionTransportStatus = {
  phase: ProjectionTransportPhase;
  scope: ProjectionScope;
  reason?: string;
  dropped?: number;
  epoch?: number;
};

export type WebSocketProjectionSourceConfig = {
  url: string;
  codec?: ProjectionProtoCodec;
  query?: () => Record<string, string> | Promise<Record<string, string>>;
  minBackoffMs?: number;
  maxBackoffMs?: number;
  // Reconnect budget: consecutive connections that close without ever opening
  // before the source gives up and emits "closed". The browser WebSocket API
  // hides the HTTP status of a failed upgrade, so a rejection the server will
  // always repeat (e.g. an unauthenticated 401) is indistinguishable from an
  // outage — without a budget it becomes an infinite request loop. A
  // successful open resets the budget; a new subscribeProjection() starts a
  // fresh one. Defaults to 8.
  maxConsecutiveFailures?: number;
  onStatus?: (status: ProjectionTransportStatus) => void;
  createSocket?: (url: string) => WebSocket;
};

type ProjectionControlFrame = {
  type?: string;
  reason?: string;
  dropped?: number;
  epoch?: number;
  // Scope tags on the multiplexed stream; empty on a single-scope stream.
  domain?: string;
  collection?: string;
};

const DEFAULT_MIN_BACKOFF = 500;
const DEFAULT_MAX_BACKOFF = 15_000;
const DEFAULT_MAX_CONSECUTIVE_FAILURES = 8;

// createWebSocketProjectionSource multiplexes every subscribed scope over ONE
// WebSocket to the gateway root ({url}/). Browsers cap and tax per-connection
// fan-out — a screen binding N collections must not cost N sockets — so scopes
// ride in subscribe/unsubscribe control frames (projectiongw's multiplexed
// endpoint) and inbound delta frames are routed by the (domain, collection)
// every mutation already carries. The connection is opened lazily at the first
// subscription, resubscribes everything on reconnect, and closes when the last
// subscription is disposed.
export const createWebSocketProjectionSource = (
  config: WebSocketProjectionSourceConfig,
): Required<Pick<HermesProjectionSourceCompatible, "subscribeProjection">> => {
  const codec = config.codec ?? createRuntimeTransportProjectionCodec();
  const createSocket = config.createSocket ?? ((url: string) => new WebSocket(url));
  const minBackoff = Math.max(0, config.minBackoffMs ?? DEFAULT_MIN_BACKOFF);
  const maxBackoff = Math.max(minBackoff, config.maxBackoffMs ?? DEFAULT_MAX_BACKOFF);
  const maxFailures = Math.max(
    1,
    config.maxConsecutiveFailures ?? DEFAULT_MAX_CONSECUTIVE_FAILURES,
  );

  type Entry = {
    scope: ProjectionScope;
    listener: (event: unknown) => void;
    resumeWatermark: string;
  };

  const entries = new Map<string, Set<Entry>>();
  let socket: WebSocket | undefined;
  let opening = false;
  let live = false;
  let exhausted = false;
  let attempt = 0;
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

  const entryKey = (scope: Pick<ProjectionScope, "domain" | "collection">): string =>
    `${scope.domain}/${scope.collection}`;

  const allEntries = (): Entry[] => {
    const out: Entry[] = [];
    for (const set of entries.values()) out.push(...set);
    return out;
  };

  const emitStatus = (scope: ProjectionScope, status: Omit<ProjectionTransportStatus, "scope">) => {
    config.onStatus?.({ scope, ...status });
  };
  const emitStatusAll = (status: Omit<ProjectionTransportStatus, "scope">) => {
    for (const entry of allEntries()) emitStatus(entry.scope, status);
  };

  const trySend = (payload: string) => {
    if (!socket || !live) return;
    try {
      socket.send(payload);
    } catch {
      /* a failed send surfaces as a close; the reconnect path recovers */
    }
  };

  const subscribeMessage = (targets: Entry[]): string =>
    JSON.stringify({
      type: "subscribe",
      scopes: targets.map((entry) => ({
        domain: entry.scope.domain,
        collection: entry.scope.collection,
        ...(entry.resumeWatermark ? { since: entry.resumeWatermark } : {}),
      })),
    });

  const handleFrame = (data: ArrayBuffer | Uint8Array) => {
    const bytes = data instanceof Uint8Array ? data : new Uint8Array(data);
    let batch: { mutations?: WireRecordMutation[] };
    try {
      batch = codec.decodeDeltaFrame(bytes);
    } catch {
      return;
    }
    for (const wire of batch.mutations ?? []) {
      // Route by the scope the mutation carries. A mutation without them can
      // only be resolved when a single scope is subscribed (legacy frames).
      let set = wire.domain && wire.collection ? entries.get(entryKey(wire)) : undefined;
      if (!set && entries.size === 1) {
        set = entries.values().next().value;
      }
      if (!set) continue;
      for (const entry of set) {
        const mutation = mapMutation(wire, entry.scope);
        if (!mutation) continue;
        if (mutation.version) entry.resumeWatermark = String(mutation.version);
        entry.listener(mutation);
      }
    }
  };

  const handleControl = (raw: string) => {
    let control: ProjectionControlFrame;
    try {
      control = JSON.parse(raw) as ProjectionControlFrame;
    } catch {
      return;
    }
    if (control.type !== "resync") return;
    // The server shed frames for this consumer: the stream now has gaps.
    // Surface "degraded" so the app re-runs the snapshot load and reconciles.
    const status = {
      phase: "degraded",
      reason: control.reason ?? "resync",
      dropped: control.dropped,
      epoch: control.epoch,
    } as const;
    const set =
      control.domain && control.collection
        ? entries.get(entryKey({ domain: control.domain, collection: control.collection }))
        : undefined;
    if (set) {
      for (const entry of set) emitStatus(entry.scope, status);
      return;
    }
    // An untagged resync (single-scope server) degrades everything subscribed.
    emitStatusAll(status);
  };

  const backoffDelay = (): number => {
    const exp = Math.min(maxBackoff, minBackoff * 2 ** attempt);
    return Math.random() * exp;
  };

  const scheduleReconnect = () => {
    if (entries.size === 0 || exhausted) return;
    // attempt counts reconnects already scheduled, so this close is
    // consecutive failure number attempt + 1.
    if (attempt + 1 >= maxFailures) {
      exhausted = true;
      emitStatusAll({ phase: "closed", reason: "retry-limit" });
      return;
    }
    const delay = backoffDelay();
    attempt += 1;
    reconnectTimer = setTimeout(() => {
      void connect();
    }, delay);
  };

  const connect = async () => {
    if (socket || opening || exhausted || entries.size === 0) return;
    opening = true;
    emitStatusAll({ phase: "connecting" });
    const params = (await config.query?.()) ?? {};
    if (entries.size === 0) {
      opening = false;
      return;
    }
    // The multiplexed stream lives at the gateway root; scopes ride in
    // subscribe control frames instead of the URL path.
    const url = new URL(`${config.url.replace(/\/+$/, "")}/`);
    for (const [key, value] of Object.entries(params)) url.searchParams.set(key, value);

    const ws = createSocket(url.toString());
    ws.binaryType = "arraybuffer";
    socket = ws;

    ws.onopen = () => {
      opening = false;
      live = true;
      attempt = 0;
      exhausted = false;
      trySend(subscribeMessage(allEntries()));
      emitStatusAll({ phase: "live" });
    };
    ws.onmessage = (event: MessageEvent) => {
      const { data } = event;
      // Binary frames are deltas; text frames are out-of-band control signals.
      if (typeof data === "string") handleControl(data);
      else if (data instanceof ArrayBuffer) handleFrame(data);
      else if (data instanceof Uint8Array) handleFrame(data);
    };
    ws.onclose = () => {
      socket = undefined;
      opening = false;
      live = false;
      // Nothing to do after a deliberate shutdown or once the budget is spent
      // (a retired socket may still fire onclose).
      if (entries.size === 0 || exhausted) return;
      emitStatusAll({ phase: "reconnecting" });
      scheduleReconnect();
    };
    ws.onerror = () => {
      try {
        ws.close();
      } catch {
        /* ignore */
      }
    };
  };

  return {
    subscribeProjection<TRecord extends Record<string, unknown>>(
      scope: ProjectionScope,
      listener: (event: unknown, record?: TRecord) => void,
    ): () => void {
      const key = entryKey(scope);
      const entry: Entry = {
        scope,
        listener: listener as Entry["listener"],
        resumeWatermark: "",
      };
      let set = entries.get(key);
      if (!set) {
        set = new Set();
        entries.set(key, set);
      }
      set.add(entry);
      // A fresh subscription restarts an exhausted budget, preserving the
      // old per-subscription contract: new subscribeProjection, fresh budget.
      if (exhausted) {
        exhausted = false;
        attempt = 0;
      }
      if (live) {
        trySend(subscribeMessage([entry]));
        emitStatus(scope, { phase: "live" });
      } else {
        void connect();
      }

      let disposed = false;
      return () => {
        if (disposed) return;
        disposed = true;
        const bucket = entries.get(key);
        if (bucket) {
          bucket.delete(entry);
          if (bucket.size === 0) {
            entries.delete(key);
            trySend(
              JSON.stringify({
                type: "unsubscribe",
                scopes: [{ domain: scope.domain, collection: scope.collection }],
              }),
            );
          }
        }
        emitStatus(scope, { phase: "closed" });
        if (entries.size === 0) {
          if (reconnectTimer) clearTimeout(reconnectTimer);
          exhausted = false;
          attempt = 0;
          const ws = socket;
          socket = undefined;
          opening = false;
          live = false;
          if (ws) {
            try {
              ws.close();
            } catch {
              /* ignore */
            }
          }
        }
      };
    },
  };
};

// --- Combined source ------------------------------------------------------

export type ProjectionSourceConfig = {
  http: HttpProjectionSourceConfig;
  ws: WebSocketProjectionSourceConfig;
};

// createProjectionSource composes the HTTP snapshot loader and WebSocket delta
// stream into a single HermesProjectionSource-shaped object: snapshot on load,
// live deltas on subscribe. This is what the scaffold passes to
// createHermesProjectionAdapter.
export const createProjectionSource = (
  config: ProjectionSourceConfig,
): HermesProjectionSourceCompatible => ({
  ...createHttpProjectionSource(config.http),
  ...createWebSocketProjectionSource(config.ws),
});

// --- Convention-based endpoint resolution ---------------------------------
//
// The gateway is served at a standard path on the same backend the app already
// talks to, so the endpoints are derived rather than configured. A browser can
// only reach the app's public origin anyway (never a Docker-internal service
// name), so same-origin derivation is the correct default, not merely the
// convenient one. The WebSocket URL is the HTTP URL with the scheme upgraded.

// DEFAULT_PROJECTION_PATH matches projectiongw's default HTTP/WS route prefix.
export const DEFAULT_PROJECTION_PATH = "/v1/projections";

// httpToWebSocketUrl upgrades an http(s) URL to its ws(s) equivalent.
export const httpToWebSocketUrl = (httpUrl: string): string =>
  httpUrl.replace(/^https:/i, "wss:").replace(/^http:/i, "ws:");

export type ProjectionEndpointOptions = {
  // Explicit, fully-qualified gateway base (e.g. https://host/v1/projections).
  // When set it wins and no derivation happens.
  baseUrl?: string;
  // Origin to derive from (e.g. the app's API base). Defaults to the page
  // origin (window.location.origin) when available.
  originUrl?: string;
  // Gateway path appended to the origin. Defaults to DEFAULT_PROJECTION_PATH.
  path?: string;
};

// resolveProjectionEndpoints derives the HTTP snapshot and WS delta URLs from a
// single origin (or an explicit base). Returns undefined when no origin is
// resolvable (e.g. SSR/non-browser with nothing supplied), in which case the
// app stays offline rather than guessing.
export const resolveProjectionEndpoints = (
  options: ProjectionEndpointOptions = {},
): { httpUrl: string; wsUrl: string } | undefined => {
  let httpUrl = options.baseUrl?.trim();
  if (!httpUrl) {
    const origin =
      options.originUrl?.trim() ||
      (typeof globalThis !== "undefined" && globalThis.location ? globalThis.location.origin : "");
    if (!origin) return undefined;
    httpUrl = joinUrl(origin, (options.path ?? DEFAULT_PROJECTION_PATH).replace(/^\/+/, ""));
  }
  return { httpUrl, wsUrl: httpToWebSocketUrl(httpUrl) };
};

export type DefaultProjectionSourceOptions = ProjectionEndpointOptions & {
  codec?: ProjectionProtoCodec;
  headers?: HttpProjectionSourceConfig["headers"];
  query?: WebSocketProjectionSourceConfig["query"];
  onStatus?: (status: ProjectionTransportStatus) => void;
};

// createDefaultProjectionSource builds a projection source from derived
// endpoints, sharing one proto codec across both lanes. Returns undefined when
// no endpoint is resolvable, so the caller can fall back to offline mode.
export const createDefaultProjectionSource = (
  options: DefaultProjectionSourceOptions = {},
): HermesProjectionSourceCompatible | undefined => {
  const endpoints = resolveProjectionEndpoints(options);
  if (!endpoints) return undefined;
  const codec = options.codec ?? createRuntimeTransportProjectionCodec();
  return createProjectionSource({
    http: { baseUrl: endpoints.httpUrl, codec, headers: options.headers },
    ws: { url: endpoints.wsUrl, codec, query: options.query, onStatus: options.onStatus },
  });
};
