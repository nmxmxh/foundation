import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL(".", import.meta.url));
const runtimeTransport = fileURLToPath(new URL("../../runtime-transport/ts/src/index.ts", import.meta.url));

export default {
  root,
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
  benchmark: {
    include: ["src/**/*.bench.ts"],
  },
  resolve: {
    alias: {
      "@ovasabi/runtime-transport": runtimeTransport,
    },
  },
};
