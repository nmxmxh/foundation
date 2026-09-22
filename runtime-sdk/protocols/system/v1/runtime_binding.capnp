@0x9eccda21d814e5f2;

# Native resource bindings use fixed scalar records on the control plane.
# Local calls pass generated values directly. Remote calls encode these records.

const bindingSchemaVersion :UInt32 = 1;
const bindingMaxTimeoutMillis :UInt32 = 30000;
const bindingMaxOutputBytes :UInt32 = 2097152;

struct RuntimeBindingRequest {
  schemaVersion @0 :UInt32;
  registryId @1 :UInt64;
  bindingId @2 :UInt64;
  bindingRevision @3 :UInt64;
  resourceId @4 :UInt64;
  resourceGeneration @5 :UInt64;
  inputTypeId @6 :UInt64;
  outputTypeId @7 :UInt64;
  outputCapacity @8 :UInt32;
  maxTransferBytes @9 :UInt64;
  timeoutMillis @10 :UInt32;
  maxInputCopyBytes @11 :UInt64;
}

struct RuntimeBindingReceipt {
  schemaVersion @0 :UInt32;
  bindingRevision @1 :UInt64;
  resourceGeneration @2 :UInt64;
  outputTypeId @3 :UInt64;
  outputBytes @4 :UInt32;
  transferredBytes @5 :UInt64;
  inputCopiedBytes @6 :UInt64;
  status @7 :UInt32;
}
