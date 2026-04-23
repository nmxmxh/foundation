import { describe, expect, it, vi } from "vitest";

import { createEventStore } from "./eventStore";

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
