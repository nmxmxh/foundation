----------------------- MODULE MCFrontendLiveProjection ----------------------
EXTENDS Naturals, Sequences

\* Finite model instance of FrontendLiveProjection. Constant operators bind the
\* numeric bounds; the model-value constants are assigned in the .cfg. The
\* VARIABLES line binds the base state variables by name so a plain INSTANCE
\* resolves. applied/rejected/dropped counters and the buffered sequence grow
\* without bound, so a StateConstraint caps them for finite checking.

CONSTANTS Tenant, OtherTenant, Domain, Collection, Record, NULL

\* Kept small: buffered/liveQueue are sequences over the Mutation set, so the
\* state space is very sensitive to these bounds. MaxQueued = 1 still exercises
\* the "queue full -> drop -> degrade" transition.
MaxQueued == 1
MaxVersion == 2

VARIABLES store, status, buffered, liveQueue, lastVersion, applied, rejected, dropped

INSTANCE FrontendLiveProjection

StateConstraint ==
  /\ applied <= 2
  /\ rejected <= 2
  /\ dropped <= 2
  /\ Len(buffered) <= MaxQueued

==============================================================================
