/**
 * A shared-memory state channel for the render surface lane.
 *
 * ## What it replaces
 *
 * `setState` posts a message, and a posted message is a structured clone: the
 * runtime walks the object, allocates a copy, and the worker allocates another
 * on the way out. For a pointer position that is nothing. For the payloads a
 * surface actually wants — per-entity transforms, audio levels, a particle
 * field — it is the dominant cost of the frame, paid twice, on the main thread,
 * every frame, forever.
 *
 * Foundation has owned the machinery to avoid this the whole time and its own
 * render lane never touched it. This is the light lane beside `arena.ts`'s
 * heavy one: the arena is a descriptor queue for variable-shaped batches with
 * lifecycle states, and it is the wrong shape for one fixed-size latest-value
 * frame written by one producer and read by one consumer. That case gets three
 * atomics and a memcpy.
 *
 * ## Three versions, not two
 *
 * INOS pairs two buffers with an epoch and selects with `epoch % 2` — physics
 * writes A while rendering reads B, no locks. That works because its writer and
 * reader both run at 60 Hz, so the writer can never lap the reader.
 *
 * A render surface breaks that assumption on purpose. The host writes on
 * `requestAnimationFrame` — 120 Hz on a ProMotion panel — and the worker reads
 * at its rung's cadence, which is 4 Hz for a paper texture. The writer laps the
 * reader thirty times between reads, and two laps land back on the slot being
 * read.
 *
 * The obvious repair is a sequence lock: read, then check the sequence did not
 * move, and copy into a private buffer so the check means something. It is
 * correct, and it costs a full copy of the payload on every read — 750 ns for
 * 16KB, on the worker, every frame.
 *
 * The better answer is the one MVCC has always given: **stop treating state as
 * a single present tense.** Keep more versions than there are participants, and
 * a reader can hold a stable one for as long as it likes while the writer
 * carries on — no copy, no retry, no blocking, in either direction.
 *
 * Three slots is the smallest number that achieves it for one writer and one
 * reader. The writer owns one, the reader owns one, and the third is the
 * handoff, swapped by a single compare-and-exchange. Neither side ever waits
 * for the other and neither side ever touches memory the other owns, so a read
 * is an index lookup rather than a memcpy — O(1) instead of O(n).
 *
 * The cost is one extra slot of memory. For a 16KB payload that is 16KB, to
 * remove 750 ns from every frame of every surface.
 *
 * ## The handoff word
 *
 * One atomic integer carries the whole protocol:
 *
 * ```
 *   bits 0-1  write   the slot the writer owns
 *   bits 2-3  spare   the handoff, most recently published
 *   bits 4-5  read    the slot the reader owns
 *   bit  6    fresh   spare holds a generation the reader has not taken
 * ```
 *
 * Publishing swaps `write` and `spare` and sets `fresh`. Acquiring swaps `read`
 * and `spare` and clears it. Both are one compare-and-exchange on one word, and
 * because the two sides only ever move their own index into the shared one, no
 * ordering of them can produce a slot owned twice.
 *
 * ## What is still copied, and by whom
 *
 * Nothing, on the reading side. On the writing side `fill` receives the slot
 * directly, so a caller that *computes* its state — transforms, a particle
 * field — writes it once, into its final location, and pays nothing at all. A
 * caller that already holds the data elsewhere pays one bulk `set`, which is
 * the irreducible cost of getting bytes into shared memory and is still the
 * bulk form INOS measures at three orders of magnitude over per-element work.
 */

/** Control-block slot indices, in `Int32Array` elements. */
const IDX_MAGIC = 0;
/** The handoff word: write/spare/read indices plus the freshness bit. */
const IDX_FLAGS = 1;
/** Elements each slot can hold. */
const IDX_CAPACITY = 2;
/** Generations published. Also the version a reader reports. */
const IDX_GENERATION = 3;
/** Diagnostics: publishes, contended exchanges, reads that found nothing new. */
const IDX_WRITES = 4;
const IDX_RETRIES = 5;
const IDX_SKIPS = 6;
/** Per-slot element counts and generations, three of each. */
const IDX_SLOT_LENGTH = 8;
const IDX_SLOT_GENERATION = 11;

const CONTROL_ELEMENTS = 16;
const CONTROL_BYTES = CONTROL_ELEMENTS * 4;
const SLOTS = 3;
const MAGIC = 0x52534332; // "RSC2"

const MASK_WRITE = 0x03;
const MASK_SPARE = 0x0c;
const MASK_READ = 0x30;
const BIT_FRESH = 0x40;
/** write=0, spare=1, read=2, nothing fresh. */
const FLAGS_INITIAL = 0 | (1 << 2) | (2 << 4);

export type RenderStateChannelDiagnostics = {
  /** Generations published by the writer. */
  writes: number;
  /**
   * Compare-and-exchange attempts that lost a race and went round again.
   *
   * Expected to stay at or near zero: with one writer and one reader the only
   * contention is the two of them swapping the handoff slot at the same
   * instant, and neither blocks when it happens. A number that climbs means
   * more than one writer or more than one reader is attached, which this
   * protocol does not support.
   */
  retries: number;
  /** Reads that found no new generation and did no work at all. */
  skips: number;
  /** Generations published. */
  generation: number;
  /** Elements each slot can hold. */
  capacity: number;
};

export type RenderStateChannel = {
  /** Hand this to the worker. Transferring is wrong; it is shared, not moved. */
  readonly buffer: SharedArrayBuffer;
  /** Elements per slot. */
  readonly capacity: number;
  /**
   * Publish a generation.
   *
   * `fill` receives the slot the writer owns and returns how many elements it
   * wrote. Write *into* it rather than copying into it where you can: a caller
   * that computes its state directly into the slot pays nothing beyond the
   * exchange. It must not retain the view — the slot rotates.
   *
   * Returns `false` if `fill` reported more elements than the slot holds, in
   * which case nothing is published and the previous generation stands.
   */
  write: (fill: (slot: Float32Array) => number) => boolean;
  readonly diagnostics: RenderStateChannelDiagnostics;
};

export type RenderStateReader = {
  /**
   * Take the newest generation, or `null` when there is nothing new.
   *
   * The returned view is the slot itself, held by this reader until the next
   * `read()`. No copy is made, so it is valid for exactly as long as the frame
   * that took it — read it during `draw`, never retain it across frames.
   */
  read: () => Float32Array | null;
  /** The held generation, whether or not this frame brought a new one. */
  readonly current: Float32Array | null;
  /** Which generation `current` holds. */
  readonly generation: number;
  readonly diagnostics: RenderStateChannelDiagnostics;
};

const controlOf = (buffer: SharedArrayBuffer) => new Int32Array(buffer, 0, CONTROL_ELEMENTS);

const slotViews = (buffer: SharedArrayBuffer, elements: number): Float32Array[] => {
  const views: Float32Array[] = [];
  for (let slot = 0; slot < SLOTS; slot += 1) {
    views.push(new Float32Array(buffer, CONTROL_BYTES + slot * elements * 4, elements));
  }
  return views;
};

/** Bytes needed to carry `capacity` float elements per slot. */
export const renderStateChannelBytes = (capacity: number): number =>
  CONTROL_BYTES + SLOTS * Math.max(1, Math.trunc(capacity)) * 4;

/**
 * Allocate a channel, or `null` where shared memory is unavailable.
 *
 * `null` is an ordinary answer, not an error: `SharedArrayBuffer` needs
 * cross-origin isolation, a render surface does not, and a lane that refused to
 * run without it would have made every consumer adopt COOP/COEP headers to draw
 * a background texture. Callers fall back to `setState`.
 */
export const createRenderStateChannel = (capacity: number): RenderStateChannel | null => {
  const elements = Math.max(1, Math.trunc(capacity));
  if (typeof SharedArrayBuffer === "undefined") return null;

  let buffer: SharedArrayBuffer;
  try {
    buffer = new SharedArrayBuffer(renderStateChannelBytes(elements));
  } catch {
    return null;
  }

  const control = controlOf(buffer);
  Atomics.store(control, IDX_MAGIC, MAGIC);
  Atomics.store(control, IDX_CAPACITY, elements);
  Atomics.store(control, IDX_FLAGS, FLAGS_INITIAL);

  // Views built once. A view allocated per write would be a fresh object on the
  // hot path, which is the cost this module exists to remove.
  const slots = slotViews(buffer, elements);

  return {
    buffer,
    capacity: elements,

    write(fill) {
      const flags = Atomics.load(control, IDX_FLAGS);
      const index = flags & MASK_WRITE;
      const written = fill(slots[index]!);
      if (!Number.isFinite(written) || written < 0 || written > elements) {
        // Nothing published: the reader keeps whatever it holds and the spare
        // still carries the last good generation.
        return false;
      }

      const generation = Atomics.add(control, IDX_GENERATION, 1) + 1;
      // Plain stores into a slot this side exclusively owns. They are published
      // by the exchange below, which is a release.
      control[IDX_SLOT_LENGTH + index] = written;
      control[IDX_SLOT_GENERATION + index] = generation;

      // Swap write and spare, and mark the spare fresh.
      for (;;) {
        const current = Atomics.load(control, IDX_FLAGS);
        const next =
          ((current & MASK_SPARE) >> 2) |
          ((current & MASK_WRITE) << 2) |
          (current & MASK_READ) |
          BIT_FRESH;
        if (Atomics.compareExchange(control, IDX_FLAGS, current, next) === current) break;
        Atomics.add(control, IDX_RETRIES, 1);
      }
      Atomics.add(control, IDX_WRITES, 1);
      return true;
    },

    get diagnostics() {
      return {
        writes: Atomics.load(control, IDX_WRITES),
        retries: Atomics.load(control, IDX_RETRIES),
        skips: Atomics.load(control, IDX_SKIPS),
        generation: Atomics.load(control, IDX_GENERATION),
        capacity: elements,
      };
    },
  };
};

/** Attach the reading half, in the worker. Returns `null` for a foreign buffer. */
export const attachRenderStateReader = (buffer: SharedArrayBuffer): RenderStateReader | null => {
  if (buffer.byteLength < CONTROL_BYTES) return null;
  const control = controlOf(buffer);
  if (Atomics.load(control, IDX_MAGIC) !== MAGIC) return null;
  const elements = Atomics.load(control, IDX_CAPACITY);
  if (elements < 1 || buffer.byteLength < renderStateChannelBytes(elements)) return null;

  const slots = slotViews(buffer, elements);
  /*
   * One cached view per slot, rebuilt only when that slot's length changes.
   *
   * A `subarray` is a small object, but it is an object, and a surface drawing
   * at 40 Hz would allocate one every frame forever. Payload lengths are almost
   * always constant between frames, so in the steady state this is zero.
   */
  const held: (Float32Array | null)[] = [null, null, null];
  const heldLength = [-1, -1, -1];

  let generation = 0;
  let index = -1;

  const viewOf = (slot: number, length: number): Float32Array => {
    if (heldLength[slot] !== length || held[slot] === null) {
      held[slot] = length === elements ? slots[slot]! : slots[slot]!.subarray(0, length);
      heldLength[slot] = length;
    }
    return held[slot]!;
  };

  return {
    read() {
      const flags = Atomics.load(control, IDX_FLAGS);
      if ((flags & BIT_FRESH) === 0) {
        // Nothing published since the last take. The held slot is still ours
        // and still valid, so there is no work to do at all — not a copy, not
        // an exchange. This is the frame a surface spends most of its life in.
        Atomics.add(control, IDX_SKIPS, 1);
        return null;
      }

      // Swap read and spare, clearing fresh. The writer never touches the slot
      // we are moving out of, so nothing is in flight when this lands.
      for (;;) {
        const current = Atomics.load(control, IDX_FLAGS);
        if ((current & BIT_FRESH) === 0) {
          Atomics.add(control, IDX_SKIPS, 1);
          return null;
        }
        const next =
          (current & MASK_WRITE) | ((current & MASK_READ) >> 2) | ((current & MASK_SPARE) << 2);
        if (Atomics.compareExchange(control, IDX_FLAGS, current, next) === current) {
          index = (next & MASK_READ) >> 4;
          break;
        }
        Atomics.add(control, IDX_RETRIES, 1);
      }

      const length = Math.min(control[IDX_SLOT_LENGTH + index]!, elements);
      generation = control[IDX_SLOT_GENERATION + index]!;
      return viewOf(index, length);
    },

    get current() {
      if (index < 0) return null;
      return viewOf(index, Math.min(control[IDX_SLOT_LENGTH + index]!, elements));
    },
    get generation() {
      return generation;
    },
    get diagnostics() {
      return {
        writes: Atomics.load(control, IDX_WRITES),
        retries: Atomics.load(control, IDX_RETRIES),
        skips: Atomics.load(control, IDX_SKIPS),
        generation: Atomics.load(control, IDX_GENERATION),
        capacity: elements,
      };
    },
  };
};
