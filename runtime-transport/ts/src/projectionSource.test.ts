import { describe, expect, it, vi } from "vitest";

import {
  createHttpProjectionSource,
  createDefaultProjectionSource,
  createProjectionSource,
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

  it("warns once per scope/tenant pair on a tenant-mismatch drop", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    try {
      const mismatchScope: ProjectionScope = {
        tenantId: "tenant-warn-test",
        domain: "signals",
        collection: "ticks",
      };
      expect(mapMutation(wireMutation(), mismatchScope)).toBeUndefined();
      expect(mapMutation(wireMutation(), mismatchScope)).toBeUndefined();
      const tenantWarnings = warn.mock.calls.filter((call) =>
        String(call[0]).includes('scope tenant "tenant-warn-test"'),
      );
      expect(tenantWarnings).toHaveLength(1);
    } finally {
      warn.mockRestore();
    }
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

  it("passes bounded query options and headers and rejects failed snapshots", async () => {
    const seen: URL[] = [];
    const fetchImpl = vi.fn(async (input: string | URL, init?: RequestInit) => {
      seen.push(new URL(String(input)));
      expect(new Headers(init?.headers).get("authorization")).toBe("Bearer projection");
      return new Response(null, { status: 503 });
    }) as unknown as typeof fetch;
    const source = createHttpProjectionSource({ baseUrl: "https://host/v1/projections/", codec, headers: async () => ({ authorization: "Bearer projection" }), fetch: fetchImpl, maxPages: 0 });
    await expect(source.loadProjection(scope, { scope, sinceWatermark: "wm", limit: 20 })).rejects.toThrow("projection snapshot failed: 503");
    expect(seen[0].searchParams.get("since")).toBe("wm");
    expect(seen[0].searchParams.get("limit")).toBe("20");
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

  it("ignores malformed control and delta frames and closes safely", async () => {
    let instance: FakeSocket | undefined;
    const badCodec: ProjectionProtoCodec = { ...codec, decodeDeltaFrame: () => { throw new Error("bad frame"); } };
    const source = createWebSocketProjectionSource({ url: "wss://host/v1/projections/", codec: badCodec, query: async () => ({ token: "safe" }), createSocket: (url) => {
      expect(new URL(url).searchParams.get("token")).toBe("safe");
      instance = new FakeSocket();
      return instance as unknown as WebSocket;
    } });
    const unsubscribe = source.subscribeProjection(scope, vi.fn());
    await Promise.resolve();
    await Promise.resolve();
    instance!.onmessage?.({ data: "not-json" } as MessageEvent);
    instance!.onmessage?.({ data: new Uint8Array([1]) } as MessageEvent);
    instance!.onmessage?.({ data: new Uint8Array([1]).buffer } as MessageEvent);
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

  it("stops reconnecting once the consecutive-failure budget is exhausted", async () => {
    vi.useFakeTimers();
    try {
      const sockets: FakeSocket[] = [];
      const statuses: { phase: string; reason?: string }[] = [];
      const source = createWebSocketProjectionSource({
        url: "wss://host/v1/projections",
        codec,
        maxConsecutiveFailures: 3,
        createSocket: () => {
          const instance = new FakeSocket();
          sockets.push(instance);
          return instance as unknown as WebSocket;
        },
        onStatus: (status) => statuses.push({ phase: status.phase, reason: status.reason }),
      });
      source.subscribeProjection(scope, () => {});

      // Every connection closes without ever opening — the shape of an upgrade
      // the server rejects every time (e.g. unauthenticated). The source must
      // stop after the budget, not request forever.
      for (let i = 0; i < 6; i += 1) {
        await vi.advanceTimersByTimeAsync(20_000);
        sockets.at(-1)?.onclose?.();
      }

      expect(sockets).toHaveLength(3);
      expect(statuses.at(-1)).toMatchObject({ phase: "closed", reason: "retry-limit" });
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("createWebSocketProjectionSource multiplexing", () => {
  it("carries every subscribed scope over one socket and routes frames by scope", async () => {
    const sockets: FakeSocket[] = [];
    const quotesScope: ProjectionScope = { tenantId: "org_1", domain: "signals", collection: "quotes" };
    const multiplexCodec: ProjectionProtoCodec = {
      ...codec,
      decodeDeltaFrame: () =>
        ({
          mutations: [
            wireMutation({ recordId: "tick_5", version: 5 }),
            wireMutation({ recordId: "quote_1", version: 2, collection: "quotes" }),
          ],
        }) as ReturnType<ProjectionProtoCodec["decodeDeltaFrame"]>,
    };
    const ticks: ProjectionMutation[] = [];
    const quotes: ProjectionMutation[] = [];
    const source = createWebSocketProjectionSource({
      url: "wss://host/v1/projections",
      codec: multiplexCodec,
      createSocket: () => {
        const instance = new FakeSocket();
        sockets.push(instance);
        return instance as unknown as WebSocket;
      },
    });

    const unsubscribeTicks = source.subscribeProjection(scope, (m) => ticks.push(m as ProjectionMutation));
    const unsubscribeQuotes = source.subscribeProjection(quotesScope, (m) => quotes.push(m as ProjectionMutation));
    await Promise.resolve();

    // One socket for both scopes, connected at the gateway root.
    expect(sockets).toHaveLength(1);
    sockets[0].onopen?.();

    // The open handshake subscribes every registered scope in one frame.
    const subscribeFrame = JSON.parse(String(sockets[0].sent[0])) as {
      type: string;
      scopes: { domain: string; collection: string }[];
    };
    expect(subscribeFrame.type).toBe("subscribe");
    expect(subscribeFrame.scopes).toEqual([
      { domain: "signals", collection: "ticks" },
      { domain: "signals", collection: "quotes" },
    ]);

    // A mixed delta frame routes each mutation to its scope's listener.
    sockets[0].onmessage?.({ data: new Uint8Array([7]).buffer } as MessageEvent);
    expect(ticks.map((m) => m.recordId)).toEqual(["tick_5"]);
    expect(quotes.map((m) => m.recordId)).toEqual(["quote_1"]);

    // Dropping one scope sends an unsubscribe but keeps the socket for the other.
    unsubscribeQuotes();
    const lastFrame = JSON.parse(String(sockets[0].sent.at(-1))) as { type: string };
    expect(lastFrame.type).toBe("unsubscribe");
    expect(sockets).toHaveLength(1);

    // Dropping the last scope closes the shared socket for good.
    unsubscribeTicks();
    expect(sockets).toHaveLength(1);
  });

  it("degrades only the scope a tagged resync names", async () => {
    const degraded: string[] = [];
    let instance: FakeSocket | undefined;
    const quotesScope: ProjectionScope = { tenantId: "org_1", domain: "signals", collection: "quotes" };
    const source = createWebSocketProjectionSource({
      url: "wss://host/v1/projections",
      codec,
      createSocket: () => {
        instance = new FakeSocket();
        return instance as unknown as WebSocket;
      },
      onStatus: (status) => {
        if (status.phase === "degraded") degraded.push(status.scope.collection);
      },
    });
    const u1 = source.subscribeProjection(scope, () => {});
    const u2 = source.subscribeProjection(quotesScope, () => {});
    await Promise.resolve();
    instance!.onopen?.();
    instance!.onmessage?.({
      data: JSON.stringify({ type: "resync", dropped: 3, domain: "signals", collection: "quotes" }),
    } as MessageEvent);
    expect(degraded).toEqual(["quotes"]);
    u1();
    u2();
  });
});

describe("projection source composition", () => {
  it("composes explicit lanes and returns offline when endpoint derivation fails", () => {
    expect(createProjectionSource({ http: { baseUrl: "https://host", codec }, ws: { url: "wss://host", codec, createSocket: () => new FakeSocket() as unknown as WebSocket } })).toMatchObject({ loadProjection: expect.any(Function), subscribeProjection: expect.any(Function) });
    expect(createDefaultProjectionSource({ originUrl: "" })).toBeUndefined();
    expect(createDefaultProjectionSource({ baseUrl: "https://host/v1/projections", codec })).toMatchObject({ loadProjection: expect.any(Function), subscribeProjection: expect.any(Function) });
  });
});
