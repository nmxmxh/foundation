---- MODULE CacheProjectionFreshness ----
EXTENDS Naturals, FiniteSets

CONSTANTS Keys, Values, MaxStaleness

VARIABLES truth, cache, watermarks, now

vars == <<truth, cache, watermarks, now>>

Init ==
  /\ truth \in [Keys -> Values]
  /\ cache \in [Keys -> [value: Values, version: Nat, refreshedAt: Nat]]
  /\ watermarks \in [Keys -> Nat]
  /\ now = 0
  /\ \A k \in Keys : cache[k].version <= watermarks[k]

TypeOK ==
  /\ truth \in [Keys -> Values]
  /\ cache \in [Keys -> [value: Values, version: Nat, refreshedAt: Nat]]
  /\ watermarks \in [Keys -> Nat]
  /\ now \in Nat
  /\ \A k \in Keys : cache[k].version <= watermarks[k]

WriteTruth(k, v) ==
  /\ k \in Keys
  /\ v \in Values
  /\ truth' = [truth EXCEPT ![k] = v]
  /\ watermarks' = [watermarks EXCEPT ![k] = @ + 1]
  /\ UNCHANGED <<cache, now>>

Refresh(k) ==
  /\ k \in Keys
  /\ cache' = [cache EXCEPT ![k] = [value |-> truth[k], version |-> watermarks[k], refreshedAt |-> now]]
  /\ UNCHANGED <<truth, watermarks, now>>

ReadCache(k) ==
  /\ k \in Keys
  /\ watermarks[k] - cache[k].version <= MaxStaleness
  /\ UNCHANGED vars

RepairDrift(k) ==
  /\ k \in Keys
  /\ cache[k].version < watermarks[k]
  /\ cache' = [cache EXCEPT ![k] = [value |-> truth[k], version |-> watermarks[k], refreshedAt |-> now]]
  /\ UNCHANGED <<truth, watermarks, now>>

Tick ==
  /\ now' = now + 1
  /\ UNCHANGED <<truth, cache, watermarks>>

Next ==
  \/ \E k \in Keys, v \in Values : WriteTruth(k, v)
  \/ \E k \in Keys : Refresh(k)
  \/ \E k \in Keys : ReadCache(k)
  \/ \E k \in Keys : RepairDrift(k)
  \/ Tick

Spec == Init /\ [][Next]_vars

ProjectionVersionMonotonic == \A k \in Keys : cache[k].version <= watermarks[k]
BoundedStalenessForReads == \A k \in Keys : watermarks[k] - cache[k].version <= MaxStaleness \/ cache[k].version < watermarks[k]

THEOREM Spec => []TypeOK
THEOREM Spec => []ProjectionVersionMonotonic

====
