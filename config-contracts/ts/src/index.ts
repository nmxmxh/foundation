export type AuthMode = "guest-first" | "credential-first" | "authenticated-only";

export const RUNTIME_CONFIG_SCHEMA_VERSION = "1.0";
const configSchemaAliases = {
  v1: RUNTIME_CONFIG_SCHEMA_VERSION,
} as const;

export type TransportTimeouts = {
  http: number;
  ws: number;
  wasm: number;
};

export type WASMAssets = {
  modulePath: string;
  compressedModulePath?: string;
};

export type RuntimeSharedMemoryMode = "off" | "auto" | "required";
export type RuntimeTransportMode = "postMessage" | "transferable" | "sab" | "ws" | "http";
export type RuntimeCompressionEncoding = "identity" | "gzip" | "br" | "deflate";

export type RuntimeMemoryConfig = {
  sharedMemory: RuntimeSharedMemoryMode;
  transportOrder: RuntimeTransportMode[];
  compression: RuntimeCompressionEncoding[];
  arenaBytes?: number;
  requireSharedWasmMemory?: boolean;
};

export type LocaleDefaults = {
  timezone?: string;
  currency?: string;
};

export type PublicRuntimeConfig = {
  schemaVersion?: string;
  apiBaseUrl: string;
  wsBaseUrl: string;
  authMode: AuthMode;
  defaultLocale: string;
  featureFlags: Record<string, boolean>;
  transportTimeoutsMs: TransportTimeouts;
  wasmAssets: WASMAssets;
  runtimeMemory?: RuntimeMemoryConfig;
  diagnosticsEnabled: boolean;
  localeDefaults?: LocaleDefaults;
};

export type DatabaseConfig = {
  url: string;
  maxConnections: number;
  minConnections: number;
  acquireTimeoutMs: number;
  queryTimeoutMs?: number;
  hotReadTimeoutMs?: number;
  shardCount?: number;
};

export type RedisConfig = {
  url: string;
  shardUrls?: string[];
  keyPrefix: string;
  defaultTTLSeconds: number;
  degradeOpen?: boolean;
  poolSize?: number;
  minIdle?: number;
  maxRetries?: number;
};

export type ObjectStorageConfig = {
  endpoint: string;
  region: string;
  bucket: string;
  accessKey: string;
  secretKey: string;
  useTLS: boolean;
  strict: boolean;
};

export type JWTConfig = {
  secret: string;
  issuer: string;
  audience?: string;
};

export type RuntimeBudgetConfig = {
  dispatchMaxConcurrent: number;
  dispatchAcquireTimeoutMs: number;
};

export type SLOConfig = {
  dispatchP99LatencyMs: number;
  workerSuccessRate: number;
  eventDeliveryLagMs: number;
};

export type CompressionConfig = {
  apiMinBytes?: number;
  wasmPreferredEncoding?: "br" | "gz" | "identity";
};

export type QueueConfig = {
  concurrency: number;
  maxRetries: number;
};

export type PostQuantumConfig = {
  tlsHybridKEM: "auto" | "required" | "disabled";
  signatureAlgorithm: "classical" | "ml-dsa" | "slh-dsa";
  cryptoInventoryRequired: boolean;
  longLivedArtifactSigning: boolean;
};

export type ServerSecurityConfig = {
  postQuantum?: PostQuantumConfig;
};

export type ServerRuntimeConfig = {
  schemaVersion?: string;
  public: PublicRuntimeConfig;
  database: DatabaseConfig;
  redis: RedisConfig;
  objectStorage: ObjectStorageConfig;
  jwt: JWTConfig;
  runtimeBudgets: RuntimeBudgetConfig;
  slos?: SLOConfig;
  compression?: CompressionConfig;
  security?: ServerSecurityConfig;
  queues: Record<string, QueueConfig>;
};

const isRecord = (value: unknown): value is Record<string, unknown> => Boolean(value) && typeof value === "object";

const hasPositiveNumber = (value: unknown): value is number => typeof value === "number" && Number.isFinite(value) && value > 0;
const hasNonNegativeNumber = (value: unknown): value is number => typeof value === "number" && Number.isFinite(value) && value >= 0;

const readString = (value: Record<string, unknown>, ...keys: string[]) => {
  for (const key of keys) {
    const candidate = value[key];
    if (typeof candidate === "string" && candidate.trim() !== "") {
      return candidate;
    }
  }
  return "";
};

const readBoolean = (value: Record<string, unknown>, ...keys: string[]) => {
  for (const key of keys) {
    const candidate = value[key];
    if (typeof candidate === "boolean") {
      return candidate;
    }
  }
  return undefined;
};

const readRecord = (value: Record<string, unknown>, ...keys: string[]) => {
  for (const key of keys) {
    const candidate = value[key];
    if (isRecord(candidate)) {
      return candidate;
    }
  }
  return undefined;
};

const readArray = (value: Record<string, unknown>, ...keys: string[]) => {
  for (const key of keys) {
    const candidate = value[key];
    if (Array.isArray(candidate)) {
      return candidate;
    }
  }
  return undefined;
};

export const normalizeConfigSchemaVersion = (value: string | undefined): string => {
  const trimmed = value?.trim() ?? "";
  if (trimmed === "") {
    return RUNTIME_CONFIG_SCHEMA_VERSION;
  }
  return configSchemaAliases[trimmed as keyof typeof configSchemaAliases] ?? trimmed;
};

export const assertCompatibleConfigSchemaVersion = (value: string | undefined): string => {
  const normalized = normalizeConfigSchemaVersion(value);
  if (normalized !== RUNTIME_CONFIG_SCHEMA_VERSION) {
    throw new Error(`unsupported runtime config schema version "${value ?? ""}"`);
  }
  return normalized;
};

export const validatePublicRuntimeConfig = (value: unknown): value is PublicRuntimeConfig => {
  if (!isRecord(value)) {
    return false;
  }
  try {
    assertCompatibleConfigSchemaVersion(readString(value, "schemaVersion", "schema_version"));
  } catch {
    return false;
  }
  const timeouts = readRecord(value, "transportTimeoutsMs", "transport_timeouts_ms");
  const assets = readRecord(value, "wasmAssets", "wasm_assets");
  const featureFlags = readRecord(value, "featureFlags", "feature_flags");
  const runtimeMemory = readRecord(value, "runtimeMemory", "runtime_memory");
  const diagnosticsEnabled = readBoolean(value, "diagnosticsEnabled", "diagnostics_enabled");
  return (
    readString(value, "apiBaseUrl", "api_base_url") !== "" &&
    readString(value, "wsBaseUrl", "ws_base_url") !== "" &&
    readString(value, "defaultLocale", "default_locale") !== "" &&
    ["guest-first", "credential-first", "authenticated-only"].includes(readString(value, "authMode", "auth_mode")) &&
    (featureFlags === undefined || isRecord(featureFlags)) &&
    isRecord(timeouts) &&
    hasPositiveNumber(timeouts.http) &&
    hasPositiveNumber(timeouts.ws) &&
    hasPositiveNumber(timeouts.wasm) &&
    isRecord(assets) &&
    readString(assets, "modulePath", "module_path") !== "" &&
    (runtimeMemory === undefined || validateRuntimeMemoryConfig(runtimeMemory)) &&
    (diagnosticsEnabled === undefined || typeof diagnosticsEnabled === "boolean")
  );
};

export const validateRuntimeMemoryConfig = (value: unknown): value is RuntimeMemoryConfig => {
  if (!isRecord(value)) {
    return false;
  }
  const sharedMemory = readString(value, "sharedMemory", "shared_memory");
  const transportOrder = readArray(value, "transportOrder", "transport_order");
  const compression = readArray(value, "compression");
  const arenaBytes = value.arenaBytes ?? value.arena_bytes;
  const requireSharedWasmMemory = value.requireSharedWasmMemory ?? value.require_shared_wasm_memory;
  return (
    ["off", "auto", "required"].includes(sharedMemory) &&
    Array.isArray(transportOrder) &&
    transportOrder.length > 0 &&
    transportOrder.every((item) => ["postMessage", "transferable", "sab", "ws", "http"].includes(String(item))) &&
    Array.isArray(compression) &&
    compression.length > 0 &&
    compression.every((item) => ["identity", "gzip", "br", "deflate"].includes(String(item))) &&
    (arenaBytes === undefined || (typeof arenaBytes === "number" && Number.isFinite(arenaBytes) && arenaBytes > 0)) &&
    (requireSharedWasmMemory === undefined || typeof requireSharedWasmMemory === "boolean")
  );
};

export const validateServerRuntimeConfig = (value: unknown): value is ServerRuntimeConfig => {
  if (!isRecord(value)) {
    return false;
  }
  try {
    assertCompatibleConfigSchemaVersion(readString(value, "schemaVersion", "schema_version"));
  } catch {
    return false;
  }
  const publicConfig = readRecord(value, "public");
  if (!validatePublicRuntimeConfig(publicConfig)) {
    return false;
  }
  const database = readRecord(value, "database");
  const redis = readRecord(value, "redis");
  const storage = readRecord(value, "objectStorage", "object_storage");
  const jwt = value.jwt;
  const budgets = readRecord(value, "runtimeBudgets", "runtime_budgets");
  const slos = readRecord(value, "slos");
  const security = readRecord(value, "security");
  const queues = value.queues;

  if (!isRecord(database) || !isRecord(redis) || !isRecord(storage) || !isRecord(jwt) || !isRecord(budgets) || !isRecord(queues)) {
    return false;
  }

  if (
    readString(database, "url") === "" ||
    !hasPositiveNumber(database.maxConnections ?? database.max_connections) ||
    typeof (database.minConnections ?? database.min_connections) !== "number" ||
    !hasPositiveNumber(database.acquireTimeoutMs ?? database.acquire_timeout_ms)
  ) {
    return false;
  }
  for (const value of [database.queryTimeoutMs ?? database.query_timeout_ms, database.hotReadTimeoutMs ?? database.hot_read_timeout_ms, database.shardCount ?? database.shard_count]) {
    if (value !== undefined && !hasNonNegativeNumber(value)) {
      return false;
    }
  }

  if (slos !== undefined && !validateSLOConfig(slos)) {
    return false;
  }

  if (
    readString(redis, "url") === "" ||
    readString(redis, "keyPrefix", "key_prefix") === "" ||
    !hasPositiveNumber(redis.defaultTTLSeconds ?? redis.default_ttl_seconds)
  ) {
    return false;
  }
  const shardUrls = redis.shardUrls ?? redis.shard_urls;
  if (shardUrls !== undefined && (!Array.isArray(shardUrls) || shardUrls.some((url) => typeof url !== "string" || url.trim() === ""))) {
    return false;
  }
  for (const value of [redis.poolSize ?? redis.pool_size, redis.minIdle ?? redis.min_idle, redis.maxRetries ?? redis.max_retries]) {
    if (value !== undefined && !hasNonNegativeNumber(value)) {
      return false;
    }
  }

  if (
    readString(storage, "endpoint") === "" ||
    readString(storage, "region") === "" ||
    readString(storage, "bucket") === "" ||
    readString(storage, "accessKey", "access_key") === "" ||
    readString(storage, "secretKey", "secret_key") === "" ||
    typeof (storage.useTLS ?? storage.use_tls) !== "boolean" ||
    typeof storage.strict !== "boolean"
  ) {
    return false;
  }

  if (
    readString(jwt, "secret") === "" ||
    readString(jwt, "issuer") === "" ||
    !hasPositiveNumber(budgets.dispatchMaxConcurrent ?? budgets.dispatch_max_concurrent) ||
    !hasPositiveNumber(budgets.dispatchAcquireTimeoutMs ?? budgets.dispatch_acquire_timeout_ms)
  ) {
    return false;
  }

  if (security !== undefined && !validateServerSecurityConfig(security)) {
    return false;
  }

  return Object.values(queues).every((queue) => {
    if (!isRecord(queue)) {
      return false;
    }
    const maxRetries = queue.maxRetries ?? queue.max_retries;
    return hasPositiveNumber(queue.concurrency) && typeof maxRetries === "number" && maxRetries >= 0;
  });
};

export const validateSLOConfig = (value: unknown): value is SLOConfig => {
  if (!isRecord(value)) {
    return false;
  }
  const dispatchP99LatencyMs = value.dispatchP99LatencyMs ?? value.dispatch_p99_latency_ms;
  const workerSuccessRate = value.workerSuccessRate ?? value.worker_success_rate;
  const eventDeliveryLagMs = value.eventDeliveryLagMs ?? value.event_delivery_lag_ms;
  return (
    hasPositiveNumber(dispatchP99LatencyMs) &&
    typeof workerSuccessRate === "number" &&
    Number.isFinite(workerSuccessRate) &&
    workerSuccessRate > 0 &&
    workerSuccessRate <= 1 &&
    hasPositiveNumber(eventDeliveryLagMs)
  );
};

export const validateServerSecurityConfig = (value: unknown): value is ServerSecurityConfig => {
  if (!isRecord(value)) {
    return false;
  }
  const postQuantum = readRecord(value, "postQuantum", "post_quantum");
  if (postQuantum === undefined) {
    return true;
  }
  return validatePostQuantumConfig(postQuantum);
};

export const validatePostQuantumConfig = (value: unknown): value is PostQuantumConfig => {
  if (!isRecord(value)) {
    return false;
  }
  const tlsHybridKEM = readString(value, "tlsHybridKEM", "tls_hybrid_kem");
  const signatureAlgorithm = readString(value, "signatureAlgorithm", "signature_algorithm");
  const cryptoInventoryRequired = value.cryptoInventoryRequired ?? value.crypto_inventory_required;
  const longLivedArtifactSigning = value.longLivedArtifactSigning ?? value.long_lived_artifact_signing;
  return (
    ["auto", "required", "disabled"].includes(tlsHybridKEM) &&
    ["classical", "ml-dsa", "slh-dsa"].includes(signatureAlgorithm) &&
    typeof cryptoInventoryRequired === "boolean" &&
    typeof longLivedArtifactSigning === "boolean"
  );
};

export const normalizePublicRuntimeConfig = (config: PublicRuntimeConfig): PublicRuntimeConfig => ({
  ...config,
  schemaVersion: assertCompatibleConfigSchemaVersion(config.schemaVersion),
});

export const normalizeServerRuntimeConfig = (config: ServerRuntimeConfig): ServerRuntimeConfig => ({
  ...config,
  schemaVersion: assertCompatibleConfigSchemaVersion(config.schemaVersion),
  public: normalizePublicRuntimeConfig({
    ...config.public,
    schemaVersion: config.public.schemaVersion ?? config.schemaVersion,
  }),
});

export const derivePublicRuntimeConfig = (config: ServerRuntimeConfig): PublicRuntimeConfig =>
  normalizePublicRuntimeConfig({
    ...config.public,
    schemaVersion: config.public.schemaVersion ?? config.schemaVersion,
  });
