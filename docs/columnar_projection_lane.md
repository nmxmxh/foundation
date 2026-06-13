# Hermes Columnar Projection Lane (Arrow-Compatible Structure-of-Arrays)

This document specifies the architecture and implementation design for the **Hermes Columnar Projection Lane**. For scan-heavy reports, aggregations, and telemetry, mapping data as row-oriented structs or maps introduces significant memory overhead and cache misses. Transposing this data into columnar Structure-of-Arrays (SoA) buffers allows SIMD-accelerated filtering and zero-copy integration with DuckDB or Apache Arrow.

---

## 1. Architectural Motivation

Currently, Hermes partitions store projections as row-oriented slices: `[]DomainRecord`. While highly efficient for point-lookups (e.g., retrieving a single entity by ID), scan-heavy operations like:

- "Compute the average latency for tenant X over the last 1 hour"
- "Sum total bandwidth consumed by all client nodes"
suffer from two primary inefficiencies:

1. **Cache Locality**: Accessing a single field requires loading the entire `DomainRecord` object into CPU L1/L2 cache, bringing along unrelated fields.
2. **Allocation Churn**: Serializing map-oriented representations or slices of structures generates substantial garbage collection overhead during scans.

By restructuring the in-memory layout into columnar buffers matching the **Apache Arrow format**, we can:

- Execute filter-and-sum loops in a single pass with contiguous memory access.
- Leverage **SIMD instructions** (via Go vector packages or assembly code generation) for parallel vector scanning.
- Integrate directly with embedded columnar engines (e.g., DuckDB via Arrow C Data Interface) with zero serialization cost.

---

## 2. Memory Layout (Structure-of-Arrays)

Instead of a slice of rows:

```go
type DomainRecord struct {
    Timestamp  int64
    TenantID   string
    MetricName string
    Value      float64
}
// Memory: [Timestamp, TenantID, MetricName, Value][Timestamp, TenantID, MetricName, Value]...
```

The Columnar Projection Lane defines a **RecordBatch** composed of contiguous vector arrays:

```text
+-----------------------+----------------------------------+
| Timestamp Vector      | [ int64, int64, int64, int64 ]   |
+-----------------------+----------------------------------+
| TenantID Vector       | [ offsets: 0, 8, 16, 24 ][ bytes ]|
+-----------------------+----------------------------------+
| Value Vector          | [ float64, float64, float64 ]    |
+-----------------------+----------------------------------+
| Validity Bitmap       | [ 11110111... ]                  | (for nullable values)
+-----------------------+----------------------------------+
```

---

## 3. Go Interface Draft

The following interfaces define the columnar API for Hermes:

```go
package hermes

import (
 "context"
)

// DataType represents the Arrow-compatible data type of a column.
type DataType int

const (
 TypeInt64 DataType = iota
 TypeFloat64
 TypeString
 TypeBinary
 TypeTimestamp
)

// Vector represents a contiguous memory slice for a single column.
type Vector interface {
 Type() DataType
 Len() int
 NullCount() int
 IsValid(i int) bool
 
 // Interface methods to access raw underlying slices (zero-copy)
 Int64Values() []int64
 Float64Values() []float64
 StringValues() []string
 BytesValues() [][]byte
}

// Column represents a named Vector.
type Column struct {
 Name string
 Data Vector
}

// RecordBatch groups columns representing a chunk of data.
type RecordBatch struct {
 Columns []Column
 Rows    int
}

// ColumnarProjector defines the mutator interface for projecting
// streaming events into columnar memory frames.
type ColumnarProjector interface {
 // ProjectBatch ingests streaming updates and appends/updates the columnar record batches.
 ProjectBatch(ctx context.Context, batch RecordBatch) error
}

// ColumnarQueryExecutor executes vectorized scan queries over partition lanes.
type ColumnarQueryExecutor interface {
 // ExecuteScan performs a vectorized query using the provided projection list and filter.
 ExecuteScan(ctx context.Context, selectFields []string, filter FilterExpr) (*RecordBatch, error)
}

// FilterExpr defines the AST structure for vectorized filters.
type FilterExpr interface {
 Evaluate(batch *RecordBatch) []bool // Returns a boolean mask vector
}
```

---

## 4. Vectorized SIMD Filtering Example

Using Go's contiguous slices, a vectorized filter loop performs operations with zero allocation:

```go
// SumFloat64Filtered sums values in a float64 column where the corresponding row in filterMask is true.
// The Go compiler optimizes this pattern into SIMD instructions (AVX-512 / ARM Neon) when loop unrolling and bounds checks are eliminated.
func SumFloat64Filtered(values []float64, filterMask []bool) float64 {
 if len(values) != len(filterMask) {
  return 0
 }
 
 var total float64
 // Bounds check elimination hint
 _ = values[len(values)-1]
 _ = filterMask[len(filterMask)-1]
 
 for i := 0; i < len(values); i++ {
  if filterMask[i] {
   total += values[i]
  }
 }
 return total
}
```

---

## 5. Integration with DuckDB & Arrow

To transfer this data to DuckDB or external analytical services:

1. **C Data Interface**: Hermes implements the Arrow C Data interface (`ArrowArray` and `ArrowSchema` structures).
2. **Pointer Swapping**: When DuckDB runs a query, it accesses these structures using direct memory pointers via cgo, eliminating serialization and memory allocation overhead.
