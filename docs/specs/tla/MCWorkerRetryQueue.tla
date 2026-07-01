--------------------------- MODULE MCWorkerRetryQueue ------------------------
EXTENDS Naturals, Sequences, FiniteSets, TLC

\* Finite model instance of WorkerRetryQueue. Constant operators bind the
\* constants; the VARIABLES line binds the base state variables by name so a
\* plain INSTANCE resolves. Bounded work (mathematical_practices.md section 6):
\* the queue is capped at MaxQueue and every job's attempt budget at MaxAttempts,
\* so the reachable state space is finite with no constraint.

CONSTANTS j1, j2

Jobs == {j1, j2}
MaxQueue == 2
MaxAttempts == 2

VARIABLES queue, leased, attempts, terminal

INSTANCE WorkerRetryQueue

\* Jobs are interchangeable; collapse the symmetric state space.
Symmetry == Permutations({j1, j2})

==============================================================================
