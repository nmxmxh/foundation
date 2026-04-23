@0xfafac001d15ea003;

struct RuntimeDiagnostics {
  mode @0 :Text;
  degraded @1 :Bool;
  activeUnits @2 :UInt32;
  inFlight @3 :UInt32;
  lastRuntimeSource @4 :Text;
  lastError @5 :Text;
  lastEpoch @6 :UInt32;
}
