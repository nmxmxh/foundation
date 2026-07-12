import { describe, expect, it } from "vitest";

import { looksLikeJWT, mergeGlobalContextExtras, normalizeRuntimeString, pickRuntimeSessionID, resolveRuntimeAuthToken, resolveRuntimeSessionID, stripBearerPrefix } from "./runtimeMetadata";

describe("runtime metadata normalization", () => {
  it("separates runtime session identity from bearer credentials", () => {
    expect(normalizeRuntimeString(4)).toBe("");
    expect(looksLikeJWT("")).toBe(false);
    expect(looksLikeJWT("a.b.c")).toBe(true);
    expect(stripBearerPrefix(" Bearer token ")).toBe("token");
    expect(pickRuntimeSessionID("loading", "Bearer a.b.c", "session-safe")).toBe("session-safe");
    expect(pickRuntimeSessionID("loading", "a.b.c")).toBe("");
    expect(resolveRuntimeSessionID("loading")).toMatch(/^session_/);
    expect(resolveRuntimeAuthToken("", "loading", "Bearer secret")).toBe("secret");
    expect(resolveRuntimeAuthToken("", "loading")).toBe("");
  });

  it("merges and deletes explicit global-context extras", () => {
    expect(mergeGlobalContextExtras({ keep: 1, remove: 2 }, { remove: null, add: "ok" })).toEqual({ keep: 1, add: "ok" });
    expect(mergeGlobalContextExtras([], { add: "ok" })).toEqual({ add: "ok" });
  });
});
