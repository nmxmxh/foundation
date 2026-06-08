@0xfafac001d15ea006;

# Host syscall contracts for Rust/WASM modules that follow the INOS runtime
# pattern: stable exported entrypoints, host-managed memory, and bounded lanes.

const syscallSchemaVersion :UInt32 = 1;
const syscallInlineMaxBytes :UInt32 = 4096;
const syscallRouteMaxBytes :UInt32 = 128;
const syscallDefaultTimeoutMs :UInt32 = 5000;
const syscallMaxTimeoutMs :UInt32 = 30000;
const syscallQueueSlotBytes :UInt32 = 64;
const syscallQueueSlotCount :UInt32 = 1024;

const syscallFlagRequiresAuthority :UInt32 = 1;
const syscallFlagAllowFallback :UInt32 = 2;
const syscallFlagResponseInArena :UInt32 = 4;
const syscallFlagDeadlineRequired :UInt32 = 8;

enum RuntimeSyscallCode {
  none @0;
  fetchChunk @1;
  storeChunk @2;
  publishEvent @3;
  subscribeProjection @4;
  dispatchCommand @5;
  readClock @6;
  randomBytes @7;
  diagnostics @8;
}

enum RuntimeSyscallStatus {
  ok @0;
  rejected @1;
  timeout @2;
  notFound @3;
  backpressure @4;
  invalid @5;
  fallbackUsed @6;
}

struct RuntimePayloadRef {
  encoding @0 :Text;
  inlineBytes @1 :Data;
  arenaDescriptorId @2 :UInt32;
  offset @3 :UInt32;
  length @4 :UInt32;
  hash64 @5 :UInt64;
}

struct RuntimeChunkDescriptor {
  storeId @0 :Text;
  chunkId @1 :Text;
  contentHash @2 :Text;
  mediaType @3 :Text;
  offset @4 :UInt64;
  length @5 :UInt32;
  compressedLength @6 :UInt32;
  compression @7 :Text;
  encrypted @8 :Bool;
}

struct RuntimeSyscallRequest {
  requestId @0 :UInt64;
  tenantId @1 :Text;
  capability @2 :Text;
  route @3 :Text;
  code @4 :RuntimeSyscallCode;
  flags @5 :UInt32;
  deadlineUnixMs @6 :UInt64;
  payload @7 :RuntimePayloadRef;
  chunk @8 :RuntimeChunkDescriptor;
}

struct RuntimeSyscallResponse {
  requestId @0 :UInt64;
  status @1 :RuntimeSyscallStatus;
  statusCode @2 :UInt32;
  message @3 :Text;
  payload @4 :RuntimePayloadRef;
  chunk @5 :RuntimeChunkDescriptor;
}
