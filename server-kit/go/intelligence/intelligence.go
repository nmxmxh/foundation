package intelligence

import (
	"context"
	"maps"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

const (
	DefaultKeywordLimit = 24
	DefaultVectorDims   = 64
	DefaultQueueSize    = 1024
	MaxVectorDims       = 256
)

var tokenPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9_:-]{1,63}`)

type contextKey string

const signalKey contextKey = "ovasabi_intelligence_signal"

type Reference struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

type Edge struct {
	From      Reference `json:"from"`
	Relation  string    `json:"relation"`
	To        Reference `json:"to"`
	Relevance float32   `json:"relevance"`
}

type Signal struct {
	EventType      string            `json:"event_type"`
	Domain         string            `json:"domain"`
	Action         string            `json:"action"`
	Version        string            `json:"version"`
	KnowledgeGraph string            `json:"knowledge_graph"`
	SourceRef      string            `json:"source_ref"`
	Tags           []string          `json:"tags"`
	Categories     []string          `json:"categories"`
	Keywords       []string          `json:"keywords"`
	Relevance      []float32         `json:"relevance"`
	Actors         []Reference       `json:"actors"`
	Entities       []Reference       `json:"entities"`
	Edges          []Edge            `json:"edges"`
	Attributes     map[string]string `json:"attributes"`
}

type Input struct {
	EventType    string
	Payload      map[string]any
	PayloadBytes []byte
	Metadata     map[string]any
}

type Observer interface {
	ObserveIntelligence(ctx context.Context, signal Signal)
}

type ObserverFunc func(context.Context, Signal)

func (f ObserverFunc) ObserveIntelligence(ctx context.Context, signal Signal) {
	if f != nil {
		f(ctx, signal)
	}
}

type AsyncObserver struct {
	observer Observer
	queue    chan Signal
	dropped  atomic.Uint64
	closed   atomic.Bool
	once     sync.Once
}

func NewAsyncObserver(observer Observer, queueSize int) *AsyncObserver {
	if queueSize <= 0 {
		queueSize = DefaultQueueSize
	}
	async := &AsyncObserver{
		observer: observer,
		queue:    make(chan Signal, queueSize),
	}
	go async.run()
	return async
}

func (o *AsyncObserver) ObserveIntelligence(_ context.Context, signal Signal) {
	if o == nil || o.observer == nil || o.closed.Load() {
		return
	}
	select {
	case o.queue <- signal:
	default:
		o.dropped.Add(1)
	}
}

func (o *AsyncObserver) Dropped() uint64 {
	if o == nil {
		return 0
	}
	return o.dropped.Load()
}

func (o *AsyncObserver) Close() {
	if o == nil {
		return
	}
	o.once.Do(func() {
		o.closed.Store(true)
		close(o.queue)
	})
}

type Injector struct {
	Observer     Observer
	KeywordLimit int
	VectorDims   int
}

func NewInjector(observer Observer) *Injector {
	return &Injector{Observer: observer, KeywordLimit: DefaultKeywordLimit, VectorDims: DefaultVectorDims}
}

func NewAsyncInjector(observer Observer, queueSize int) *Injector {
	return NewInjector(NewAsyncObserver(observer, queueSize))
}

func (i *Injector) Inject(ctx context.Context, input Input) (context.Context, map[string]any, Signal) {
	signal := Extract(input, i.keywordLimit(), i.vectorDims())
	patch := signal.MetadataPatch()
	merged := metadata.MergeMaps(input.Metadata, patch)
	ctx = metadata.NewContext(ctx, merged)
	ctx = IntoContext(ctx, signal)
	if i != nil && i.Observer != nil {
		i.Observer.ObserveIntelligence(ctx, signal)
	}
	return ctx, merged, signal
}

func (o *AsyncObserver) run() {
	for signal := range o.queue {
		o.observer.ObserveIntelligence(context.Background(), signal)
	}
}

func Extract(input Input, keywordLimit, vectorDims int) Signal {
	domain, action, version := splitEventType(input.EventType)
	md := metadata.FromMap(input.Metadata)
	keywords := collectKeywords(input.EventType, input.Payload, md, keywordLimit)
	actors := collectActors(md)
	entities := collectEntities(input.Payload)

	graph := strings.TrimSpace(md.KnowledgeGraph)
	if graph == "" && domain != "" {
		graph = domain + ".intelligence"
	}
	sourceRef := strings.TrimSpace(md.SourceRef)
	if sourceRef == "" && input.EventType != "" {
		sourceRef = "event:" + input.EventType
	}

	signal := Signal{
		EventType:      input.EventType,
		Domain:         domain,
		Action:         action,
		Version:        version,
		KnowledgeGraph: graph,
		SourceRef:      sourceRef,
		Tags:           signalTags(domain, action, actors, entities, md.Tags),
		Categories:     signalCategories(domain, md.Categories),
		Keywords:       keywords,
		Relevance:      sparseRelevanceVector(keywords, vectorDims),
		Actors:         actors,
		Entities:       entities,
		Attributes:     signalAttributes(domain, action, version, keywords, md.Attributes),
	}
	signal.Edges = collectEdges(actors, entities)
	return signal
}

func FromContext(ctx context.Context) (Signal, bool) {
	if ctx == nil {
		return Signal{}, false
	}
	signal, ok := ctx.Value(signalKey).(Signal)
	return signal, ok
}

func IntoContext(ctx context.Context, signal Signal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, signalKey, signal)
}

func (s Signal) MetadataPatch() map[string]any {
	patch := map[string]any{
		"tags":            s.Tags,
		"categories":      s.Categories,
		"knowledge_graph": s.KnowledgeGraph,
		"source_ref":      s.SourceRef,
	}
	attrs := map[string]string{}
	maps.Copy(attrs, s.Attributes)
	if len(s.Keywords) > 0 {
		attrs["intelligence_keywords"] = strings.Join(s.Keywords, ",")
	}
	if len(s.Relevance) > 0 {
		attrs["intelligence_vector"] = "sparse-hash-v1"
	}
	if len(attrs) > 0 {
		patch["attributes"] = attrs
	}
	return patch
}

func (i *Injector) keywordLimit() int {
	if i == nil || i.KeywordLimit <= 0 {
		return DefaultKeywordLimit
	}
	if i.KeywordLimit > 128 {
		return 128
	}
	return i.KeywordLimit
}

func (i *Injector) vectorDims() int {
	if i == nil || i.VectorDims <= 0 {
		return DefaultVectorDims
	}
	if i.VectorDims > MaxVectorDims {
		return MaxVectorDims
	}
	return i.VectorDims
}

func splitEventType(eventType string) (string, string, string) {
	parts := strings.Split(eventType, ":")
	if len(parts) < 3 {
		return "", "", ""
	}
	domain := normalizeToken(parts[0])
	action := normalizeToken(parts[1])
	version := normalizeToken(parts[2])
	return domain, action, version
}

func collectKeywords(eventType string, payload map[string]any, md metadata.EnvelopeMetadata, limit int) []string {
	counts := map[string]int{}
	addKeywordTokens(counts, eventType)
	for _, tag := range md.Tags {
		addKeywordTokens(counts, tag)
	}
	for _, category := range md.Categories {
		addKeywordTokens(counts, category)
	}
	for key, value := range md.Attributes {
		addKeywordTokens(counts, key)
		addKeywordTokens(counts, value)
	}
	collectPayloadKeywords(counts, payload, 0)
	return topKeywords(counts, limit)
}

func collectPayloadKeywords(counts map[string]int, payload map[string]any, depth int) {
	if depth > 2 || len(counts) > 512 {
		return
	}
	for key, value := range payload {
		addKeywordTokens(counts, key)
		switch typed := value.(type) {
		case string:
			addKeywordTokens(counts, typed)
		case map[string]any:
			collectPayloadKeywords(counts, typed, depth+1)
		case []any:
			for idx, item := range typed {
				if idx >= 8 {
					break
				}
				if nested, ok := item.(map[string]any); ok {
					collectPayloadKeywords(counts, nested, depth+1)
				}
				if str, ok := item.(string); ok {
					addKeywordTokens(counts, str)
				}
			}
		}
	}
}

func addKeywordTokens(counts map[string]int, value string) {
	for _, token := range tokenPattern.FindAllString(value, -1) {
		token = normalizeToken(token)
		if len(token) < 2 || containsNoisyToken(token) {
			continue
		}
		counts[token]++
	}
}

func topKeywords(counts map[string]int, limit int) []string {
	type candidate struct {
		token string
		count int
	}
	candidates := make([]candidate, 0, len(counts))
	for token, count := range counts {
		candidates = append(candidates, candidate{token: token, count: count})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].count == candidates[j].count {
			return candidates[i].token < candidates[j].token
		}
		return candidates[i].count > candidates[j].count
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.token)
	}
	return out
}

func collectActors(md metadata.EnvelopeMetadata) []Reference {
	refs := make([]Reference, 0, 2)
	if md.GlobalContext == nil {
		return refs
	}
	if ref := normalizeToken(md.GlobalContext.UserID); ref != "" {
		refs = append(refs, Reference{Kind: "user", Ref: ref})
	}
	if ref := normalizeToken(md.GlobalContext.OrganizationID); ref != "" {
		refs = append(refs, Reference{Kind: "organization", Ref: ref})
	}
	return refs
}

func collectEntities(payload map[string]any) []Reference {
	seen := map[string]struct{}{}
	refs := make([]Reference, 0, 4)
	for key, value := range payload {
		key = normalizeToken(key)
		if !(strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_ref") || strings.HasSuffix(key, "public_id")) {
			continue
		}
		ref, ok := value.(string)
		if !ok {
			continue
		}
		ref = normalizeToken(ref)
		if ref == "" {
			continue
		}
		kind := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(key, "_public_id"), "_id"), "_ref")
		id := kind + ":" + ref
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		refs = append(refs, Reference{Kind: kind, Ref: ref})
		if len(refs) >= 8 {
			break
		}
	}
	return refs
}

func collectEdges(actors, entities []Reference) []Edge {
	edges := make([]Edge, 0, len(actors)*len(entities))
	for _, actor := range actors {
		for _, entity := range entities {
			edges = append(edges, Edge{From: actor, Relation: "touched", To: entity, Relevance: 1})
		}
	}
	return edges
}

func signalTags(domain, action string, actors, entities []Reference, existing []string) []string {
	tags := append([]string(nil), existing...)
	if tag, ok := metadata.BuildTag("domain", domain); ok {
		tags = append(tags, tag)
	}
	if tag, ok := metadata.BuildTag("event", action); ok {
		tags = append(tags, tag)
	}
	for _, actor := range actors {
		if tag, ok := metadata.BuildTag("actor", actor.Kind); ok {
			tags = append(tags, tag)
		}
	}
	for _, entity := range entities {
		if tag, ok := metadata.BuildTag("entity", entity.Kind); ok {
			tags = append(tags, tag)
		}
	}
	return metadata.NormalizeTags(tags)
}

func signalCategories(domain string, existing []string) []string {
	categories := append([]string(nil), existing...)
	if domain != "" {
		categories = append(categories, domain)
	}
	return metadata.NormalizeCategories(categories)
}

func signalAttributes(domain, action, version string, keywords []string, existing map[string]string) map[string]string {
	attrs := map[string]string{}
	for key, value := range existing {
		key = normalizeToken(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			attrs[key] = value
		}
	}
	if domain != "" {
		attrs["intelligence_domain"] = domain
	}
	if action != "" {
		attrs["intelligence_action"] = action
	}
	if version != "" {
		attrs["intelligence_version"] = version
	}
	if len(keywords) > 0 {
		attrs["intelligence_keyword_count"] = intString(len(keywords))
	}
	return attrs
}

func sparseRelevanceVector(keywords []string, dims int) []float32 {
	if dims <= 0 {
		dims = DefaultVectorDims
	}
	vector := make([]float32, dims)
	var norm float64
	for rank, keyword := range keywords {
		weight := float32(1.0 / math.Sqrt(float64(rank+1)))
		idx := hashIndex(keyword, dims)
		vector[idx] += weight
		norm += float64(vector[idx] * vector[idx])
	}
	if norm == 0 {
		return vector
	}
	scale := float32(1 / math.Sqrt(norm))
	for idx := range vector {
		vector[idx] *= scale
	}
	return vector
}

func hashIndex(value string, dims int) int {
	if dims <= 0 {
		return 0
	}
	hash := 2166136261
	for i := 0; i < len(value); i++ {
		hash ^= int(value[i])
		hash *= 16777619
		hash %= dims
	}
	if hash < 0 {
		return -hash % dims
	}
	return hash % dims
}

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.Trim(value, "._:-")
	return value
}

func containsNoisyToken(value string) bool {
	switch value {
	case "metadata", "request", "response", "requested", "success", "failed", "true", "false", "null", "undefined":
		return true
	default:
		return false
	}
}

func intString(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
