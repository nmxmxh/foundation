import { describe, expect, it } from "vitest";

import {
  createLocalProjectionNormalizer,
  createProjectionEventPipeline,
} from "./projectionWorkerPipeline";
import {
  createDummyDataFactory,
  createPrototypeRuntimeCache,
  createTenantProjectionStore,
  type DummySchemaSpec,
  type ProjectionMutation,
} from "./runtimeWorkbench";

type ProfileRecord = {
  id: string;
  name?: string;
  email?: string;
  balance?: number;
};

type RuntimeWithMemory = typeof globalThis & {
  gc?: () => void;
  process?: {
    memoryUsage?: () => {
      heapUsed: number;
    };
  };
};

const runtime = globalThis as RuntimeWithMemory;

const schema: DummySchemaSpec = {
  domain: "finance",
  collection: "accounts",
  fields: [
    { name: "name", kind: "string" },
    { name: "email", kind: "email" },
    { name: "balance", kind: "float" },
  ],
};

const profileMutations: ProjectionMutation<ProfileRecord>[] = Array.from(
  { length: 5000 },
  (_, index) => ({
    collection: "accounts",
    domain: "finance",
    fields: { balance: index },
    operation: "patch",
    recordId: `acct-${index}`,
    sourceWatermark: `wm-${index}`,
    tenantId: "tenant-profile",
    version: index,
  })
);

const profileRawEvents = profileMutations.map((mutation) => ({
  payload: {
    collection: mutation.collection,
    domain: mutation.domain,
    fields: mutation.fields,
    operation: mutation.operation,
    record_id: mutation.recordId,
    source_watermark: mutation.sourceWatermark,
    tenant_id: mutation.tenantId,
    version: mutation.version,
  },
}));

const heapUsed = (): number => {
  runtime.gc?.();
  return runtime.process?.memoryUsage?.().heapUsed ?? 0;
};

const profileHeap = async (name: string, run: () => void | Promise<void>): Promise<number> => {
  const before = heapUsed();
  await run();
  const after = heapUsed();
  const delta = Math.max(0, after - before);
  console.log(`PROFILE\t${name}\t${delta}\tbytes`);
  return delta;
};

describe("frontend workbench allocation profile", () => {
  it("keeps generated dummy and store paths within bounded retained heap", async () => {
    const dummyDelta = await profileHeap("dummy_factory_list_5000_heap_delta", () => {
      createDummyDataFactory<ProfileRecord>(schema, { seed: "profile" }).list(5000);
    });

    const applyDelta = await profileHeap("tenant_projection_apply_5000_heap_delta", () => {
      const store = createTenantProjectionStore<ProfileRecord>({
        tenantId: "tenant-profile",
        domain: "finance",
        collection: "accounts",
      });
      for (const mutation of profileMutations) {
        store.apply(mutation);
      }
    });

    const applyManyDelta = await profileHeap("tenant_projection_apply_many_5000_heap_delta", () => {
      const store = createTenantProjectionStore<ProfileRecord>({
        tenantId: "tenant-profile",
        domain: "finance",
        collection: "accounts",
      });
      store.applyMany(profileMutations);
    });

    const pipelineDelta = await profileHeap("projection_event_pipeline_5000_heap_delta", async () => {
      const pipeline = createProjectionEventPipeline<ProfileRecord>(
        { tenantId: "tenant-profile", domain: "finance", collection: "accounts" },
        createLocalProjectionNormalizer(),
        () => undefined,
        { maxBatchSize: 10000 }
      );
      for (const event of profileRawEvents) {
        pipeline.push(event);
      }
      await pipeline.flush();
      pipeline.close();
    });

    const resetDelta = await profileHeap("runtime_cache_reset_50_tenants_heap_delta", () => {
      const cache = createPrototypeRuntimeCache();
      for (let index = 0; index < 50; index += 1) {
        const scope = {
          collection: "accounts",
          domain: "finance",
          tenantId: `tenant-${index}`,
        };
        const store = cache.getStore<ProfileRecord>(scope, () => createTenantProjectionStore(scope));
        store.upsert({ id: `acct-${index}`, name: "Profile" });
        cache.resetTenant(scope.tenantId);
      }
      cache.clear();
    });

    expect(dummyDelta).toBeLessThan(32 * 1024 * 1024);
    expect(applyDelta).toBeLessThan(16 * 1024 * 1024);
    expect(applyManyDelta).toBeLessThan(16 * 1024 * 1024);
    expect(pipelineDelta).toBeLessThan(16 * 1024 * 1024);
    expect(resetDelta).toBeLessThan(8 * 1024 * 1024);
  });
});
