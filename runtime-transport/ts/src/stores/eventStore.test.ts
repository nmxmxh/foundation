import { describe, expect, it, vi } from "vitest";

import { buildRequestFingerprint, clearLoadingState, createEventStore, isMutationRequestedEvent, isReplayEligibleRequest, setLoadingState, stableStringify } from "./eventStore";

describe("createEventStore", () => {
  it("coalesces semantically identical read requests within the same metadata context", async () => {
    let resolveDispatch: ((value: { ok: boolean }) => void) | undefined;
    const dispatch = vi.fn(
      () =>
        new Promise<{ ok: boolean }>((resolve) => {
          resolveDispatch = resolve;
        })
    );

    const eventStore = createEventStore(dispatch, () => ({
      session_id: "session_1",
      organization_id: "org_1",
    }));

    const first = eventStore.emitEvent("media:get_asset_preview:v1:requested", {
      assetPublicId: "asset_123",
      filters: { b: 2, a: 1 },
    });
    const second = eventStore.emitEvent("media:get_asset_preview:v1:requested", {
      filters: { a: 1, b: 2 },
      assetPublicId: "asset_123",
    });

    expect(dispatch).toHaveBeenCalledTimes(1);

    if (!resolveDispatch) {
      throw new Error("dispatch resolver was not created");
    }
    resolveDispatch({ ok: true });

    await expect(first).resolves.toEqual({ ok: true });
    await expect(second).resolves.toEqual({ ok: true });
  });

  it("tracks keyed loading, errors, cache lifecycle, and reset", async () => {
    const eventStore = createEventStore(async () => { throw "offline"; });
    await expect(eventStore.emitEvent("asset:get:v1:requested", {}, { loadingKey: "asset" })).rejects.toThrow("offline");
    expect(eventStore.store.getState()).toMatchObject({ status: "error", lastError: "offline", isLoading: false });
    eventStore.store.getState().setIsLoading(true, "a");
    eventStore.store.getState().setIsLoading(true, "a");
    expect(eventStore.store.getState().isLoadingKeyActive("a")).toBe(true);
    eventStore.store.getState().setIsLoading(false, "a");
    eventStore.store.getState().clearLoadingState("a");
    eventStore.store.getState().clearCache();
    eventStore.store.getState().reset();
    expect(eventStore.store.getState()).toMatchObject({ status: "idle", lastError: null, loadingStates: {} });
  });

  // Regression: status describes the batch of overlapping requests, not the last one to settle.
  it("keeps status loading while requests overlap and keeps a batch failure", async () => {
    const settlers: Array<{ resolve: (value: unknown) => void; reject: (error: Error) => void }> = [];
    const dispatch = vi.fn(() => new Promise<unknown>((resolve, reject) => { settlers.push({ resolve, reject }); }));
    const eventStore = createEventStore(dispatch);
    const state = () => eventStore.store.getState();

    const failing = eventStore.emitEvent("asset:update:v1:requested", { id: 1 });
    const passing = eventStore.emitEvent("asset:update:v1:requested", { id: 2 });
    settlers[0].reject(new Error("first failed"));
    await expect(failing).rejects.toThrow("first failed");
    expect(state()).toMatchObject({ status: "loading", lastError: "first failed" });

    settlers[1].resolve({ ok: true });
    await passing;
    expect(state()).toMatchObject({ status: "error", lastError: "first failed" });

    const next = eventStore.emitEvent("asset:update:v1:requested", { id: 3 });
    expect(state()).toMatchObject({ status: "loading", lastError: null });
    settlers[2].resolve({ ok: true });
    await next;
    expect(state()).toMatchObject({ status: "success", lastError: null });
  });

  it("ignores requests from before reset when it computes status", async () => {
    const settlers: Array<(value: unknown) => void> = [];
    const dispatch = vi.fn(() => new Promise<unknown>((resolve) => { settlers.push(resolve); }));
    const eventStore = createEventStore(dispatch);
    const stale = eventStore.emitEvent("asset:update:v1:requested", { id: 1 });
    eventStore.store.getState().reset();
    const fresh = eventStore.emitEvent("asset:update:v1:requested", { id: 2 });
    settlers[0]({ ok: true });
    await stale;
    expect(eventStore.store.getState().status).toBe("loading");
    settlers[1]({ ok: true });
    await fresh;
    expect(eventStore.store.getState().status).toBe("success");
  });

  it("normalizes stable replay fingerprints for binary and cyclic values", () => {
    expect(stableStringify({ b: 2, a: new Uint8Array([1, 2]) })).toBe(stableStringify({ a: new Uint8Array([1, 2]), b: 2 }));
    expect(buildRequestFingerprint(new ArrayBuffer(2))).toMatch(/^[a-z0-9]+$/);
    const cyclic: Record<string, unknown> = {};
    cyclic.self = cyclic;
    expect(() => stableStringify(cyclic)).toThrow();
    expect(isMutationRequestedEvent("workspace:update:v1:requested")).toBe(true);
    expect(isMutationRequestedEvent("workspace:get:v1:success")).toBe(false);
    expect(isReplayEligibleRequest("auth:get:v1:requested")).toBe(false);
    expect(isReplayEligibleRequest("workspace:get:v1:requested")).toBe(true);
    expect(clearLoadingState({}, "missing")).toEqual({});
    expect(setLoadingState(setLoadingState({}, true), false)).toEqual({});
  });

  it("does not coalesce mutation requests unless explicitly opted in", async () => {
    let resolvers: Array<(value: { ok: boolean }) => void> = [];
    const dispatch = vi.fn(
      () =>
        new Promise<{ ok: boolean }>((resolve) => {
          resolvers = [...resolvers, resolve];
        })
    );

    const eventStore = createEventStore(dispatch, () => ({
      session_id: "session_1",
      organization_id: "org_1",
    }));

    const first = eventStore.emitEvent("workspace:create_review_task:v1:requested", {
      workspacePublicId: "workspace_1",
    });
    const second = eventStore.emitEvent("workspace:create_review_task:v1:requested", {
      workspacePublicId: "workspace_1",
    });

    expect(dispatch).toHaveBeenCalledTimes(2);
    expect(eventStore.store.getState().isLoading).toBe(true);

    resolvers[0]?.({ ok: true });
    await Promise.resolve();
    expect(eventStore.store.getState().isLoading).toBe(true);

    resolvers[1]?.({ ok: true });
    await expect(Promise.all([first, second])).resolves.toEqual([{ ok: true }, { ok: true }]);
    expect(eventStore.store.getState().isLoading).toBe(false);
  });

  it("allows mutation coalescing when the caller opts in", async () => {
    let resolveDispatch: ((value: { ok: boolean }) => void) | undefined;
    const dispatch = vi.fn(
      () =>
        new Promise<{ ok: boolean }>((resolve) => {
          resolveDispatch = resolve;
        })
    );

    const eventStore = createEventStore(dispatch, () => ({
      session_id: "session_1",
      organization_id: "org_1",
    }));

    const first = eventStore.emitEvent(
      "workspace:create_review_task:v1:requested",
      { workspacePublicId: "workspace_1" },
      { coalescingPolicy: "always" }
    );
    const second = eventStore.emitEvent(
      "workspace:create_review_task:v1:requested",
      { workspacePublicId: "workspace_1" },
      { coalescingPolicy: "always" }
    );

    expect(dispatch).toHaveBeenCalledTimes(1);

    if (!resolveDispatch) {
      throw new Error("dispatch resolver was not created");
    }
    resolveDispatch({ ok: true });

    await expect(first).resolves.toEqual({ ok: true });
    await expect(second).resolves.toEqual({ ok: true });
  });

  it("does not reuse cached responses across metadata contexts", async () => {
    const dispatch = vi.fn(async () => ({ ok: true }));
    let metadataContext = {
      session_id: "session_1",
      organization_id: "org_1",
    };

    const eventStore = createEventStore(dispatch, () => metadataContext);

    await eventStore.emitEvent(
      "workspace:list_review_tasks:v1:requested",
      { workspacePublicId: "workspace_1" },
      { cacheDurationMs: 5_000 }
    );

    metadataContext = {
      session_id: "session_2",
      organization_id: "org_1",
    };

    await eventStore.emitEvent(
      "workspace:list_review_tasks:v1:requested",
      { workspacePublicId: "workspace_1" },
      { cacheDurationMs: 5_000 }
    );

    expect(dispatch).toHaveBeenCalledTimes(2);
  });

  it("keeps replay keys stable when only non-identity metadata changes", async () => {
    const dispatch = vi.fn(async () => ({ ok: true }));
    let metadataContext = {
      correlationId: "corr_1",
      global_context: {
        session_id: "session_1",
        organization_id: "org_1",
        extras: {
          auth_token: "token_1",
        },
      },
    };

    const eventStore = createEventStore(dispatch, () => metadataContext);

    await eventStore.emitEvent(
      "workspace:list_review_tasks:v1:requested",
      { workspacePublicId: "workspace_1" },
      { cacheDurationMs: 5_000 }
    );

    metadataContext = {
      correlationId: "corr_2",
      global_context: {
        session_id: "session_1",
        organization_id: "org_1",
        extras: {
          auth_token: "token_2",
        },
      },
    };

    await eventStore.emitEvent(
      "workspace:list_review_tasks:v1:requested",
      { workspacePublicId: "workspace_1" },
      { cacheDurationMs: 5_000 }
    );

    expect(dispatch).toHaveBeenCalledTimes(1);
  });
});
