import { bench, describe } from "vitest";
import {
  createLocalProjectionNormalizer,
  createProjectionEventPipeline,
} from "./projectionWorkerPipeline";
import {
  createDummyDataFactory,
  createLiveProjectionBinding,
  createRuntimeWorkbench,
  createTenantProjectionStore,
  type DummySchemaSpec,
} from "./runtimeWorkbench";

type BenchRecord = {
  id: string;
  name?: string;
  email?: string;
  status?: string;
  balance?: number;
};

const schema: DummySchemaSpec = {
  domain: "finance",
  collection: "accounts",
  fields: [
    { name: "name", kind: "string" },
    { name: "email", kind: "email" },
    { name: "status", kind: "enum", enumValues: ["active", "paused", "closed"] },
    { name: "balance", kind: "float" },
  ],
};

const benchScope = {
  tenantId: "tenant-a",
  domain: "finance",
  collection: "accounts",
};

const patchMutations = Array.from({ length: 1000 }, (_, index) => ({
  operation: "patch" as const,
  tenantId: benchScope.tenantId,
  domain: benchScope.domain,
  collection: benchScope.collection,
  recordId: `acct-${index}`,
  version: index,
  sourceWatermark: `wm-${index}`,
  fields: { balance: index },
}));

const rawProjectionEvents = patchMutations.map((mutation) => ({
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

describe("frontend runtime workbench", () => {
  bench("dummy factory list 1k", () => {
    createDummyDataFactory<BenchRecord>(schema, { seed: "bench" }).list(1000);
  });

  bench("tenant projection apply 1k patches", () => {
    const store = createTenantProjectionStore<BenchRecord>(benchScope);

    for (const mutation of patchMutations) {
      store.apply(mutation);
    }
  });

  bench("tenant projection applyMany 1k patches", () => {
    const store = createTenantProjectionStore<BenchRecord>(benchScope);

    store.applyMany(patchMutations);
  });

  bench("tenant projection apply 1k with snapshot reads", () => {
    const store = createTenantProjectionStore<BenchRecord>(benchScope);

    for (const mutation of patchMutations) {
      store.apply(mutation);
      store.getSnapshot().records.length;
    }
  });

  bench("live projection binding apply 1k", () => {
    const store = createTenantProjectionStore<BenchRecord>(benchScope);
    const binding = createLiveProjectionBinding({ scope: store.scope(), store });

    for (const mutation of patchMutations) {
      binding.applyLiveMutation(mutation);
    }
  });

  bench("live projection binding applyMany 1k", () => {
    const store = createTenantProjectionStore<BenchRecord>(benchScope);
    const binding = createLiveProjectionBinding({ scope: store.scope(), store });

    binding.applyLiveMutations(patchMutations);
  });

  bench("projection event pipeline normalize 1k", async () => {
    let normalized = 0;
    const pipeline = createProjectionEventPipeline<BenchRecord>(
      benchScope,
      createLocalProjectionNormalizer(),
      () => {
        normalized += 1;
      },
      { maxBatchSize: 2000 }
    );

    for (const event of rawProjectionEvents) {
      pipeline.push(event);
    }
    await pipeline.flush();
    pipeline.close();
    if (normalized !== rawProjectionEvents.length) {
      throw new Error(`normalized ${normalized} of ${rawProjectionEvents.length}`);
    }
  });

  const workbench = createRuntimeWorkbench("runtime", {
    planCompute: () => ({ lane: "wasm-sab", reason: "bench" }),
    dispatchCompute: async () => 1,
  });

  bench("planned compute 1k", async () => {
    for (let index = 0; index < 1000; index += 1) {
      await workbench.dispatchCompute({ job: index, byteLength: 256 });
    }
  });
});
