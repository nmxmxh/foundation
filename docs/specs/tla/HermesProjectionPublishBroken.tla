-------------------- MODULE HermesProjectionPublishBroken --------------------
EXTENDS Naturals

\* NEGATIVE CONTROL for HermesProjectionPublish.
\*
\* This model reuses the real spec via INSTANCE and injects one deliberately bad
\* transition: a publish that decouples the record's bucket from its status
\* (always writing bucket 1). That is exactly the torn write the real Publish
\* forbids by construction. Model-checking this MUST report TearFreeRead
\* violated, with a counterexample such as status = "archived" with bucket = 1.
\*
\* Purpose: prove the TearFreeRead invariant has teeth. A safety invariant that
\* cannot fail is not evidence of anything. This is the TLA analogue of a
\* mutation test and mirrors the runtime negative control we use when weakening
\* the Go/Rust memory orderings under -race / loom.
\*
\* The formal-methods check runs this and asserts it FAILS; a passing negative
\* control is itself a failure.

MaxPublishes == 4

VARIABLES snapshot, epoch, watermark

INSTANCE HermesProjectionPublish

\* Same as Publish, but decouples bucket from status (the injected bug).
BadPublish(s) ==
  /\ s \in Status
  /\ snapshot' = [status |-> s, bucket |-> 1, version |-> watermark + 1]
  /\ watermark' = watermark + 1
  /\ epoch' = epoch + 1

BrokenNext == Next \/ (\E s \in Status : BadPublish(s))

BrokenSpec == Init /\ [][BrokenNext]_vars

==============================================================================
