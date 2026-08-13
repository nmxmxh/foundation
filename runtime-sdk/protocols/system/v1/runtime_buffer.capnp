@0xfafac001d15ea001;

const BUFFER_TOTAL_BYTES :UInt32 = 4096;

const OFFSET_EPOCHS :UInt32 = 0;
const EPOCH_SLOT_COUNT :UInt32 = 16;
const EPOCH_SLOT_BYTES :UInt32 = 4;

const IDX_KERNEL_READY :UInt32 = 0;
const IDX_INPUT_WRITTEN :UInt32 = 1;
const IDX_OUTPUT_WRITTEN :UInt32 = 2;
const IDX_OUTPUT_CONSUMED :UInt32 = 3;
const IDX_PANIC_STATE :UInt32 = 4;
const IDX_DIAGNOSTICS_WRITTEN :UInt32 = 5;
const IDX_RUNTIME_TICK :UInt32 = 6;
const IDX_VISIBILITY_STATE :UInt32 = 7;

# The route names the unit for an exchange that has no side channel to carry it.
#
# The stdio and shm transports send the unit id as a pipe frame. The epoch
# transport has no pipe in its hot path, so the id travels in the buffer like
# everything else. This region is the gap between the 16 epoch slots (64 bytes)
# and the header integers at 128 — it was always reserved, and claiming it means
# the epoch doorbell needs no layout change and no new mapping.
#
# 64 bytes is a hard limit rather than a soft one: an id that does not fit is
# refused at the exchange, because a truncated route resolves to a different
# unit or to none, and both are worse than a clear error.
const OFFSET_ROUTE_BYTES :UInt32 = 64;
const ROUTE_MAX_BYTES :UInt32 = 64;

const OFFSET_HEADER_INTS :UInt32 = 128;
const HEADER_INT_COUNT :UInt32 = 8;
const INT_IDX_SCHEMA_VERSION :UInt32 = 0;
const INT_IDX_INPUT_LENGTH :UInt32 = 1;
const INT_IDX_OUTPUT_LENGTH :UInt32 = 2;
const INT_IDX_STATUS_CODE :UInt32 = 3;
const INT_IDX_CONTEXT_HASH :UInt32 = 4;
const INT_IDX_MODULE_VERSION :UInt32 = 5;
const INT_IDX_RESERVED0 :UInt32 = 6;
const INT_IDX_RESERVED1 :UInt32 = 7;

const BUFFER_SCHEMA_VERSION :UInt32 = 1;

const OFFSET_INPUT_BYTES :UInt32 = 256;
const INPUT_MAX_BYTES :UInt32 = 1024;

const OFFSET_OUTPUT_BYTES :UInt32 = 1280;
const OUTPUT_MAX_BYTES :UInt32 = 2048;

const OFFSET_DIAGNOSTIC_BYTES :UInt32 = 3328;
const DIAGNOSTIC_MAX_BYTES :UInt32 = 768;
