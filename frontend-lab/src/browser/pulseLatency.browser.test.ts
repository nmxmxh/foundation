import { commands } from "vitest/browser";
import { describe, expect, it } from "vitest";
import { BUFFER_TOTAL_BYTES, IDX_RUNTIME_TICK, createPulseManager } from "@ovasabi/runtime-browser";

/*
 * How long a real module pulse worker takes to deliver its first tick in an
 * isolated page. frameClock gives the worker a fixed window before it drives
 * itself; if a cold worker routinely misses that window, the clock never runs
 * on its worker lane in practice. This measures the window it actually needs.
 */

const pulseWorkerUrl = new URL("../../../runtime-sdk/ts/browser-host/src/pulse/pulse.worker.ts", import.meta.url);

type Diagnostics = { mode: string; degraded: boolean; issues: unknown[] };

describe("pulse worker first-tick latency (chromium, cross-origin isolated)", () => {
  it("delivers ticks from the worker, and records how long the first one took", async () => {
    const diagnostics: Diagnostics[] = [];
    const tickTimes: number[] = [];
    const manager = createPulseManager({
      createWorker: () => new Worker(pulseWorkerUrl, { type: "module" }),
      onDiagnostics: (d: Diagnostics) => diagnostics.push({ mode: d.mode, degraded: d.degraded, issues: d.issues }),
    });
    manager.watchEpochs([IDX_RUNTIME_TICK], () => {
      tickTimes.push(performance.now());
    });

    const startedAt = performance.now();
    manager.start(new SharedArrayBuffer(BUFFER_TOTAL_BYTES));

    const deadline = startedAt + 5000;
    while (tickTimes.length < 10 && performance.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    manager.stop();

    const firstTickMs = tickTimes.length ? Math.round(tickTimes[0] - startedAt) : null;
    const intervals = tickTimes.slice(1).map((t, i) => Math.round(t - tickTimes[i]));
    // Browser mode does not forward console output to the terminal, so the
    // measurement is written to results/ from the Node side instead. The path
    // resolves from the lab root, not from this file.
    await commands.writeFile(
      "results/pulse-latency.json",
      `${JSON.stringify(
        {
          measuredAt: new Date().toISOString(),
          userAgent: navigator.userAgent,
          firstTickMs,
          frameClockGraceMs: 120,
          missedGraceWindow: firstTickMs === null ? null : firstTickMs > 120,
          ticks: tickTimes.length,
          intervals,
          diagnostics: diagnostics.map((d) => `${d.mode}${d.degraded ? " degraded" : ""}`),
        },
        null,
        2,
      )}\n`,
    );

    expect(diagnostics.some((d) => d.mode === "worker" && !d.degraded), "pulse never reported the worker lane").toBe(true);
    expect(tickTimes.length, "worker never delivered ten ticks").toBeGreaterThanOrEqual(10);
    expect(firstTickMs).not.toBeNull();
  });
});
