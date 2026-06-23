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
  // Set false to force offline 'dummy' mode even when a gateway origin resolves.
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
  // When no origin resolves (or live is disabled) the prototype stays offline.
  // createDefaultProjectionSource returns a HermesProjectionSource-shaped object,
  // so this assigns with no cast at the seam.
  const hermesSource: HermesProjectionSource | undefined =
    options.hermesSource ??
    (options.live === false
      ? undefined
      : createDefaultProjectionSource({
          baseUrl: options.projectionBaseUrl,
          originUrl: import.meta.env.VITE_API_URL,
          headers: options.projectionHeaders,
          query: options.projectionQuery,
          onStatus: handleStatus,
        }))

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

export const offlinePrototypeRuntime = createPrototypeRuntimeContext()
