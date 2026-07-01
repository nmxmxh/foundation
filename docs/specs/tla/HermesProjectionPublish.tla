---- MODULE HermesProjectionPublish ----
\* Implementation-mapped spec for the Hermes lock-free projection publish path.
\*
\* Maps to server-kit/go/hermes:
\*   * `snapshot`  -> the immutable value behind `atomic.Pointer[recordEntry]`
\*                   / `atomic.Pointer[indexSnapshot]` (store.go recordCell /
\*                   indexCell). Readers load this pointer with no lock
\*                   (apply.go:388, indexes.go:190).
\*   * `epoch`     -> partition.epoch (atomic.Uint64), bumped once per applied
\*                   batch (store.go:88, Epoch()).
\*   * `watermark` -> partition.watermark / source_watermark (store.go:89),
\*                   the fence value used by `fenced` reads (hermes_read_modes).
\*
\* The record carries two COUPLED fields, status and bucket, exactly like the
\* concurrency test TestStoreConcurrentPatchReadsNeverObserveTornArchiveState
\* (store_test.go:280): a consistent record is (active,1) or (archived,2); any
\* other pairing is a torn read.
\*
\* Key modeling fact: the writer is single-threaded per partition (mu +
\* publishing atomic.Bool, store.go:87-90) and publishes a fully-formed,
\* immutable snapshot with ONE atomic pointer swap. In TLA every action is
\* atomic, so `Publish` swapping `snapshot` in a single step models the atomic
\* `cell.ptr.Store(entry)`. Because the published value is immutable and the
\* swap is atomic, no reachable state ever exposes a half-updated record. That
\* is what makes the lock-free read tear-free, and it is the property the Go
\* -race gate and this spec both assert.

EXTENDS Naturals

CONSTANT MaxPublishes

Status == {"active", "archived"}

\* The bucket a status must carry for the record to be internally consistent.
BucketFor(s) == IF s = "active" THEN 1 ELSE 2

ConsistentPair(rec) ==
  /\ rec.status \in Status
  /\ rec.bucket = BucketFor(rec.status)

VARIABLES snapshot, epoch, watermark

vars == <<snapshot, epoch, watermark>>

RecordType == [status: Status, bucket: {1, 2}, version: Nat]

TypeOK ==
  /\ snapshot \in RecordType
  /\ epoch \in Nat
  /\ watermark \in Nat

Init ==
  /\ snapshot = [status |-> "active", bucket |-> 1, version |-> 1]
  /\ epoch = 1
  /\ watermark = 1

\* Single-writer copy-on-write publish: build a brand-new immutable snapshot
\* whose coupled fields are consistent by construction, then swap it in and bump
\* epoch + watermark together. Models hermes apply/bulkLoad replacing the
\* atomic.Pointer and incrementing the partition epoch.
Publish(s) ==
  /\ s \in Status
  /\ snapshot' = [status |-> s, bucket |-> BucketFor(s), version |-> watermark + 1]
  /\ watermark' = watermark + 1
  /\ epoch' = epoch + 1

\* Lock-free read: observes whatever snapshot is currently published. It changes
\* no state; its correctness is the state invariant TearFreeRead below, which
\* must hold of EVERY reachable snapshot a reader could load.
Read ==
  /\ UNCHANGED vars

\* A fenced (read-your-write) read is only served from Hermes when the published
\* version has caught up to the requested fence; otherwise the caller falls back
\* to Postgres (hermes_read_modes.md `fenced`). Either way it never returns a
\* snapshot older than the fence.
ReadFenced(f) ==
  /\ f \in 1..(MaxPublishes + 1)
  /\ snapshot.version >= f
  /\ UNCHANGED vars

Next ==
  \/ \E s \in Status : Publish(s)
  \/ Read
  \/ \E f \in 1..(MaxPublishes + 1) : ReadFenced(f)

Spec == Init /\ [][Next]_vars

\* Invariants -------------------------------------------------------------

\* No reader ever loads a torn record: the published snapshot's coupled fields
\* are always consistent. Direct analogue of the Go tear-free test assertion.
TearFreeRead == ConsistentPair(snapshot)

\* The published version never runs ahead of the source watermark, and epoch
\* advances in lockstep with published versions (one bump per publish).
VersionWatermarkConsistent ==
  /\ snapshot.version <= watermark
  /\ epoch = watermark

\* Bound the otherwise-infinite Nat state space for TLC.
StateConstraint == watermark <= MaxPublishes

THEOREM Spec => []TypeOK
THEOREM Spec => []TearFreeRead
THEOREM Spec => []VersionWatermarkConsistent

====
