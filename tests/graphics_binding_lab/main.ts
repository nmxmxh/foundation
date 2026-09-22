import { createRenderSurfaceHost } from "@ovasabi/runtime-browser";

const run = document.querySelector<HTMLButtonElement>("#run")!;
const save = document.querySelector<HTMLButtonElement>("#save")!;
const report = document.querySelector<HTMLElement>("#report")!;
const surface = document.querySelector<HTMLElement>("#surface")!;
let lastReport = "";

run.onclick = () => {
  const maxBackingPixels = Number(
    document.querySelector<HTMLInputElement>("#pixels")!.value,
  );
  if (
    !Number.isSafeInteger(maxBackingPixels) ||
    maxBackingPixels < 10000 ||
    maxBackingPixels > 4000000
  ) {
    report.textContent = "Choose a pixel limit from 10,000 to 4,000,000.";
    return;
  }
  const mode = document.querySelector<HTMLSelectElement>("#mode")!.value;
  run.disabled = true;
  save.disabled = true;
  surface.replaceChildren();
  const canvas = document.createElement("canvas");
  surface.append(canvas);
  const worker = new Worker(new URL("./worker.ts", import.meta.url), {
    type: "module",
  });
  worker.postMessage({ kind: "LAB_CONFIG", mode });
  let evidence: unknown;
  let finished = false;
  let timer: ReturnType<typeof setTimeout>;
  const finish = (result: unknown) => {
    if (finished) return;
    finished = true;
    clearTimeout(timer);
    lastReport = JSON.stringify(
      {
        mode,
        evidence,
        pixelLimit: maxBackingPixels,
        viewport: {
          width: innerWidth,
          height: innerHeight,
          ratio: devicePixelRatio,
        },
        crossOriginIsolated,
        sample: result,
      },
      null,
      2,
    );
    report.textContent = lastReport;
    run.disabled = false;
    save.disabled = false;
    host.dispose();
    worker.terminate();
  };
  const host = createRenderSurfaceHost({
    canvas,
    createWorker: () => worker,
    ownsWorker: false,
    surface: "blackHole",
    tiers: [
      { scale: 1, cadenceMs: 25, detail: 160 },
      { scale: 0.7, cadenceMs: 50, detail: 96 },
      { scale: 0.5, cadenceMs: 100, detail: 64 },
    ],
    startingTier: 0,
    stateChannelElements: 4,
    requirements: {
      maxBackingPixels,
      ...(mode === "settled" ? { gpuCompletion: "required" as const } : {}),
    },
    initialState: { pointerX: 0, pointerY: 0, level: 0, reduced: false },
    onEvidence: (value) => {
      evidence = value;
      report.textContent = JSON.stringify(value, null, 2);
      worker.postMessage({ kind: "LAB_START" });
    },
    onFailed: (reason) => queueMicrotask(() => finish({ failure: reason })),
  });
  worker.addEventListener("message", (event) => {
    if (event.data.kind === "LAB_RESULT") finish(event.data.result);
  });
  timer = setTimeout(
    () => finish({ failure: "sample exceeded its 15-second bound" }),
    15000,
  );
};

save.onclick = () => {
  const url = URL.createObjectURL(
    new Blob([lastReport], { type: "application/json" }),
  );
  const link = document.createElement("a");
  link.href = url;
  link.download = "foundation-graphics-evidence.json";
  link.click();
  URL.revokeObjectURL(url);
};
