/** Requirements apply to the selected pass and its worker state channel. */
export type RenderSurfaceRequirements = {
  gpuCompletion?: "required";
  sharedState?: "required";
  maxBackingPixels?: number;
};

/** Evidence describes the admitted implementation, including its actual fallback behavior. */
export type RenderSurfaceEvidence = {
  version: 1;
  completion: "settled" | "submitted";
  state: "shared" | "messages";
  maxBackingPixels?: number;
};

export function renderRequirementsFailure(
  requirements: RenderSurfaceRequirements | undefined,
  evidence: RenderSurfaceEvidence | undefined,
): string | null {
  if (!requirements) return null;
  if (
    requirements.gpuCompletion !== undefined &&
    requirements.gpuCompletion !== "required"
  )
    return "invalid completion requirement";
  if (
    requirements.sharedState !== undefined &&
    requirements.sharedState !== "required"
  )
    return "invalid state requirement";
  const pixels = requirements.maxBackingPixels;
  if (
    pixels !== undefined &&
    (!Number.isSafeInteger(pixels) || pixels < 1 || pixels > 67_108_864)
  )
    return "invalid backing pixel budget";
  if (!evidence || evidence.version !== 1) return "render evidence unavailable";
  if (requirements.gpuCompletion && evidence.completion !== "settled")
    return "GPU completion unavailable";
  if (requirements.sharedState && evidence.state !== "shared")
    return "shared state unavailable";
  if (pixels !== undefined && evidence.maxBackingPixels !== pixels)
    return "backing pixel budget unsupported";
  return null;
}
