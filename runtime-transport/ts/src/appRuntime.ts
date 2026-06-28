import {
  canDispatch as canDispatchRoute,
  createCommandBus,
  createEnvelope,
  type RouteRegistry,
  type RuntimeEnvelope,
  type RuntimeRoute,
  type Subscription,
  type TransportDiagnostics,
  type TransportStrategy,
} from "./index";

/**
 * createAppRuntime is the thin, opinionated facade that collapses the per-app
 * dispatch wiring into one call. It composes the generated route registry, the
 * transport strategies, and the command bus, and exposes an ergonomic
 * `dispatch(eventType, payload)` that builds a RuntimeEnvelope and routes it.
 *
 * It replaces hand-rolled runtime seams that re-derive method/path from the
 * eventType in TypeScript (which drifts from server-kit's catalog.go): the bus
 * resolves the real route — including custom paths — from the generated
 * registry, so the client never re-derives routing.
 *
 * Actor scope (auth, tenant/edition, role headers) is NOT a concern of this
 * facade: it rides the transport's own `getHeaders` callback, so the app
 * configures it once where it builds `createHTTPTransport`. The transports stay
 * the app's to construct because their endpoints and headers are app-specific.
 */
export type AppRuntimeOptions = {
  /** The app route registry, typically `createAppRouteRegistry()` from the generated runtimeRoutes.ts. */
  registry: RouteRegistry;
  /** Ordered transport strategies (e.g. http, ws). At least one is required. */
  strategies: TransportStrategy[];
  /**
   * Capabilities the current actor holds, used for the client-side dispatch
   * gate. Defaults to `["*"]` (allow-all) so the facade works out of the box.
   * The client gate is a UX affordance only — server-side authorization (CP-20)
   * remains the security boundary. Pass the real granted capabilities in
   * production so the UI can disable commands the actor cannot perform.
   */
  grantedCapabilities?: string[];
  /** Optional policy predicate layered on top of capability checks. Defaults to allow. */
  hasPolicyAccess?: (route: RuntimeRoute | undefined) => boolean;
  /** Per-attempt transport diagnostics (transport chosen, fallback, timing). */
  onDiagnostics?: (diagnostics: TransportDiagnostics) => void;
  /** Per-dispatch timeout in milliseconds. */
  defaultTimeoutMs?: number;
};

/** Optional per-dispatch envelope overrides; sensible tokens are generated when omitted. */
export type DispatchOverrides = {
  correlationId?: string;
  requestId?: string;
  idempotencyKey?: string;
  schemaVersion?: string;
  extra?: Record<string, unknown>;
};

/**
 * The command ack plus the correlation/idempotency tokens that were sent. Durable
 * state still arrives via projections, not this return value.
 */
export type DispatchResult<TData> = {
  data: TData;
  correlationId: string;
  requestId: string;
  idempotencyKey: string;
};

export type AppRuntime = {
  /** Emit a command for `eventType`. The route (method/path) is resolved from the registry. */
  dispatch: <TData = unknown, TPayload = unknown>(
    eventType: string,
    payload?: TPayload,
    overrides?: DispatchOverrides,
  ) => Promise<DispatchResult<TData>>;
  /** Subscribe to a projection/event pattern over the websocket transport. */
  subscribe: (pattern: string, callback: (envelope: RuntimeEnvelope<unknown>) => void) => Promise<Subscription>;
  /** Whether the current actor may dispatch `eventType` (for disabling UI affordances). */
  canDispatch: (eventType: string) => boolean;
  /** Resolve the route for an eventType, or undefined if not registered. */
  resolveRoute: (eventType: string) => RuntimeRoute | undefined;
  /** The underlying command bus, for advanced use. */
  bus: ReturnType<typeof createCommandBus>;
};

const ALLOW_ALL_CAPABILITIES = Object.freeze(["*"]);

export const createAppRuntime = (options: AppRuntimeOptions): AppRuntime => {
  if (!options.registry) {
    throw new Error("createAppRuntime requires a route registry");
  }
  if (!options.strategies || options.strategies.length === 0) {
    throw new Error("createAppRuntime requires at least one transport strategy");
  }

  const grantedCapabilities = options.grantedCapabilities ?? Array.from(ALLOW_ALL_CAPABILITIES);
  const hasPolicyAccess = options.hasPolicyAccess ?? (() => true);

  const bus = createCommandBus({
    registry: options.registry,
    strategies: options.strategies,
    grantedCapabilities,
    hasPolicyAccess,
    onDiagnostics: options.onDiagnostics,
    defaultTimeoutMs: options.defaultTimeoutMs,
  });

  const dispatch = async <TData = unknown, TPayload = unknown>(
    eventType: string,
    payload?: TPayload,
    overrides?: DispatchOverrides,
  ): Promise<DispatchResult<TData>> => {
    const envelope = createEnvelope<TPayload>({
      eventType,
      payload: (payload ?? {}) as TPayload,
      correlationId: overrides?.correlationId,
      requestId: overrides?.requestId,
      idempotencyKey: overrides?.idempotencyKey,
      schemaVersion: overrides?.schemaVersion,
      extra: overrides?.extra,
    });
    const data = (await bus.dispatch(envelope)) as TData;
    return {
      data,
      correlationId: envelope.metadata.correlationId,
      requestId: envelope.metadata.requestId,
      idempotencyKey: envelope.metadata.idempotencyKey,
    };
  };

  return {
    dispatch,
    subscribe: (pattern, callback) => bus.subscribe(pattern, callback),
    canDispatch: (eventType) =>
      canDispatchRoute(options.registry.resolveRoute(eventType), grantedCapabilities, hasPolicyAccess),
    resolveRoute: (eventType) => options.registry.resolveRoute(eventType),
    bus,
  };
};
