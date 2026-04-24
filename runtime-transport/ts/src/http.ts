import type { RuntimeEnvelope, RuntimeRoute, TransportStrategy } from "./index";
import { compressRuntimeBytes, type RuntimeCompressionOptions } from "./compression";

type HTTPTransportOptions = {
  baseUrl: string;
  fetchImpl?: typeof fetch;
  getHeaders?: () => HeadersInit;
  compression?: RuntimeCompressionOptions;
};

const CONTENT_TYPE_JSON = "application/json";
const CONTENT_TYPE_PROTOBUF = "application/x-protobuf";

export const createHTTPTransport = (options: HTTPTransportOptions): TransportStrategy => {
  const fetchImpl = options.fetchImpl ?? fetch;

  return {
    kind: "http",
    async dispatch<TPayload>(envelope: RuntimeEnvelope<TPayload>, route: RuntimeRoute, signal: AbortSignal): Promise<unknown> {
      const request = await buildRequest(options, route, envelope);
      const response = await fetchImpl(request.url, {
        method: request.method,
        headers: request.headers,
        body: request.body,
        signal,
      });

      if (!response.ok) {
        const errorBody = await parseErrorBody(response);
        throw new Error(errorBody);
      }

      const contentType = response.headers.get("content-type")?.toLowerCase() ?? "";
      if (contentType.includes(CONTENT_TYPE_PROTOBUF) || contentType.includes("application/protobuf")) {
        return new Uint8Array(await response.arrayBuffer());
      }

      if (contentType.includes("application/octet-stream")) {
        return response.body;
      }

      if (contentType.includes("application/x-ndjson")) {
        return (async function* () {
          const reader = response.body?.getReader();
          if (!reader) return;
          const decoder = new TextDecoder();
          let buffer = "";
          try {
            while (true) {
              const { done, value } = await reader.read();
              if (done) break;
              buffer += decoder.decode(value, { stream: true });
              const lines = buffer.split("\n");
              buffer = lines.pop() ?? "";
              for (const line of lines) {
                const trimmed = line.trim();
                if (trimmed) {
                  yield JSON.parse(trimmed);
                }
              }
            }
            if (buffer.trim()) {
              yield JSON.parse(buffer.trim());
            }
          } finally {
            reader.releaseLock();
          }
        })();
      }

      const decoded = (await response.json()) as Record<string, unknown>;
      return decoded.response_payload ?? decoded;
    },
  };
};

type BuiltRequest = {
  url: string;
  method: string;
  headers: Headers;
  body?: BodyInit;
};

const buildRequest = async <TPayload>(options: HTTPTransportOptions, route: RuntimeRoute, envelope: RuntimeEnvelope<TPayload>): Promise<BuiltRequest> => {
  const url = new URL(route.path, options.baseUrl);
  const headers = new Headers({
    "X-Correlation-ID": envelope.metadata.correlationId,
    "X-Request-ID": envelope.metadata.requestId,
  });
  const extraHeaders = optionsHeaders(options);
  for (const [key, value] of extraHeaders.entries()) {
    headers.set(key, value);
  }
  if (envelope.metadata.idempotencyKey) {
    headers.set("X-Idempotency-Key", envelope.metadata.idempotencyKey);
  }

  const method = route.method.toUpperCase();
  const responseEncoding = envelope.payloadEncoding === "protobuf" ? CONTENT_TYPE_PROTOBUF : CONTENT_TYPE_JSON;
  headers.set("Accept", responseEncoding);
  headers.set("Accept-Encoding", "br, gzip, deflate");

  if (method === "GET" || method === "DELETE") {
    appendQuery(url, envelope.payload);
    return {
      url: url.toString(),
      method,
      headers,
    };
  }

  let body: BodyInit;
  if (envelope.payloadEncoding === "protobuf") {
    const bytes = toBytes(envelope.payload);
    body = new Uint8Array(bytes);
    headers.set("Content-Type", CONTENT_TYPE_PROTOBUF);
  } else {
    body = JSON.stringify(envelope.payload ?? {});
    headers.set("Content-Type", CONTENT_TYPE_JSON);
  }

  const compressed = await compressIfNeeded(body, headers, options.compression);
  return {
    url: url.toString(),
    method,
    headers,
    body: compressed,
  };
};

const compressIfNeeded = async (
  body: BodyInit,
  headers: Headers,
  options: RuntimeCompressionOptions = { enabled: true, minBytes: 4096, preferred: ["gzip"] }
): Promise<BodyInit> => {
  let data: Uint8Array;
  if (typeof body === "string") {
    data = new TextEncoder().encode(body);
  } else if (body instanceof Uint8Array) {
    data = body;
  } else {
    return body;
  }

  const compressed = await compressRuntimeBytes(data, options);
  if (compressed.encoding === "identity") {
    return body;
  }
  headers.set("Content-Encoding", compressed.encoding);
  return new Uint8Array(compressed.bytes).buffer as ArrayBuffer;
};

const optionsHeaders = (options: HTTPTransportOptions): Headers => {
  const headers = new Headers();
  const source = options.getHeaders?.();
  if (!source) {
    return headers;
  }
  const extras = new Headers(source);
  for (const [key, value] of extras.entries()) {
    headers.set(key, value);
  }
  return headers;
};

const appendQuery = (url: URL, payload: unknown) => {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    return;
  }
  for (const [key, value] of Object.entries(payload as Record<string, unknown>)) {
    if (value === undefined || value === null) {
      continue;
    }
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item === undefined || item === null) {
          continue;
        }
        url.searchParams.append(key, String(item));
      }
      continue;
    }
    url.searchParams.set(key, String(value));
  }
};

const toBytes = (payload: unknown): Uint8Array => {
  if (payload instanceof Uint8Array) {
    return payload;
  }
  if (payload instanceof ArrayBuffer) {
    return new Uint8Array(payload);
  }
  throw new Error("protobuf HTTP payloads require Uint8Array bodies");
};

const parseErrorBody = async (response: Response): Promise<string> => {
  const contentType = response.headers.get("content-type")?.toLowerCase() ?? "";
  if (contentType.includes("application/json")) {
    try {
      const body = (await response.json()) as Record<string, unknown>;
      if (typeof body.error === "object" && body.error !== null) {
        const message = (body.error as Record<string, unknown>).message;
        if (typeof message === "string" && message.trim() !== "") {
          return message;
        }
      }
      return JSON.stringify(body);
    } catch {
      return `http transport failed with status ${response.status}`;
    }
  }
  try {
    const text = await response.text();
    if (text.trim() !== "") {
      return text;
    }
  } catch {}
  return `http transport failed with status ${response.status}`;
};
