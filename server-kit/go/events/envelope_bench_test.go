package events

import (
	"testing"
)

func BenchmarkEnvelope_ToJSON(b *testing.B) {
	env := makeTestEnvelope("media:upload:requested", "corr-123")
	env.Payload = ObjectFromMap(map[string]any{
		"id":      "file-123",
		"size":    1024 * 1024,
		"type":    "image/png",
		"tags":    []string{"vacation", "summer", "beach"},
		"version": 1,
	})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = env.ToJSON()
	}
}

func BenchmarkEnvelope_ToBinary(b *testing.B) {
	env := makeTestEnvelope("media:upload:requested", "corr-123")
	env.PayloadBytes = []byte(`{"id":"file-123","size":1048576,"type":"image/png","tags":["vacation","summer","beach"],"version":1}`)
	env.PayloadEncoding = PayloadEncodingProtobuf

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = env.ToBinary()
	}
}

func BenchmarkEnvelope_FromJSON(b *testing.B) {
	env := makeTestEnvelope("media:upload:requested", "corr-123")
	data, _ := env.ToJSON()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = FromJSON(data)
	}
}

func BenchmarkEnvelope_FromBinary(b *testing.B) {
	env := makeTestEnvelope("media:upload:requested", "corr-123")
	env.PayloadBytes = []byte(`{"id":"file-123"}`)
	env.PayloadEncoding = PayloadEncodingProtobuf
	data, _ := env.ToBinary()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = FromBinary(data)
	}
}
