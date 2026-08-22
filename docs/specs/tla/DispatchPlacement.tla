MODULE DispatchPlacement
\placement placement decisions over a shared lane table.
\anchor docs/mesh_dispatch_practices.md
\source runtime-sdk/rust/crates/ovrt-dispatch/src/decide.rs

EXTENDS Naturals, Sequences

CONSTANTS
  \* Table geometry mirrors runtime_dispatch.capnp constants.
  MAX_LANES,
  STALE_TICKS,
  GLOBAL_JURISDICTION,
  REQ_JURISDICTION,
  REQ_MASK

VARIABLES
  \* tick is the global click; every decision advances it by one.
  tick,
  \* per lane: classMask, jurisdiction, lastSeen (heartbeat click), ewma>0 flag
  laneMask,
  laneJurisdiction,
  laneLastSeen,
  laneSampled,
  \* selected is the lane id chosen by the last Decide action, or 0 for none.
  selected

TypeOK ==
    /\ tick \in Nat
    /\ \A l \in 1..MAX_LANES:
         /\ laneLastSeen[l] \in Nat
         /\ laneJurisdiction[l] \in Nat
         /\ laneMask[l] \in Nat

Init ==
    /\ tick = 0
    /\ \A l \in 1..MAX_LANES:
         /\ laneLastSeen[l] = 0
         /\ laneJurisdiction[l] = 0
         /\ laneMask[l] = 0
         /\ laneSampled[l] = FALSE
    /\ selected = 0

AdvanceTick ==
    tick' = tick + 1
    /\ UNCHANGED <<laneMask, laneJurisdiction, laneLastSeen, laneSampled, selected>>

Heartbeat(l) == laneLastSeen' = [laneLastSeen EXCEPT ![l] = tick]

Publish(l) ==
    laneSampled' = [laneSampled EXCEPT ![l] = TRUE]
    /\ UNCHANGED <<tick, laneMask, laneJurisdiction, laneLastSeen, selected>>

Retire(l) ==
    laneMask' = [laneMask EXCEPT ![l] = 0]
    /\ UNCHANGED <<tick, laneJurisdiction, laneLastSeen, laneSampled, selected>>

Fresh(l) ==
    /\ laneLastSeen[l] # 0
    /\ tick - laneLastSeen[l] <= STALE_TICKS

Covers(l) == laneMask[l] & REQ_MASK = REQ_MASK

JurisdictionOK(l) ==
    \/ laneJurisdiction[l] = GLOBAL_JURISDICTION
    \/ laneJurisdiction[l] = REQ_JURISDICTION

Eligible(l) ==
    /\ Covers(l)
    /\ JurisdictionOK(l)
    /\ Fresh(l)
    /\ laneSampled[l]
    /\ laneMask[l] # 0

Decide ==
    \E chosen \in 1..MAX_LANES:
        /\ Eligible(chosen)
        /\ \A other \in 1..MAX_LANES: ~Eligible(other) \/ chosen <= other
        /\ selected' = chosen
    /\ UNCHANGED <<tick, laneMask, laneJurisdiction, laneLastSeen, laneSampled>>

DecideNone ==
    /\ \A l \in 1..MAX_LANES: ~Eligible(l)
    /\ selected' = 0
    /\ UNCHANGED <<tick, laneMask, laneJurisdiction, laneLastSeen, laneSampled>>

Next == AdvanceTick \/ Heartbeat(1..MAX_LANES) \/ Publish(1..MAX_LANES) \/ Retire(1..MAX_LANES) \/ Decide \/ DecideNone

InvTypeOK == TypeOK

\* The click never moves backwards.
TickMonotonic ==
    \A l \in 1..MAX_LANES: laneLastSeen[l] <= tick

\* A served lane always matches the request jurisdiction exactly or is
\* declared global. Unknown jurisdictions can never be served.
JurisdictionFailClosed ==
    selected # 0 =>
      \/ laneJurisdiction[selected] = REQ_JURISDICTION
      \/ laneJurisdiction[selected] = GLOBAL_JURISDICTION

\* A served lane's heartbeat sits inside the freshness window.
StaleLaneExcluded ==
    selected # 0 => Fresh(selected)

\* Retired slots never serve traffic, even unconstrained requests.
RetiredSlotExcluded ==
    selected # 0 => laneMask[selected] # 0

Spec == Init /\ [][Next]_<<tick, laneMask, laneJurisdiction, laneLastSeen, laneSampled, selected>>

THEOREM Spec => [](InvTypeOK /\ TickMonotonic /\ JurisdictionFailClosed /\ StaleLaneExcluded /\ RetiredSlotExcluded)

=============================================================================
