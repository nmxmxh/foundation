export type TransportKind = "postMessage" | "transferable" | "sab" | "native" | "ws" | "http" | "wasm";
export type PayloadEncoding = "json" | "protobuf";
export type ProtobufCodec<TPayload> = {
  encode: (payload: TPayload) => Uint8Array;
  decode: (payload: Uint8Array) => TPayload;
};

export type EnvelopeMetadata = {
  correlationId: string;
  requestId: string;
  idempotencyKey: string;
  schemaVersion: string;
  timestamp: string;
  extra: Record<string, unknown>;
};

export type RuntimeEnvelope<TPayload = Record<string, unknown>> = {
  eventType: string;
  payload: TPayload;
  payloadEncoding: PayloadEncoding;
  metadata: EnvelopeMetadata;
};

export type RuntimeRoute = {
  method: string;
  path: string;
  eventType: string;
  requiredCapability: string;
  permission: "view" | "write" | "admin";
  transportOrder?: TransportKind[];
};

export type TransportDiagnostics = {
  transport: TransportKind;
  fallback: boolean;
  durationMs: number;
  eventType: string;
  schemaVersion: string;
  correlationId: string;
  requestId: string;
  idempotencyKey: string;
  attempt: number;
  error?: string;
};

export type EnvelopeFactoryInput<TPayload> = {
  eventType: string;
  payload: TPayload;
  extra?: Record<string, unknown>;
  correlationId?: string;
  requestId?: string;
  idempotencyKey?: string;
  schemaVersion?: string;
};

export type RouteRegistry = ReturnType<typeof createRouteRegistry>;

export type Subscription = {
  unsubscribe: () => void;
};

export type TransportStrategy = {
  kind: TransportKind;
  dispatch: <TPayload>(
    envelope: RuntimeEnvelope<TPayload>,
    route: RuntimeRoute,
    signal: AbortSignal
  ) => Promise<unknown>;
  subscribe?: (
    pattern: string,
    callback: (envelope: RuntimeEnvelope<unknown>) => void
  ) => Promise<Subscription>;
};

type CommandBusOptions = {
  registry: RouteRegistry;
  strategies: TransportStrategy[];
  grantedCapabilities: string[];
  hasPolicyAccess: (route: RuntimeRoute | undefined) => boolean;
  onDiagnostics?: (diagnostics: TransportDiagnostics) => void;
  defaultTimeoutMs?: number;
};

export type EnvelopeCompatibilityMatrix = {
  current: string;
  supported: readonly string[];
  aliases: Readonly<Record<string, string>>;
};

export type ParsedEventType = {
  raw: string;
  domain: string;
  action: string;
  version: string | null;
  state: string;
  segments: readonly string[];
};

const terminalStates = new Set(["requested", "success", "failed", "ack"]);
export const PERFORMANCE_TRANSPORT_ORDER: readonly TransportKind[] = Object.freeze([
  "sab",
  "wasm",
  "native",
  "transferable",
  "ws",
  "http",
  "postMessage",
]);
const envelopeSchemaAliases = Object.freeze({
  v1: "1.0",
});

export const RUNTIME_ENVELOPE_SCHEMA_VERSION = "1.0";
export const RUNTIME_ENVELOPE_COMPATIBILITY_MATRIX: EnvelopeCompatibilityMatrix = Object.freeze({
  current: RUNTIME_ENVELOPE_SCHEMA_VERSION,
  supported: [RUNTIME_ENVELOPE_SCHEMA_VERSION],
  aliases: envelopeSchemaAliases,
});

const nextToken = (prefix: string) => `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2, 10)}`;
const normalizeString = (value: string | null | undefined) => value?.trim() ?? "";

const lowerSnakeSegmentPattern = /^[a-z0-9_]+$/;
const versionSegmentPattern = /^v\d+$/;

const isLowerSnakeSegment = (segment: string) =>
  segment !== "" && lowerSnakeSegmentPattern.test(segment);

const isVersionSegment = (segment: string) =>
  versionSegmentPattern.test(segment);

export const normalizeSchemaVersion = (value: string | null | undefined): string => {
  const trimmed = normalizeString(value);
  if (trimmed === "") {
    return RUNTIME_ENVELOPE_SCHEMA_VERSION;
  }
  return envelopeSchemaAliases[trimmed as keyof typeof envelopeSchemaAliases] ?? trimmed;
};

export const isCompatibleSchemaVersion = (value: string | null | undefined): boolean =>
  RUNTIME_ENVELOPE_COMPATIBILITY_MATRIX.supported.includes(normalizeSchemaVersion(value));

export const assertCompatibleSchemaVersion = (value: string | null | undefined): string => {
  const normalized = normalizeSchemaVersion(value);
  if (!isCompatibleSchemaVersion(normalized)) {
    throw new Error(
      `unsupported envelope schema version "${normalizeString(value)}" (supported: ${RUNTIME_ENVELOPE_COMPATIBILITY_MATRIX.supported.join(", ")})`
    );
  }
  return normalized;
};

export const parseEventType = (eventType: string): ParsedEventType => {
  const raw = normalizeString(eventType);
  if (raw === "") {
    throw new Error("event type is required");
  }

  const segments = raw.split(":");
  if (segments.length < 3) {
    throw new Error(`event type "${raw}" must contain at least 3 segments`);
  }

  const state = segments[segments.length - 1] ?? "";
  if (!terminalStates.has(state)) {
    throw new Error(`event type "${raw}" has invalid terminal state "${state}"`);
  }

  const domain = segments[0] ?? "";
  if (!isLowerSnakeSegment(domain)) {
    throw new Error(`event type "${raw}" has invalid domain "${domain}"`);
  }

  let version: string | null = null;
  let actionSegments = segments.slice(1, -1);
  const possibleVersion = actionSegments[actionSegments.length - 1];
  if (possibleVersion && isVersionSegment(possibleVersion)) {
    version = possibleVersion;
    actionSegments = actionSegments.slice(0, -1);
  }

  if (actionSegments.length === 0) {
    throw new Error(`event type "${raw}" must include an action segment`);
  }
  if (!actionSegments.every(isLowerSnakeSegment)) {
    throw new Error(`event type "${raw}" contains invalid action segments`);
  }

  return {
    raw,
    domain,
    action: actionSegments.join(":"),
    version,
    state,
    segments,
  };
};

export const eventTerminalState = (eventType: string): string => parseEventType(eventType).state;

export const createEnvelope = <TPayload>(input: EnvelopeFactoryInput<TPayload>): RuntimeEnvelope<TPayload> => ({
  eventType: parseEventType(input.eventType).raw,
  payload: input.payload,
  payloadEncoding: "json",
  metadata: {
    correlationId: input.correlationId ?? nextToken("corr"),
    requestId: input.requestId ?? nextToken("req"),
    idempotencyKey: input.idempotencyKey ?? nextToken("idem"),
    schemaVersion: assertCompatibleSchemaVersion(input.schemaVersion),
    timestamp: new Date().toISOString(),
    extra: input.extra ?? {},
  },
});

export const createProtobufEnvelope = <TPayload>(
  input: EnvelopeFactoryInput<TPayload>,
  codec: ProtobufCodec<TPayload>
): RuntimeEnvelope<Uint8Array> => {
  const envelope = createEnvelope({
    ...input,
    payload: codec.encode(input.payload),
  });
  return {
    ...envelope,
    payloadEncoding: "protobuf",
  };
};

export const decodeProtobufPayload = <TPayload>(payload: unknown, codec: ProtobufCodec<TPayload>): TPayload => {
  if (payload instanceof Uint8Array) {
    return codec.decode(payload);
  }
  if (payload instanceof ArrayBuffer) {
    return codec.decode(new Uint8Array(payload));
  }
  throw new Error("protobuf payloads require Uint8Array transport bodies");
};

export const createRouteRegistry = (routes: RuntimeRoute[]) => {
  const normalizedRoutes = routes.map((route) => ({
    ...route,
    method: normalizeString(route.method).toUpperCase(),
    path: normalizeString(route.path),
    eventType: parseEventType(route.eventType).raw,
    transportOrder: route.transportOrder ? Array.from(new Set(route.transportOrder)) : Array.from(PERFORMANCE_TRANSPORT_ORDER),
  }));
  const byEventType = new Map<string, RuntimeRoute>();
  const byPath = new Map<string, RuntimeRoute>();

  for (const route of normalizedRoutes) {
    if (route.method === "" || route.path === "") {
      throw new Error(`route registration for "${route.eventType}" requires method and path`);
    }
    if (byEventType.has(route.eventType)) {
      throw new Error(`duplicate route registration for event type "${route.eventType}"`);
    }

    const pathKey = `${route.method}:${route.path}`;
    if (byPath.has(pathKey)) {
      throw new Error(`duplicate route registration for path "${pathKey}"`);
    }

    byEventType.set(route.eventType, route);
    byPath.set(pathKey, route);
  }

  return {
    routes: normalizedRoutes,
    resolveRoute: (eventType: string): RuntimeRoute | undefined => byEventType.get(eventType),
    resolveRouteByPath: (method: string, path: string): RuntimeRoute | undefined =>
      byPath.get(`${normalizeString(method).toUpperCase()}:${normalizeString(path)}`),
  };
};

export const resolveRoute = (registry: RouteRegistry, eventType: string): RuntimeRoute | undefined =>
  registry.resolveRoute(eventType);

export const canDispatch = (
  route: RuntimeRoute | undefined,
  grantedCapabilities: string[],
  hasPolicyAccess: (route: RuntimeRoute | undefined) => boolean
): boolean => {
  if (!route || !hasPolicyAccess(route)) {
    return false;
  }
  if (!route.requiredCapability) {
    return true;
  }
  if (grantedCapabilities.includes("*") || grantedCapabilities.includes(route.requiredCapability)) {
    return true;
  }
  const domain = route.requiredCapability.split(".")[0] ?? "";
  if (!domain) {
    return false;
  }
  if (grantedCapabilities.includes(`${domain}.*`)) {
    return true;
  }
  if (route.permission === "view") {
    const view = `${domain}.view`;
    const write = `${domain}.write`;
    const admin = `${domain}.admin`;
    return grantedCapabilities.some((capability) => capability === view || capability === write || capability === admin);
  }
  if (route.permission === "write") {
    const write = `${domain}.write`;
    const admin = `${domain}.admin`;
    return grantedCapabilities.some((capability) => capability === write || capability === admin);
  }
  return grantedCapabilities.includes(`${domain}.admin`);
};

const dispatchWithTimeout = async <TPayload>(
  strategy: TransportStrategy,
  envelope: RuntimeEnvelope<TPayload>,
  route: RuntimeRoute,
  timeoutMs: number
) => {
  const controller = new AbortController();
  const timeout = globalThis.setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await strategy.dispatch(envelope, route, controller.signal);
  } finally {
    globalThis.clearTimeout(timeout);
  }
};

export const createCommandBus = (options: CommandBusOptions) => {
  const strategiesByKind = new Map(options.strategies.map((strategy) => [strategy.kind, strategy]));

  return {
    async dispatch<TPayload>(envelope: RuntimeEnvelope<TPayload>): Promise<unknown> {
      const normalizedEnvelope: RuntimeEnvelope<TPayload> = {
        ...envelope,
        eventType: parseEventType(envelope.eventType).raw,
        metadata: {
          ...envelope.metadata,
          schemaVersion: assertCompatibleSchemaVersion(envelope.metadata.schemaVersion),
        },
      };
      const route = options.registry.resolveRoute(normalizedEnvelope.eventType);
      if (!route) {
        throw new Error(`route is not registered for ${normalizedEnvelope.eventType}`);
      }
      if (!canDispatch(route, options.grantedCapabilities, options.hasPolicyAccess)) {
        throw new Error(`dispatch is not allowed for ${normalizedEnvelope.eventType}`);
      }

      const transports = route.transportOrder ?? Array.from(PERFORMANCE_TRANSPORT_ORDER);
      let lastError: Error | null = null;

      for (let index = 0; index < transports.length; index += 1) {
        const transport = transports[index];
        const strategy = strategiesByKind.get(transport);
        if (!strategy) {
          continue;
        }

        const startedAt = performance.now();
        try {
          const result = await dispatchWithTimeout(
            strategy,
            normalizedEnvelope,
            route,
            options.defaultTimeoutMs ?? 3000
          );
          options.onDiagnostics?.({
            transport,
            fallback: index > 0,
            durationMs: performance.now() - startedAt,
            eventType: normalizedEnvelope.eventType,
            schemaVersion: normalizedEnvelope.metadata.schemaVersion,
            correlationId: normalizedEnvelope.metadata.correlationId,
            requestId: normalizedEnvelope.metadata.requestId,
            idempotencyKey: normalizedEnvelope.metadata.idempotencyKey,
            attempt: index + 1,
          });
          return result;
        } catch (error) {
          lastError = error instanceof Error ? error : new Error("transport dispatch failed");
          options.onDiagnostics?.({
            transport,
            fallback: index > 0,
            durationMs: performance.now() - startedAt,
            eventType: normalizedEnvelope.eventType,
            schemaVersion: normalizedEnvelope.metadata.schemaVersion,
            correlationId: normalizedEnvelope.metadata.correlationId,
            requestId: normalizedEnvelope.metadata.requestId,
            idempotencyKey: normalizedEnvelope.metadata.idempotencyKey,
            attempt: index + 1,
            error: lastError.message,
          });
        }
      }

      throw lastError ?? new Error(`no transport strategy could dispatch ${normalizedEnvelope.eventType}`);
    },
    async subscribe(
      pattern: string,
      callback: (envelope: RuntimeEnvelope<unknown>) => void
    ): Promise<Subscription> {
      const strategy = strategiesByKind.get("ws");
      if (!strategy || !strategy.subscribe) {
        throw new Error("websocket transport is required for subscriptions");
      }
      return strategy.subscribe(pattern, callback);
    },
  };
};

export * from "./binaryEnvelope";
export * from "./compression";
export * from "./http";
export * from "./offlineQueue";
export * from "./runtimeMetadata";
export * from "./websocket";

export * from "./stores/metadataStore";
export * from "./stores/eventStore";
