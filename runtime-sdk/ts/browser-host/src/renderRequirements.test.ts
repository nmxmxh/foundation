import { describe, expect, it } from "vitest";
import {
  renderRequirementsFailure,
  type RenderSurfaceEvidence,
  type RenderSurfaceRequirements,
} from "./renderRequirements";

const evidence: RenderSurfaceEvidence = {
  version: 1,
  completion: "settled",
  state: "shared",
  maxBackingPixels: 1000,
};

describe("render implementation requirements", () => {
  it("accepts legacy callers and checked implementations", () => {
    expect(renderRequirementsFailure(undefined, undefined)).toBeNull();
    expect(
      renderRequirementsFailure(
        {
          gpuCompletion: "required",
          sharedState: "required",
          maxBackingPixels: 1000,
        },
        evidence,
      ),
    ).toBeNull();
  });
  it("rejects missing evidence and unsupported implementations", () => {
    expect(renderRequirementsFailure({}, undefined)).toBe(
      "render evidence unavailable",
    );
    expect(
      renderRequirementsFailure({}, {
        ...evidence,
        version: 2,
      } as unknown as RenderSurfaceEvidence),
    ).toBe("render evidence unavailable");
    expect(
      renderRequirementsFailure(
        { gpuCompletion: "required" },
        { ...evidence, completion: "submitted" },
      ),
    ).toBe("GPU completion unavailable");
    expect(
      renderRequirementsFailure(
        { sharedState: "required" },
        { ...evidence, state: "messages" },
      ),
    ).toBe("shared state unavailable");
    expect(
      renderRequirementsFailure({ maxBackingPixels: 1001 }, evidence),
    ).toBe("backing pixel budget unsupported");
  });
  it("validates structured-clone input at runtime", () => {
    for (const maxBackingPixels of [0, -1, 1.5, NaN, Infinity, 67_108_865]) {
      expect(renderRequirementsFailure({ maxBackingPixels }, evidence)).toBe(
        "invalid backing pixel budget",
      );
    }
    expect(
      renderRequirementsFailure(
        { gpuCompletion: true } as unknown as RenderSurfaceRequirements,
        evidence,
      ),
    ).toBe("invalid completion requirement");
    expect(
      renderRequirementsFailure(
        { sharedState: true } as unknown as RenderSurfaceRequirements,
        evidence,
      ),
    ).toBe("invalid state requirement");
  });
});
