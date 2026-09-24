import { RuntimeModuleLoader } from "@ovasabi/runtime-browser";

const loader = new RuntimeModuleLoader();
let loaded: Awaited<ReturnType<RuntimeModuleLoader["load"]>> | null = null;

self.onmessage = async (event: MessageEvent<{ kind: string; url?: string }>) => {
  try {
    if (event.data.kind === "INIT") {
      loaded = await loader.load("parity_fixture", undefined, { rawUrl: event.data.url });
      const region = loaded.controlBuffer;
      if (!region?.shared) throw new Error("shared linear ABI was not selected");
      self.postMessage({ memory: loaded.memory, byteOffset: region.byteOffset, byteLength: region.byteLength, handle: region.handle });
    } else if (event.data.kind === "RUN" && loaded?.controlBuffer) {
      const run = loaded.exports.ovrt_unit_run as (handle: number) => number;
      self.postMessage({ result: run(loaded.controlBuffer.handle) });
    } else {
      throw new Error("worker is not initialized");
    }
  } catch (error) {
    self.postMessage({ error: String(error) });
  }
};
