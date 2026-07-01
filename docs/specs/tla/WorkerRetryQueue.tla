---- MODULE WorkerRetryQueue ----
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Jobs, MaxQueue, MaxAttempts

\* Set of jobs currently in the queue sequence. (Previously used but undefined,
\* which made the module fail to parse under SANY.)
SeqToSet(s) == {s[i] : i \in 1..Len(s)}

VARIABLES queue, leased, attempts, terminal

vars == <<queue, leased, attempts, terminal>>

Init ==
  /\ queue = <<>>
  /\ leased = {}
  /\ attempts = [j \in Jobs |-> 0]
  /\ terminal = {}

TypeOK ==
  /\ queue \in Seq(Jobs)
  /\ Len(queue) <= MaxQueue
  /\ leased \subseteq Jobs
  /\ terminal \subseteq Jobs
  /\ DOMAIN attempts = Jobs
  /\ \A j \in Jobs : attempts[j] \in 0..MaxAttempts
  /\ leased \cap terminal = {}

Enqueue(j) ==
  /\ j \in Jobs
  /\ j \notin terminal
  /\ j \notin leased
  /\ j \notin SeqToSet(queue)
  /\ Len(queue) < MaxQueue
  /\ queue' = Append(queue, j)
  /\ UNCHANGED <<leased, attempts, terminal>>

Lease ==
  /\ queue # <<>>
  /\ LET j == Head(queue) IN
     /\ queue' = Tail(queue)
     /\ leased' = leased \cup {j}
     /\ attempts' = [attempts EXCEPT ![j] = @ + 1]
     /\ UNCHANGED terminal

Succeed(j) ==
  /\ j \in leased
  /\ terminal' = terminal \cup {j}
  /\ leased' = leased \ {j}
  /\ UNCHANGED <<queue, attempts>>

Retry(j) ==
  /\ j \in leased
  /\ attempts[j] < MaxAttempts
  /\ Len(queue) < MaxQueue
  /\ leased' = leased \ {j}
  /\ queue' = Append(queue, j)
  /\ UNCHANGED <<attempts, terminal>>

Exhaust(j) ==
  /\ j \in leased
  /\ attempts[j] >= MaxAttempts
  /\ terminal' = terminal \cup {j}
  /\ leased' = leased \ {j}
  /\ UNCHANGED <<queue, attempts>>

Next ==
  \/ \E j \in Jobs : Enqueue(j)
  \/ Lease
  \/ \E j \in Jobs : Succeed(j)
  \/ \E j \in Jobs : Retry(j)
  \/ \E j \in Jobs : Exhaust(j)

Spec == Init /\ [][Next]_vars

QueueBounded == Len(queue) <= MaxQueue
RetryBudgetBounded == \A j \in Jobs : attempts[j] <= MaxAttempts
AtLeastTerminalOrPending == \A j \in Jobs : j \in terminal \/ j \in leased \/ j \in SeqToSet(queue) \/ attempts[j] = 0

THEOREM Spec => []TypeOK
THEOREM Spec => []QueueBounded
THEOREM Spec => []RetryBudgetBounded

====
