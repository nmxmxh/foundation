import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(fileURLToPath(import.meta.url));
const foundation = path.resolve(root, "../..");
const app = process.env.OVASABI_PROJECT;
if (!app)
  throw new Error("Set OVASABI_PROJECT to the Ovasabi application directory");

export default {
  root,
  publicDir: path.join(app, "frontend/public"),
  cacheDir: "/tmp/foundation-graphics-vite-cache",
  resolve: {
    preserveSymlinks: true,
    alias: {
      "@ovasabi/runtime-browser": path.join(
        foundation,
        "runtime-sdk/ts/browser-host",
      ),
      "@reference": path.join(app, "frontend/src/lib"),
      react: path.join(app, "frontend/node_modules/react"),
      "react-dom": path.join(app, "frontend/node_modules/react-dom"),
      zustand: path.join(app, "frontend/node_modules/zustand"),
    },
  },
  server: {
    host: "127.0.0.1",
    port: 5187,
    strictPort: true,
    fs: { allow: [foundation, path.join(app, "frontend")] },
    headers: {
      "Cross-Origin-Opener-Policy": "same-origin",
      "Cross-Origin-Embedder-Policy": "require-corp",
    },
  },
  build: {
    outDir: "/tmp/foundation-graphics-lab-build", emptyOutDir: true,
    rollupOptions: { input: [path.join(root, "index.html"), path.join(root, "fps.html")] },
  },
};
