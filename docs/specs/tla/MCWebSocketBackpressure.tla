------------------------ MODULE MCWebSocketBackpressure ----------------------
EXTENDS Naturals, Sequences, FiniteSets, TLC

\* Finite model instance of WebSocketBackpressure. Constant operators bind the
\* constants; the VARIABLES line binds the base state variables by name so a
\* plain INSTANCE resolves. Connection/auth/subscription/closed sets are subsets
\* of finite Connections/Topics, and writeQueue is bounded by MaxWriteQueue, so
\* the reachable state space is finite with no constraint.

CONSTANTS c1, c2, t1

Connections == {c1, c2}
Topics == {t1}
MaxWriteQueue == 2

VARIABLES open, authenticated, subscriptions, writeQueue, closed

INSTANCE WebSocketBackpressure

\* Connections are interchangeable; collapse the symmetric state space.
Symmetry == Permutations({c1, c2})

==============================================================================
