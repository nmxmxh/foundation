import { test } from "vitest";
import { routeRuntimeStream } from "./payloadRouter";

for (const bytes of [64 * 1024, 256 * 1024, 1024 * 1024, 4 * 1024 * 1024]) {
  test(`rechunk ${bytes / 1024} KiB from 1 KiB inputs`, async ({ bench }) => {
    const inputs = Array.from({ length: bytes / 1024 }, (_, index) => {
      const chunk = new Uint8Array(1024);
      chunk.fill(index % 251);
      return chunk;
    });
    let consumed = 0;

    await bench(`64 KiB output, ${bytes / 1024} KiB total`, async () => {
      for await (const chunk of routeRuntimeStream(inputs, { chunkBytes: 64 * 1024 })) {
        consumed += chunk.byteLength;
      }
    }).run();

    if (consumed === 0) {
      throw new Error("stream benchmark did not consume output");
    }
  });
}
