import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL(".", import.meta.url));
const runtimeBrowser = fileURLToPath(new URL("../../runtime-sdk/ts/browser-host/src/index.ts", import.meta.url));
const runtimeTransport = fileURLToPath(new URL("../../runtime-transport/ts/src/index.ts", import.meta.url));
const serial = process.env.FOUNDATION_VITEST_SERIAL !== "0";
const maxWorkers = Number.parseInt(process.env.FOUNDATION_VITEST_WORKERS ?? "0", 10);

export default {
  root,
  test: {
    environment: "node",
    fileParallelism: !serial,
    ...(Number.isFinite(maxWorkers) && maxWorkers > 0 ? { maxWorkers } : {}),
    include: ["src/**/*.test.ts"],
  },
  benchmark: {
    include: ["src/**/*.bench.ts"],
  },
  resolve: {
    alias: {
      "@ovasabi/runtime-browser": runtimeBrowser,
      "@ovasabi/runtime-transport": runtimeTransport,
    },
  },
};
