const run = document.querySelector<HTMLButtonElement>("#run")!;
const save = document.querySelector<HTMLButtonElement>("#save")!;
const status = document.querySelector<HTMLElement>("#status")!;
const report = document.querySelector<HTMLElement>("#report")!;
const summary = document.querySelector<HTMLElement>("#summary")!;
const surface = document.querySelector<HTMLElement>("#surface")!;
let results: Record<string, any>[] = [];

function sample(mode: string, width: number): Promise<Record<string, any>> {
  return new Promise((resolve, reject) => {
    const canvas = document.createElement("canvas");
    surface.replaceChildren(canvas);
    const worker = new Worker(new URL("./fps.worker.ts", import.meta.url), {
      type: "module",
    });
    const height = (width * 9) / 16;
    let done = false;
    let wasHidden = document.hidden;
    const visibility = () => { wasHidden ||= document.hidden; };
    document.addEventListener("visibilitychange", visibility);
    const finish = (result: Record<string, any>, failed = false) => {
      if (done) return;
      done = true;
      clearTimeout(timeout);
      document.removeEventListener("visibilitychange", visibility);
      worker.terminate();
      if (wasHidden) reject(new Error("Page became hidden; discard this comparison."));
      else if (failed) reject(new Error(JSON.stringify(result)));
      else resolve(result);
    };
    const timeout = setTimeout(
      () => finish({ failure: "20-second sample deadline" }, true),
      20000,
    );
    worker.onerror = (event) => finish({ failure: event.message }, true);
    worker.onmessage = (event) => {
      if (event.data.kind === "RESULT") finish(event.data.result);
      if (event.data.kind === "FAILED")
        finish({ failure: event.data.reason }, true);
    };
    const offscreen = canvas.transferControlToOffscreen();
    worker.postMessage({ mode, canvas: offscreen, width, height }, [offscreen]);
  });
}
run.onclick = async () => {
  const comparison =
    document.querySelector<HTMLSelectElement>("#comparison")!.value;
  const width = Number(
    document.querySelector<HTMLSelectElement>("#resolution")!.value,
  );
  if (![1280, 1920, 2560].includes(width)) return;
  const control = comparison === "cadence" ? "current40" : "baseline60";
  const order = [
    control,
    "current60",
    "current60",
    control,
    control,
    "current60",
  ];
  results = [];
  run.disabled = true;
  save.disabled = true;
  summary.textContent = "";
  try {
    for (let index = 0; index < order.length; index++) {
      status.textContent = `Sample ${index + 1}/6 · ${order[index]} · ${width} × ${(width * 9) / 16}. Keep this page visible.`;
      const result = await sample(order[index]!, width);
      results.push({
        ...result,
        userAgent: navigator.userAgent,
        crossOriginIsolated,
        hidden: document.hidden,
      });
      report.textContent = JSON.stringify(results, null, 2);
      if (document.hidden)
        throw new Error("Page became hidden; discard this comparison.");
    }
    const median = (mode: string) =>
      results
        .filter((row) => row.mode === mode)
        .map((row) => row.completedFps)
        .sort((a, b) => a - b)[1]!;
    const before = median(control),
      after = median("current60");
    summary.textContent = `Median completed FPS: ${before.toFixed(1)} → ${after.toFixed(1)} (${((after / before - 1) * 100).toFixed(1)}%).`;
    status.textContent =
      "Complete. Same camera, shader detail, backing resolution, and one outstanding GPU frame. Results apply to this device and workload.";
  } catch (error) {
    status.textContent = `Comparison failed: ${String(error)}`;
  } finally {
    run.disabled = false;
    save.disabled = results.length === 0;
  }
};
save.onclick = () => {
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(results, null, 2)], { type: "application/json" }),
  );
  const link = document.createElement("a");
  link.href = url;
  link.download = "foundation-fps-comparison.json";
  link.click();
  URL.revokeObjectURL(url);
};
