/** Summarize milliseconds with nearest-rank percentiles and an explicit sample floor. */
export function summarizeLatency(values: readonly number[]) {
  if (values.some((value) => !Number.isFinite(value) || value < 0)) {
    throw new RangeError("latency samples must be finite and nonnegative");
  }
  const sorted = [...values].sort((a, b) => a - b);
  const quantile = (fraction: number) => sorted.length
    ? Math.round(sorted[Math.max(0, Math.ceil(fraction * sorted.length) - 1)]! * 10) / 10
    : null;
  return {
    n: sorted.length,
    p50: quantile(0.5),
    p95: quantile(0.95),
    max: quantile(1),
    p95UnderSampled: sorted.length < 100,
  };
}
