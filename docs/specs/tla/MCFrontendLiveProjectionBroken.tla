-------------------- MODULE MCFrontendLiveProjectionBroken -------------------
EXTENDS Naturals, Sequences

\* NEGATIVE CONTROL for FrontendLiveProjection.
\*
\* Reuses the real spec via INSTANCE and injects one bad transition: enqueuing a
\* live mutation into the frontend store's buffer WITHOUT the MaxQueued capacity
\* guard (the real EnqueueLiveMutation requires Len(liveQueue) < MaxQueued, and
\* over-capacity is supposed to drop and degrade). Without the bound the live
\* queue grows past MaxQueued, so LiveQueueBounded MUST be violated.

CONSTANTS Tenant, OtherTenant, Domain, Collection, Record, NULL

MaxQueued == 1
MaxVersion == 2

VARIABLES store, status, buffered, liveQueue, lastVersion, applied, rejected, dropped

INSTANCE FrontendLiveProjection

\* Like EnqueueLiveMutation, but drops the Len(liveQueue) < MaxQueued guard.
BadEnqueueLive(m) ==
  /\ status = "live"
  /\ m \in Mutation
  /\ liveQueue' = Append(liveQueue, m)
  /\ UNCHANGED <<store, status, buffered, lastVersion, applied, rejected, dropped>>

BrokenNext == Next \/ (\E m \in Mutation : BadEnqueueLive(m))

BrokenSpec == Init /\ [][BrokenNext]_vars

StateConstraint ==
  /\ applied <= 3
  /\ rejected <= 3
  /\ dropped <= 3
  /\ Len(buffered) <= MaxQueued
  /\ Len(liveQueue) <= MaxQueued + 1

==============================================================================
