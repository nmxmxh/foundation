export type UnknownRecord = Record<string, unknown>;

export interface FoundationGlobalContext extends UnknownRecord {
  userId?: string;
  user_id?: string;
  sessionId?: string;
  session_id?: string;
  source?: string;
  deviceId?: string;
  device_id?: string;
  organizationId?: string;
  organization_id?: string;
  roleId?: string;
  role_id?: string;
  correlationId?: string;
  correlation_id?: string;
  auditContext?: string;
  audit_context?: string;
  extras?: UnknownRecord;
}

export interface FoundationMetadata extends UnknownRecord {
  globalContext?: FoundationGlobalContext;
  global_context?: FoundationGlobalContext;
  tags?: string[];
  aiConfidence?: number;
  ai_confidence?: number;
  categories?: string[];
  knowledgeGraph?: string;
  knowledge_graph?: string;
  sourceRef?: string;
  source_ref?: string;
  gamificationState?: string;
  gamification_state?: string;
  extras?: UnknownRecord;
}

const metadataKeys = new Set([
  "global_context",
  "globalContext",
  "tags",
  "ai_confidence",
  "aiConfidence",
  "embedding_id",
  "embeddingId",
  "categories",
  "knowledge_graph",
  "knowledgeGraph",
  "source_ref",
  "sourceRef",
  "validity_period",
  "validityPeriod",
  "gamification_state",
  "gamificationState",
  "extras",
]);

const globalContextKeys = new Set([
  "user_id",
  "userId",
  "session_id",
  "sessionId",
  "source",
  "device_id",
  "deviceId",
  "organization_id",
  "organizationId",
  "role_id",
  "roleId",
  "audit_context",
  "auditContext",
  "correlation_id",
  "correlationId",
  "extras",
]);

export const isRecord = (value: unknown): value is UnknownRecord =>
  value !== null && typeof value === "object" && !Array.isArray(value);

export const mergeExtras = (existing: unknown, injected: UnknownRecord): UnknownRecord => ({
  ...(isRecord(existing) ? existing : {}),
  ...injected,
});

export const normalizeMetadataForTransport = (metadata: unknown): FoundationMetadata => {
  const metadataRecord: FoundationMetadata = isRecord(metadata) ? { ...metadata } : {};
  const globalContextRaw = isRecord(metadataRecord.global_context)
    ? metadataRecord.global_context
    : isRecord(metadataRecord.globalContext)
      ? metadataRecord.globalContext
      : {};
  const normalizedGlobalContext: FoundationGlobalContext = { ...globalContextRaw };

  const globalExtras: UnknownRecord = {};
  Object.entries(globalContextRaw).forEach(([key, value]) => {
    if (!globalContextKeys.has(key)) {
      globalExtras[key] = value;
      delete normalizedGlobalContext[key];
    }
  });
  if (Object.keys(globalExtras).length > 0 || isRecord(globalContextRaw.extras)) {
    normalizedGlobalContext.extras = mergeExtras(globalContextRaw.extras, globalExtras);
  }

  if ("globalContext" in metadataRecord && !("global_context" in metadataRecord)) {
    metadataRecord.globalContext = normalizedGlobalContext;
  } else {
    metadataRecord.global_context = normalizedGlobalContext;
    delete metadataRecord.globalContext;
  }

  const metadataExtras: UnknownRecord = {};
  Object.entries(metadataRecord).forEach(([key, value]) => {
    if (!metadataKeys.has(key)) {
      metadataExtras[key] = value;
      delete metadataRecord[key];
    }
  });
  if (Object.keys(metadataExtras).length > 0 || isRecord(metadataRecord.extras)) {
    metadataRecord.extras = mergeExtras(metadataRecord.extras, metadataExtras);
  }

  return metadataRecord;
};

export const createDeviceId = (storageKey = "ovasabi_device_id"): string => {
  if (typeof window === "undefined") return "server";
  const existing = window.localStorage.getItem(storageKey);
  if (existing && existing.trim() !== "") return existing;
  const created =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `device_${Date.now()}_${Math.random().toString(36).slice(2, 10)}`;
  window.localStorage.setItem(storageKey, created);
  return created;
};

export const createFoundationMetadata = (
  overrides: FoundationMetadata = {},
  context: { eventType?: string; caller?: string; source?: string } = {}
): FoundationMetadata => {
  const globalContext = {
    source: context.source ?? "frontend",
    deviceId: createDeviceId(),
    ...(overrides.global_context ?? overrides.globalContext ?? {}),
  };
  const tags = Array.from(
    new Set([
      ...(Array.isArray(overrides.tags) ? overrides.tags : []),
      context.caller ? `caller:${context.caller}` : "",
      context.eventType ? `event:${context.eventType}` : "",
    ].filter(Boolean))
  );

  return normalizeMetadataForTransport({
    ...overrides,
    tags,
    global_context: globalContext,
  });
};
