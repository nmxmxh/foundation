import { useSyncExternalStore } from "react";

import {
  createIndexedDBStorage,
  type AsyncKeyValueStorage,
  type IndexedDBStorageOptions,
} from "./indexedDBStorage";
import type { ProjectionEventPipelineOptions, ProjectionNormalizer } from "./projectionWorkerPipeline";

export type PrototypeMode = "dummy" | "live" | "runtime";

export type DummyFieldKind =
  | "string"
  | "text"
  | "email"
  | "url"
  | "uuid"
  | "int"
  | "float"
  | "bool"
  | "timestamp"
  | "enum"
  | "bytes"
  | "object";

export type DummyFieldSpec = {
  name: string;
  kind: DummyFieldKind;
  repeated?: boolean;
  optional?: boolean;
  enumValues?: readonly string[];
  dummy?: unknown;
  allowSensitiveDummy?: boolean;
};

export type DummySchemaSpec = {
  domain: string;
  collection: string;
  idField?: string;
  fields: readonly DummyFieldSpec[];
};

export type DummyValueRequest = {
  field: DummyFieldSpec;
  index: number;
  schema: DummySchemaSpec;
  tenantId?: string;
  seed: number;
};

export type DummyValueProvider = Partial<
  Record<DummyFieldKind, (request: DummyValueRequest) => unknown>
> & {
  value?(kind: DummyFieldKind, request: DummyValueRequest): unknown;
};

export type FakerLike = {
  string?: {
    uuid?: () => string;
  };
  internet?: {
    email?: (options?: { firstName?: string; lastName?: string }) => string;
    url?: () => string;
  };
  lorem?: {
    sentence?: (wordCount?: number) => string;
    words?: (wordCount?: number) => string;
  };
  number?: {
    int?: (options?: { min?: number; max?: number }) => number;
    float?: (options?: { min?: number; max?: number; fractionDigits?: number }) => number;
  };
  datatype?: {
    boolean?: () => boolean;
  };
  date?: {
    recent?: (options?: { days?: number }) => Date;
  };
  helpers?: {
    arrayElement?: <T>(values: readonly T[]) => T;
  };
};

export type DummyDataFactoryOptions = {
  tenantId?: string;
  seed?: string;
  sensitiveFieldNames?: readonly string[];
  provider?: DummyValueProvider;
};

export type DomainStoreSnapshot<TRecord> = {
  records: readonly TRecord[];
  byId: ReadonlyMap<string, TRecord>;
  version: number;
};

export type DomainStore<TRecord> = {
  getSnapshot(): DomainStoreSnapshot<TRecord>;
  get(id: string): TRecord | undefined;
  subscribe(listener: () => void): () => void;
  useSnapshot(): DomainStoreSnapshot<TRecord>;
  upsert(record: TRecord): void;
  remove(id: string): void;
  replace(records: readonly TRecord[]): void;
  reset(): void;
  batch(run: () => void): void;
};

export type ProjectionMutationOperation = "upsert" | "patch" | "delete";

export type ProjectionMutation<TRecord extends Record<string, unknown>> = {
  operation: ProjectionMutationOperation;
  tenantId: string;
  domain: string;
  collection: string;
  recordId: string;
  version: number;
  sourceWatermark?: string;
  epoch?: number;
  fields?: Partial<TRecord>;
  record?: TRecord;
};

export type ProjectionScope = {
  tenantId: string;
  domain: string;
  collection: string;
};

export type TenantProjectionStore<TRecord extends Record<string, unknown>> = DomainStore<TRecord> & {
  apply(mutation: ProjectionMutation<TRecord>): boolean;
  applyMany(mutations: readonly ProjectionMutation<TRecord>[]): ProjectionApplyResult;
  scope(): ProjectionScope;
};

export type ProjectionApplyResult = {
  accepted: number;
  rejected: number;
  lastAcceptedVersion?: number;
  sourceWatermark?: string;
};

export type ProjectionLoadRequest = {
  scope: ProjectionScope;
  sinceWatermark?: string;
  limit?: number;
  signal?: AbortSignal;
};

export type ProjectionLoadResult<TRecord extends Record<string, unknown>> = {
  records?: readonly TRecord[];
  mutations?: readonly ProjectionMutation<TRecord>[];
  sourceWatermark?: string;
  version?: number;
};

export type LiveProjectionStatus =
  | "idle"
  | "loading"
  | "live"
  | "degraded"
  | "closed"
  | "error";

export type LiveProjectionSnapshot = {
  scope: ProjectionScope;
  status: LiveProjectionStatus;
  loading: boolean;
  appliedMutations: number;
  rejectedMutations: number;
  queuedMutations?: number;
  droppedMutations?: number;
  lastVersion: number;
  sourceWatermark?: string;
  error?: string;
  connectedAt?: string;
  updatedAt: string;
};

export type LiveProjectionBindingOptions<TRecord extends Record<string, unknown>> = {
  scope: ProjectionScope;
  store: TenantProjectionStore<TRecord>;
  adapter?: RuntimeWorkbenchAdapter;
  initialRecords?: readonly TRecord[];
  limit?: number;
  autoConnect?: boolean;
  ingest?: LiveProjectionIngestOptions;
};

export type LiveProjectionIngestOptions = {
  maxBatchSize?: number;
  maxQueuedMutations?: number;
  flushIntervalMs?: number;
};

export type LiveProjectionBinding<TRecord extends Record<string, unknown>> = {
  store: TenantProjectionStore<TRecord>;
  scope(): ProjectionScope;
  connect(): Promise<void>;
  disconnect(): void;
  reset(): void;
  applyLiveMutation(mutation: ProjectionMutation<TRecord>): boolean;
  applyLiveMutations(mutations: readonly ProjectionMutation<TRecord>[]): ProjectionApplyResult;
  getStatusSnapshot(): LiveProjectionSnapshot;
  subscribeStatus(listener: () => void): () => void;
  useStatus(): LiveProjectionSnapshot;
};

export type PrototypeRuntimeCache = {
  getStore<TRecord extends Record<string, unknown>>(
    scope: ProjectionScope,
    createStore: () => TenantProjectionStore<TRecord>
  ): TenantProjectionStore<TRecord>;
  getDummyRecords<TRecord extends Record<string, unknown>>(
    schema: DummySchemaSpec,
    options: DummyDataFactoryOptions,
    count: number,
    createRecords: () => readonly TRecord[]
  ): readonly TRecord[];
  resetScope(scope: ProjectionScope): void;
  resetTenant(tenantId: string): void;
  clear(): void;
};

export type TenantSnapshotPersistenceState<TRecord extends Record<string, unknown>> = {
  schemaVersion: "foundation.frontend.tenant_snapshot.v1";
  scope: ProjectionScope;
  storeVersion: number;
  savedAt: string;
  records: readonly TRecord[];
};

export type TenantSnapshotPersistenceOptions<TRecord extends Record<string, unknown>> = {
  storage: AsyncKeyValueStorage;
  store: TenantProjectionStore<TRecord>;
  keyPrefix?: string;
  maxRecords?: number;
  sensitiveFieldNames?: readonly string[];
  allowSensitivePersistence?: boolean;
  serialize?: (state: TenantSnapshotPersistenceState<TRecord>) => string;
  deserialize?: (value: string) => TenantSnapshotPersistenceState<TRecord>;
  onError?: (operation: string, error: unknown) => void;
};

export type TenantSnapshotPersistence<TRecord extends Record<string, unknown>> = {
  getStorageKey(): string;
  hydrate(): Promise<number>;
  persist(): Promise<number>;
  flush(): Promise<void>;
  clearPersisted(): Promise<void>;
  resetSession(): Promise<void>;
  start(): () => void;
  stop(): void;
  readonly _recordType?: TRecord;
};

export type IndexedDBTenantSnapshotPersistenceOptions<TRecord extends Record<string, unknown>> =
  Omit<TenantSnapshotPersistenceOptions<TRecord>, "storage"> &
    IndexedDBStorageOptions & {
      snapshotStoreName?: string;
    };

export type HermesProjectionSource = {
  loadProjection?<TRecord extends Record<string, unknown>>(
    scope: ProjectionScope,
    request: ProjectionLoadRequest
  ): Promise<unknown & { _phantom?: TRecord }>;
  subscribeProjection?<TRecord extends Record<string, unknown>>(
    scope: ProjectionScope,
    listener: (event: unknown, record?: TRecord) => void
  ): (() => void) | { unsubscribe(): void };
};

export type HermesProjectionAdapterOptions = {
  normalizer?: ProjectionNormalizer;
  eventPipeline?: ProjectionEventPipelineOptions;
};

export type RuntimeComputeLane =
  | "disabled"
  | "local"
  | "worker"
  | "wasm-sab"
  | "wasm-transfer"
  | "http";

export type RuntimeComputePlan = {
  lane: RuntimeComputeLane;
  reason: string;
  deadlineRisk?: "low" | "medium" | "high";
  fallback?: RuntimeComputeLane;
};

export type RuntimeComputeRequest<TJob = unknown> = {
  job: TJob;
  tenantId?: string;
  domain?: string;
  collection?: string;
  byteLength?: number;
  deadlineMs?: number;
};

export type RuntimeWorkbenchAdapter = {
  dispatch?(command: unknown): Promise<unknown>;
  query?(query: unknown): Promise<unknown>;
  loadProjection?<TRecord extends Record<string, unknown>>(
    scope: ProjectionScope,
    request: ProjectionLoadRequest
  ): Promise<ProjectionLoadResult<TRecord>>;
  subscribeProjection?<TRecord extends Record<string, unknown>>(
    scope: ProjectionScope,
    listener: (mutation: ProjectionMutation<TRecord>) => void
  ): () => void;
  planCompute?(request: RuntimeComputeRequest): RuntimeComputePlan;
  dispatchCompute?(request: RuntimeComputeRequest, plan: RuntimeComputePlan): Promise<unknown>;
  transfer?(request: unknown): Promise<unknown>;
  stream?(request: unknown): AsyncIterable<unknown>;
};

export type RuntimeWorkbench = {
  mode: PrototypeMode;
  adapter?: RuntimeWorkbenchAdapter;
  planCompute(request: RuntimeComputeRequest): RuntimeComputePlan;
  dispatchCompute(request: RuntimeComputeRequest): Promise<unknown>;
};

const DEFAULT_SENSITIVE_FIELDS = [
  "token",
  "secret",
  "password",
  "authorization",
  "organization_id",
  "tenant_id",
  "owner_id",
  "role",
  "permissions",
  "audit",
  "metadata",
];

const DEFAULT_MAX_PERSISTED_RECORDS = 5000;
const DEFAULT_LIVE_INGEST_BATCH_SIZE = 128;
const DEFAULT_LIVE_INGEST_QUEUE_SIZE = 4096;
const DEFAULT_LIVE_INGEST_FLUSH_MS = 0;

const stableHash = (value: string): number => {
  let hash = 2166136261;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
};

const scalarDummyValue = (
  field: DummyFieldSpec,
  index: number,
  schema: DummySchemaSpec,
  options: DummyDataFactoryOptions
): unknown => {
  if (field.dummy !== undefined) return field.dummy;
  const seed = stableHash(`${options.seed ?? schema.domain}:${schema.collection}:${field.name}:${index}`);
  const request: DummyValueRequest = {
    field,
    index,
    schema,
    tenantId: options.tenantId,
    seed,
  };
  const provided = options.provider?.value?.(field.kind, request) ?? options.provider?.[field.kind]?.(request);
  if (provided !== undefined) return provided;

  switch (field.kind) {
    case "uuid":
      return `${schema.domain}-${schema.collection}-${String(index + 1).padStart(4, "0")}`;
    case "email":
      return `${field.name}${index + 1}@example.test`;
    case "url":
      return `https://example.test/${schema.collection}/${index + 1}`;
    case "int":
      return (seed % 9000) + 100;
    case "float":
      return Number(((seed % 10000) / 100).toFixed(2));
    case "bool":
      return index % 2 === 0;
    case "timestamp":
      return new Date(Date.UTC(2026, 0, 1 + index)).toISOString();
    case "enum":
      return field.enumValues?.[0] ?? "unknown";
    case "bytes":
      return new Uint8Array([seed & 0xff, (seed >> 8) & 0xff]);
    case "object":
      return {};
    case "text":
      return `${schema.collection} ${index + 1} ${field.name}`;
    case "string":
    default:
      return `${field.name}-${index + 1}`;
  }
};

export const createFakerDummyValueProvider = (faker: FakerLike): DummyValueProvider => ({
  uuid: () => faker.string?.uuid?.(),
  email: ({ field, index }) =>
    faker.internet?.email?.({ firstName: field.name, lastName: String(index + 1) }),
  url: () => faker.internet?.url?.(),
  text: () => faker.lorem?.sentence?.(6) ?? faker.lorem?.words?.(8),
  string: ({ field, index }) => faker.lorem?.words?.(2) ?? `${field.name}-${index + 1}`,
  int: ({ seed }) => faker.number?.int?.({ min: 100, max: 9999 }) ?? (seed % 9000) + 100,
  float: ({ seed }) =>
    faker.number?.float?.({ min: 0, max: 100, fractionDigits: 2 }) ??
    Number(((seed % 10000) / 100).toFixed(2)),
  bool: ({ index }) => faker.datatype?.boolean?.() ?? index % 2 === 0,
  timestamp: ({ index }) =>
    (faker.date?.recent?.({ days: 30 }) ?? new Date(Date.UTC(2026, 0, 1 + index))).toISOString(),
  enum: ({ field }) =>
    field.enumValues && field.enumValues.length > 0
      ? faker.helpers?.arrayElement?.(field.enumValues) ?? field.enumValues[0]
      : "unknown",
});

const shouldOmitSensitiveField = (
  field: DummyFieldSpec,
  options: DummyDataFactoryOptions
): boolean => {
  if (field.allowSensitiveDummy) return false;
  const sensitive = options.sensitiveFieldNames ?? DEFAULT_SENSITIVE_FIELDS;
  const lower = field.name.toLowerCase();
  return sensitive.some((name) => lower === name || lower.includes(name));
};

export const createDummyDataFactory = <TRecord extends Record<string, unknown>>(
  schema: DummySchemaSpec,
  options: DummyDataFactoryOptions = {}
) => ({
  create(index = 0, overrides: Partial<TRecord> = {}): TRecord {
    const record: Record<string, unknown> = {};
    const idField = schema.idField ?? "id";
    record[idField] = `${schema.domain}-${schema.collection}-${index + 1}`;

    for (const field of schema.fields) {
      if (shouldOmitSensitiveField(field, options)) continue;
      if (field.optional && index % 3 === 2) continue;
      const value = scalarDummyValue(field, index, schema, options);
      record[field.name] = field.repeated ? [value] : value;
    }

    return { ...record, ...overrides } as TRecord;
  },

  list(count: number, overrides: (index: number) => Partial<TRecord> = () => ({})): TRecord[] {
    const safeCount = Math.max(0, count);
    const records = new Array<TRecord>(safeCount);
    for (let index = 0; index < safeCount; index += 1) {
      records[index] = this.create(index, overrides(index));
    }
    return records;
  },
});

const createDomainStoreSnapshot = <TRecord>(
  source: ReadonlyMap<string, TRecord>,
  version: number
): DomainStoreSnapshot<TRecord> => {
  let records: readonly TRecord[] | undefined;
  return {
    get records() {
      records ??= Array.from(source.values());
      return records;
    },
    byId: source,
    version,
  };
};

export const createDomainStore = <TRecord>(
  getId: (record: TRecord) => string,
  initialRecords: readonly TRecord[] = []
): DomainStore<TRecord> => {
  let version = 0;
  let byId = new Map<string, TRecord>();
  let snapshot = createDomainStoreSnapshot<TRecord>(new Map(), version);
  let snapshotDirty = false;
  let batchDepth = 0;
  let pendingBatchEmit = false;
  const listeners = new Set<() => void>();

  const rebuildSnapshot = () => {
    if (!snapshotDirty) return;
    snapshot = createDomainStoreSnapshot(new Map(byId), version);
    snapshotDirty = false;
  };

  const emit = () => {
    snapshotDirty = true;
    if (batchDepth > 0) {
      pendingBatchEmit = true;
      return;
    }
    version += 1;
    listeners.forEach((listener) => listener());
  };

  const flushBatch = () => {
    if (!pendingBatchEmit || batchDepth > 0) return;
    pendingBatchEmit = false;
    version += 1;
    snapshotDirty = true;
    listeners.forEach((listener) => listener());
  };

  const replace = (records: readonly TRecord[]) => {
    byId = new Map(records.map((record) => [getId(record), record]));
    emit();
  };

  replace(initialRecords);

  const getSnapshot = (): DomainStoreSnapshot<TRecord> => {
    rebuildSnapshot();
    return snapshot;
  };

  return {
    getSnapshot,
    get(id) {
      return byId.get(id);
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    useSnapshot() {
      return useSyncExternalStore(this.subscribe, this.getSnapshot, this.getSnapshot);
    },
    upsert(record) {
      byId.set(getId(record), record);
      emit();
    },
    remove(id) {
      if (!byId.has(id)) return;
      byId.delete(id);
      emit();
    },
    replace,
    reset() {
      byId.clear();
      emit();
    },
    batch(run) {
      batchDepth += 1;
      try {
        run();
      } finally {
        batchDepth -= 1;
        flushBatch();
      }
    },
  };
};

export const createTenantProjectionStore = <TRecord extends Record<string, unknown>>(
  scope: ProjectionScope,
  getId: (record: TRecord) => string = (record) => String(record.id ?? "")
): TenantProjectionStore<TRecord> => {
  const store = createDomainStore<TRecord>(getId);
  const versions = new Map<string, number>();

  const applyOne = (mutation: ProjectionMutation<TRecord>): boolean => {
    if (
      mutation.tenantId !== scope.tenantId ||
      mutation.domain !== scope.domain ||
      mutation.collection !== scope.collection
    ) {
      return false;
    }

    const currentVersion = versions.get(mutation.recordId) ?? -1;
    if (mutation.version < currentVersion) {
      return false;
    }
    versions.set(mutation.recordId, mutation.version);

    if (mutation.operation === "delete") {
      store.remove(mutation.recordId);
      return true;
    }

    if (mutation.operation === "patch") {
      const current = store.get(mutation.recordId) ?? ({ id: mutation.recordId } as unknown as TRecord);
      store.upsert({ ...current, ...mutation.fields, id: mutation.recordId } as TRecord);
      return true;
    }

    const record =
      mutation.record ?? ({ ...mutation.fields, id: mutation.recordId } as unknown as TRecord);
    store.upsert(record);
    return true;
  };

  const apply = (mutation: ProjectionMutation<TRecord>): boolean => applyOne(mutation);

  return {
    ...store,
    apply,
    applyMany(mutations): ProjectionApplyResult {
      let accepted = 0;
      let rejected = 0;
      let lastAcceptedVersion: number | undefined;
      let sourceWatermark: string | undefined;
      store.batch(() => {
        for (const mutation of mutations) {
          if (applyOne(mutation)) {
            accepted += 1;
            lastAcceptedVersion =
              lastAcceptedVersion === undefined
                ? mutation.version
                : Math.max(lastAcceptedVersion, mutation.version);
            sourceWatermark = mutation.sourceWatermark ?? sourceWatermark;
          } else {
            rejected += 1;
          }
        }
      });
      return { accepted, rejected, lastAcceptedVersion, sourceWatermark };
    },
    scope: () => ({ ...scope }),
    reset() {
      versions.clear();
      store.reset();
    },
  };
};

const scopeKey = (scope: ProjectionScope): string =>
  `${scope.tenantId}:${scope.domain}:${scope.collection}`;

const nowIso = (): string => new Date().toISOString();

const messageFromError = (error: unknown): string =>
  error instanceof Error ? error.message : String(error);

const asRecord = (value: unknown): Record<string, unknown> | undefined =>
  value !== null && typeof value === "object" ? (value as Record<string, unknown>) : undefined;

const readString = (source: Record<string, unknown>, ...keys: string[]): string => {
  for (const key of keys) {
    const value = source[key];
    if (typeof value === "string" && value.trim() !== "") return value;
  }
  return "";
};

const readNumber = (source: Record<string, unknown>, ...keys: string[]): number | undefined => {
  for (const key of keys) {
    const value = source[key];
    if (typeof value === "number" && Number.isFinite(value)) return value;
    if (typeof value === "string" && value.trim() !== "") {
      const parsed = Number(value);
      if (Number.isFinite(parsed)) return parsed;
    }
  }
  return undefined;
};

const normalizeOperation = (value: string): ProjectionMutationOperation | undefined => {
  switch (value) {
    case "upsert":
    case "create":
    case "created":
    case "update":
    case "updated":
      return "upsert";
    case "patch":
    case "patched":
      return "patch";
    case "delete":
    case "deleted":
    case "remove":
    case "removed":
      return "delete";
    default:
      return undefined;
  }
};

const unwrapProjectionPayload = (input: unknown): Record<string, unknown> | undefined => {
  const root = asRecord(input);
  if (!root) return undefined;
  const payload = asRecord(root.payload);
  const candidate =
    asRecord(root.mutation) ??
    asRecord(root.projection) ??
    asRecord(payload?.mutation) ??
    asRecord(payload?.projection) ??
    payload ??
    root;
  return candidate;
};

export const normalizeHermesProjectionMutation = <TRecord extends Record<string, unknown>>(
  input: unknown,
  scope: ProjectionScope
): ProjectionMutation<TRecord> | undefined => {
  const payload = unwrapProjectionPayload(input);
  if (!payload) return undefined;

  const operation = normalizeOperation(readString(payload, "operation", "op", "action").toLowerCase());
  const tenantId = readString(payload, "tenantId", "tenant_id") || scope.tenantId;
  const domain = readString(payload, "domain") || scope.domain;
  const collection = readString(payload, "collection") || scope.collection;
  if (!operation || tenantId !== scope.tenantId || domain !== scope.domain || collection !== scope.collection) {
    return undefined;
  }

  const record = asRecord(payload.record) as TRecord | undefined;
  const fields = asRecord(payload.fields) as Partial<TRecord> | undefined;
  const recordId =
    readString(payload, "recordId", "record_id", "id") ||
    (record ? readString(record, "id") : "") ||
    (fields ? readString(fields as Record<string, unknown>, "id") : "");
  const version = readNumber(payload, "version", "sequence", "epoch");
  if (!recordId || version === undefined) return undefined;

  return {
    operation,
    tenantId,
    domain,
    collection,
    recordId,
    version,
    epoch: readNumber(payload, "epoch"),
    sourceWatermark: readString(payload, "sourceWatermark", "source_watermark", "watermark") || undefined,
    fields,
    record,
  };
};

export const normalizeProjectionLoadResult = <TRecord extends Record<string, unknown>>(
  input: unknown,
  scope: ProjectionScope
): ProjectionLoadResult<TRecord> => {
  const payload = asRecord(input);
  if (!payload) return {};
  const root = asRecord(payload.payload) ?? payload;
  const rawRecords = root.records ?? root.items ?? root.entities;
  const rawMutations = root.mutations ?? root.projections;
  const records = Array.isArray(rawRecords) ? (rawRecords.filter(asRecord) as TRecord[]) : undefined;
  const mutations = Array.isArray(rawMutations)
    ? rawMutations
        .map((entry) => normalizeHermesProjectionMutation<TRecord>(entry, scope))
        .filter((entry): entry is ProjectionMutation<TRecord> => Boolean(entry))
    : undefined;
  return {
    records,
    mutations,
    sourceWatermark: readString(root, "sourceWatermark", "source_watermark", "watermark") || undefined,
    version: readNumber(root, "version"),
  };
};

const scheduleProjectionFlush = (run: () => void, delayMs: number): (() => void) => {
  if (delayMs <= 0 && typeof queueMicrotask === "function") {
    let cancelled = false;
    queueMicrotask(() => {
      if (!cancelled) run();
    });
    return () => {
      cancelled = true;
    };
  }
  const timeoutId = setTimeout(run, Math.max(0, delayMs));
  return () => clearTimeout(timeoutId);
};

export const createHermesProjectionAdapter = (
  source: HermesProjectionSource,
  options: HermesProjectionAdapterOptions = {}
): RuntimeWorkbenchAdapter => ({
  async loadProjection<TRecord extends Record<string, unknown>>(
    scope: ProjectionScope,
    request: ProjectionLoadRequest
  ): Promise<ProjectionLoadResult<TRecord>> {
    if (!source.loadProjection) return {};
    const rawResult = await source.loadProjection<TRecord>(scope, request);
    return (
      await options.normalizer?.normalizeLoadResult<TRecord>(rawResult, scope)
    ) ?? normalizeProjectionLoadResult<TRecord>(rawResult, scope);
  },
  subscribeProjection<TRecord extends Record<string, unknown>>(
    scope: ProjectionScope,
    listener: (mutation: ProjectionMutation<TRecord>) => void
  ): () => void {
    if (!source.subscribeProjection) return () => undefined;

    const normalizer = options.normalizer;
    const maxBatchSize = Math.max(1, options.eventPipeline?.maxBatchSize ?? 128);
    const maxQueuedEvents = Math.max(maxBatchSize, options.eventPipeline?.maxQueuedEvents ?? 4096);
    const flushIntervalMs = Math.max(0, options.eventPipeline?.flushIntervalMs ?? 0);
    let closed = false;
    let scheduledCancel: (() => void) | undefined;
    let flushing = false;
    const queue: unknown[] = [];

    const flush = async () => {
      if (closed || flushing || queue.length === 0) return;
      flushing = true;
      scheduledCancel = undefined;
      const batch = queue.splice(0, maxBatchSize);
      try {
        if (normalizer) {
          const mutations = await normalizer
            .normalizeMutations<TRecord>(batch, scope)
            .catch(() => batch
              .map((event) => normalizeHermesProjectionMutation<TRecord>(event, scope))
              .filter((entry): entry is ProjectionMutation<TRecord> => Boolean(entry)));
          if (!closed) {
            mutations.forEach(listener);
          }
        } else {
          for (const event of batch) {
            const mutation = normalizeHermesProjectionMutation<TRecord>(event, scope);
            if (mutation && !closed) listener(mutation);
          }
        }
      } finally {
        flushing = false;
        if (!closed && queue.length > 0) {
          scheduledCancel = scheduleProjectionFlush(() => {
            void flush();
          }, flushIntervalMs);
        }
      }
    };

    const scheduleFlush = () => {
      if (scheduledCancel || flushing) return;
      scheduledCancel = scheduleProjectionFlush(() => {
        void flush();
      }, flushIntervalMs);
    };

    const subscription = source.subscribeProjection<TRecord>(scope, (event) => {
      if (closed) return;
      if (queue.length >= maxQueuedEvents) {
        const mutation = normalizeHermesProjectionMutation<TRecord>(event, scope);
        if (mutation) listener(mutation);
        return;
      }
      queue.push(event);
      if (queue.length >= maxBatchSize) {
        scheduledCancel?.();
        scheduledCancel = undefined;
        void flush();
        return;
      }
      scheduleFlush();
    });

    return () => {
      closed = true;
      scheduledCancel?.();
      scheduledCancel = undefined;
      queue.length = 0;
      if (typeof subscription === "function") {
        subscription();
        return;
      }
      subscription.unsubscribe();
    };
  },
});

export const createPrototypeRuntimeCache = (): PrototypeRuntimeCache => {
  const stores = new Map<string, TenantProjectionStore<Record<string, unknown>>>();
  const dummyRecords = new Map<string, readonly Record<string, unknown>[]>();

  return {
    getStore<TRecord extends Record<string, unknown>>(
      scope: ProjectionScope,
      createStore: () => TenantProjectionStore<TRecord>
    ): TenantProjectionStore<TRecord> {
      const key = scopeKey(scope);
      const existing = stores.get(key);
      if (existing) return existing as TenantProjectionStore<TRecord>;
      const store = createStore();
      stores.set(key, store as TenantProjectionStore<Record<string, unknown>>);
      return store;
    },
    getDummyRecords<TRecord extends Record<string, unknown>>(
      schema: DummySchemaSpec,
      options: DummyDataFactoryOptions,
      count: number,
      createRecords: () => readonly TRecord[]
    ): readonly TRecord[] {
      if (options.provider) return createRecords();
      const sensitiveKey = options.sensitiveFieldNames?.join(",") ?? "";
      const key = [
        options.tenantId ?? "",
        schema.domain,
        schema.collection,
        schema.idField ?? "id",
        options.seed ?? "",
        sensitiveKey,
        String(count),
      ].join(":");
      const existing = dummyRecords.get(key);
      if (existing) return existing as readonly TRecord[];
      const records = createRecords();
      dummyRecords.set(key, records as readonly Record<string, unknown>[]);
      return records;
    },
    resetScope(scope: ProjectionScope): void {
      stores.get(scopeKey(scope))?.reset();
      stores.delete(scopeKey(scope));
      for (const key of Array.from(dummyRecords.keys())) {
        if (key.startsWith(`${scope.tenantId}:${scope.domain}:${scope.collection}:`)) {
          dummyRecords.delete(key);
        }
      }
    },
    resetTenant(tenantId: string): void {
      for (const [key, store] of Array.from(stores.entries())) {
        if (key.startsWith(`${tenantId}:`)) {
          store.reset();
          stores.delete(key);
        }
      }
      for (const key of Array.from(dummyRecords.keys())) {
        if (key.startsWith(`${tenantId}:`)) {
          dummyRecords.delete(key);
        }
      }
    },
    clear(): void {
      stores.forEach((store) => store.reset());
      stores.clear();
      dummyRecords.clear();
    },
  };
};

const storageKeyPart = (value: string): string => encodeURIComponent(value);

const tenantSnapshotStorageKey = (scope: ProjectionScope, keyPrefix = "prototype-store"): string =>
  [
    keyPrefix,
    storageKeyPart(scope.tenantId),
    storageKeyPart(scope.domain),
    storageKeyPart(scope.collection),
  ].join(":");

const isSensitiveFieldName = (fieldName: string, sensitiveFieldNames: readonly string[]): boolean => {
  const lower = fieldName.toLowerCase();
  return sensitiveFieldNames.some((name) => lower === name || lower.includes(name));
};

const sanitizePersistedRecord = <TRecord extends Record<string, unknown>>(
  record: TRecord,
  sensitiveFieldNames: readonly string[],
  allowSensitivePersistence: boolean
): TRecord => {
  if (allowSensitivePersistence) return record;
  const safeRecord: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(record)) {
    if (isSensitiveFieldName(key, sensitiveFieldNames)) continue;
    safeRecord[key] = value;
  }
  return safeRecord as TRecord;
};

const sameScope = (left: ProjectionScope, right: ProjectionScope): boolean =>
  left.tenantId === right.tenantId && left.domain === right.domain && left.collection === right.collection;

const defaultPersistenceOnError = (operation: string, error: unknown) => {
  if (typeof console !== "undefined") {
    console.warn(`[frontend-kit:tenant-persistence] ${operation} failed`, error);
  }
};

export const createTenantSnapshotPersistence = <TRecord extends Record<string, unknown>>({
  storage,
  store,
  keyPrefix,
  maxRecords = DEFAULT_MAX_PERSISTED_RECORDS,
  sensitiveFieldNames = DEFAULT_SENSITIVE_FIELDS,
  allowSensitivePersistence = false,
  serialize = JSON.stringify,
  deserialize = JSON.parse,
  onError = defaultPersistenceOnError,
}: TenantSnapshotPersistenceOptions<TRecord>): TenantSnapshotPersistence<TRecord> => {
  const scope = store.scope();
  const storageKey = tenantSnapshotStorageKey(scope, keyPrefix);
  let unsubscribe: (() => void) | undefined;
  let pendingWrite: Promise<void> = Promise.resolve();
  let suppressPersistence = false;

  const persist = async (): Promise<number> => {
    const snapshot = store.getSnapshot();
    if (snapshot.records.length > maxRecords) {
      throw new Error(
        `tenant snapshot exceeds persistence bound: ${snapshot.records.length} > ${maxRecords}`
      );
    }
    const state: TenantSnapshotPersistenceState<TRecord> = {
      schemaVersion: "foundation.frontend.tenant_snapshot.v1",
      scope,
      storeVersion: snapshot.version,
      savedAt: nowIso(),
      records: snapshot.records.map((record) =>
        sanitizePersistedRecord(record, sensitiveFieldNames, allowSensitivePersistence)
      ),
    };
    await storage.setItem(storageKey, serialize(state));
    return state.records.length;
  };

  const schedulePersist = () => {
    if (suppressPersistence) return;
    pendingWrite = pendingWrite
      .catch(() => undefined)
      .then(async () => {
        try {
          await persist();
        } catch (error) {
          onError("persist", error);
        }
      });
  };

  return {
    getStorageKey: () => storageKey,
    async hydrate(): Promise<number> {
      try {
        const raw = await storage.getItem(storageKey);
        if (!raw) return 0;
        const state = deserialize(raw);
        if (state.schemaVersion !== "foundation.frontend.tenant_snapshot.v1" || !sameScope(state.scope, scope)) {
          await storage.removeItem(storageKey);
          return 0;
        }
        store.replace(state.records);
        return state.records.length;
      } catch (error) {
        onError("hydrate", error);
        return 0;
      }
    },
    persist,
    flush: async () => {
      await pendingWrite;
    },
    clearPersisted: () => storage.removeItem(storageKey),
    async resetSession(): Promise<void> {
      await pendingWrite;
      suppressPersistence = true;
      store.reset();
      suppressPersistence = false;
      await storage.removeItem(storageKey);
    },
    start(): () => void {
      if (unsubscribe) return unsubscribe;
      unsubscribe = store.subscribe(schedulePersist);
      return () => {
        unsubscribe?.();
        unsubscribe = undefined;
      };
    },
    stop(): void {
      unsubscribe?.();
      unsubscribe = undefined;
    },
  };
};

export const createIndexedDBTenantSnapshotPersistence = <TRecord extends Record<string, unknown>>({
  dbName,
  storeName,
  snapshotStoreName,
  version,
  onError,
  ...options
}: IndexedDBTenantSnapshotPersistenceOptions<TRecord>): TenantSnapshotPersistence<TRecord> & {
  close(): void;
} => {
  const storage = createIndexedDBStorage({
    dbName,
    onError,
    storeName: snapshotStoreName ?? storeName ?? "prototype-snapshots",
    version,
  });
  return {
    ...createTenantSnapshotPersistence({ ...options, onError, storage }),
    close: storage.close,
  };
};

export const createLiveProjectionBinding = <TRecord extends Record<string, unknown>>(
  options: LiveProjectionBindingOptions<TRecord>
): LiveProjectionBinding<TRecord> => {
  const { scope, store } = options;
  const maxBatchSize = Math.max(1, options.ingest?.maxBatchSize ?? DEFAULT_LIVE_INGEST_BATCH_SIZE);
  const maxQueuedMutations = Math.max(
    1,
    options.ingest?.maxQueuedMutations ?? DEFAULT_LIVE_INGEST_QUEUE_SIZE
  );
  const flushIntervalMs = Math.max(0, options.ingest?.flushIntervalMs ?? DEFAULT_LIVE_INGEST_FLUSH_MS);
  const statusListeners = new Set<() => void>();
  let unsubscribeProjection: (() => void) | undefined;
  let abortController: AbortController | undefined;
  let connectionEpoch = 0;
  let queuedLiveMutations: ProjectionMutation<TRecord>[] = [];
  let scheduledLiveFlushCancel: (() => void) | undefined;
  let droppedLiveMutations = 0;
  let status: LiveProjectionSnapshot = {
    scope: { ...scope },
    status: "idle",
    loading: false,
    appliedMutations: 0,
    rejectedMutations: 0,
    lastVersion: -1,
    updatedAt: nowIso(),
  };

  const setStatus = (patch: Partial<LiveProjectionSnapshot>) => {
    status = {
      ...status,
      ...patch,
      scope: { ...scope },
      updatedAt: nowIso(),
    };
    statusListeners.forEach((listener) => listener());
  };

  const applyLiveMutation = (mutation: ProjectionMutation<TRecord>): boolean => {
    const accepted = store.apply(mutation);
    if (!accepted) {
      setStatus({ rejectedMutations: status.rejectedMutations + 1 });
      return false;
    }
    setStatus({
      appliedMutations: status.appliedMutations + 1,
      lastVersion: Math.max(status.lastVersion, mutation.version),
      sourceWatermark: mutation.sourceWatermark ?? status.sourceWatermark,
    });
    return true;
  };

  const applyLiveMutations = (mutations: readonly ProjectionMutation<TRecord>[]): ProjectionApplyResult => {
    if (mutations.length === 0) return { accepted: 0, rejected: 0 };
    const result = store.applyMany(mutations);
    if (result.accepted === 0 && result.rejected === 0) return result;
    setStatus({
      appliedMutations: status.appliedMutations + result.accepted,
      rejectedMutations: status.rejectedMutations + result.rejected,
      lastVersion:
        result.lastAcceptedVersion === undefined
          ? status.lastVersion
          : Math.max(status.lastVersion, result.lastAcceptedVersion),
      sourceWatermark: result.sourceWatermark ?? status.sourceWatermark,
    });
    return result;
  };

  const flushQueuedLiveMutations = () => {
    scheduledLiveFlushCancel = undefined;
    if (queuedLiveMutations.length === 0) return;
    const batch = queuedLiveMutations.splice(0, maxBatchSize);
    applyLiveMutations(batch);
    if (queuedLiveMutations.length === 0) {
      setStatus({ queuedMutations: 0 });
    }
    if (queuedLiveMutations.length > 0) {
      scheduledLiveFlushCancel = scheduleProjectionFlush(flushQueuedLiveMutations, flushIntervalMs);
    }
  };

  const enqueueLiveMutation = (mutation: ProjectionMutation<TRecord>) => {
    if (queuedLiveMutations.length >= maxQueuedMutations) {
      droppedLiveMutations += 1;
      setStatus({
        droppedMutations: droppedLiveMutations,
        error: `live projection queue saturated (${maxQueuedMutations})`,
        queuedMutations: queuedLiveMutations.length,
        rejectedMutations: status.rejectedMutations + 1,
        status: "degraded",
      });
      return;
    }
    queuedLiveMutations.push(mutation);
    if (queuedLiveMutations.length >= maxBatchSize) {
      scheduledLiveFlushCancel?.();
      scheduledLiveFlushCancel = undefined;
      flushQueuedLiveMutations();
      return;
    }
    scheduledLiveFlushCancel ??= scheduleProjectionFlush(flushQueuedLiveMutations, flushIntervalMs);
  };

  const clearQueuedLiveMutations = () => {
    scheduledLiveFlushCancel?.();
    scheduledLiveFlushCancel = undefined;
    queuedLiveMutations = [];
    setStatus({ queuedMutations: 0 });
  };

  const disconnect = () => {
    connectionEpoch += 1;
    abortController?.abort();
    abortController = undefined;
    unsubscribeProjection?.();
    unsubscribeProjection = undefined;
    clearQueuedLiveMutations();
    setStatus({ loading: false, status: "closed" });
  };

  const connect = async () => {
    const adapter = options.adapter;
    if (options.initialRecords && options.initialRecords.length > 0) {
      store.replace(options.initialRecords);
    }
    if (!adapter?.loadProjection && !adapter?.subscribeProjection) {
      setStatus({
        error: "live projection adapter is not configured",
        loading: false,
        status: "degraded",
      });
      return;
    }

    connectionEpoch += 1;
    const activeEpoch = connectionEpoch;
    unsubscribeProjection?.();
    abortController = new AbortController();
    setStatus({ error: undefined, loading: true, status: "loading" });

    try {
      const bufferedMutations: ProjectionMutation<TRecord>[] = [];
      if (adapter.subscribeProjection) {
        unsubscribeProjection = adapter.subscribeProjection<TRecord>(scope, (mutation) => {
          if (status.loading) {
            bufferedMutations.push(mutation);
            return;
          }
          enqueueLiveMutation(mutation);
        });
      }
      if (adapter.loadProjection) {
        const result = await adapter.loadProjection<TRecord>(scope, {
          scope,
          limit: options.limit,
          sinceWatermark: status.sourceWatermark,
          signal: abortController.signal,
        });
        if (activeEpoch !== connectionEpoch) return;
        if (result.records) store.replace(result.records);
        applyLiveMutations([...(result.mutations ?? []), ...bufferedMutations]);
        setStatus({
          lastVersion:
            result.version !== undefined ? Math.max(status.lastVersion, result.version) : status.lastVersion,
          sourceWatermark: result.sourceWatermark ?? status.sourceWatermark,
        });
      }
      if (activeEpoch !== connectionEpoch) return;
      setStatus({
        connectedAt: nowIso(),
        loading: false,
        status: adapter.subscribeProjection ? "live" : "degraded",
      });
    } catch (error) {
      if (activeEpoch !== connectionEpoch) return;
      unsubscribeProjection?.();
      unsubscribeProjection = undefined;
      setStatus({
        error: messageFromError(error),
        loading: false,
        status: "error",
      });
    }
  };

  if (options.autoConnect) {
    void connect();
  }

  return {
    store,
    scope: () => ({ ...scope }),
    connect,
    disconnect,
    reset() {
      disconnect();
      store.reset();
      setStatus({
        appliedMutations: 0,
        error: undefined,
        lastVersion: -1,
        rejectedMutations: 0,
        sourceWatermark: undefined,
        status: "idle",
      });
    },
    applyLiveMutation,
    applyLiveMutations,
    getStatusSnapshot: () => status,
    subscribeStatus(listener) {
      statusListeners.add(listener);
      return () => statusListeners.delete(listener);
    },
    useStatus() {
      return useSyncExternalStore(this.subscribeStatus, this.getStatusSnapshot, this.getStatusSnapshot);
    },
  };
};

export const createGeneratedHookRegistry = <TStore extends { useSnapshot(): unknown }>() => {
  const stores = new Map<string, TStore>();
  return {
    register(name: string, store: TStore): void {
      stores.set(name, store);
    },
    get(name: string): TStore | undefined {
      return stores.get(name);
    },
    use(name: string): unknown {
      const store = stores.get(name);
      if (!store) {
        throw new Error(`generated store is not registered: ${name}`);
      }
      return store.useSnapshot();
    },
  };
};

export const createRuntimeWorkbench = (
  mode: PrototypeMode,
  adapter?: RuntimeWorkbenchAdapter
): RuntimeWorkbench => {
  const planCompute = (request: RuntimeComputeRequest): RuntimeComputePlan => {
    const adapterPlan = adapter?.planCompute?.(request);
    if (adapterPlan) return adapterPlan;
    if (mode === "dummy") {
      return {
        lane: "disabled",
        reason: "dummy mode does not dispatch runtime compute",
      };
    }
    if (adapter?.dispatchCompute) {
      return {
        lane: "local",
        reason: "runtime compute adapter is available without a specialized planner",
        deadlineRisk: request.deadlineMs !== undefined && request.deadlineMs < 1 ? "medium" : "low",
      };
    }
    return {
      lane: "disabled",
      reason: "runtime compute adapter is not configured",
    };
  };

  return {
    mode,
    adapter,
    planCompute,
    async dispatchCompute(request) {
      const plan = planCompute(request);
      if (plan.lane === "disabled") {
        throw new Error(`runtime compute is disabled: ${plan.reason}`);
      }
      if (!adapter?.dispatchCompute) {
        throw new Error("runtime compute adapter is not configured");
      }
      return await adapter.dispatchCompute(request, plan);
    },
  };
};
