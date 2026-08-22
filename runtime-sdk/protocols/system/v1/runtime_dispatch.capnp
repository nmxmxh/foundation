@0xdf5cac001d15ea01;

# Dispatch lane table shared between the Rust kernel, Go hosts, and browser
# workers. One region holds a dual-buffered descriptor table, a global tick,
# and per-lane live statistics. Layout is fixed: every offset below is a
# generated constant consumed by all three runtimes through
# generate_system_bindings.sh.

const dispatchSchemaVersion :UInt32 = 1;

# Table geometry. A descriptor row and a stat row are each one cache line.
const dispatchMaxLanes :UInt32 = 32;
const dispatchLaneRowBytes :UInt32 = 64;

# Region map.
# [0 .. 64)                  header: flip index + global tick, one cache line
# [dispatchStatsOffset ..)   stat rows, one per lane, single writer per row
# [dispatchBuffersOffset ..) two descriptor buffers, swapped by publication
const dispatchFlipIndexOffset :UInt32 = 0;
const dispatchTickOffset :UInt32 = 8;
const dispatchHeaderBytes :UInt32 = 64;
const dispatchStatsOffset :UInt32 = 64;
const dispatchBufferBytes :UInt32 = 2048;
const dispatchBuffersOffset :UInt32 = 2112;
const dispatchRegionBytes :UInt32 = 6208;

# Freshness and scoring policy.
# A lane whose last_tick_seen lags the global tick by more than staleTicks is
# excluded from selection. EWMA alpha is num/den = 1/8. Affinity bonus is
# subtracted from expected latency when the request key hits a lane's Bloom
# set; measurement always outranks the hint because the bonus stays far below
# one queueing interval of any healthy lane.
const dispatchStaleTicks :UInt32 = 4;
const dispatchEwmaAlphaNum :UInt32 = 1;
const dispatchEwmaAlphaDen :UInt32 = 8;
const dispatchAffinityBonusNs :UInt32 = 250000;

# Jurisdiction 0 marks a lane as globally serviceable. Any other value must
# match the request exactly; unknown jurisdictions select nothing.
const dispatchJurisdictionGlobal :UInt32 = 0;

struct DispatchLaneDescriptor {
  # Bit set of unit classes this lane executes.
  unitClassMask @0 :UInt64;

  # Bloom bit set over locality keys (partition, scope) this lane holds.
  affinityBloom @1 :UInt64;

  laneId @2 :UInt16;
  jurisdiction @3 :UInt16;

  maxConcurrency @4 :UInt32;

  # Publisher generation: incremented on every buffer swap so readers can
  # detect an observed table older than they expect.
  generation @5 :UInt32;
}

struct DispatchLaneStats {
  # Exponentially weighted mean completion latency, nanoseconds.
  ewmaNs @0 :UInt64;

  inflight @1 :UInt32;
  maxConcurrency @2 :UInt32;

  # Owner heartbeat in global-tick units.
  lastTickSeen @3 :UInt64;
}

enum DispatchDecision {
  selected @0;
  noEligibleLane @1;
  staleOnly @2;
}
