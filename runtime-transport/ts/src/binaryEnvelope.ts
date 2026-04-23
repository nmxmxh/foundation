import {
  EventEnvelope as ProtoEventEnvelope,
  PayloadEncoding as ProtoPayloadEncoding,
  type EventEnvelope,
} from "./generated/transport/v1/envelope";
import { type Metadata as ProtoMetadata } from "./generated/transport/v1/metadata";
import {
  RUNTIME_ENVELOPE_SCHEMA_VERSION,
  assertCompatibleSchemaVersion,
  type EnvelopeMetadata,
  type PayloadEncoding,
  type RuntimeEnvelope,
} from "./index";

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();

export const encodeRuntimeEnvelope = <TPayload>(envelope: RuntimeEnvelope<TPayload>): Uint8Array => {
  const payloadEncoding = normalizePayloadEncoding(envelope.payloadEncoding);
  const metadata = normalizeEnvelopeMetadata(envelope.metadata);
  return ProtoEventEnvelope.encode({
    id: metadata.requestId,
    eventType: envelope.eventType,
    payload: encodePayload(envelope.payload, payloadEncoding),
    metadata: encodeMetadata(metadata),
    correlationId: metadata.correlationId,
    schemaVersion: metadata.schemaVersion,
    occurredAt: new Date(metadata.timestamp),
    payloadEncoding: payloadEncodingToProto(payloadEncoding),
    sourceNodeId: "",
  }).finish();
};

export const decodeRuntimeEnvelope = (bytes: Uint8Array): RuntimeEnvelope<unknown> => {
  const envelope = ProtoEventEnvelope.decode(bytes);
  const payloadEncoding = protoToPayloadEncoding(envelope.payloadEncoding);
  return {
    eventType: envelope.eventType,
    payload: decodePayload(envelope.payload, payloadEncoding),
    payloadEncoding,
    metadata: decodeMetadata(envelope),
  };
};

export const tryDecodeRuntimeEnvelope = (input: ArrayBuffer | Uint8Array | string): RuntimeEnvelope<unknown> => {
  if (typeof input === "string") {
    return decodeJSONRuntimeEnvelope(input);
  }
  const bytes = input instanceof Uint8Array ? input : new Uint8Array(input);
  return decodeRuntimeEnvelope(bytes);
};

export const encodeJSONRuntimeEnvelope = <TPayload>(envelope: RuntimeEnvelope<TPayload>): string =>
  JSON.stringify((() => {
    const metadata = normalizeEnvelopeMetadata(envelope.metadata);
    const extra = stripReservedMetadataKeys(metadata.extra);
    return {
      event_type: envelope.eventType,
      payload: envelope.payloadEncoding === "json" ? (envelope.payload ?? {}) : {},
      payload_encoding: normalizePayloadEncoding(envelope.payloadEncoding),
      metadata: {
        ...extra,
        correlation_id: metadata.correlationId,
        request_id: metadata.requestId,
        idempotency_key: metadata.idempotencyKey,
        schema_version: metadata.schemaVersion,
      },
      correlation_id: metadata.correlationId,
      schema_version: metadata.schemaVersion,
      timestamp: metadata.timestamp,
    };
  })());

export const decodeJSONRuntimeEnvelope = (payload: string): RuntimeEnvelope<unknown> => {
  const parsed = JSON.parse(payload) as Record<string, unknown>;
  const nestedMetadata = asRecord(parsed.metadata);
  const metadata = normalizeEnvelopeMetadata({
    correlationId:
      readString(nestedMetadata ?? {}, "correlation_id", "correlationId") ||
      readString(parsed, "correlation_id", "correlationId"),
    requestId:
      readString(nestedMetadata ?? {}, "request_id", "requestId") ||
      readString(parsed, "request_id", "requestId"),
    idempotencyKey:
      readString(nestedMetadata ?? {}, "idempotency_key", "idempotencyKey") ||
      readString(parsed, "idempotency_key", "idempotencyKey"),
    schemaVersion:
      readString(nestedMetadata ?? {}, "schema_version", "schemaVersion") ||
      readString(parsed, "schema_version", "schemaVersion") ||
      RUNTIME_ENVELOPE_SCHEMA_VERSION,
    timestamp: readString(parsed, "timestamp") || new Date().toISOString(),
    extra: nestedMetadata ?? {},
  });
  return {
    eventType: readString(parsed, "event_type", "eventType"),
    payload: (parsed.payload as Record<string, unknown> | undefined) ?? {},
    payloadEncoding: normalizePayloadEncoding(readString(parsed, "payload_encoding", "payloadEncoding") as PayloadEncoding),
    metadata,
  };
};

const encodePayload = <TPayload>(payload: TPayload, encoding: PayloadEncoding): Uint8Array => {
  if (encoding === "protobuf") {
    if (payload instanceof Uint8Array) {
      return payload;
    }
    if (payload instanceof ArrayBuffer) {
      return new Uint8Array(payload);
    }
    throw new Error("protobuf runtime envelopes require Uint8Array payloads");
  }
  return textEncoder.encode(JSON.stringify(payload ?? {}));
};

const decodePayload = (payload: Uint8Array, encoding: PayloadEncoding): unknown => {
  if (encoding === "protobuf") {
    return payload;
  }
  if (payload.byteLength === 0) {
    return {};
  }
  return JSON.parse(textDecoder.decode(payload)) as Record<string, unknown>;
};

const encodeMetadata = (metadata: EnvelopeMetadata): ProtoMetadata => ({
  globalContext: undefined,
  tags: [],
  aiConfidence: 0,
  embeddingId: "",
  categories: [],
  knowledgeGraph: "",
  sourceRef: "",
  validityPeriod: undefined,
  gamificationState: "",
  correlationId: metadata.correlationId,
  causationId: "",
  requestId: metadata.requestId,
  idempotencyKey: metadata.idempotencyKey,
  traceId: "",
  spanId: "",
  channel: "",
  locale: "",
  tenantRegion: "",
  attributes: {},
  extrasJson: textEncoder.encode(JSON.stringify(metadata.extra ?? {})),
});

const decodeMetadata = (envelope: EventEnvelope): EnvelopeMetadata => {
  const metadata = envelope.metadata;
  return normalizeEnvelopeMetadata({
    correlationId: metadata?.correlationId || envelope.correlationId,
    requestId: metadata?.requestId || envelope.id || envelope.correlationId,
    idempotencyKey: metadata?.idempotencyKey || "",
    schemaVersion: envelope.schemaVersion || RUNTIME_ENVELOPE_SCHEMA_VERSION,
    timestamp: envelope.occurredAt?.toISOString() || new Date().toISOString(),
    extra: decodeExtras(metadata?.extrasJson),
  });
};

const decodeExtras = (extras?: Uint8Array): Record<string, unknown> => {
  if (!extras || extras.byteLength === 0) {
    return {};
  }
  try {
    return JSON.parse(textDecoder.decode(extras)) as Record<string, unknown>;
  } catch {
    return {};
  }
};

const readString = (source: Record<string, unknown>, ...keys: string[]): string => {
  for (const key of keys) {
    const value = source[key];
    if (typeof value === "string" && value.trim() !== "") {
      return value;
    }
  }
  return "";
};

const normalizeEnvelopeMetadata = (metadata: EnvelopeMetadata): EnvelopeMetadata => {
  const correlationId = metadata.correlationId || `corr_${Date.now()}`;
  return {
    correlationId,
    requestId: metadata.requestId || correlationId || `req_${Date.now()}`,
    idempotencyKey: metadata.idempotencyKey || "",
    schemaVersion: assertCompatibleSchemaVersion(metadata.schemaVersion),
    timestamp: metadata.timestamp || new Date().toISOString(),
    extra: metadata.extra ?? {},
  };
};

const normalizePayloadEncoding = (value: PayloadEncoding): PayloadEncoding => (value === "protobuf" ? "protobuf" : "json");

const payloadEncodingToProto = (value: PayloadEncoding): ProtoPayloadEncoding =>
  value === "protobuf" ? ProtoPayloadEncoding.PAYLOAD_ENCODING_PROTOBUF : ProtoPayloadEncoding.PAYLOAD_ENCODING_JSON;

const protoToPayloadEncoding = (value: ProtoPayloadEncoding): PayloadEncoding =>
  value === ProtoPayloadEncoding.PAYLOAD_ENCODING_PROTOBUF ? "protobuf" : "json";

const asRecord = (value: unknown): Record<string, unknown> | undefined =>
  value && typeof value === "object" ? (value as Record<string, unknown>) : undefined;

const stripReservedMetadataKeys = (value: Record<string, unknown>): Record<string, unknown> => {
  const next = { ...value };
  delete next.correlation_id;
  delete next.correlationId;
  delete next.request_id;
  delete next.requestId;
  delete next.idempotency_key;
  delete next.idempotencyKey;
  delete next.schema_version;
  delete next.schemaVersion;
  return next;
};
