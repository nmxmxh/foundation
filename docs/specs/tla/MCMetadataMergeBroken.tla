-------------------------- MODULE MCMetadataMergeBroken ----------------------
EXTENDS Naturals, FiniteSets

\* NEGATIVE CONTROL for MetadataMerge.
\*
\* This model replaces the set-union join with a NON-IDEMPOTENT fold: each
\* replica keeps a running COUNT of the deliveries it has applied, counting
\* duplicates. This is the CRDT-1 violation from mathematical_practices.md
\* section 5 -- a "merge" that is not idempotent.
\*
\* Because duplicates change the state, two replicas that have delivered the
\* SAME SET of updates can hold different states (different duplicate counts).
\* Model-checking StrongEventualConsistency here MUST fail, with a counterexample
\* such as deliveredA = deliveredB = {u1} but countA = 2, countB = 1. This proves
\* the SEC invariant has teeth and that the idempotence requirement is real.

CONSTANTS u1, u2

Updates == {u1, u2}

VARIABLES deliveredA, deliveredB, countA, countB

vars == <<deliveredA, deliveredB, countA, countB>>

Init ==
  /\ deliveredA = {}
  /\ deliveredB = {}
  /\ countA = 0
  /\ countB = 0

\* Non-idempotent fold: increments on every delivery, including duplicates.
DeliverA(u) ==
  /\ u \in Updates
  /\ deliveredA' = deliveredA \cup {u}
  /\ countA' = countA + 1
  /\ UNCHANGED <<deliveredB, countB>>

DeliverB(u) ==
  /\ u \in Updates
  /\ deliveredB' = deliveredB \cup {u}
  /\ countB' = countB + 1
  /\ UNCHANGED <<deliveredA, countA>>

Next ==
  \/ \E u \in Updates : DeliverA(u)
  \/ \E u \in Updates : DeliverB(u)

Spec == Init /\ [][Next]_vars

\* Duplicate deliveries make the counts grow without bound; cap them.
StateConstraint == countA <= 4 /\ countB <= 4

\* Same statement as the real spec, but over the broken (counting) state.
StrongEventualConsistency ==
  (deliveredA = deliveredB) => (countA = countB)

==============================================================================
