---- MODULE WebSocketBackpressure ----
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Connections, Topics, MaxWriteQueue

VARIABLES open, authenticated, subscriptions, writeQueue, closed

vars == <<open, authenticated, subscriptions, writeQueue, closed>>

Init ==
  /\ open = {}
  /\ authenticated = {}
  /\ subscriptions = [c \in Connections |-> {}]
  /\ writeQueue = [c \in Connections |-> <<>>]
  /\ closed = {}

TypeOK ==
  /\ open \subseteq Connections
  /\ authenticated \subseteq open
  /\ closed \subseteq Connections
  /\ authenticated \cap closed = {}
  /\ DOMAIN subscriptions = Connections
  /\ DOMAIN writeQueue = Connections
  /\ \A c \in Connections : subscriptions[c] \subseteq Topics
  /\ \A c \in Connections : writeQueue[c] \in Seq(Topics)
  /\ \A c \in Connections : Len(writeQueue[c]) <= MaxWriteQueue

Open(c) ==
  /\ c \in Connections
  /\ c \notin open
  /\ c \notin closed
  /\ open' = open \cup {c}
  /\ UNCHANGED <<authenticated, subscriptions, writeQueue, closed>>

Authenticate(c) ==
  /\ c \in open
  /\ authenticated' = authenticated \cup {c}
  /\ UNCHANGED <<open, subscriptions, writeQueue, closed>>

Subscribe(c, t) ==
  /\ c \in authenticated
  /\ t \in Topics
  /\ subscriptions' = [subscriptions EXCEPT ![c] = @ \cup {t}]
  /\ UNCHANGED <<open, authenticated, writeQueue, closed>>

EnqueueWrite(c, t) ==
  /\ c \in authenticated
  /\ t \in subscriptions[c]
  /\ Len(writeQueue[c]) < MaxWriteQueue
  /\ writeQueue' = [writeQueue EXCEPT ![c] = Append(@, t)]
  /\ UNCHANGED <<open, authenticated, subscriptions, closed>>

FlushWrite(c) ==
  /\ c \in open
  /\ writeQueue[c] # <<>>
  /\ writeQueue' = [writeQueue EXCEPT ![c] = Tail(@)]
  /\ UNCHANGED <<open, authenticated, subscriptions, closed>>

CloseSlow(c) ==
  /\ c \in open
  /\ Len(writeQueue[c]) >= MaxWriteQueue
  /\ open' = open \ {c}
  /\ authenticated' = authenticated \ {c}
  /\ subscriptions' = [subscriptions EXCEPT ![c] = {}]
  /\ writeQueue' = [writeQueue EXCEPT ![c] = <<>>]
  /\ closed' = closed \cup {c}

Next ==
  \/ \E c \in Connections : Open(c)
  \/ \E c \in Connections : Authenticate(c)
  \/ \E c \in Connections, t \in Topics : Subscribe(c, t)
  \/ \E c \in Connections, t \in Topics : EnqueueWrite(c, t)
  \/ \E c \in Connections : FlushWrite(c)
  \/ \E c \in Connections : CloseSlow(c)

Spec == Init /\ [][Next]_vars

WriteQueueBounded == \A c \in Connections : Len(writeQueue[c]) <= MaxWriteQueue
TopicAuthorized == \A c \in Connections : subscriptions[c] # {} => c \in authenticated
DisconnectCleansState == \A c \in closed : subscriptions[c] = {} /\ writeQueue[c] = <<>>

THEOREM Spec => []TypeOK
THEOREM Spec => []WriteQueueBounded
THEOREM Spec => []TopicAuthorized
THEOREM Spec => []DisconnectCleansState

====
