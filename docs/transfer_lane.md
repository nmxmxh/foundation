# Transfer Lane (Progress-Bearing Operations)

Status: 0.0.1
Date: 2026-06-24
Owner: Platform Architecture

## Purpose and scope

Some operations have a durable truth of only "started / finished" yet a user
experience that depends on the continuous in-between: uploads, downloads,
exports, media transcodes, batch jobs, and token streams. The canonical event
contract (`<domain>:<action>[:vN]:requested|success|failed`) is deliberately
terminal-only because the event log is a durable, replayable, idempotent fact
lane. High-frequency progress ticks are the opposite of a durable fact: lossy,
coalescible, last-write-wins, and meaningless to replay.

The `transfer` package (`server-kit/go/transfer`) resolves this tension with two
lanes tied together by one `CorrelationID` (and a `TransferID`).

| | Fact lane (`events`) | Progress lane (`transfer`) |
| :--- | :--- | :--- |
| Carrier | `events.Envelope` bookends | `transfer.Update` snapshots |
| States | `:requested` / `:success` / `:failed` | `pending → uploading → staged → processing → ready` (+ `failed`/`aborted`) |
| Durability | Durable, replayable (event log) | Ephemeral, fan-out only — **never** written to the event log |
| Frequency | 2–3 per operation | Many; coalesced, monotonic, droppable |
| Guarantees | Idempotent, ordered | Best-effort, monotonic byte high-water mark |
| Threads | `correlation_id`, `transfer_id` | The **same** `correlation_id`, `transfer_id` |

The shared correlation is what gives progress event-grade *capability*
(correlation, tracing, RBAC metadata, subscription) without inheriting event
*semantics* it must not have.

## Components

- **`Phase` / `CanTransition`** — the forward-only lifecycle state machine.
  `failed` and `aborted` are reachable from any non-terminal phase; terminals
  are sinks.
- **`Tracker`** — live, concurrency-safe state for one transfer. Byte progress
  is a strictly monotonic high-water mark, so resumable/retried writes that
  re-send a known offset are naturally idempotent on the progress lane.
  `Subscribe` delivers the current snapshot immediately so late subscribers are
  never blind, then streams coalesced updates.
- **`Manager`** — a bounded (CP-02) registry that brackets each transfer with
  the durable bookend events on the fact lane and reaps the tracker on settle.
  A failed `:requested` bookend rolls back registration; a failed terminal
  bookend still reaps the tracker so a transient bus outage cannot pin memory.

## Rules

1. Progress `Update`s MUST NOT be appended to the event log or any durable
   stream. They ride the in-memory bus / Redis pub-sub / WebSocket only.
2. Every transfer MUST be bracketed by terminal bookend events on the fact
   lane; durable consumers depend on `:requested` / `:success` / `:failed`.
3. Every transfer MUST carry a `CorrelationID`; it is propagated onto every
   bookend and is the join key between the two lanes (see the correlation rule
   in `agent_operating_contract.md`).
4. Byte progress MUST be monotonic. Consumers MUST treat a lower-or-equal
   `Seq`/`BytesDone` as stale and ignore it.
5. The active transfer set MUST be bounded (`Manager.MaxActive`).

## HTTP surface

`httpapi.MakeTransferRoute` composes the byte plane and the lifecycle plane into
a streaming upload route. Unlike `MakeEventRoute`, it never buffers the whole
body: bytes flow from the request straight into the object store while progress
is reported on the progress lane, bracketed by the durable bookends.

```go
m, _ := transfer.NewManager(transfer.Config{
    Domain: "media", Action: "upload", Version: "v1", Bus: bus,
})

route, _ := httpapi.MakeTransferRoute(httpapi.TransferRouteConfig{
    Method:    http.MethodPut,
    Path:      "/media/upload",
    EventType: "media:upload:v1:requested",
    Manager:   m,
    Store:     store, // *objectstore.Store
    KeyPrefix: "uploads",
    MaxBytes:  256 << 20,
}, httpapi.WithStreaming())
```

The handler: enforces the `MaxBytes` ingress ceiling (early on `Content-Length`,
and again with `http.MaxBytesReader` mid-stream), derives the object key from
`security.GetOrganizationIDFromContext` for tenant isolation, begins the
transfer (`:requested`), streams into `objectstore.PutStream` while advancing a
progress reader, then `Complete`s (`:success`) or `Fail`s (`:failed`). The byte
plane (`PutStream` / `GetRange` / presign) and the `transfer` lifecycle share
nothing but the correlation thread.

## Resumable uploads (composes with `bulk`)

Large or flaky uploads need to survive a dropped connection. Rather than
reimplement chunking, the resumable surface delegates the durable byte/part
plane to `server-kit/go/bulk` — which already owns offset-aware multipart
storage, **idempotent part replay**, manifests, and tenant/correlation scope —
while the `transfer` Manager keeps owning progress and bookends. The two planes
share only the transfer id and correlation thread.

`httpapi.MakeResumableTransferRoutes` returns three routes (tus-inspired):

| Method / Path | Role |
| :--- | :--- |
| `POST {base}` | `bulk.Initiate` + `transfer.Begin` → returns `transfer_id`, `X-Chunk-Size`, `Location` |
| `HEAD {base}/{id}` | `bulk.Status` → `Upload-Offset` / `Upload-Length` / `Upload-Complete` (the resume point) |
| `PATCH {base}/{id}` | one aligned chunk (`Upload-Offset`, `X-Chunk-SHA256`) → `bulk.AcceptPart`, mirror progress; on full coverage `bulk.Complete` + `transfer.Complete` |

Invariants: chunks must align to the plan's chunk size (`Offset % ChunkSize == 0`,
`PartNumber = Offset / ChunkSize`); each `PATCH` carries a per-chunk SHA-256; the
chunk body is bounded by `MaxChunkBytes` (CP-02/CP-18); a re-sent chunk is a
no-op via bulk's receipt replay, so retries are safe. Progress mirroring is
**best-effort** — if the in-memory tracker is gone (for example, after a restart) bulk
remains the durable source of truth and the upload still resumes from `HEAD`.
Errors from bulk are surfaced through `errors.HTTPError` so the app error code
maps to the right status (404 unknown, 401 missing org, 409 conflict, …).

## Client surface

`@ovasabi/frontend-kit` exposes `useTransfer(source, transferId)` (and the
framework-agnostic `createTransferStore` / pure `reduceTransfer`). The browser
subscribes to the progress lane through a transport-supplied
`TransferProgressSource` and reconciles a monotonic `TransferSnapshot`
(`phase`, `bytesDone/bytesTotal`, `fraction`, `checksum`, `error`, `terminal`).
The reducer enforces the same invariants as the server: stale/duplicate `seq`
dropped, byte progress never regresses, terminal phase is a sink — replacing the
per-app upload stores (for example, the fintech_v1 `mediaUploadStore`).

## Testing posture

- `server-kit/go/transfer` (97%+): state machine (boundary/illegal/terminal
  transitions), tracker monotonicity and coalescing (race-checked
  concurrent-producer test), manager capacity/duplicate/rollback/reap, and a
  producer/consumer contract test asserting bookends validate against and are
  observable on the real `events` bus.
- `server-kit/go/httpapi` transfer route: happy-path stream-to-store with
  bookend + key-derivation assertions, config validation, method/ceiling/
  overrun (413) and store-failure (`:failed`) paths, and a progress-reader
  threshold/EOF test.
- `server-kit/go/httpapi` resumable routes: full create→HEAD→PATCH→complete
  lifecycle against a real `bulk.Manager`, idempotent chunk replay, misaligned/
  missing-checksum/oversize rejection, unknown-transfer (404), missing-org (401),
  and AcceptPart/Complete failure (`:failed`) paths via a bulk double.
- `frontend-kit/ts` `transferProgress`: pure reducer (stale/duplicate/regression/
  terminal-sink) and store subscription lifecycle (lazy subscribe / teardown /
  re-subscribe).

See `testing_practices.md` (TE-01..TE-04).
