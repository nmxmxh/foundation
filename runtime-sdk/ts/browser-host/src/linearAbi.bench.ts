import { test } from "vitest";
import { createAbiFixture, loadAbiModule } from "../fixtures/browserAbi";

const module = await loadAbiModule(true);
for (const bytes of [4096, 65536, 1048576, 4194304]) {
  const { host, exports } = await createAbiFixture(module, true);
  const direct = host.createBuffer(bytes);
  const copied = new SharedArrayBuffer(bytes);
  const copiedHandle = host.registerBuffer(copied);
  direct.bytes.fill(3);
  new Uint8Array(copied).fill(3);
  const scan = (handle: number) => {
    if (exports.ovrt_buffer_checksum(handle, 0, bytes) !== bytes * 3) throw new Error("checksum mismatch");
  };
  for (let i = 0; i < 200; i += 1) { scan(direct.handle); scan(copiedHandle); }
  test(`Rust scan ${bytes} bytes`, async ({ bench }) => {
    await bench("copied host ABI", () => scan(copiedHandle)).run();
    await bench("direct linear ABI", () => scan(direct.handle)).run();
    host.dispose();
  });
}

for (const bytes of [128, 1024]) {
  test(`control request ${bytes} bytes`, async ({ bench }) => {
    const { host, exports } = await createAbiFixture(module, true);
    const direct = host.createRuntimeBuffer();
    const copied = new SharedArrayBuffer(4096);
    const legacy = host.registerBuffer(copied);
    const input = new Uint8Array(bytes).fill(3);
    const run = (buffer: typeof direct | SharedArrayBuffer, handle: number) => {
      host.setInputBytes(buffer, input);
      if (exports.ovrt_unit_run(handle) !== 0 || host.readOutputBytes(buffer)[bytes - 1] !== (3 ^ 0x5a)) {
        throw new Error("control request mismatch");
      }
      host.markOutputConsumed(buffer);
    };
    for (let i = 0; i < 200; i += 1) { run(direct, direct.handle); run(copied, legacy); }
    await bench("copied control request", () => run(copied, legacy)).run();
    await bench("direct control request", () => run(direct, direct.handle)).run();
    host.dispose();
  });
}
