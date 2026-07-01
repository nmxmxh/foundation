--------------------- MODULE MCWebSocketBackpressureBroken -------------------
EXTENDS Naturals, Sequences, FiniteSets, TLC

\* NEGATIVE CONTROL for WebSocketBackpressure.
\*
\* Reuses the real spec via INSTANCE and injects one bad transition: an enqueue
\* to a connection's outbound write queue that skips the MaxWriteQueue capacity
\* guard (the real EnqueueWrite requires Len(writeQueue[c]) < MaxWriteQueue).
\* Without the bound, a slow client's queue grows unboundedly, so
\* WriteQueueBounded MUST be violated -- the backpressure failure the invariant
\* forbids.

CONSTANTS c1, c2, t1

Connections == {c1, c2}
Topics == {t1}
MaxWriteQueue == 2

VARIABLES open, authenticated, subscriptions, writeQueue, closed

INSTANCE WebSocketBackpressure

\* Like EnqueueWrite, but drops the capacity guard (the injected bug).
BadEnqueueWrite(c, t) ==
  /\ c \in authenticated
  /\ t \in subscriptions[c]
  /\ writeQueue' = [writeQueue EXCEPT ![c] = Append(@, t)]
  /\ UNCHANGED <<open, authenticated, subscriptions, closed>>

BrokenNext == Next \/ (\E c \in Connections, t \in Topics : BadEnqueueWrite(c, t))

BrokenSpec == Init /\ [][BrokenNext]_vars

\* The unbounded enqueue grows the write queue; cap exploration.
StateConstraint == \A c \in Connections : Len(writeQueue[c]) <= MaxWriteQueue + 1

==============================================================================
