------------------------------ MODULE MetadataMerge --------------------------
EXTENDS Naturals, FiniteSets

\* Convergence of the Foundation metadata merge under concurrent, reordered,
\* at-least-once delivery.
\*
\* Maps to mathematical_practices.md section 5 (CRDT-shaped merges) and governs
\* server-kit/go/metadata merges plus any folded/replayed state under
\* server-kit/go/events. The result being encoded (Shapiro et al. 2011): a
\* state-based replicated type whose states form a JOIN-SEMILATTICE and whose
\* merge is the lattice join achieves Strong Eventual Consistency (SEC) --
\* replicas that have received the same set of updates have equal state, with no
\* coordination.
\*
\* Model. Metadata is a set of tags (grow-only set, the simplest CRDT). Each of
\* two replicas independently receives an arbitrary subset of a shared universe
\* of updates, in any order, with duplicates. State is the union of received
\* updates. The merge is set union.
\*
\* Set union is the canonical join-semilattice join:
\*   Commutative:  a \cup b = b \cup a
\*   Associative:  (a \cup b) \cup c = a \cup (b \cup c)
\*   Idempotent:   a \cup a = a
\* The invariant below is the SEC conclusion: once both replicas have delivered
\* the SAME set of updates, their merged state is identical -- regardless of the
\* order or multiplicity in which they arrived.

CONSTANTS Updates          \* the shared universe of metadata updates (tags)

ASSUME Updates # {}

VARIABLES
  deliveredA,   \* set of updates replica A has applied
  deliveredB,   \* set of updates replica B has applied
  stateA,       \* replica A's folded state
  stateB        \* replica B's folded state

vars == <<deliveredA, deliveredB, stateA, stateB>>

\* The merge under test: the join of the semilattice. For a grow-only set this
\* is union. Kept as a named operator so the negative-control model can swap it.
Merge(a, b) == a \cup b

TypeOK ==
  /\ deliveredA \subseteq Updates
  /\ deliveredB \subseteq Updates
  /\ stateA \subseteq Updates
  /\ stateB \subseteq Updates

Init ==
  /\ deliveredA = {}
  /\ deliveredB = {}
  /\ stateA = {}
  /\ stateB = {}

\* Replica A applies an update (possibly one it already has -- duplicate/reorder
\* safe because Merge is idempotent and commutative).
DeliverA(u) ==
  /\ u \in Updates
  /\ deliveredA' = deliveredA \cup {u}
  /\ stateA' = Merge(stateA, {u})
  /\ UNCHANGED <<deliveredB, stateB>>

DeliverB(u) ==
  /\ u \in Updates
  /\ deliveredB' = deliveredB \cup {u}
  /\ stateB' = Merge(stateB, {u})
  /\ UNCHANGED <<deliveredA, stateA>>

\* Anti-entropy: replicas exchange and merge full states. This must not break
\* convergence -- merging a peer's state is just another join.
GossipAtoB ==
  /\ stateB' = Merge(stateB, stateA)
  /\ UNCHANGED <<deliveredA, deliveredB, stateA>>

GossipBtoA ==
  /\ stateA' = Merge(stateA, stateB)
  /\ UNCHANGED <<deliveredA, deliveredB, stateB>>

Next ==
  \/ \E u \in Updates : DeliverA(u)
  \/ \E u \in Updates : DeliverB(u)
  \/ GossipAtoB
  \/ GossipBtoA

Spec == Init /\ [][Next]_vars

\* --------------------------------------------------------------------------
\* Invariants
\* --------------------------------------------------------------------------

\* Each replica's state is exactly the set of updates it has delivered (plus any
\* it learned by gossip). Gossip can only add updates the peer had delivered, so
\* state is always a subset of the union of both delivery sets.
StateReflectsDeliveries ==
  /\ stateA \subseteq (deliveredA \cup deliveredB)
  /\ stateB \subseteq (deliveredB \cup deliveredA)

\* STRONG EVENTUAL CONSISTENCY (the theorem being model-checked): if both
\* replicas have delivered the same set of updates, their states are equal --
\* independent of order and duplication. This is the safety core of SEC; the
\* liveness half (gossip eventually equalizes delivery sets) is an operational
\* obligation, not a state invariant.
StrongEventualConsistency ==
  (deliveredA = deliveredB) => (stateA = stateB)

\* Monotone growth: a grow-only CRDT never loses an update (no action removes
\* from state). Expressed as a step property in the .cfg via PROPERTY.
MonotoneA == [][stateA \subseteq stateA']_vars
MonotoneB == [][stateB \subseteq stateB']_vars

THEOREM Spec => []TypeOK
THEOREM Spec => []StateReflectsDeliveries
THEOREM Spec => []StrongEventualConsistency

==============================================================================
