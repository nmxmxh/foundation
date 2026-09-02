# Vector Projection Practices

Status: experimental
Date: 2026-08-28
Owner: Runtime Performance

## Purpose

This document controls dense vector handling in Hermes projections.

The generic projection transport is a record-view lane. It excludes dense
vectors by default. Compute consumers request vectors explicitly.

## Fast path

HTTP and single-scope WebSocket handlers exclude vectors unless the request
has `vectors=include`. The TypeScript projection source sends that parameter
only when `ProjectionLoadRequest.includeVectors` is true.

The gateway builds record-only mutations before protobuf encoding. It does not
clone, serialize, decode, or send vectors that the consumer cannot observe.

Live fan-out counts vector and record-only subscribers before encoding. It
builds only the frame variants that active subscribers require.

## Snapshot formats

HCS1 remains the compatibility format for missing or variable-width vectors.
HCS3 is selected when every record has the same non-zero vector dimension.
It stores the dimension once and stores all F32 values as one row-major run.

Both formats decode through `streamSnapshotRecords`. The result must match the
legacy row-protobuf snapshot. This is the `FallbackRefinement` invariant.

## Runtime compute

Use `runtimehost.WriteFloat32Matrix` for dense F32 compute input. It validates
the row and dimension shape, then writes one contiguous aligned arena column.

Do not use a generic projection snapshot as a compute transport. Use an arena,
transferable buffer, or stream selected by the runtime lane planner.

## Evidence

`BenchmarkVectorProjectionModes` measures 1,000, 10,000, and 100,000 rows at
384, 768, and 1,536 dimensions. It measures include and exclude modes.

The 100,000-row, 1,536-dimension run on the local Apple M1 Pro measured:

| Mode | Time | Allocated bytes | Allocations |
| --- | ---: | ---: | ---: |
| Include vectors | 179.7 ms | 672.8 MB | 800,008 |
| Exclude vectors | 52.5 ms | 58.4 MB | 700,008 |

This benchmark is a regression signal. Run it on the production hardware
before selecting capacity or latency budgets.

## Deferred model contract

Foundation does not yet define cross-language model generation metadata. A
future contract must identify the model, dimension count, encoding,
normalization, and active generation. Until then, applications must prevent
mixed-generation comparisons at their persistence and compute boundaries.
