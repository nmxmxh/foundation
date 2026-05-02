export interface WasmManifestArtifact {
  id: string;
  role: "go-compat" | "kernel" | "rust-module" | string;
  kind: "wasm";
  url: string;
  brotliUrl?: string;
  bytes: number;
}

export interface WasmManifest {
  schemaVersion: number;
  generatedAt: string;
  artifacts: WasmManifestArtifact[];
}

export interface LoadWasmManifestOptions {
  fetcher?: typeof fetch;
  manifestUrl?: string;
  required?: boolean;
}

export async function loadWasmManifest(options: LoadWasmManifestOptions = {}): Promise<WasmManifest | null> {
  const fetcher = options.fetcher ?? globalThis.fetch;
  if (!fetcher) {
    if (options.required) throw new Error("fetch is not available to load the WASM manifest");
    return null;
  }

  const response = await fetcher(options.manifestUrl ?? "/runtime/wasm-manifest.json", {
    headers: { accept: "application/json" },
  });

  if (!response.ok) {
    if (options.required) throw new Error(`WASM manifest request failed with ${response.status}`);
    return null;
  }

  return (await response.json()) as WasmManifest;
}

export function findWasmArtifact(
  manifest: WasmManifest | null | undefined,
  role: WasmManifestArtifact["role"],
): WasmManifestArtifact | undefined {
  return manifest?.artifacts.find((artifact) => artifact.role === role);
}
