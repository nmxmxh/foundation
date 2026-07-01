----------------------- MODULE MCCacheProjectionFreshness --------------------
EXTENDS Naturals, FiniteSets

\* Finite model instance of CacheProjectionFreshness. Constant operators bind
\* the constants; the VARIABLES line binds the base state variables by name so a
\* plain INSTANCE resolves. The clock and per-key watermarks grow without bound
\* in the base spec, so a StateConstraint caps them for finite checking.

CONSTANTS k1, v1, v2

Keys == {k1}
Values == {v1, v2}
MaxStaleness == 2

VARIABLES truth, cache, watermarks, now

INSTANCE CacheProjectionFreshness

StateConstraint ==
  /\ now <= 2
  /\ \A k \in Keys : watermarks[k] <= 3

==============================================================================
