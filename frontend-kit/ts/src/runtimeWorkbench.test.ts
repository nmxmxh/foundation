import { describe, expect, it } from "vitest";
import type { AsyncKeyValueStorage } from "./indexedDBStorage";
import {
  createProjectionWorkerNormalizer,
  installProjectionWorkerHandler,
  type ProjectionWorkerMessage,
  type ProjectionWorkerLike,
  type ProjectionWorkerResponse,
} from "./projectionWorkerPipeline";
import {
  createDummyDataFactory,
  createFakerDummyValueProvider,
  createGeneratedHookRegistry,
  createHermesProjectionAdapter,
  createLiveProjectionBinding,
  createPrototypeRuntimeCache,
  createRuntimeWorkbench,
  createTenantSnapshotPersistence,
  createTenantProjectionStore,
  normalizeHermesProjectionMutation,
  type DummySchemaSpec,
  type ProjectionMutation,
} from "./runtimeWorkbench";

type RecordFixture = {
  id: string;
  name?: string;
  email?: string;
  status?: string;
  password?: string;
  metadata?: unknown;
};

const schema: DummySchemaSpec = {
  domain: "finance",
  collection: "accounts",
  fields: [
    { name: "name", kind: "string" },
    { name: "email", kind: "email" },
    { name: "status", kind: "enum", enumValues: ["active", "paused"] },
    { name: "password", kind: "string" },
    { name: "metadata", kind: "object" },
  ],
};

const createMemoryStorage = (): AsyncKeyValueStorage & {
  values: Map<string, string>;
} => {
  const values = new Map<string, string>();
  return {
    values,
    async getItem(name) {
      return values.get(name) ?? null;
    },
    async setItem(name, value) {
      values.set(name, value);
    },
    async removeItem(name) {
      values.delete(name);
    },
  };
};

const waitForAsyncFlush = async (): Promise<void> => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));
};

describe("createDummyDataFactory", () => {
  it("generates deterministic records while omitting sensitive fields", () => {
    const factory = createDummyDataFactory<RecordFixture>(schema, { seed: "tenant-a" });

    expect(factory.create(0)).toEqual({
      id: "finance-accounts-1",
      name: "name-1",
      email: "email1@example.test",
      status: "active",
    });
    expect(factory.list(2).map((record) => record.id)).toEqual([
      "finance-accounts-1",
      "finance-accounts-2",
    ]);
  });

  it("accepts a faker-compatible provider without calling it for omitted secrets", () => {
    const providerCalls: string[] = [];
    const provider = createFakerDummyValueProvider({
      internet: {
        email: () => {
          providerCalls.push("email");
          return "provided@example.test";
        },
      },
      lorem: {
        words: () => {
          providerCalls.push("string");
          return "Provided Name";
        },
      },
      helpers: {
        arrayElement: (values) => {
          providerCalls.push("enum");
          return values[values.length - 1];
        },
      },
    });

    const record = createDummyDataFactory<RecordFixture>(schema, { provider }).create(0);

    expect(record).toMatchObject({
      name: "Provided Name",
      email: "provided@example.test",
      status: "paused",
    });
    expect(record.password).toBeUndefined();
    expect(providerCalls).toEqual(["string", "email", "enum"]);
  });
});

describe("createTenantProjectionStore", () => {
  it("applies matching tenant mutations and rejects stale or cross-tenant updates", () => {
    const store = createTenantProjectionStore<RecordFixture>({
      tenantId: "tenant-a",
      domain: "finance",
      collection: "accounts",
    });

    expect(
      store.apply({
        operation: "upsert",
        tenantId: "tenant-a",
        domain: "finance",
        collection: "accounts",
        recordId: "acct-1",
        version: 2,
        record: { id: "acct-1", name: "Primary" },
      })
    ).toBe(true);
    expect(store.getSnapshot().byId.get("acct-1")?.name).toBe("Primary");

    expect(
      store.apply({
        operation: "patch",
        tenantId: "tenant-b",
        domain: "finance",
        collection: "accounts",
        recordId: "acct-1",
        version: 3,
        fields: { name: "Wrong tenant" },
      })
    ).toBe(false);

    expect(
      store.apply({
        operation: "patch",
        tenantId: "tenant-a",
        domain: "finance",
        collection: "accounts",
        recordId: "acct-1",
        version: 1,
        fields: { name: "Stale" },
      })
    ).toBe(false);

    expect(store.getSnapshot().byId.get("acct-1")?.name).toBe("Primary");
    expect(
      store.apply({
        operation: "delete",
        tenantId: "tenant-a",
        domain: "finance",
        collection: "accounts",
        recordId: "acct-1",
        version: 3,
      })
    ).toBe(true);
    expect(store.getSnapshot().byId.has("acct-1")).toBe(false);
  });

  it("keeps old snapshots immutable while batching projection mutations", () => {
    const store = createTenantProjectionStore<RecordFixture>({
      tenantId: "tenant-a",
      domain: "finance",
      collection: "accounts",
    });

    const emptySnapshot = store.getSnapshot();
    const result = store.applyMany([
      {
        operation: "upsert",
        tenantId: "tenant-a",
        domain: "finance",
        collection: "accounts",
        recordId: "acct-1",
        version: 1,
        sourceWatermark: "wm-accepted",
        record: { id: "acct-1", name: "Primary" },
      },
      {
        operation: "patch",
        tenantId: "tenant-b",
        domain: "finance",
        collection: "accounts",
        recordId: "acct-2",
        version: 5,
        sourceWatermark: "wm-rejected",
        fields: { name: "Wrong tenant" },
      },
    ]);
    const nextSnapshot = store.getSnapshot();

    expect(result).toEqual({
      accepted: 1,
      rejected: 1,
      lastAcceptedVersion: 1,
      sourceWatermark: "wm-accepted",
    });
    expect(emptySnapshot.records).toEqual([]);
    expect(emptySnapshot.byId.has("acct-1")).toBe(false);
    expect(nextSnapshot.records).toEqual([{ id: "acct-1", name: "Primary" }]);
    expect(nextSnapshot.version).toBe(emptySnapshot.version + 1);
  });
});

describe("live projection bindings", () => {
  it("loads initial state, buffers live mutations, and exposes loading status", async () => {
    const store = createTenantProjectionStore<RecordFixture>({
      tenantId: "tenant-a",
      domain: "finance",
      collection: "accounts",
    });
    let listener: ((mutation: ProjectionMutation<RecordFixture>) => void) | undefined;
    const binding = createLiveProjectionBinding<RecordFixture>({
      scope: store.scope(),
      store,
      adapter: {
        async loadProjection() {
          listener?.({
            operation: "patch",
            tenantId: "tenant-a",
            domain: "finance",
            collection: "accounts",
            recordId: "acct-1",
            version: 2,
            fields: { name: "Live buffered" },
            sourceWatermark: "wm-live",
          });
          return {
            records: [{ id: "acct-1", name: "Snapshot" }],
            sourceWatermark: "wm-snapshot",
            version: 1,
          };
        },
        subscribeProjection(_scope, next) {
          listener = next;
          return () => {
            listener = undefined;
          };
        },
      },
    });

    const connect = binding.connect();
    expect(binding.getStatusSnapshot().status).toBe("loading");
    expect(binding.getStatusSnapshot().loading).toBe(true);
    await connect;

    expect(binding.getStatusSnapshot()).toMatchObject({
      status: "live",
      loading: false,
      appliedMutations: 1,
      rejectedMutations: 0,
      lastVersion: 2,
      sourceWatermark: "wm-snapshot",
    });
    expect(store.getSnapshot().byId.get("acct-1")?.name).toBe("Live buffered");
  });

  it("rejects cross-tenant live mutations and stops updates after disconnect", async () => {
    const store = createTenantProjectionStore<RecordFixture>({
      tenantId: "tenant-a",
      domain: "finance",
      collection: "accounts",
    });
    let listener: ((mutation: ProjectionMutation<RecordFixture>) => void) | undefined;
    const binding = createLiveProjectionBinding<RecordFixture>({
      scope: store.scope(),
      store,
      adapter: {
        subscribeProjection(_scope, next) {
          listener = next;
          return () => {
            listener = undefined;
          };
        },
      },
    });

    await binding.connect();
    listener?.({
      operation: "upsert",
      tenantId: "tenant-b",
      domain: "finance",
      collection: "accounts",
      recordId: "acct-1",
      version: 1,
      record: { id: "acct-1", name: "Wrong tenant" },
    });
    await waitForAsyncFlush();

    expect(binding.getStatusSnapshot().rejectedMutations).toBe(1);
    expect(store.getSnapshot().records).toEqual([]);

    binding.disconnect();
    expect(binding.getStatusSnapshot().status).toBe("closed");
    expect(listener).toBeUndefined();
  });

  it("batches live subscription mutations and degrades on queue saturation", async () => {
    const store = createTenantProjectionStore<RecordFixture>({
      tenantId: "tenant-a",
      domain: "finance",
      collection: "accounts",
    });
    let listener: ((mutation: ProjectionMutation<RecordFixture>) => void) | undefined;
    const binding = createLiveProjectionBinding<RecordFixture>({
      ingest: {
        flushIntervalMs: 0,
        maxBatchSize: 8,
        maxQueuedMutations: 4,
      },
      scope: store.scope(),
      store,
      adapter: {
        subscribeProjection(_scope, next) {
          listener = next;
          return () => {
            listener = undefined;
          };
        },
      },
    });

    await binding.connect();
    for (let index = 0; index < 5; index += 1) {
      listener?.({
        fields: { status: `live-${index}` },
        operation: "patch",
        tenantId: "tenant-a",
        domain: "finance",
        collection: "accounts",
        recordId: `acct-${index}`,
        version: index,
      });
    }

    expect(binding.getStatusSnapshot()).toMatchObject({
      droppedMutations: 1,
      rejectedMutations: 1,
      status: "degraded",
    });
    await waitForAsyncFlush();
    expect(binding.getStatusSnapshot().appliedMutations).toBe(4);
    expect(store.getSnapshot().records).toHaveLength(4);
  });

  it("fails closed when live adapters are missing", async () => {
    const store = createTenantProjectionStore<RecordFixture>({
      tenantId: "tenant-a",
      domain: "finance",
      collection: "accounts",
    });
    const binding = createLiveProjectionBinding<RecordFixture>({
      scope: store.scope(),
      store,
    });

    await binding.connect();

    expect(binding.getStatusSnapshot()).toMatchObject({
      status: "degraded",
      loading: false,
      error: "live projection adapter is not configured",
    });
    expect(store.getSnapshot().records).toEqual([]);
  });
});

describe("prototype runtime cache", () => {
  it("caches stores by tenant scope and resets tenant-owned state", () => {
    const cache = createPrototypeRuntimeCache();
    const scope = { tenantId: "tenant-a", domain: "finance", collection: "accounts" };
    const first = cache.getStore<RecordFixture>(scope, () => createTenantProjectionStore(scope));
    const second = cache.getStore<RecordFixture>(scope, () => {
      throw new Error("store should be cached");
    });

    expect(second).toBe(first);
    first.upsert({ id: "acct-1", name: "Cached" });
    cache.resetTenant("tenant-a");

    const replacement = cache.getStore<RecordFixture>(scope, () => createTenantProjectionStore(scope));
    expect(replacement).not.toBe(first);
    expect(replacement.getSnapshot().records).toEqual([]);
  });

  it("caches deterministic dummy records and bypasses cache for providers", () => {
    const cache = createPrototypeRuntimeCache();
    let builds = 0;
    const createRecords = () => {
      builds += 1;
      return createDummyDataFactory<RecordFixture>(schema, { tenantId: "tenant-a" }).list(2);
    };

    const first = cache.getDummyRecords(schema, { tenantId: "tenant-a" }, 2, createRecords);
    const second = cache.getDummyRecords(schema, { tenantId: "tenant-a" }, 2, createRecords);
    const providerBacked = cache.getDummyRecords(
      schema,
      { tenantId: "tenant-a", provider: { string: ({ index }) => `provider-${index}` } },
      2,
      createRecords
    );

    expect(second).toBe(first);
    expect(providerBacked).not.toBe(first);
    expect(builds).toBe(2);
  });
});

describe("tenant snapshot persistence", () => {
  it("persists and hydrates tenant snapshots while redacting sensitive fields", async () => {
    const storage = createMemoryStorage();
    const scope = { tenantId: "tenant-a", domain: "finance", collection: "accounts" };
    const store = createTenantProjectionStore<RecordFixture>(scope);
    const persistence = createTenantSnapshotPersistence({ storage, store });

    store.upsert({
      id: "acct-1",
      metadata: { internal: true },
      name: "Primary",
      password: "secret",
    });
    await expect(persistence.persist()).resolves.toBe(1);

    const raw = storage.values.get(persistence.getStorageKey()) ?? "";
    expect(raw).toContain("Primary");
    expect(raw).not.toContain("secret");
    expect(raw).not.toContain("metadata");

    const hydratedStore = createTenantProjectionStore<RecordFixture>(scope);
    const hydratedPersistence = createTenantSnapshotPersistence({
      storage,
      store: hydratedStore,
    });

    await expect(hydratedPersistence.hydrate()).resolves.toBe(1);
    expect(hydratedStore.getSnapshot().byId.get("acct-1")).toEqual({
      id: "acct-1",
      name: "Primary",
    });
  });

  it("clears tenant session state and persisted snapshots together", async () => {
    const storage = createMemoryStorage();
    const scope = { tenantId: "tenant-a", domain: "finance", collection: "accounts" };
    const store = createTenantProjectionStore<RecordFixture>(scope);
    const persistence = createTenantSnapshotPersistence({ storage, store });

    const stop = persistence.start();
    store.upsert({ id: "acct-1", name: "Primary" });
    await persistence.flush();
    expect(storage.values.has(persistence.getStorageKey())).toBe(true);

    await persistence.resetSession();

    expect(store.getSnapshot().records).toEqual([]);
    expect(storage.values.has(persistence.getStorageKey())).toBe(false);
    stop();
  });

  it("fails closed on cross-scope persisted snapshots", async () => {
    const storage = createMemoryStorage();
    const scope = { tenantId: "tenant-a", domain: "finance", collection: "accounts" };
    const store = createTenantProjectionStore<RecordFixture>(scope);
    const persistence = createTenantSnapshotPersistence({ storage, store });

    await storage.setItem(
      persistence.getStorageKey(),
      JSON.stringify({
        records: [{ id: "acct-1", name: "Wrong tenant" }],
        savedAt: new Date().toISOString(),
        schemaVersion: "foundation.frontend.tenant_snapshot.v1",
        scope: { ...scope, tenantId: "tenant-b" },
        storeVersion: 1,
      })
    );

    await expect(persistence.hydrate()).resolves.toBe(0);
    expect(store.getSnapshot().records).toEqual([]);
    expect(storage.values.has(persistence.getStorageKey())).toBe(false);
  });
});

describe("Hermes projection adapter", () => {
  it("normalizes envelope-shaped Hermes projections into tenant mutations", async () => {
    const events: unknown[] = [];
    const adapter = createHermesProjectionAdapter({
      async loadProjection() {
        return {
          payload: {
            records: [{ id: "acct-1", name: "Loaded" }],
            source_watermark: "wm-1",
            version: 1,
          },
        };
      },
      subscribeProjection(_scope, listener) {
        events.push(listener);
        return () => undefined;
      },
    });
    const scope = { tenantId: "tenant-a", domain: "finance", collection: "accounts" };

    await expect(adapter.loadProjection?.<RecordFixture>(scope, { scope })).resolves.toEqual({
      records: [{ id: "acct-1", name: "Loaded" }],
      sourceWatermark: "wm-1",
      version: 1,
    });
    expect(events).toHaveLength(0);
  });

  it("rejects conflicting tenant projection envelopes", () => {
    const scope = { tenantId: "tenant-a", domain: "finance", collection: "accounts" };

    expect(
      normalizeHermesProjectionMutation<RecordFixture>(
        {
          payload: {
            operation: "upsert",
            tenant_id: "tenant-b",
            domain: "finance",
            collection: "accounts",
            record_id: "acct-1",
            version: 1,
            record: { id: "acct-1" },
          },
        },
        scope
      )
    ).toBeUndefined();
  });

  it("normalizes subscription events through a worker-backed bounded pipeline", async () => {
    let workerListener: ((event: MessageEvent<ProjectionWorkerMessage>) => void) | undefined;
    const worker: ProjectionWorkerLike = {
      postMessage(message: ProjectionWorkerMessage) {
        workerListener?.(new MessageEvent("message", { data: message }));
      },
      addEventListener(_type: "message", listener: (event: MessageEvent<ProjectionWorkerResponse>) => void) {
        const target = {
          addEventListener(
            _targetType: "message",
            handler: (event: MessageEvent<ProjectionWorkerMessage>) => void
          ) {
            workerListener = handler;
          },
          postMessage(response: ProjectionWorkerResponse) {
            setTimeout(() => {
              listener(new MessageEvent("message", { data: response }));
            }, 0);
          },
        };
        installProjectionWorkerHandler(target);
      },
    };
    const normalizer = createProjectionWorkerNormalizer(worker, {
      maxPendingRequests: 2,
      timeoutMs: 50,
    });
    const scope = { tenantId: "tenant-a", domain: "finance", collection: "accounts" };
    let sourceListener: ((event: unknown) => void) | undefined;
    const adapter = createHermesProjectionAdapter(
      {
        subscribeProjection(_scope, listener) {
          sourceListener = listener;
          return () => {
            sourceListener = undefined;
          };
        },
      },
      {
        eventPipeline: { maxBatchSize: 2, flushIntervalMs: 0 },
        normalizer,
      }
    );
    const mutations: ProjectionMutation<RecordFixture>[] = [];
    const unsubscribe = adapter.subscribeProjection?.<RecordFixture>(scope, (mutation) => {
      mutations.push(mutation);
    });

    sourceListener?.({
      payload: {
        collection: "accounts",
        domain: "finance",
        operation: "patch",
        record_id: "acct-1",
        tenant_id: "tenant-a",
        version: 1,
        fields: { name: "Worker normalized" },
      },
    });
    await waitForAsyncFlush();

    expect(mutations).toHaveLength(1);
    expect(mutations[0]?.fields?.name).toBe("Worker normalized");
    expect(normalizer.getSnapshot()).toMatchObject({
      fallbackRuns: 0,
      processedBatches: 1,
      processedEvents: 1,
    });
    unsubscribe?.();
    normalizer.close();
  });

  it("falls back to local projection normalization when a worker times out", async () => {
    const worker: ProjectionWorkerLike = {
      postMessage(_message: ProjectionWorkerMessage) {
        return undefined;
      },
      addEventListener(
        _type: "message",
        _listener: (event: MessageEvent<ProjectionWorkerResponse>) => void
      ) {
        return undefined;
      },
    };
    const normalizer = createProjectionWorkerNormalizer(worker, {
      fallback: "local",
      maxPendingRequests: 1,
      timeoutMs: 1,
    });
    const scope = { tenantId: "tenant-a", domain: "finance", collection: "accounts" };

    await expect(
      normalizer.normalizeMutations<RecordFixture>(
        [
          {
            payload: {
              collection: "accounts",
              domain: "finance",
              operation: "patch",
              record_id: "acct-1",
              tenant_id: "tenant-a",
              version: 1,
              fields: { status: "fallback" },
            },
          },
        ],
        scope
      )
    ).resolves.toMatchObject([{ recordId: "acct-1" }]);
    expect(normalizer.getSnapshot().fallbackRuns).toBe(1);
    normalizer.close();
  });
});

describe("Hermes live projection integration fixture", () => {
  it("loads, subscribes, persists, hydrates, and resets a tenant projection", async () => {
    const storage = createMemoryStorage();
    const scope = { tenantId: "tenant-a", domain: "finance", collection: "accounts" };
    const store = createTenantProjectionStore<RecordFixture>(scope);
    const persistence = createTenantSnapshotPersistence({ storage, store });
    let listener: ((event: unknown) => void) | undefined;
    const adapter = createHermesProjectionAdapter({
      async loadProjection() {
        listener?.({
          payload: {
            collection: "accounts",
            domain: "finance",
            operation: "patch",
            record_id: "acct-1",
            tenant_id: "tenant-a",
            version: 2,
            fields: { status: "live" },
          },
        });
        return {
          payload: {
            records: [{ id: "acct-1", name: "Backend snapshot", status: "loaded" }],
            source_watermark: "wm-backend",
            version: 1,
          },
        };
      },
      subscribeProjection(_scope, next) {
        listener = next;
        return () => {
          listener = undefined;
        };
      },
    });
    const binding = createLiveProjectionBinding<RecordFixture>({
      adapter,
      scope,
      store,
    });

    await binding.connect();
    expect(binding.getStatusSnapshot()).toMatchObject({
      appliedMutations: 1,
      sourceWatermark: "wm-backend",
      status: "live",
    });
    expect(store.getSnapshot().byId.get("acct-1")).toMatchObject({
      name: "Backend snapshot",
      status: "live",
    });

    await persistence.persist();
    const hydratedStore = createTenantProjectionStore<RecordFixture>(scope);
    const hydratedPersistence = createTenantSnapshotPersistence({
      storage,
      store: hydratedStore,
    });
    await expect(hydratedPersistence.hydrate()).resolves.toBe(1);
    expect(hydratedStore.getSnapshot().byId.get("acct-1")?.status).toBe("live");

    await persistence.resetSession();
    expect(store.getSnapshot().records).toEqual([]);
    expect(storage.values.has(persistence.getStorageKey())).toBe(false);
  });

  it("fails closed when backend projection loading fails", async () => {
    const scope = { tenantId: "tenant-a", domain: "finance", collection: "accounts" };
    const store = createTenantProjectionStore<RecordFixture>(scope);
    const binding = createLiveProjectionBinding<RecordFixture>({
      adapter: createHermesProjectionAdapter({
        async loadProjection() {
          throw new Error("backend projection unavailable");
        },
      }),
      scope,
      store,
    });

    await binding.connect();

    expect(binding.getStatusSnapshot()).toMatchObject({
      error: "backend projection unavailable",
      loading: false,
      status: "error",
    });
    expect(store.getSnapshot().records).toEqual([]);
  });
});

describe("runtime workbench hooks", () => {
  it("registers generated stores and exposes runtime mode", () => {
    const registry = createGeneratedHookRegistry<{ useSnapshot(): unknown }>();
    registry.register("accounts", { useSnapshot: () => ({ records: [] }) });

    expect(registry.use("accounts")).toEqual({ records: [] });
    expect(() => registry.use("missing")).toThrow("generated store is not registered");
    expect(createRuntimeWorkbench("runtime", { dispatchCompute: async () => ({ ok: true }) }).mode).toBe("runtime");
  });

  it("routes runtime compute through a plan before dispatch", async () => {
    const seen: unknown[] = [];
    const workbench = createRuntimeWorkbench("runtime", {
      planCompute(request) {
        seen.push(request);
        return { lane: "wasm-sab", reason: "fits local runtime arena", deadlineRisk: "low" };
      },
      async dispatchCompute(request, plan) {
        return { job: request.job, lane: plan.lane };
      },
    });

    await expect(workbench.dispatchCompute({ job: { operation: "score" }, byteLength: 1024 }))
      .resolves.toEqual({ job: { operation: "score" }, lane: "wasm-sab" });
    expect(seen).toHaveLength(1);
  });

  it("fails closed when compute has no runtime adapter", async () => {
    const workbench = createRuntimeWorkbench("dummy");

    expect(workbench.planCompute({ job: "score" }).lane).toBe("disabled");
    await expect(workbench.dispatchCompute({ job: "score" })).rejects.toThrow("runtime compute is disabled");
  });
});
