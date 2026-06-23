import { describe, expect, it, vi } from "vitest";

import {
  createHttpProjectionSource,
  createWebSocketProjectionSource,
  httpToWebSocketUrl,
  mapMutation,
  resolveProjectionEndpoints,
  type ProjectionLoadResult,
  type ProjectionMutation,
  type ProjectionProtoCodec,
  type ProjectionScope,
} from "./projectionSource";

const scope: ProjectionScope = { tenantId: "org_1", domain: "signals", collection: "ticks" };

type WireMutation = {
  operation: number;
  domain: string;
  collection: string;
  organizationId: string;
  recordId: string;
  version: number;
  fields: { name: string; value?: { stringValue?: string } }[];
};

const wireMutation = (overrides: Partial<WireMutation> = {}): WireMutation => ({
  operation: 1,
  domain: "signals",
  collection: "ticks",
  organizationId: "org_1",
  recordId: "tick_1",
  version: 3,
  fields: [{ name: "symbol", value: { stringValue: "OVS" } }],
  ...overrides,
});

describe("mapMutation", () => {
  it("maps an upsert proto mutation into a ProjectionMutation", () => {
    expect(mapMutation(wireMutation(), scope)).toMatchObject({
      operation: "upsert",
      recordId: "tick_1",
      version: 3,
      fields: { symbol: "OVS" },
    });
  });

  it("drops mutations outside the requested scope", () => {
    expect(mapMutation(wireMutation({ collection: "other" }), scope)).toBeUndefined();
    expect(mapMutation(wireMutation({ organizationId: "org_2" }), scope)).toBeUndefined();
  });

  it("drops unknown operations and empty record ids", () => {
    expect(mapMutation(wireMutation({ operation: 0 }), scope)).toBeUndefined();
    expect(mapMutation(wireMutation({ recordId: "" }), scope)).toBeUndefined();
  });
});

describe("resolveProjectionEndpoints", () => {
  it("derives http+ws endpoints from an origin and the standard path", () => {
    expect(resolveProjectionEndpoints({ originUrl: "https://app.example.com" })).toEqual({
      httpUrl: "https://app.example.com/v1/projections",
      wsUrl: "wss://app.example.com/v1/projections",
    });
  });

  it("upgrades plain http to ws", () => {
    expect(resolveProjectionEndpoints({ originUrl: "http://localhost:8080" })).toEqual({
      httpUrl: "http://localhost:8080/v1/projections",
      wsUrl: "ws://localhost:8080/v1/projections",
    });
  });

  it("honors an explicit base url over derivation", () => {
    expect(resolveProjectionEndpoints({ baseUrl: "https://gw.example.com/projections", originUrl: "https://ignored" })).toEqual({
      httpUrl: "https://gw.example.com/projections",
      wsUrl: "wss://gw.example.com/projections",
    });
  });

  it("returns undefined when no origin resolves", () => {
    expect(resolveProjectionEndpoints({})).toBeUndefined();
  });

  it("exposes the scheme upgrade helper", () => {
    expect(httpToWebSocketUrl("https://h/p")).toBe("wss://h/p");
    expect(httpToWebSocketUrl("http://h/p")).toBe("ws://h/p");
  });
});

const codec: ProjectionProtoCodec = {
  decodeSnapshot: () =>
    ({ batch: { mutations: [wireMutation()] }, watermark: "3", epoch: 7 }) as ReturnType<
      ProjectionProtoCodec["decodeSnapshot"]
    >,
  decodeDeltaFrame: () =>
    ({ mutations: [wireMutation({ recordId: "tick_2", version: 4 })] }) as ReturnType<
      ProjectionProtoCodec["decodeDeltaFrame"]
    >,
};

describe("createHttpProjectionSource", () => {
  it("loads a snapshot as scope-filtered mutations", async () => {
    const fetchImpl = vi.fn(async () => new Response(new Uint8Array([1, 2, 3]), { status: 200 })) as unknown as typeof fetch;
    const source = createHttpProjectionSource({ baseUrl: "https://host/v1/projections", codec, fetch: fetchImpl });
    // loadProjection returns the phantom HermesProjectionSource shape; the test
    // decodes the concrete ProjectionLoadResult it actually resolves to.
    const result = (await source.loadProjection(scope, { scope })) as unknown as ProjectionLoadResult;
    expect(result.mutations).toHaveLength(1);
    expect(result.mutations[0].recordId).toBe("tick_1");
    expect(result.sourceWatermark).toBe("3");
    expect(result.version).toBe(7);
  });
});

class FakeSocket {
  binaryType = "blob";
  onopen: (() => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  sent: unknown[] = [];
  send(data: unknown) {
    this.sent.push(data);
  }
  close() {
    this.onclose?.();
  }
}

describe("createHttpProjectionSource pagination", () => {
  it("backfills a scope larger than the limit by following the cursor", async () => {
    // Page 1: hasMore + cursor "10"; page 2: final page, no more.
    const pages = [
      { batch: { mutations: [wireMutation({ recordId: "tick_11", version: 11 })] }, watermark: "11", epoch: 2, nextCursor: "11", hasMore: true },
      { batch: { mutations: [wireMutation({ recordId: "tick_1", version: 1 })] }, watermark: "", epoch: 2, nextCursor: "", hasMore: false },
    ];
    const seenCursors: string[] = [];
    const pagingCodec: ProjectionProtoCodec = {
      decodeSnapshot: () => pages.shift() as ReturnType<ProjectionProtoCodec["decodeSnapshot"]>,
      decodeDeltaFrame: codec.decodeDeltaFrame,
    };
    const fetchImpl = vi.fn(async (input: string | URL) => {
      seenCursors.push(new URL(String(input)).searchParams.get("cursor") ?? "");
      return new Response(new Uint8Array([1]), { status: 200 });
    }) as unknown as typeof fetch;

    const source = createHttpProjectionSource({ baseUrl: "https://host/v1/projections", codec: pagingCodec, fetch: fetchImpl });
    const result = (await source.loadProjection(scope, { scope })) as unknown as ProjectionLoadResult;

    // Two pages fetched (cursor empty then "11"); both records assembled; resume
    // watermark comes from the first page.
    expect(seenCursors).toEqual(["", "11"]);
    expect(result.mutations.map((m) => m.recordId)).toEqual(["tick_11", "tick_1"]);
    expect(result.sourceWatermark).toBe("11");
  });
});

describe("createWebSocketProjectionSource", () => {
  it("delivers decoded delta mutations to the listener", async () => {
    let instance: FakeSocket | undefined;
    const received: ProjectionMutation[] = [];
    const source = createWebSocketProjectionSource({
      url: "wss://host/v1/projections",
      codec,
      createSocket: () => {
        instance = new FakeSocket();
        return instance as unknown as WebSocket;
      },
    });
    const unsubscribe = source.subscribeProjection(scope, (mutation) =>
      received.push(mutation as ProjectionMutation),
    );

    await Promise.resolve();
    instance!.onopen?.();
    instance!.onmessage?.({ data: new Uint8Array([9, 9]).buffer } as MessageEvent);

    expect(received).toHaveLength(1);
    expect(received[0].recordId).toBe("tick_2");
    expect(received[0].version).toBe(4);
    unsubscribe();
  });

  it("surfaces a degraded status on a resync control frame", async () => {
    let instance: FakeSocket | undefined;
    const statuses: string[] = [];
    let dropped: number | undefined;
    const source = createWebSocketProjectionSource({
      url: "wss://host/v1/projections",
      codec,
      createSocket: () => {
        instance = new FakeSocket();
        return instance as unknown as WebSocket;
      },
      onStatus: (status) => {
        statuses.push(status.phase);
        if (status.phase === "degraded") dropped = status.dropped;
      },
    });
    const unsubscribe = source.subscribeProjection(scope, () => {});

    await Promise.resolve();
    instance!.onopen?.();
    instance!.onmessage?.({ data: JSON.stringify({ type: "resync", reason: "slow-consumer", dropped: 5 }) } as MessageEvent);

    expect(statuses).toContain("connecting");
    expect(statuses).toContain("live");
    expect(statuses).toContain("degraded");
    expect(dropped).toBe(5);
    unsubscribe();
    expect(statuses).toContain("closed");
  });
});
