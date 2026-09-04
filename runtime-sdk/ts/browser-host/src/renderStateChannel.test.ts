import { describe, expect, it, vi } from "vitest";

import {
  attachRenderStateReader,
  createRenderStateChannel,
  renderStateChannelBytes,
} from "./renderStateChannel";

const CONTROL_ELEMENTS = 16;
const SLOTS = 3;

const fillWith = (values: readonly number[]) => (slot: Float32Array) => {
  slot.set(values);
  return values.length;
};

describe("the shared render state channel", () => {
  it("carries a generation across without a clone", () => {
    const channel = createRenderStateChannel(8)!;
    const reader = attachRenderStateReader(channel.buffer)!;

    expect(reader.read()).toBeNull(); // nothing published yet
    expect(reader.current).toBeNull();

    channel.write(fillWith([1, 2, 3]));
    expect(Array.from(reader.read()!)).toEqual([1, 2, 3]);
    expect(reader.generation).toBe(1);
  });

  /*
   * The frame a surface spends most of its life in. A 40Hz surface reading state
   * that only moves on pointer input finds nothing new on almost every frame,
   * and that frame must cost nothing — not a copy, not even an exchange.
   */
  it("does no work at all when the generation has not moved", () => {
    const channel = createRenderStateChannel(8)!;
    const reader = attachRenderStateReader(channel.buffer)!;
    channel.write(fillWith([7, 8]));

    expect(reader.read()).not.toBeNull();
    expect(reader.read()).toBeNull();
    expect(reader.read()).toBeNull();
    expect(reader.diagnostics.skips).toBe(2);
    expect(reader.diagnostics.retries).toBe(0);
    // The data is still held; only the take was skipped.
    expect(Array.from(reader.current!)).toEqual([7, 8]);
  });

  /**
   * The race the whole design exists for, and the reason there are three slots.
   *
   * INOS's `epoch % 2` is safe because its writer and reader both run at 60 Hz.
   * Here the host writes on `requestAnimationFrame` and a paper texture reads at
   * 4 Hz, so the writer laps the reader — and with two slots, two laps land back
   * on the slot being read. With three it can lap forever without touching what
   * the reader holds, so this is not a retry test: it tests that the situation
   * which used to need retries no longer arises.
   */
  it("lets the writer lap the reader indefinitely without disturbing it", () => {
    const channel = createRenderStateChannel(4)!;
    const reader = attachRenderStateReader(channel.buffer)!;

    channel.write(fillWith([1, 1, 1, 1]));
    const held = reader.read()!;
    expect(Array.from(held)).toEqual([1, 1, 1, 1]);

    // Thirty generations while the reader holds one — the 120Hz-host,
    // 4Hz-surface ratio, exactly.
    for (let generation = 2; generation <= 31; generation += 1) {
      channel.write(fillWith([generation, generation, generation, generation]));
    }

    // The view taken before all of that is still the generation it was.
    expect(Array.from(held)).toEqual([1, 1, 1, 1]);
    expect(channel.diagnostics.retries).toBe(0);

    // And the next take is a whole generation, never a mixture of two.
    const fresh = Array.from(reader.read()!);
    expect(new Set(fresh).size).toBe(1);
    expect(fresh[0]).toBe(31);
  });

  it("hands back the slot itself rather than a copy", () => {
    const channel = createRenderStateChannel(4)!;
    const reader = attachRenderStateReader(channel.buffer)!;
    channel.write(fillWith([1, 2, 3, 4]));

    // A view onto the shared buffer, not a private copy of it. This is the whole
    // difference between O(1) and O(n) on the reading side.
    expect(reader.read()!.buffer).toBe(channel.buffer);
  });

  it("reuses its slot views instead of allocating one per read", () => {
    const channel = createRenderStateChannel(8)!;
    const reader = attachRenderStateReader(channel.buffer)!;

    const seen = new Set<Float32Array>();
    for (let i = 0; i < 12; i += 1) {
      channel.write(fillWith([i, i, i]));
      seen.add(reader.read()!);
    }
    expect(seen.size).toBeLessThanOrEqual(SLOTS);
  });

  it("rebuilds a slot view only when that slot's length changes", () => {
    const channel = createRenderStateChannel(8)!;
    const reader = attachRenderStateReader(channel.buffer)!;

    channel.write(fillWith([1, 2, 3]));
    expect(reader.read()!.length).toBe(3);

    channel.write(fillWith([1, 2, 3, 4, 5]));
    const five = reader.read()!;
    expect(five.length).toBe(5);
    expect(Array.from(five)).toEqual([1, 2, 3, 4, 5]);
  });

  it("never lets the writer and reader own the same slot", () => {
    const channel = createRenderStateChannel(4)!;
    const reader = attachRenderStateReader(channel.buffer)!;
    const control = new Int32Array(channel.buffer, 0, CONTROL_ELEMENTS);

    // Interleave in every order the two sides can actually produce, and check
    // the invariant the protocol rests on after each step.
    const distinct = () => {
      const flags = Atomics.load(control, 1);
      const indices = new Set([flags & 0x03, (flags & 0x0c) >> 2, (flags & 0x30) >> 4]);
      expect(indices.size).toBe(SLOTS);
    };

    distinct();
    for (let i = 0; i < 40; i += 1) {
      channel.write(fillWith([i, i, i, i]));
      distinct();
      if (i % 3 === 0) {
        reader.read();
        distinct();
      }
    }
  });

  it("keeps the previous generation when a fill overruns the slot", () => {
    const channel = createRenderStateChannel(4)!;
    const reader = attachRenderStateReader(channel.buffer)!;
    channel.write(fillWith([9, 9]));
    expect(Array.from(reader.read()!)).toEqual([9, 9]);

    expect(channel.write(() => 999)).toBe(false);
    // Nothing new was published, so nothing new is read and the good data holds.
    expect(reader.read()).toBeNull();
    expect(Array.from(reader.current!)).toEqual([9, 9]);
    expect(channel.diagnostics.writes).toBe(1);
  });

  it("refuses a buffer it did not write", () => {
    expect(attachRenderStateReader(new SharedArrayBuffer(8))).toBeNull();
    expect(attachRenderStateReader(new SharedArrayBuffer(1024))).toBeNull();
  });

  it("reports absent rather than throwing where shared memory is unavailable", () => {
    vi.stubGlobal("SharedArrayBuffer", undefined);
    expect(createRenderStateChannel(16)).toBeNull();
    vi.unstubAllGlobals();
  });

  it("sizes itself for three slots plus a control block", () => {
    expect(renderStateChannelBytes(16)).toBe(CONTROL_ELEMENTS * 4 + SLOTS * 16 * 4);
    expect(createRenderStateChannel(16)!.buffer.byteLength).toBe(renderStateChannelBytes(16));
  });
});
