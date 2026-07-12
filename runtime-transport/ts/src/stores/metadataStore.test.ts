import { describe, expect, it } from "vitest";

import { createMetadataStore, type BaseMetadata } from "./metadataStore";

describe("createMetadataStore", () => {
  it("initializes metadata with a generated runtime session id", () => {
    const store = createMetadataStore();

    expect(store.getState().metadata.global_context?.session_id).toMatch(/^session_/);
  });

  it("does not allow bearer-looking values to replace the runtime session id", () => {
    const store = createMetadataStore();
    const initialSessionId = store.getState().metadata.global_context?.session_id;

    store.getState().setMetadata({
      global_context: {
        session_id: "header.payload.signature",
        extras: {
          auth_token: "token_1",
        },
      },
    });

    expect(store.getState().metadata.global_context?.session_id).toBe(initialSessionId);
    expect(store.getState().metadata.global_context?.extras).toEqual({ auth_token: "token_1" });
  });

  it("deep-merges global_context extras instead of replacing them", () => {
    const store = createMetadataStore({
      global_context: {
        extras: {
          trace_hint: "edge",
        },
      },
    });

    store.getState().setMetadata({
      global_context: {
        extras: {
          auth_token: "token_2",
        },
      },
    });

    expect(store.getState().metadata.global_context?.extras).toEqual({
      trace_hint: "edge",
      auth_token: "token_2",
    });
  });

  it("updates metadata through an isolated copy and manages organization scope", () => {
    const store = createMetadataStore<BaseMetadata>({ correlationId: "corr-fixed", global_context: { organization_id: "org-1" } });
    store.getState().updateMetadata((current) => ({ ...current, correlationId: "", global_context: { ...current.global_context, extras: { mode: "updated" } } }));
    expect(store.getState().metadata).toMatchObject({ correlationId: "corr-fixed", global_context: { organization_id: "org-1", extras: { mode: "updated" } } });
    store.getState().setOrganizationId("org-2");
    expect(store.getState().metadata.global_context?.organization_id).toBe("org-2");
    store.getState().setOrganizationId(undefined);
    expect(store.getState().metadata.global_context?.organization_id).toBeUndefined();
    store.getState().reset();
    expect(store.getState().metadata.global_context).toMatchObject({ source: "backend" });
  });
});
