import {
  createHermesProjectionAdapter,
  createTenantSnapshotPersistence,
  type AsyncKeyValueStorage,
  type HermesProjectionSource,
  type ProjectionNormalizer,
  type PrototypeMode,
  type RuntimeWorkbenchAdapter,
  type TenantSnapshotPersistence,
} from '@ovasabi/frontend-kit'
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
}

export function createPrototypeRuntimeContext(
  options: PrototypeRuntimeContextOptions = {},
) {
  const tenantId = options.tenantId ?? DEFAULT_PROTOTYPE_TENANT_ID
  const adapter: RuntimeWorkbenchAdapter | undefined = options.hermesSource
    ? createHermesProjectionAdapter(options.hermesSource, {
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
  const liveBindings = adapter
    ? prototypeDomains.map((domain) =>
        domain.connectLiveProjection({
          adapter,
          autoConnect: options.autoConnect ?? false,
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
