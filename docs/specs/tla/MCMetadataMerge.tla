----------------------------- MODULE MCMetadataMerge -------------------------
EXTENDS Naturals, FiniteSets

\* Finite model instance of MetadataMerge. Two updates in the shared universe is
\* enough to exercise concurrent, reordered, and duplicate delivery across the
\* two replicas. State is a set, so the space is finite with no constraint.

CONSTANTS u1, u2

Updates == {u1, u2}

VARIABLES deliveredA, deliveredB, stateA, stateB

INSTANCE MetadataMerge

==============================================================================
