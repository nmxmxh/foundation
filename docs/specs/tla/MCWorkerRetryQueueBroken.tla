------------------------ MODULE MCWorkerRetryQueueBroken ---------------------
EXTENDS Naturals, Sequences, FiniteSets, TLC

\* NEGATIVE CONTROL for WorkerRetryQueue.
\*
\* Reuses the real spec via INSTANCE and injects one bad transition: a retry
\* that re-enqueues a job WITHOUT checking its attempt budget (the real Retry
\* guards attempts[j] < MaxAttempts). A subsequent Lease then pushes attempts
\* past MaxAttempts, so RetryBudgetBounded MUST be violated -- an unbounded retry
\* loop, the exact failure the budget invariant forbids.

CONSTANTS j1, j2

Jobs == {j1, j2}
MaxQueue == 2
MaxAttempts == 2

VARIABLES queue, leased, attempts, terminal

INSTANCE WorkerRetryQueue

\* Same as Retry, but drops the attempts[j] < MaxAttempts guard (the injected
\* bug): an exhausted job gets requeued and re-leased past its budget.
BadRetry(j) ==
  /\ j \in leased
  /\ Len(queue) < MaxQueue
  /\ leased' = leased \ {j}
  /\ queue' = Append(queue, j)
  /\ UNCHANGED <<attempts, terminal>>

BrokenNext == Next \/ (\E j \in Jobs : BadRetry(j))

BrokenSpec == Init /\ [][BrokenNext]_vars

\* The unbounded retry loop grows attempts without bound; cap exploration.
StateConstraint == \A j \in Jobs : attempts[j] <= MaxAttempts + 1

==============================================================================
