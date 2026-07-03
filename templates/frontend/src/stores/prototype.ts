import {
  createHermesProjectionAdapter,
  createTenantSnapshotPersistence,
  type AsyncKeyValueStorage,
  type HermesProjectionSource,
  type LiveProjectionBinding,
  type ProjectionNormalizer,
  type PrototypeMode,
  type RuntimeWorkbenchAdapter,
  type TenantSnapshotPersistence,
} from '@ovasabi/frontend-kit'
import {
  createDefaultProjectionSource,
  type ProjectionTransportStatus,
} from '@ovasabi/runtime-transport'
import {
  createPrototypeRuntimeWorkbench,
  createPrototypeTenantStores,
  prototypeBenchmarkFixtures,
  prototypeDomains,
  prototypeRuntimeCache,
  prototypeSchemaRuntimeConstants,
  prototypeSchemas,
} from '../generated/prototypeRuntime'

export const DEFAULT_PROTOTYPE_TENANT_ID = 'tenant-prototype'

export type PrototypeRuntimeContextOptions = {
  tenantId?: string
  hermesSource?: HermesProjectionSource
  mode?: PrototypeMode
  autoConnect?: boolean
  dummyRecordCount?: number
  storage?: AsyncKeyValueStorage
  projectionNormalizer?: ProjectionNormalizer
  liveApplyBatchSize?: number
  // Notified of every projection transport state change (connecting/live/
  // reconnecting/degraded/closed). The runtime already reconciles dropped state
  // internally; this is for surfacing connection health to the UI.
  onProjectionStatus?: (status: ProjectionTransportStatus) => void
  // Per-request auth/tenant headers for the snapshot endpoint and query params
  // for the WebSocket connection (e.g. a bearer token).
  projectionHeaders?: () => Record<string, string> | Promise<Record<string, string>>
  projectionQuery?: () => Record<string, string> | Promise<Record<string, string>>
  // Tri-state liveness. Unset (default): go live only when a credential
  // provider (projectionHeaders or projectionQuery) is supplied — the
  // projection gateway scopes every read by the authenticated organization, so
  // a context without credentials can only ever collect 401s. true: force live
  // without credentials (e.g. a custom unauthenticated gateway). false: always
  // offline.
  live?: boolean
  // Override the derived gateway base (e.g. a dedicated projection host). When
  // unset the endpoints derive from VITE_API_URL / the page origin.
  projectionBaseUrl?: string
}

export function createPrototypeRuntimeContext(
  options: PrototypeRuntimeContextOptions = {},
) {
  const tenantId = options.tenantId ?? DEFAULT_PROTOTYPE_TENANT_ID

  // The closed communication loop: connect -> snapshot -> live deltas, and on a
  // server "degraded" signal (frames were dropped for this client) re-run each
  // binding's connect() to re-snapshot and reconcile. liveBindings is assigned
  // below; reconcile reads it lazily so the onStatus closure can reference it.
  let liveBindings: LiveProjectionBinding<Record<string, unknown>>[] = []
  const handleStatus = (status: ProjectionTransportStatus) => {
    options.onProjectionStatus?.(status)
    if (status.phase === 'degraded') {
      for (const binding of liveBindings) {
        void binding.connect()
      }
    }
  }

  // Prefer an explicitly supplied source; otherwise derive the gateway endpoints
  // from VITE_API_URL / the page origin plus the standard /v1/projections path.
  // Liveness is gated on auth availability, not origin resolvability: in a
  // browser an origin always resolves, so keying on the origin alone would
  // send every domain's snapshot + subscribe unauthenticated at startup (N
  // domains x every load x every retry — a self-inflicted request storm).
  // createDefaultProjectionSource returns a HermesProjectionSource-shaped object,
  // so this assigns with no cast at the seam.
  const wantsLive =
    options.live ?? Boolean(options.projectionHeaders ?? options.projectionQuery)
  const hermesSource: HermesProjectionSource | undefined =
    options.hermesSource ??
    (wantsLive
      ? createDefaultProjectionSource({
          baseUrl: options.projectionBaseUrl,
          originUrl: import.meta.env.VITE_API_URL,
          headers: options.projectionHeaders,
          query: options.projectionQuery,
          onStatus: handleStatus,
        })
      : undefined)

  const adapter: RuntimeWorkbenchAdapter | undefined = hermesSource
    ? createHermesProjectionAdapter(hermesSource, {
        normalizer: options.projectionNormalizer,
      })
    : undefined
  const mode = options.mode ?? (adapter ? 'live' : 'dummy')
  const stores = createPrototypeTenantStores(
    tenantId,
    prototypeRuntimeCache,
    options.dummyRecordCount ?? 8,
  )
  const storage = options.storage
  const persistence: TenantSnapshotPersistence<Record<string, unknown>>[] =
    storage
      ? stores.map((store) =>
          createTenantSnapshotPersistence({
            storage,
            store,
          }),
        )
      : []
  liveBindings = adapter
    ? prototypeDomains.map((domain) =>
        domain.connectLiveProjection({
          adapter,
          // Default to auto-connecting when the live path is configured so the
          // loop runs without extra app wiring.
          autoConnect: options.autoConnect ?? true,
          ingest: {
            maxBatchSize: options.liveApplyBatchSize ?? 128,
          },
          tenantId,
        }),
      )
    : []
  const workbench = createPrototypeRuntimeWorkbench(mode, adapter)

  return {
    adapter,
    benchmarkFixtures: prototypeBenchmarkFixtures,
    cache: prototypeRuntimeCache,
    domains: prototypeDomains,
    liveBindings,
    mode,
    persistence,
    hydratePersistence: async () => {
      await Promise.all(persistence.map((binding) => binding.hydrate()))
    },
    resetTenant: async () => {
      await Promise.all(persistence.map((binding) => binding.resetSession()))
      prototypeRuntimeCache.resetTenant(tenantId)
    },
    schemaRuntimeConstants: prototypeSchemaRuntimeConstants,
    schemas: prototypeSchemas,
    startPersistence: () => {
      const stops = persistence.map((binding) => binding.start())
      return () => {
        stops.forEach((stop) => stop())
      }
    },
    stores,
    tenantId,
    workbench,
  }
}

// Offline by construction, not by circumstance: live is pinned false so this
// module-level singleton can never open network connections at import time.
// Apps go live by building their own context after auth is available:
//   createPrototypeRuntimeContext({ projectionHeaders: () => authHeaders() })
export const offlinePrototypeRuntime = createPrototypeRuntimeContext({ live: false })
