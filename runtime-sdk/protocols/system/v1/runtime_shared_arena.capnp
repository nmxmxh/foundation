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
const ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH :UInt32 = 5;
const ARENA_DESCRIPTOR_TYPE_COLUMNAR_FIELD :UInt32 = 6;
const ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALUES :UInt32 = 7;
const ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALIDITY :UInt32 = 8;
const ARENA_DESCRIPTOR_TYPE_COLUMNAR_OFFSETS :UInt32 = 9;

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

# Columnar batch descriptor payload.
#
# This is intentionally a compact Foundation metadata slab rather than full
# Arrow IPC. It follows Arrow's useful physical-model vocabulary: record batch,
# fields with the same row count, validity buffers, offsets buffers, values
# buffers, optional dictionary/aux buffers, and 64-byte alignment for SIMD/cache
# friendliness. A descriptor of type ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH points
# at a payload with this header followed by fixed-size field descriptors.
#
# Header u32 slots, little-endian:
# 0 magic, 1 schema version, 2 row count, 3 column count, 4 flags,
# 5 metadata descriptor id, 6 dictionary descriptor id, 7 reserved.
#
# Field descriptor u32 slots, little-endian:
# 0 field id, 1 logical type, 2 physical type, 3 flags, 4 length,
# 5 null count, 6 validity descriptor id, 7 offsets descriptor id,
# 8 values descriptor id, 9 auxiliary descriptor id, 10 byte width,
# 11 scale, 12 precision, 13 timezone hash, 14 dictionary id, 15 reserved.
const COLUMNAR_BATCH_SCHEMA_VERSION :UInt32 = 1;
const COLUMNAR_BATCH_MAGIC :UInt32 = 1129460291; # "OVRC" little-endian
const COLUMNAR_BATCH_ALIGNMENT_BYTES :UInt32 = 64;
const COLUMNAR_BATCH_MAX_COLUMNS :UInt32 = 1024;
const COLUMNAR_BATCH_HEADER_BYTES :UInt32 = 32;
const COLUMNAR_FIELD_DESCRIPTOR_BYTES :UInt32 = 64;
const COLUMNAR_BATCH_HEADER_IDX_MAGIC :UInt32 = 0;
const COLUMNAR_BATCH_HEADER_IDX_SCHEMA_VERSION :UInt32 = 1;
const COLUMNAR_BATCH_HEADER_IDX_ROW_COUNT :UInt32 = 2;
const COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT :UInt32 = 3;
const COLUMNAR_BATCH_HEADER_IDX_FLAGS :UInt32 = 4;
const COLUMNAR_BATCH_HEADER_IDX_METADATA_DESCRIPTOR_ID :UInt32 = 5;
const COLUMNAR_BATCH_HEADER_IDX_DICTIONARY_DESCRIPTOR_ID :UInt32 = 6;
const COLUMNAR_BATCH_HEADER_IDX_RESERVED :UInt32 = 7;
const COLUMNAR_FIELD_IDX_FIELD_ID :UInt32 = 0;
const COLUMNAR_FIELD_IDX_LOGICAL_TYPE :UInt32 = 1;
const COLUMNAR_FIELD_IDX_PHYSICAL_TYPE :UInt32 = 2;
const COLUMNAR_FIELD_IDX_FLAGS :UInt32 = 3;
const COLUMNAR_FIELD_IDX_LENGTH :UInt32 = 4;
const COLUMNAR_FIELD_IDX_NULL_COUNT :UInt32 = 5;
const COLUMNAR_FIELD_IDX_VALIDITY_DESCRIPTOR_ID :UInt32 = 6;
const COLUMNAR_FIELD_IDX_OFFSETS_DESCRIPTOR_ID :UInt32 = 7;
const COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID :UInt32 = 8;
const COLUMNAR_FIELD_IDX_AUX_DESCRIPTOR_ID :UInt32 = 9;
const COLUMNAR_FIELD_IDX_BYTE_WIDTH :UInt32 = 10;
const COLUMNAR_FIELD_IDX_SCALE :UInt32 = 11;
const COLUMNAR_FIELD_IDX_PRECISION :UInt32 = 12;
const COLUMNAR_FIELD_IDX_TIMEZONE_HASH :UInt32 = 13;
const COLUMNAR_FIELD_IDX_DICTIONARY_ID :UInt32 = 14;
const COLUMNAR_FIELD_IDX_RESERVED :UInt32 = 15;
const COLUMNAR_DESCRIPTOR_ID_NONE :UInt32 = 4294967295;
const COLUMNAR_LOGICAL_TYPE_NULL :UInt32 = 0;
const COLUMNAR_LOGICAL_TYPE_BOOL :UInt32 = 1;
const COLUMNAR_LOGICAL_TYPE_INT :UInt32 = 2;
const COLUMNAR_LOGICAL_TYPE_UINT :UInt32 = 3;
const COLUMNAR_LOGICAL_TYPE_FLOAT :UInt32 = 4;
const COLUMNAR_LOGICAL_TYPE_DECIMAL :UInt32 = 5;
const COLUMNAR_LOGICAL_TYPE_TIMESTAMP :UInt32 = 6;
const COLUMNAR_LOGICAL_TYPE_BINARY :UInt32 = 7;
const COLUMNAR_LOGICAL_TYPE_UTF8 :UInt32 = 8;
const COLUMNAR_LOGICAL_TYPE_DICTIONARY :UInt32 = 9;
const COLUMNAR_PHYSICAL_TYPE_NULL :UInt32 = 0;
const COLUMNAR_PHYSICAL_TYPE_FIXED_WIDTH :UInt32 = 1;
const COLUMNAR_PHYSICAL_TYPE_VARIABLE_BINARY :UInt32 = 2;
const COLUMNAR_PHYSICAL_TYPE_DICTIONARY_INDEX :UInt32 = 3;
const COLUMNAR_FIELD_FLAG_NULLABLE :UInt32 = 1;
const COLUMNAR_FIELD_FLAG_DICTIONARY_ENCODED :UInt32 = 2;
const COLUMNAR_FIELD_FLAG_SORTED_ASC :UInt32 = 4;
const COLUMNAR_FIELD_FLAG_SORTED_DESC :UInt32 = 8;
