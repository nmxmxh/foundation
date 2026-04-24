@0xfafac001d15ea002;

# Generic optional SharedArrayBuffer data-plane for foundation runtime apps.
# The 4KB runtime_buffer.capnp remains the hot control buffer. This arena is
# only allocated when the browser/runtime negotiates shared-memory capability.

const ARENA_SCHEMA_VERSION :UInt32 = 1;

# Sizing tiers. Apps may request smaller or larger arenas, but runtimes should
# clamp to these limits unless a project-owned runtime explicitly documents why.
const ARENA_MIN_BYTES :UInt32 = 1048576;       # 1MB
const ARENA_DEFAULT_BYTES :UInt32 = 8388608;   # 8MB
const ARENA_INTERACTIVE_BYTES :UInt32 = 33554432; # 32MB
const ARENA_HEAVY_BYTES :UInt32 = 67108864;    # 64MB
const ARENA_MAX_BYTES :UInt32 = 536870912;     # 512MB

# Header region: magic/schema/capacity/counters.
const ARENA_OFFSET_HEADER :UInt32 = 0;
const ARENA_HEADER_BYTES :UInt32 = 256;
const ARENA_HEADER_MAGIC :UInt32 = 1330400321; # "OVRA" little-endian
const ARENA_HEADER_IDX_MAGIC :UInt32 = 0;
const ARENA_HEADER_IDX_SCHEMA_VERSION :UInt32 = 1;
const ARENA_HEADER_IDX_CAPACITY_BYTES :UInt32 = 2;
const ARENA_HEADER_IDX_ALLOCATED_BYTES :UInt32 = 3;
const ARENA_HEADER_IDX_DESCRIPTOR_COUNT :UInt32 = 4;
const ARENA_HEADER_IDX_QUEUE_DROPPED :UInt32 = 5;
const ARENA_HEADER_IDX_FLAGS :UInt32 = 6;
const ARENA_HEADER_IDX_RESERVED :UInt32 = 7;

# Atomic epoch region. Workers may block here; the main thread should prefer
# Atomics.waitAsync or message fallback.
const ARENA_OFFSET_EPOCHS :UInt32 = 256;
const ARENA_EPOCH_COUNT :UInt32 = 64;
const ARENA_EPOCH_BYTES :UInt32 = 256;
const ARENA_IDX_READY :UInt32 = 0;
const ARENA_IDX_ALLOC_HEAD :UInt32 = 1;
const ARENA_IDX_DESCRIPTOR_EPOCH :UInt32 = 2;
const ARENA_IDX_QUEUE_HEAD :UInt32 = 3;
const ARENA_IDX_QUEUE_TAIL :UInt32 = 4;
const ARENA_IDX_QUEUE_EPOCH :UInt32 = 5;
const ARENA_IDX_DIAGNOSTICS_EPOCH :UInt32 = 6;
const ARENA_IDX_BACKPRESSURE :UInt32 = 7;

# Descriptor table. Each descriptor points at a page-aligned slab in the arena.
const ARENA_OFFSET_DESCRIPTOR_TABLE :UInt32 = 4096;
const ARENA_DESCRIPTOR_SIZE :UInt32 = 32;
const ARENA_DESCRIPTOR_COUNT :UInt32 = 512;
const ARENA_DESCRIPTOR_TABLE_BYTES :UInt32 = 16384;
const ARENA_DESCRIPTOR_STATE_FREE :UInt32 = 0;
const ARENA_DESCRIPTOR_STATE_ALLOCATED :UInt32 = 1;
const ARENA_DESCRIPTOR_STATE_READY :UInt32 = 2;
const ARENA_DESCRIPTOR_STATE_CONSUMED :UInt32 = 3;
const ARENA_DESCRIPTOR_TYPE_BYTES :UInt32 = 0;
const ARENA_DESCRIPTOR_TYPE_CAPNP :UInt32 = 1;
const ARENA_DESCRIPTOR_TYPE_TEXT :UInt32 = 2;
const ARENA_DESCRIPTOR_TYPE_IMAGE :UInt32 = 3;
const ARENA_DESCRIPTOR_TYPE_MEDIA :UInt32 = 4;

# Queue slots carry descriptor IDs and small routing metadata. Payload bytes
# stay in slabs; queue entries should never carry large application data.
const ARENA_OFFSET_QUEUE :UInt32 = 20480;
const ARENA_QUEUE_SLOT_SIZE :UInt32 = 64;
const ARENA_QUEUE_SLOT_COUNT :UInt32 = 1024;
const ARENA_QUEUE_BYTES :UInt32 = 65536;
const ARENA_QUEUE_OP_NONE :UInt32 = 0;
const ARENA_QUEUE_OP_DESCRIPTOR_READY :UInt32 = 1;
const ARENA_QUEUE_OP_DESCRIPTOR_CONSUMED :UInt32 = 2;
const ARENA_QUEUE_OP_DIAGNOSTIC :UInt32 = 3;

const ARENA_OFFSET_DIAGNOSTICS :UInt32 = 86016;
const ARENA_DIAGNOSTIC_BYTES :UInt32 = 4096;

# First byte available to app/runtime slabs. Page aligned to keep views cheap
# and to preserve room for future foundation-owned control structures.
const ARENA_OFFSET_PAGES :UInt32 = 131072;
const ARENA_PAGE_BYTES :UInt32 = 4096;
