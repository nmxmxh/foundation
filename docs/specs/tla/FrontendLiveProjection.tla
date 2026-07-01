--------------------------- MODULE FrontendLiveProjection ---------------------------
EXTENDS Naturals, Sequences

\* Lightweight model for generated frontend tenant stores connected to Hermes
\* projection streams. It documents the correctness shape; production tests map
\* these invariants to frontend-kit live projection binding tests.

\* NULL is the empty-record sentinel; MaxVersion bounds the version axis so the
\* Mutation set is finite (it was `version : Nat`, an infinite set TLC cannot
\* enumerate). NULL was previously used in Init but never declared.
CONSTANTS Tenant, OtherTenant, Domain, Collection, Record, MaxQueued, NULL, MaxVersion

VARIABLES store, status, buffered, liveQueue, lastVersion, applied, rejected, dropped

StatusValues == {"idle", "loading", "live", "degraded", "closed", "error"}

Mutation ==
  [ tenant : {Tenant, OtherTenant},
    domain : {Domain},
    collection : {Collection},
    recordId : {"record-1"},
    version : 1..MaxVersion,
    op : {"upsert", "patch", "delete"},
    record : {Record} ]

Init ==
  /\ store = [r \in {"record-1"} |-> NULL]
  /\ status = "idle"
  /\ buffered = <<>>
  /\ liveQueue = <<>>
  /\ lastVersion = 0
  /\ applied = 0
  /\ rejected = 0
  /\ dropped = 0

StartConnect ==
  /\ status \in {"idle", "closed", "degraded", "error"}
  /\ status' = "loading"
  /\ UNCHANGED <<store, buffered, liveQueue, lastVersion, applied, rejected, dropped>>

BufferLiveMutation(m) ==
  /\ status = "loading"
  /\ m \in Mutation
  /\ buffered' = Append(buffered, m)
  /\ UNCHANGED <<store, status, liveQueue, lastVersion, applied, rejected, dropped>>

EnqueueLiveMutation(m) ==
  /\ status = "live"
  /\ m \in Mutation
  /\ Len(liveQueue) < MaxQueued
  /\ liveQueue' = Append(liveQueue, m)
  /\ UNCHANGED <<store, status, buffered, lastVersion, applied, rejected, dropped>>

DropLiveMutation(m) ==
  /\ status = "live"
  /\ m \in Mutation
  /\ Len(liveQueue) >= MaxQueued
  /\ status' = "degraded"
  /\ dropped' = dropped + 1
  /\ rejected' = rejected + 1
  /\ UNCHANGED <<store, buffered, liveQueue, lastVersion, applied>>

Accept(m) ==
  /\ m.tenant = Tenant
  /\ m.domain = Domain
  /\ m.collection = Collection
  /\ m.version >= lastVersion

ApplyAccepted(m) ==
  /\ Accept(m)
  /\ store' = [store EXCEPT ![m.recordId] = IF m.op = "delete" THEN NULL ELSE m.record]
  /\ lastVersion' = m.version
  /\ applied' = applied + 1
  /\ UNCHANGED <<status, buffered, liveQueue, rejected, dropped>>

RejectMutation(m) ==
  /\ m \in Mutation
  /\ ~Accept(m)
  /\ rejected' = rejected + 1
  /\ UNCHANGED <<store, status, buffered, liveQueue, lastVersion, applied, dropped>>

FlushLiveQueue ==
  /\ Len(liveQueue) > 0
  /\ liveQueue' = Tail(liveQueue)
  /\ UNCHANGED <<store, status, buffered, lastVersion, applied, rejected, dropped>>

FinishInitialLoad ==
  /\ status = "loading"
  /\ status' = "live"
  /\ buffered' = <<>>
  /\ UNCHANGED <<store, liveQueue, lastVersion, applied, rejected, dropped>>

Disconnect ==
  /\ status \in {"loading", "live", "degraded", "error"}
  /\ status' = "closed"
  /\ buffered' = <<>>
  /\ liveQueue' = <<>>
  /\ UNCHANGED <<store, lastVersion, applied, rejected, dropped>>

Reset ==
  /\ store' = [r \in {"record-1"} |-> NULL]
  /\ status' = "idle"
  /\ buffered' = <<>>
  /\ liveQueue' = <<>>
  /\ lastVersion' = 0
  /\ applied' = 0
  /\ rejected' = 0
  /\ dropped' = 0

Next ==
  \/ StartConnect
  \/ \E m \in Mutation : BufferLiveMutation(m)
  \/ \E m \in Mutation : EnqueueLiveMutation(m)
  \/ \E m \in Mutation : DropLiveMutation(m)
  \/ \E m \in Mutation : ApplyAccepted(m)
  \/ \E m \in Mutation : RejectMutation(m)
  \/ FlushLiveQueue
  \/ FinishInitialLoad
  \/ Disconnect
  \/ Reset

TypeOK ==
  /\ status \in StatusValues
  /\ buffered \in Seq(Mutation)
  /\ liveQueue \in Seq(Mutation)
  /\ lastVersion \in Nat
  /\ applied \in Nat
  /\ rejected \in Nat
  /\ dropped \in Nat

TenantScopeStable ==
  \A m \in Mutation : m.tenant # Tenant => ~Accept(m)

\* The applied version stays within the deliverable range. (State invariant.)
VersionWithinBound ==
  lastVersion <= MaxVersion

ClosedDoesNotBuffer ==
  status = "closed" => Len(buffered) = 0 /\ Len(liveQueue) = 0

LiveQueueBounded ==
  Len(liveQueue) <= MaxQueued

vars == <<store, status, buffered, liveQueue, lastVersion, applied, rejected, dropped>>

Spec == Init /\ [][Next]_vars

\* Real monotonicity as a STEP property (the old `lastVersion >= 0` was
\* vacuous): every step either keeps/advances the applied version or resets it
\* to 0 on teardown. ApplyAccepted only fires when m.version >= lastVersion, so
\* it never regresses; only Reset zeroes it. This is the read-your-write /
\* no-stale-overwrite guarantee the frontend store depends on.
VersionMonotone ==
  [][ lastVersion' >= lastVersion \/ lastVersion' = 0 ]_vars

THEOREM Spec => []TypeOK
THEOREM Spec => []TenantScopeStable
THEOREM Spec => []VersionWithinBound
THEOREM Spec => []ClosedDoesNotBuffer
THEOREM Spec => []LiveQueueBounded
THEOREM Spec => VersionMonotone

================================================================================
