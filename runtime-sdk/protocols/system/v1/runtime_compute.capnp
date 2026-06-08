@0xfafac001d15ea007;

# Compute capsules used between the TypeScript host, Rust SDK modules, and
# workers. These are local prototype/runtime descriptors, not durable app truth.

const computeSchemaVersion :UInt32 = 1;
const computeRouteMaxBytes :UInt32 = 128;
const computeParamsInlineMaxBytes :UInt32 = 8192;
const computeInputInlineMaxBytes :UInt32 = 65536;
const computeDefaultTimeoutMs :UInt32 = 10000;
const computeMaxTimeoutMs :UInt32 = 120000;
const computeMaxCapabilities :UInt32 = 32;
const computeMaxResultInlineBytes :UInt32 = 65536;

const computeFlagRequireSab :UInt32 = 1;
const computeFlagAllowWorker :UInt32 = 2;
const computeFlagResultInArena :UInt32 = 4;
const computeFlagDeterministic :UInt32 = 8;

enum RuntimeComputeKind {
  execute @0;
  dispatch @1;
  transform @2;
  aggregate @3;
  validate @4;
  project @5;
}

enum RuntimeComputePriority {
  background @0;
  normal @1;
  interactive @2;
  urgent @3;
}

enum RuntimeComputeStatus {
  ok @0;
  rejected @1;
  timeout @2;
  saturated @3;
  invalid @4;
  fallbackUsed @5;
  cancelled @6;
}

struct RuntimeComputePayload {
  encoding @0 :Text;
  inlineBytes @1 :Data;
  arenaDescriptorId @2 :UInt32;
  offset @3 :UInt32;
  length @4 :UInt32;
  contentHash @5 :Text;
}

struct RuntimeComputeLimits {
  timeoutMs @0 :UInt32;
  memoryBytes @1 :UInt64;
  inputBytes @2 :UInt64;
  outputBytes @3 :UInt64;
  maxConcurrency @4 :UInt32;
  cpuBudgetMicros @5 :UInt64;
}

struct RuntimeComputeCapsule {
  jobId @0 :Text;
  tenantId @1 :Text;
  moduleId @2 :Text;
  unitId @3 :Text;
  operation @4 :Text;
  kind @5 :RuntimeComputeKind;
  priority @6 :RuntimeComputePriority;
  flags @7 :UInt32;
  input @8 :RuntimeComputePayload;
  params @9 :RuntimeComputePayload;
  limits @10 :RuntimeComputeLimits;
  capabilities @11 :List(Text);
}

struct RuntimeComputeReceipt {
  jobId @0 :Text;
  tenantId @1 :Text;
  status @2 :RuntimeComputeStatus;
  statusCode @3 :UInt32;
  message @4 :Text;
  result @5 :RuntimeComputePayload;
  startedUnixMs @6 :UInt64;
  completedUnixMs @7 :UInt64;
  workerId @8 :Text;
}
