package intelligence

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

func TestExtractBuildsGraphSignalAndSparseVector(t *testing.T) {
	input := Input{
		EventType: "brand:create:v1:requested",
		Payload: extension.Object{
			"brand_profile_id": extension.String("brand_123"),
			"name":             extension.String("Ovasabi Studio research memo"),
		},
		Metadata: metadata.FromMap(map[string]any{
			"global_context": map[string]any{
				"user_id":         "user_42",
				"organization_id": "org_9",
			},
			"tags":       []string{"security:jwt", "intent:research"},
			"categories": []string{"Content"},
		}).ToObject(),
	}

	signal := Extract(input, 12, 32)
	if signal.Domain != "brand" || signal.Action != "create" || signal.Version != "v1" {
		t.Fatalf("unexpected event decomposition: %+v", signal)
	}
	if signal.KnowledgeGraph != "brand.intelligence" || signal.SourceRef != "event:brand:create:v1:requested" {
		t.Fatalf("unexpected graph provenance: %+v", signal)
	}
	if len(signal.Actors) != 2 || len(signal.Entities) != 1 || len(signal.Edges) != 2 {
		t.Fatalf("unexpected graph facts: actors=%+v entities=%+v edges=%+v", signal.Actors, signal.Entities, signal.Edges)
	}
	if len(signal.Relevance) != 32 {
		t.Fatalf("vector dims = %d, want 32", len(signal.Relevance))
	}
	if hasString(signal.Tags, "security:jwt") {
		t.Fatalf("unsafe tag survived normalization: %+v", signal.Tags)
	}
	if !hasString(signal.Tags, "domain:brand") || !hasString(signal.Tags, "entity:brand_profile") || !hasString(signal.Tags, "intent:research") {
		t.Fatalf("missing expected tags: %+v", signal.Tags)
	}
}

func TestInjectorMergesMetadataAndObservesSignal(t *testing.T) {
	var observed Signal
	injector := NewInjector(ObserverFunc(func(_ context.Context, signal Signal) {
		observed = signal
	}))

	ctx, merged, signal := injector.Inject(context.Background(), Input{
		EventType: "document:index:v1:requested",
		Payload: extension.Object{
			"document_ref": extension.String("doc_1"),
			"title":        extension.String("Risk intelligence"),
		},
		Metadata: metadata.FromMap(map[string]any{"tags": []string{"source:api"}}).ToObject(),
	})
	if observed.EventType != signal.EventType {
		t.Fatalf("observer did not receive signal: %+v", observed)
	}
	if _, ok := FromContext(ctx); !ok {
		t.Fatalf("signal was not injected into context")
	}
	md := metadata.FromObject(merged)
	if md.KnowledgeGraph != "document.intelligence" {
		t.Fatalf("knowledge graph not merged: %+v", merged)
	}
	if !hasString(md.Tags, "source:api") || !hasString(md.Tags, "domain:document") {
		t.Fatalf("tags were not merged: %+v", md.Tags)
	}
	if md.Attributes["intelligence_vector"] != "sparse-hash-v1" {
		t.Fatalf("vector marker missing from attributes: %+v", md.Attributes)
	}
}

func TestAsyncObserverNeverBlocksCommandFlow(t *testing.T) {
	release := make(chan struct{})
	called := make(chan struct{}, 1)
	async := NewAsyncObserver(ObserverFunc(func(context.Context, Signal) {
		called <- struct{}{}
		<-release
	}), 1)
	defer async.Close()
	defer close(release)

	async.ObserveIntelligence(context.Background(), Signal{EventType: "first:event:v1:requested"})
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatalf("observer worker did not receive first signal")
	}

	start := time.Now()
	for range 256 {
		async.ObserveIntelligence(context.Background(), Signal{EventType: "overflow:event:v1:requested"})
	}
	if elapsed := time.Since(start); elapsed > 25*time.Millisecond {
		t.Fatalf("async observer blocked under pressure: %s", elapsed)
	}
	if async.Dropped() == 0 {
		t.Fatalf("expected pressure drops when queue is full")
	}
}

func hasString(values []string, want string) bool {
	return slices.Contains(values, want)
}
