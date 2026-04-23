@0xfafac001d15ea002;

struct RuntimeUnitDescriptor {
  unitId @0 :Text;
  role @1 :Text;
  inputSchema @2 :Text;
  outputSchema @3 :Text;
  supportsWasm @4 :Bool;
  supportsNative @5 :Bool;
  requiresSharedMemory @6 :Bool;
  supportsGpu @7 :Bool;
  maxConcurrency @8 :UInt32;
}
