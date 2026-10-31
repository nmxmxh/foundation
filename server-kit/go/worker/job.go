package worker

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
	"github.com/tidwall/gjson"
)

// Job is the transport shape used by the worker engine.
// IdempotencyKey carries river:"unique", so River deduplicates on the key and kind only.
// Without the tag, River hashes all args (created_at, attempt), and no two inserts ever match.
type Job struct {
	ID              string             `json:"id"`
	JobKind         string             `json:"kind"`
	Queue           string             `json:"queue"`
	Payload         extension.Object   `json:"payload,omitempty"`
	RawPayload      []byte             `json:"raw_payload,omitempty"`
	Metadata        extension.Object   `json:"metadata"`
	CorrelationID   string             `json:"correlation_id"`
	IdempotencyKey  string             `json:"idempotency_key" river:"unique"`
	Attempt         int                `json:"attempt"`
	MaxAttempts     int                `json:"max_attempts"`
	ScheduledAt     time.Time          `json:"scheduled_at,omitzero"`
	CreatedAt       time.Time          `json:"created_at,omitzero"`
	ExecutionPolicy JobExecutionPolicy `json:"execution_policy,omitzero"`
	RequireSidecar  bool               `json:"require_sidecar,omitempty"`
}

// MaxInlinePayloadBytes defines the threshold (64 KiB) below which RawPayload
// is inlined directly in the River job args, avoiding the secondary river_job_metadata query.
const MaxInlinePayloadBytes = 64 * 1024

// Kind implements river.JobArgs.
func (j Job) Kind() string {
	return j.JobKind
}

type JobExecutionPolicy struct {
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

const DefaultJobTimeoutMS = 30000

func (j *Job) Normalize() {
	if j == nil {
		return
	}
	if strings.TrimSpace(j.Queue) == "" {
		j.Queue = "default"
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 3
	}
	if j.Attempt < 0 {
		j.Attempt = 0
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	if j.ExecutionPolicy.TimeoutMS <= 0 {
		j.ExecutionPolicy.TimeoutMS = positiveIntFromMetadata(j.Metadata, "timeout_ms", "timeoutMs", "job_timeout_ms", "jobTimeoutMs")
	}
	if j.ExecutionPolicy.TimeoutMS <= 0 {
		j.ExecutionPolicy.TimeoutMS = DefaultJobTimeoutMS
	}
}

func (j Job) Validate() error {
	if strings.TrimSpace(j.JobKind) == "" {
		return errors.New("job.kind is required")
	}
	if strings.TrimSpace(j.Queue) == "" {
		return errors.New("job.queue is required")
	}
	if j.MaxAttempts <= 0 {
		return errors.New("job.max_attempts must be > 0")
	}
	if j.Attempt > j.MaxAttempts {
		return fmt.Errorf("job.attempt %d exceeds max_attempts %d", j.Attempt, j.MaxAttempts)
	}
	return nil
}

func (j Job) NextBackoff() time.Duration {
	base := 250 * time.Millisecond
	max := 30 * time.Second
	backoff := base
	for attempt := 1; attempt < j.Attempt && backoff < max; attempt++ {
		backoff *= 2
		if backoff > max {
			backoff = max
		}
	}
	if backoff > max {
		backoff = max
	}
	// Add ±25% jitter
	// #nosec G404 -- retry jitter is load spreading, not a security token or randomness boundary.
	jitter := time.Duration(rand.Int63n(int64(backoff/2))) - backoff/4
	return backoff + jitter
}

func (j Job) Timeout() time.Duration {
	timeoutMS := j.ExecutionPolicy.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = DefaultJobTimeoutMS
	}
	return time.Duration(timeoutMS) * time.Millisecond
}

func (j Job) HealthKey() string {
	if trimmed := strings.TrimSpace(j.ID); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(j.CorrelationID); trimmed != "" {
		return "corr:" + trimmed
	}
	if trimmed := strings.TrimSpace(j.IdempotencyKey); trimmed != "" {
		return "idem:" + trimmed
	}
	return fmt.Sprintf("%s:%s:%s", strings.TrimSpace(j.Queue), strings.TrimSpace(j.JobKind), j.CreatedAt.UTC().Format(time.RFC3339Nano))
}

func positiveIntFromMetadata(metadata extension.Object, keys ...string) int {
	for _, key := range keys {
		value, ok := metadata[key]
		if !ok {
			continue
		}
		if typed, ok := value.IntValue(); ok {
			if typed > 0 && typed <= int64(math.MaxInt) {
				return int(typed)
			}
		}
		if typed, ok := value.UintValue(); ok {
			if typed > 0 && typed <= uint64(math.MaxInt) {
				return int(typed)
			}
		}
		if typed, ok := value.FloatValue(); ok {
			if typed > 0 && typed <= float64(math.MaxInt) {
				return int(typed)
			}
		}
	}
	return 0
}

// isRawJSON reports whether b is one complete JSON object or array.
// A bracket check alone is not enough: `{"a":1},"kind":"x"` would inject top-level job fields.
// NOTE: Postgres jsonb normalises inlined JSON. Callers that need exact bytes must not send JSON.
func isRawJSON(b []byte) bool {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) < 2 {
		return false
	}
	first := trimmed[0]
	last := trimmed[len(trimmed)-1]
	if (first != '{' || last != '}') && (first != '[' || last != ']') {
		return false
	}
	return json.Valid(trimmed)
}

var jobBufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 512)
		return &b
	},
}

// MarshalJSON serializes Job into JSON without reflection.
func (j Job) MarshalJSON() ([]byte, error) {
	return j.MarshalJSONWithArena(nil)
}

// MarshalJSONWithArena serializes Job into JSON using scratch arena if available.
func (j Job) MarshalJSONWithArena(arena *scratch.Arena) ([]byte, error) {
	var buf []byte
	var poolBufPtr *[]byte
	if arena != nil {
		buf, _ = arena.Alloc(512)
		buf = buf[:0]
	} else {
		poolBufPtr = jobBufferPool.Get().(*[]byte)
		buf = (*poolBufPtr)[:0]
		defer func() {
			if cap(buf) <= 64*1024 {
				*poolBufPtr = buf
				jobBufferPool.Put(poolBufPtr)
			}
		}()
	}

	buf = append(buf, `{"id":`...)
	buf = appendJSONString(buf, j.ID)
	buf = append(buf, `,"kind":`...)
	buf = appendJSONString(buf, j.JobKind)
	buf = append(buf, `,"queue":`...)
	buf = appendJSONString(buf, j.Queue)

	if len(j.Payload) > 0 {
		buf = append(buf, `,"payload":`...)
		var payloadJSON []byte
		var err error
		if arena != nil {
			payloadJSON, err = j.Payload.MarshalJSONFastWithArena(arena)
		} else {
			payloadJSON, err = j.Payload.MarshalJSONFast()
		}
		if err != nil {
			return nil, err
		}
		buf = append(buf, payloadJSON...)
	}

	if len(j.RawPayload) > 0 {
		buf = append(buf, `,"raw_payload":`...)
		if isRawJSON(j.RawPayload) {
			buf = append(buf, j.RawPayload...)
		} else {
			buf = append(buf, '"')
			encLen := base64.StdEncoding.EncodedLen(len(j.RawPayload))
			start := len(buf)
			buf = append(buf, make([]byte, encLen)...)
			base64.StdEncoding.Encode(buf[start:], j.RawPayload)
			buf = append(buf, '"')
		}
	}

	buf = append(buf, `,"metadata":`...)
	if len(j.Metadata) > 0 {
		var metaJSON []byte
		var err error
		if arena != nil {
			metaJSON, err = j.Metadata.MarshalJSONFastWithArena(arena)
		} else {
			metaJSON, err = j.Metadata.MarshalJSONFast()
		}
		if err != nil {
			return nil, err
		}
		buf = append(buf, metaJSON...)
	} else {
		buf = append(buf, `{}`...)
	}

	buf = append(buf, `,"correlation_id":`...)
	buf = appendJSONString(buf, j.CorrelationID)
	buf = append(buf, `,"idempotency_key":`...)
	buf = appendJSONString(buf, j.IdempotencyKey)
	buf = append(buf, `,"attempt":`...)
	buf = strconv.AppendInt(buf, int64(j.Attempt), 10)
	buf = append(buf, `,"max_attempts":`...)
	buf = strconv.AppendInt(buf, int64(j.MaxAttempts), 10)

	if !j.ScheduledAt.IsZero() {
		buf = append(buf, `,"scheduled_at":"`...)
		buf = append(buf, j.ScheduledAt.UTC().Format(time.RFC3339Nano)...)
		buf = append(buf, '"')
	}

	if !j.CreatedAt.IsZero() {
		buf = append(buf, `,"created_at":"`...)
		buf = append(buf, j.CreatedAt.UTC().Format(time.RFC3339Nano)...)
		buf = append(buf, '"')
	}

	if j.ExecutionPolicy.TimeoutMS > 0 {
		buf = append(buf, `,"execution_policy":{"timeout_ms":`...)
		buf = strconv.AppendInt(buf, int64(j.ExecutionPolicy.TimeoutMS), 10)
		buf = append(buf, '}')
	}

	if j.RequireSidecar {
		buf = append(buf, `,"require_sidecar":true`...)
	}

	buf = append(buf, '}')
	if arena != nil {
		return buf, nil
	}
	res := make([]byte, len(buf))
	copy(res, buf)
	return res, nil
}

func appendJSONString(dst []byte, s string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if c < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
			} else {
				dst = append(dst, c)
			}
		}
	}
	dst = append(dst, '"')
	return dst
}

// UnmarshalJSON deserializes Job args using a single-pass zero-reflection scanner.
func (j *Job) UnmarshalJSON(data []byte) error {
	res := gjson.ParseBytes(data)
	if !res.IsObject() {
		return errors.New("job args must be a json object")
	}

	j.Metadata = extension.Object{}
	var parseErr error

	res.ForEach(func(key, val gjson.Result) bool {
		switch key.Str {
		case "id":
			j.ID = val.String()
		case "kind":
			j.JobKind = val.String()
		case "queue":
			j.Queue = val.String()
		case "correlation_id":
			j.CorrelationID = val.String()
		case "idempotency_key":
			j.IdempotencyKey = val.String()
		case "attempt":
			j.Attempt = int(val.Int())
		case "max_attempts":
			j.MaxAttempts = int(val.Int())
		case "require_sidecar":
			j.RequireSidecar = val.Bool()
		case "payload":
			if val.Raw != "" && val.Raw != "null" {
				if !val.IsObject() {
					parseErr = errors.New("payload must be a json object")
					return false
				}
				var obj extension.Object
				if err := obj.UnmarshalJSON([]byte(val.Raw)); err != nil {
					parseErr = err
					return false
				}
				j.Payload = obj
			}
		case "metadata":
			if val.Raw != "" && val.Raw != "null" {
				if !val.IsObject() {
					parseErr = errors.New("metadata must be a json object")
					return false
				}
				var obj extension.Object
				if err := obj.UnmarshalJSON([]byte(val.Raw)); err != nil {
					parseErr = err
					return false
				}
				j.Metadata = obj
			}
		case "raw_payload":
			switch val.Type {
			case gjson.String:
				if decoded, err := base64.StdEncoding.DecodeString(val.Str); err == nil {
					j.RawPayload = decoded
				} else {
					j.RawPayload = []byte(val.Str)
				}
			case gjson.JSON:
				j.RawPayload = []byte(val.Raw)
			}
		case "scheduled_at":
			if val.Str != "" {
				if t, err := time.Parse(time.RFC3339Nano, val.Str); err == nil {
					j.ScheduledAt = t
				}
			}
		case "created_at":
			if val.Str != "" {
				if t, err := time.Parse(time.RFC3339Nano, val.Str); err == nil {
					j.CreatedAt = t
				}
			}
		case "execution_policy":
			j.ExecutionPolicy.TimeoutMS = int(val.Get("timeout_ms").Int())
		}
		return true
	})

	if parseErr != nil {
		return parseErr
	}
	return nil
}
