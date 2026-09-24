import { readFileSync } from "node:fs";
import { BrowserRuntimeHost } from "../src/host";

export const loadAbiModule = (shared: boolean) => WebAssembly.compile(readFileSync(
  new URL(`./browser-abi/parity_fixture${shared ? ".shared" : ""}.wasm`, import.meta.url),
));

export async function createAbiFixture(module: WebAssembly.Module, shared: boolean) {
  const host = new BrowserRuntimeHost();
  const importedMemory = new WebAssembly.Memory({ initial: 32, maximum: 2048, shared });
  const imports = host.getImportObject({ env: { memory: importedMemory } });
  const env = imports.env as Record<string, (...args: number[]) => number>;
  const copies = { calls: 0, bytes: 0 };
  for (const name of ["ovrt_copy_to_buffer", "ovrt_copy_from_buffer"]) {
    const copy = env[name];
    env[name] = (...args: number[]) => { copies.calls += 1; copies.bytes += args[3]; return copy(...args); };
  }
  env.ovrt_get_now = () => 1_700_000_000_000;
  const instance = await WebAssembly.instantiate(module, imports);
  host.attachInstance(instance);
  const exports = instance.exports as {
    memory: WebAssembly.Memory;
    ovrt_unit_run: (handle: number) => number;
    ovrt_buffer_checksum: (handle: number, offset: number, length: number) => number;
  };
  return { host, exports, copies };
}
