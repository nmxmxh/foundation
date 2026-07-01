-------------------- MODULE MCCacheProjectionFreshnessBroken -----------------
EXTENDS Naturals, FiniteSets

\* NEGATIVE CONTROL for CacheProjectionFreshness.
\*
\* Reuses the real spec via INSTANCE and injects one bad transition: a refresh
\* that stamps the cache with a version ABOVE the source watermark. A projection
\* version must never run ahead of the source of truth (the watermark is a
\* monotone G-Counter-style lower bound; see mathematical_practices.md CRDT-4).
\* Model-checking ProjectionVersionMonotonic here MUST fail.

CONSTANTS k1, v1, v2

Keys == {k1}
Values == {v1, v2}
MaxStaleness == 2

VARIABLES truth, cache, watermarks, now

INSTANCE CacheProjectionFreshness

\* Like Refresh, but stamps a version one past the watermark (the injected bug):
\* the cache claims to be fresher than the source of truth actually is.
BadRefresh(k) ==
  /\ k \in Keys
  /\ cache' = [cache EXCEPT ![k] =
        [value |-> truth[k], version |-> watermarks[k] + 1, refreshedAt |-> now]]
  /\ UNCHANGED <<truth, watermarks, now>>

BrokenNext == Next \/ (\E k \in Keys : BadRefresh(k))

BrokenSpec == Init /\ [][BrokenNext]_vars

StateConstraint ==
  /\ now <= 2
  /\ \A k \in Keys : watermarks[k] <= 3

==============================================================================
