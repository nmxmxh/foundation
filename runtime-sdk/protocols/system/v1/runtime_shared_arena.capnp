@0xfafac001d15ea008;

# Generic optional SharedArrayBuffer data-plane for foundation runtime apps.
# The 4KB runtime_buffer.capnp remains the hot control buffer. This arena is
# only allocated when the browser/runtime negotiates shared-memory capability.

const arenaSchemaVersion :UInt32 = 1;

# Sizing tiers. Apps may request smaller or larger arenas, but runtimes should
# clamp to these limits unless a project-owned runtime explicitly documents why.
const arenaMinBytes :UInt32 = 1048576;       # 1MB
const arenaDefaultBytes :UInt32 = 8388608;   # 8MB
const arenaInteractiveBytes :UInt32 = 33554432; # 32MB
const arenaHeavyBytes :UInt32 = 67108864;    # 64MB
const arenaMaxBytes :UInt32 = 536870912;     # 512MB

# Header region: magic/schema/capacity/counters.
const arenaOffsetHeader :UInt32 = 0;
const arenaHeaderBytes :UInt32 = 256;
const arenaHeaderMagic :UInt32 = 1330400321; # "OVRA" little-endian
const arenaHeaderIdxMagic :UInt32 = 0;
const arenaHeaderIdxSchemaVersion :UInt32 = 1;
const arenaHeaderIdxCapacityBytes :UInt32 = 2;
const arenaHeaderIdxAllocatedBytes :UInt32 = 3;
const arenaHeaderIdxDescriptorCount :UInt32 = 4;
const arenaHeaderIdxQueueDropped :UInt32 = 5;
const arenaHeaderIdxFlags :UInt32 = 6;
const arenaHeaderIdxReserved :UInt32 = 7;

# Atomic epoch region. Workers may block here; the main thread should prefer
# Atomics.waitAsync or message fallback.
const arenaOffsetEpochs :UInt32 = 256;
const arenaEpochCount :UInt32 = 64;
const arenaEpochBytes :UInt32 = 256;
const arenaIdxReady :UInt32 = 0;
const arenaIdxAllocHead :UInt32 = 1;
const arenaIdxDescriptorEpoch :UInt32 = 2;
const arenaIdxQueueHead :UInt32 = 3;
const arenaIdxQueueTail :UInt32 = 4;
const arenaIdxQueueEpoch :UInt32 = 5;
const arenaIdxDiagnosticsEpoch :UInt32 = 6;
const arenaIdxBackpressure :UInt32 = 7;

# Descriptor table. Each descriptor points at a page-aligned slab in the arena.
const arenaOffsetDescriptorTable :UInt32 = 4096;
const arenaDescriptorSize :UInt32 = 32;
const arenaDescriptorCount :UInt32 = 512;
const arenaDescriptorTableBytes :UInt32 = 16384;
const arenaDescriptorStateFree :UInt32 = 0;
const arenaDescriptorStateAllocated :UInt32 = 1;
const arenaDescriptorStateReady :UInt32 = 2;
const arenaDescriptorStateConsumed :UInt32 = 3;
const arenaDescriptorTypeBytes :UInt32 = 0;
const arenaDescriptorTypeCapnp :UInt32 = 1;
const arenaDescriptorTypeText :UInt32 = 2;
const arenaDescriptorTypeImage :UInt32 = 3;
const arenaDescriptorTypeMedia :UInt32 = 4;
const arenaDescriptorTypeColumnarBatch :UInt32 = 5;
const arenaDescriptorTypeColumnarField :UInt32 = 6;
const arenaDescriptorTypeColumnarValues :UInt32 = 7;
const arenaDescriptorTypeColumnarValidity :UInt32 = 8;
const arenaDescriptorTypeColumnarOffsets :UInt32 = 9;

# Queue slots carry descriptor IDs and small routing metadata. Payload bytes
# stay in slabs; queue entries should never carry large application data.
const arenaOffsetQueue :UInt32 = 20480;
const arenaQueueSlotSize :UInt32 = 64;
const arenaQueueSlotCount :UInt32 = 1024;
const arenaQueueBytes :UInt32 = 65536;
const arenaQueueOpNone :UInt32 = 0;
const arenaQueueOpDescriptorReady :UInt32 = 1;
const arenaQueueOpDescriptorConsumed :UInt32 = 2;
const arenaQueueOpDiagnostic :UInt32 = 3;

const arenaOffsetDiagnostics :UInt32 = 86016;
const arenaDiagnosticBytes :UInt32 = 4096;

# First byte available to app/runtime slabs. Page aligned to keep views cheap
# and to preserve room for future foundation-owned control structures.
const arenaOffsetPages :UInt32 = 131072;
const arenaPageBytes :UInt32 = 4096;

# Columnar batch descriptor payload.
#
# This is intentionally a compact Foundation metadata slab rather than full
# Arrow IPC. It follows Arrow's useful physical-model vocabulary: record batch,
# fields with the same row count, validity buffers, offsets buffers, values
# buffers, optional dictionary/aux buffers, and 64-byte alignment for SIMD/cache
# friendliness. A descriptor of type arenaDescriptorTypeColumnarBatch points
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
const columnarBatchSchemaVersion :UInt32 = 1;
const columnarBatchMagic :UInt32 = 1129460291; # "OVRC" little-endian
const columnarBatchAlignmentBytes :UInt32 = 64;
const columnarBatchMaxColumns :UInt32 = 1024;
const columnarBatchHeaderBytes :UInt32 = 32;
const columnarFieldDescriptorBytes :UInt32 = 64;
const columnarBatchHeaderIdxMagic :UInt32 = 0;
const columnarBatchHeaderIdxSchemaVersion :UInt32 = 1;
const columnarBatchHeaderIdxRowCount :UInt32 = 2;
const columnarBatchHeaderIdxColumnCount :UInt32 = 3;
const columnarBatchHeaderIdxFlags :UInt32 = 4;
const columnarBatchHeaderIdxMetadataDescriptorId :UInt32 = 5;
const columnarBatchHeaderIdxDictionaryDescriptorId :UInt32 = 6;
const columnarBatchHeaderIdxReserved :UInt32 = 7;
const columnarFieldIdxFieldId :UInt32 = 0;
const columnarFieldIdxLogicalType :UInt32 = 1;
const columnarFieldIdxPhysicalType :UInt32 = 2;
const columnarFieldIdxFlags :UInt32 = 3;
const columnarFieldIdxLength :UInt32 = 4;
const columnarFieldIdxNullCount :UInt32 = 5;
const columnarFieldIdxValidityDescriptorId :UInt32 = 6;
const columnarFieldIdxOffsetsDescriptorId :UInt32 = 7;
const columnarFieldIdxValuesDescriptorId :UInt32 = 8;
const columnarFieldIdxAuxDescriptorId :UInt32 = 9;
const columnarFieldIdxByteWidth :UInt32 = 10;
const columnarFieldIdxScale :UInt32 = 11;
const columnarFieldIdxPrecision :UInt32 = 12;
const columnarFieldIdxTimezoneHash :UInt32 = 13;
const columnarFieldIdxDictionaryId :UInt32 = 14;
const columnarFieldIdxReserved :UInt32 = 15;
const columnarDescriptorIdNone :UInt32 = 4294967295;
const columnarLogicalTypeNull :UInt32 = 0;
const columnarLogicalTypeBool :UInt32 = 1;
const columnarLogicalTypeInt :UInt32 = 2;
const columnarLogicalTypeUint :UInt32 = 3;
const columnarLogicalTypeFloat :UInt32 = 4;
const columnarLogicalTypeDecimal :UInt32 = 5;
const columnarLogicalTypeTimestamp :UInt32 = 6;
const columnarLogicalTypeBinary :UInt32 = 7;
const columnarLogicalTypeUtf8 :UInt32 = 8;
const columnarLogicalTypeDictionary :UInt32 = 9;
const columnarPhysicalTypeNull :UInt32 = 0;
const columnarPhysicalTypeFixedWidth :UInt32 = 1;
const columnarPhysicalTypeVariableBinary :UInt32 = 2;
const columnarPhysicalTypeDictionaryIndex :UInt32 = 3;
const columnarFieldFlagNullable :UInt32 = 1;
const columnarFieldFlagDictionaryEncoded :UInt32 = 2;
const columnarFieldFlagSortedAsc :UInt32 = 4;
const columnarFieldFlagSortedDesc :UInt32 = 8;
