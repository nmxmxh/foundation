@0xfafac001d15ea001;

const bufferTotalBytes :UInt32 = 4096;

const offsetEpochs :UInt32 = 0;
const epochSlotCount :UInt32 = 16;
const epochSlotBytes :UInt32 = 4;

const idxKernelReady :UInt32 = 0;
const idxInputWritten :UInt32 = 1;
const idxOutputWritten :UInt32 = 2;
const idxOutputConsumed :UInt32 = 3;
const idxPanicState :UInt32 = 4;
const idxDiagnosticsWritten :UInt32 = 5;
const idxRuntimeTick :UInt32 = 6;
const idxVisibilityState :UInt32 = 7;

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
const offsetRouteBytes :UInt32 = 64;
const routeMaxBytes :UInt32 = 64;

const offsetHeaderInts :UInt32 = 128;
const headerIntCount :UInt32 = 8;
const intIdxSchemaVersion :UInt32 = 0;
const intIdxInputLength :UInt32 = 1;
const intIdxOutputLength :UInt32 = 2;
const intIdxStatusCode :UInt32 = 3;
const intIdxContextHash :UInt32 = 4;
const intIdxModuleVersion :UInt32 = 5;
const intIdxReserved0 :UInt32 = 6;
const intIdxReserved1 :UInt32 = 7;

const bufferSchemaVersion :UInt32 = 1;

const offsetInputBytes :UInt32 = 256;
const inputMaxBytes :UInt32 = 1024;

const offsetOutputBytes :UInt32 = 1280;
const outputMaxBytes :UInt32 = 2048;

const offsetDiagnosticBytes :UInt32 = 3328;
const diagnosticMaxBytes :UInt32 = 768;
