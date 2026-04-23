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
  diagnosticsEnabled: boolean;
  localeDefaults?: LocaleDefaults;
};

export type DatabaseConfig = {
  url: string;
  maxConnections: number;
  minConnections: number;
  acquireTimeoutMs: number;
};

export type RedisConfig = {
  url: string;
  keyPrefix: string;
  defaultTTLSeconds: number;
  degradeOpen?: boolean;
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

export type CompressionConfig = {
  apiMinBytes?: number;
  wasmPreferredEncoding?: "br" | "gz" | "identity";
};

export type QueueConfig = {
  concurrency: number;
  maxRetries: number;
};

export type ServerRuntimeConfig = {
  schemaVersion?: string;
  public: PublicRuntimeConfig;
  database: DatabaseConfig;
  redis: RedisConfig;
  objectStorage: ObjectStorageConfig;
  jwt: JWTConfig;
  runtimeBudgets: RuntimeBudgetConfig;
  compression?: CompressionConfig;
  queues: Record<string, QueueConfig>;
};

const isRecord = (value: unknown): value is Record<string, unknown> => Boolean(value) && typeof value === "object";

const hasPositiveNumber = (value: unknown): value is number => typeof value === "number" && Number.isFinite(value) && value > 0;

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
    (diagnosticsEnabled === undefined || typeof diagnosticsEnabled === "boolean")
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

  if (
    readString(redis, "url") === "" ||
    readString(redis, "keyPrefix", "key_prefix") === "" ||
    !hasPositiveNumber(redis.defaultTTLSeconds ?? redis.default_ttl_seconds)
  ) {
    return false;
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

  return Object.values(queues).every((queue) => {
    if (!isRecord(queue)) {
      return false;
    }
    const maxRetries = queue.maxRetries ?? queue.max_retries;
    return hasPositiveNumber(queue.concurrency) && typeof maxRetries === "number" && maxRetries >= 0;
  });
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
