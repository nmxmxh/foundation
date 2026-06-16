package metadata

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
)

const (
	MaxTags           = 64
	MaxTagLength      = 96
	MaxCategories     = 32
	MaxCategoryLength = 64
)

var (
	metadataTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	metadataTagPattern   = regexp.MustCompile(`^[a-z0-9._:-]+$`)
	metadataTagSpace     = regexp.MustCompile(`\s+`)
)

var unsafeMetadataTagFragments = []string{
	"authorization",
	"bearer",
	"cookie",
	"jwt",
	"password",
	"private_key",
	"secret",
	"session_token",
	"token",
}

type GlobalContext struct {
	UserID         string `json:"user_id,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	Source         string `json:"source,omitempty"`
	DeviceID       string `json:"device_id,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	RoleID         string `json:"role_id,omitempty"`
	AuditContext   string `json:"audit_context,omitempty"`
	IPAddress      string `json:"ip_address,omitempty"`
	UserAgent      string `json:"user_agent,omitempty"`
}

type ValidityPeriod struct {
	EffectiveFrom string `json:"effective_from,omitempty"`
	EffectiveTo   string `json:"effective_to,omitempty"`
}

type EnvelopeMetadata struct {
	GlobalContext     *GlobalContext    `json:"global_context,omitempty"`
	Tags              []string          `json:"tags,omitempty"`
	AIConfidence      float64           `json:"ai_confidence,omitempty"`
	EmbeddingID       string            `json:"embedding_id,omitempty"`
	Categories        []string          `json:"categories,omitempty"`
	KnowledgeGraph    string            `json:"knowledge_graph,omitempty"`
	SourceRef         string            `json:"source_ref,omitempty"`
	ValidityPeriod    *ValidityPeriod   `json:"validity_period,omitempty"`
	GamificationState string            `json:"gamification_state,omitempty"`
	CorrelationID     string            `json:"correlation_id,omitempty"`
	CausationID       string            `json:"causation_id,omitempty"`
	RequestID         string            `json:"request_id,omitempty"`
	IdempotencyKey    string            `json:"idempotency_key,omitempty"`
	TraceID           string            `json:"trace_id,omitempty"`
	SpanID            string            `json:"span_id,omitempty"`
	Channel           string            `json:"channel,omitempty"`
	Locale            string            `json:"locale,omitempty"`
	TenantRegion      string            `json:"tenant_region,omitempty"`
	Attributes        map[string]string `json:"attributes,omitempty"`
	Extras            extension.Object  `json:"extras,omitempty"`
}

type contextKey string

const metadataKey contextKey = "ovasabi_server_kit_metadata"

func New() EnvelopeMetadata {
	return EnvelopeMetadata{
		Tags:       []string{},
		Categories: []string{},
		Attributes: map[string]string{},
		Extras:     extension.Object{},
	}
}

func NewCorrelationID() string {
	now := time.Now().UTC().Format("20060102T150405.000000000")
	var random [8]byte
	if _, err := rand.Read(random[:]); err == nil {
		var encoded [16]byte
		hex.Encode(encoded[:], random[:])
		return "corr_" + now + "_" + string(encoded[:])
	}
	return "corr_" + now
}

func (m *EnvelopeMetadata) EnsureCorrelation(candidates ...string) string {
	if m == nil {
		for _, candidate := range candidates {
			if corr := strings.TrimSpace(candidate); corr != "" {
				return corr
			}
		}
		return NewCorrelationID()
	}
	for _, candidate := range candidates {
		if corr := strings.TrimSpace(candidate); corr != "" {
			m.CorrelationID = corr
			if strings.TrimSpace(m.RequestID) == "" {
				m.RequestID = corr
			}
			return corr
		}
	}
	if corr := strings.TrimSpace(m.CorrelationID); corr != "" {
		m.CorrelationID = corr
		if strings.TrimSpace(m.RequestID) == "" {
			m.RequestID = corr
		}
		return corr
	}
	m.CorrelationID = NewCorrelationID()
	if strings.TrimSpace(m.RequestID) == "" {
		m.RequestID = m.CorrelationID
	}
	return m.CorrelationID
}

func IntoContext(ctx context.Context, md EnvelopeMetadata) context.Context {
	return context.WithValue(ctx, metadataKey, md)
}

func FromContext(ctx context.Context) EnvelopeMetadata {
	value, ok := FromContextOK(ctx)
	if !ok {
		return New()
	}
	return value
}

func FromContextOK(ctx context.Context) (EnvelopeMetadata, bool) {
	if ctx == nil {
		return EnvelopeMetadata{}, false
	}
	value, ok := ctx.Value(metadataKey).(EnvelopeMetadata)
	if !ok {
		return EnvelopeMetadata{}, false
	}
	if value.Attributes == nil {
		value.Attributes = map[string]string{}
	}
	if value.Extras == nil {
		value.Extras = extension.Object{}
	}
	return value, true
}

func NewContext(ctx context.Context, raw map[string]any) context.Context {
	return IntoContext(ctx, FromMap(raw))
}

func NewContextObject(ctx context.Context, raw extension.Object) context.Context {
	return IntoContext(ctx, FromObject(raw))
}

func FromContextMap(ctx context.Context, overlays ...map[string]any) map[string]any {
	base := FromContext(ctx).ToObject()
	typedOverlays := make([]extension.Object, 0, len(overlays))
	for _, overlay := range overlays {
		typedOverlays = append(typedOverlays, objectFromRawMap(overlay))
	}
	return MergeObjects(base, typedOverlays...).InterfaceMap()
}

func FromContextObject(ctx context.Context, overlays ...extension.Object) extension.Object {
	base := FromContext(ctx).ToObject()
	return MergeObjects(base, overlays...)
}

func MergeMaps(base map[string]any, overlays ...map[string]any) map[string]any {
	typedOverlays := make([]extension.Object, 0, len(overlays))
	for _, overlay := range overlays {
		typedOverlays = append(typedOverlays, objectFromRawMap(overlay))
	}
	return MergeObjects(objectFromRawMap(base), typedOverlays...).InterfaceMap()
}

func MergeObjects(base extension.Object, overlays ...extension.Object) extension.Object {
	merged := base.Clone()
	for _, overlay := range overlays {
		for key, value := range overlay {
			if str, ok := value.StringValue(); ok && strings.TrimSpace(str) == "" {
				continue
			}
			if isMergeableStringSliceField(key) {
				merged[key] = extensionListFromStrings(mergeStringSliceValues(key, merged[key], value))
				continue
			}
			if key == "global_context" || key == "globalContext" {
				merged[key] = extension.ObjectValue(MergeObjects(objectFromValue(merged[key]), objectFromValue(value)))
				continue
			}
			merged[key] = value.Clone()
		}
	}
	return FromObject(merged).ToObject()
}

func BuildTag(namespace, value string) (string, bool) {
	namespace = normalizeTagPart(namespace, 24)
	value = normalizeTagPart(value, MaxTagLength-len(namespace)-1)
	if namespace == "" || value == "" {
		return "", false
	}
	return NormalizeTag(namespace + ":" + value)
}

func NormalizeTag(tag string) (string, bool) {
	normalized := normalizeTagPart(tag, MaxTagLength)
	if normalized == "" || !metadataTagPattern.MatchString(normalized) || containsUnsafeTagFragment(normalized) {
		return "", false
	}
	return normalized, true
}

func NormalizeTags(tags []string) []string {
	return normalizeStringSet(tags, MaxTags, MaxTagLength, true)
}

func NormalizeCategories(categories []string) []string {
	return normalizeStringSet(categories, MaxCategories, MaxCategoryLength, false)
}

func FromMap(raw map[string]any) EnvelopeMetadata {
	return FromObject(objectFromRawMap(raw))
}

func FromObject(raw extension.Object) EnvelopeMetadata {
	md := New()
	if raw == nil {
		return md
	}

	if gc := pickObject(raw, "global_context", "globalContext"); gc != nil {
		metaGC := &GlobalContext{
			UserID:         pickObjectString(gc, "user_id", "userId"),
			SessionID:      pickObjectString(gc, "session_id", "sessionId"),
			Source:         pickObjectString(gc, "source"),
			DeviceID:       pickObjectString(gc, "device_id", "deviceId"),
			OrganizationID: pickObjectString(gc, "organization_id", "organizationId"),
			RoleID:         pickObjectString(gc, "role_id", "roleId"),
			AuditContext:   pickObjectString(gc, "audit_context", "auditContext"),
			IPAddress:      pickObjectString(gc, "ip_address", "ipAddress"),
			UserAgent:      pickObjectString(gc, "user_agent", "userAgent"),
		}
		if !isGlobalContextEmpty(metaGC) {
			md.GlobalContext = metaGC
		}
	}

	md.Tags = NormalizeTags(parseStringSliceValue(raw["tags"]))
	md.Categories = NormalizeCategories(parseStringSliceValue(raw["categories"]))
	md.AIConfidence = pickObjectFloat64(raw, "ai_confidence", "aiConfidence")
	md.EmbeddingID = pickObjectString(raw, "embedding_id", "embeddingId")
	md.KnowledgeGraph = pickObjectString(raw, "knowledge_graph", "knowledgeGraph")
	md.SourceRef = pickObjectString(raw, "source_ref", "sourceRef")
	md.GamificationState = pickObjectString(raw, "gamification_state", "gamificationState")
	md.CorrelationID = pickObjectString(raw, "correlation_id", "correlationId")
	md.CausationID = pickObjectString(raw, "causation_id", "causationId")
	md.RequestID = pickObjectString(raw, "request_id", "requestId")
	md.IdempotencyKey = pickObjectString(raw, "idempotency_key", "idempotencyKey")
	md.TraceID = pickObjectString(raw, "trace_id", "traceId")
	md.SpanID = pickObjectString(raw, "span_id", "spanId")
	md.Channel = pickObjectString(raw, "channel")
	md.Locale = pickObjectString(raw, "locale")
	md.TenantRegion = pickObjectString(raw, "tenant_region", "tenantRegion")

	if vp := pickObject(raw, "validity_period", "validityPeriod"); vp != nil {
		period := &ValidityPeriod{
			EffectiveFrom: pickObjectString(vp, "effective_from", "effectiveFrom"),
			EffectiveTo:   pickObjectString(vp, "effective_to", "effectiveTo"),
		}
		if period.EffectiveFrom != "" || period.EffectiveTo != "" {
			md.ValidityPeriod = period
		}
	}

	if attrs := pickObject(raw, "attributes"); attrs != nil {
		md.Attributes = map[string]string{}
		for key, value := range attrs {
			if str, ok := value.StringValue(); ok {
				md.Attributes[key] = str
			} else {
				md.Attributes[key] = fmt.Sprintf("%v", value.Interface())
			}
		}
	}

	for key, value := range raw {
		if isKnownField(key) {
			continue
		}
		md.Extras[key] = value.Clone()
	}
	return md
}

func isMergeableStringSliceField(key string) bool {
	return key == "tags" || key == "categories"
}

func normalizeStringSet(values []string, limit, maxLength int, rejectSecrets bool) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, min(len(values), limit))
	for _, value := range values {
		item := normalizeTagPart(value, maxLength)
		if item == "" || !metadataTagPattern.MatchString(item) {
			continue
		}
		if rejectSecrets && containsUnsafeTagFragment(item) {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
		if len(normalized) >= limit {
			break
		}
	}
	sort.Strings(normalized)
	return normalized
}

func normalizeTagPart(value string, maxLength int) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = metadataTagSpace.ReplaceAllString(normalized, "_")
	normalized = strings.Trim(normalized, "._:-")
	if normalized == "" {
		return ""
	}
	if len(normalized) > maxLength {
		normalized = strings.Trim(normalized[:maxLength], "._:-")
	}
	return normalized
}

func containsUnsafeTagFragment(value string) bool {
	for _, fragment := range unsafeMetadataTagFragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func mergeStringSliceValues(key string, values ...extension.Value) []string {
	merged := make([]string, 0)
	for _, value := range values {
		merged = append(merged, parseStringSliceValue(value)...)
	}
	if key == "categories" {
		return NormalizeCategories(merged)
	}
	return NormalizeTags(merged)
}

func extensionListFromStrings(values []string) extension.Value {
	list := make([]extension.Value, 0, len(values))
	for _, value := range values {
		list = append(list, extension.String(value))
	}
	return extension.List(list)
}

func objectFromValue(value extension.Value) extension.Object {
	object, ok := value.ObjectValue()
	if !ok {
		return nil
	}
	return object
}

func objectFromRawMap(raw map[string]any) extension.Object {
	if len(raw) == 0 {
		return extension.Object{}
	}
	value, err := extension.FromJSON(raw)
	if err != nil {
		return extension.Object{}
	}
	object, ok := value.ObjectValue()
	if !ok {
		return extension.Object{}
	}
	return object
}

func (m EnvelopeMetadata) ToMap() map[string]any {
	return m.ToObject().InterfaceMap()
}

func (m EnvelopeMetadata) ToObject() extension.Object {
	result := extension.Object{}
	m.appendGlobalContextObject(result)
	m.appendCollectionsObject(result)
	m.appendScalarFieldsObject(result)
	m.appendValidityPeriodObject(result)
	m.appendAttributesObject(result)
	for key, value := range m.Extras {
		if _, exists := result[key]; !exists {
			result[key] = value.Clone()
		}
	}
	return result
}

// PrepareForEmit restores routing fields from request context before emission.
func PrepareForEmit(ctx context.Context, raw map[string]any) map[string]any {
	return PrepareObjectForEmit(ctx, objectFromRawMap(raw)).InterfaceMap()
}

func PrepareObjectForEmit(ctx context.Context, raw extension.Object) extension.Object {
	md := FromContext(ctx)
	emitted := MergeObjects(md.ToObject(), raw)

	if corr, ok := emitted.GetString("correlation_id"); !ok || corr == "" {
		if corr := correlationFromGlobalContextObject(raw); corr != "" {
			emitted["correlation_id"] = extension.String(corr)
		}
	}
	return emitted
}

func correlationFromGlobalContextObject(meta extension.Object) string {
	gc := objectFromValue(meta["global_context"])
	if gc == nil {
		return ""
	}
	corr, _ := gc.GetString("correlation_id")
	return corr
}

func (m EnvelopeMetadata) appendGlobalContextObject(result extension.Object) {
	if m.GlobalContext == nil {
		return
	}
	result["global_context"] = extension.ObjectValue(extension.Object{
		"user_id":         extension.String(m.GlobalContext.UserID),
		"session_id":      extension.String(m.GlobalContext.SessionID),
		"source":          extension.String(m.GlobalContext.Source),
		"device_id":       extension.String(m.GlobalContext.DeviceID),
		"organization_id": extension.String(m.GlobalContext.OrganizationID),
		"role_id":         extension.String(m.GlobalContext.RoleID),
		"audit_context":   extension.String(m.GlobalContext.AuditContext),
		"ip_address":      extension.String(m.GlobalContext.IPAddress),
		"user_agent":      extension.String(m.GlobalContext.UserAgent),
	})
}

func (m EnvelopeMetadata) appendCollectionsObject(result extension.Object) {
	if len(m.Tags) > 0 {
		result["tags"] = extensionListFromStrings(m.Tags)
	}
	if len(m.Categories) > 0 {
		result["categories"] = extensionListFromStrings(m.Categories)
	}
}

func (m EnvelopeMetadata) appendScalarFieldsObject(result extension.Object) {
	setObjectString(result, "embedding_id", m.EmbeddingID)
	setObjectString(result, "knowledge_graph", m.KnowledgeGraph)
	setObjectString(result, "source_ref", m.SourceRef)
	setObjectString(result, "gamification_state", m.GamificationState)
	setObjectString(result, "correlation_id", m.CorrelationID)
	setObjectString(result, "causation_id", m.CausationID)
	setObjectString(result, "request_id", m.RequestID)
	setObjectString(result, "idempotency_key", m.IdempotencyKey)
	setObjectString(result, "trace_id", m.TraceID)
	setObjectString(result, "span_id", m.SpanID)
	setObjectString(result, "channel", m.Channel)
	setObjectString(result, "locale", m.Locale)
	setObjectString(result, "tenant_region", m.TenantRegion)
	if m.AIConfidence != 0 {
		result["ai_confidence"] = extension.Float(m.AIConfidence)
	}
}

func (m EnvelopeMetadata) appendValidityPeriodObject(result extension.Object) {
	if m.ValidityPeriod == nil {
		return
	}
	result["validity_period"] = extension.ObjectValue(extension.Object{
		"effective_from": extension.String(m.ValidityPeriod.EffectiveFrom),
		"effective_to":   extension.String(m.ValidityPeriod.EffectiveTo),
	})
}

func (m EnvelopeMetadata) appendAttributesObject(result extension.Object) {
	if len(m.Attributes) == 0 {
		return
	}
	attrs := make(extension.Object, len(m.Attributes))
	for key, value := range m.Attributes {
		attrs[key] = extension.String(value)
	}
	result["attributes"] = extension.ObjectValue(attrs)
}

func setObjectString(target extension.Object, key, value string) {
	if value != "" {
		target[key] = extension.String(value)
	}
}

func (m *EnvelopeMetadata) NormalizeCorrelation(correlationID string) string {
	if m == nil {
		return strings.TrimSpace(correlationID)
	}

	corr := strings.TrimSpace(correlationID)
	if corr == "" {
		corr = strings.TrimSpace(m.CorrelationID)
	}
	m.CorrelationID = corr
	return corr
}

func (m *EnvelopeMetadata) ApplyDefaults(channel string) {
	if m == nil {
		return
	}
	if m.Attributes == nil {
		m.Attributes = map[string]string{}
	}
	if m.Extras == nil {
		m.Extras = extension.Object{}
	}
	if strings.TrimSpace(m.Channel) == "" {
		m.Channel = strings.TrimSpace(channel)
	}
	if strings.TrimSpace(m.RequestID) == "" {
		m.RequestID = strings.TrimSpace(m.CorrelationID)
	}
}

func (m EnvelopeMetadata) Validate() error {
	if m.AIConfidence < 0 || m.AIConfidence > 1 {
		return errors.New("metadata.ai_confidence must be between 0 and 1")
	}

	if err := validateToken("correlation_id", m.CorrelationID); err != nil {
		return err
	}
	if err := validateToken("causation_id", m.CausationID); err != nil {
		return err
	}
	if err := validateToken("request_id", m.RequestID); err != nil {
		return err
	}
	if err := validateToken("idempotency_key", m.IdempotencyKey); err != nil {
		return err
	}
	if err := validateToken("trace_id", m.TraceID); err != nil {
		return err
	}
	if err := validateToken("span_id", m.SpanID); err != nil {
		return err
	}

	if m.ValidityPeriod != nil {
		from, fromErr := parseISODateOrTime(m.ValidityPeriod.EffectiveFrom)
		to, toErr := parseISODateOrTime(m.ValidityPeriod.EffectiveTo)

		if fromErr != nil {
			return fmt.Errorf("metadata.validity_period.effective_from: %w", fromErr)
		}
		if toErr != nil {
			return fmt.Errorf("metadata.validity_period.effective_to: %w", toErr)
		}
		if !from.IsZero() && !to.IsZero() && to.Before(from) {
			return errors.New("metadata.validity_period.effective_to cannot be before effective_from")
		}
	}

	return nil
}

func (m EnvelopeMetadata) ToJSON() ([]byte, error) {
	return json.Marshal(m.ToObject())
}

func (m EnvelopeMetadata) ToTransportProto() (*foundationpb.Metadata, error) {
	pb := &foundationpb.Metadata{
		Tags:              append([]string(nil), m.Tags...),
		AiConfidence:      m.AIConfidence,
		EmbeddingId:       m.EmbeddingID,
		Categories:        append([]string(nil), m.Categories...),
		KnowledgeGraph:    m.KnowledgeGraph,
		SourceRef:         m.SourceRef,
		GamificationState: m.GamificationState,
		CorrelationId:     m.CorrelationID,
		CausationId:       m.CausationID,
		RequestId:         m.RequestID,
		IdempotencyKey:    m.IdempotencyKey,
		TraceId:           m.TraceID,
		SpanId:            m.SpanID,
		Channel:           m.Channel,
		Locale:            m.Locale,
		TenantRegion:      m.TenantRegion,
		Attributes:        copyStringMap(m.Attributes),
	}
	if m.GlobalContext != nil {
		pb.GlobalContext = &foundationpb.GlobalContext{
			UserId:         m.GlobalContext.UserID,
			SessionId:      m.GlobalContext.SessionID,
			Source:         m.GlobalContext.Source,
			DeviceId:       m.GlobalContext.DeviceID,
			OrganizationId: m.GlobalContext.OrganizationID,
			RoleId:         m.GlobalContext.RoleID,
			AuditContext:   m.GlobalContext.AuditContext,
			IpAddress:      m.GlobalContext.IPAddress,
			UserAgent:      m.GlobalContext.UserAgent,
		}
	}
	if m.ValidityPeriod != nil {
		pb.ValidityPeriod = &foundationpb.ValidityPeriod{
			EffectiveFrom: m.ValidityPeriod.EffectiveFrom,
			EffectiveTo:   m.ValidityPeriod.EffectiveTo,
		}
	}
	if len(m.Extras) > 0 {
		extrasJSON, err := json.Marshal(m.Extras)
		if err != nil {
			return nil, err
		}
		pb.ExtrasJson = extrasJSON
	}
	return pb, nil
}

func FromJSON(data []byte) (EnvelopeMetadata, error) {
	var raw extension.Object
	if err := json.Unmarshal(data, &raw); err != nil {
		return New(), err
	}
	return FromObject(raw), nil
}

func FromTransportProto(pb *foundationpb.Metadata) (EnvelopeMetadata, error) {
	md := New()
	if pb == nil {
		return md, nil
	}
	if pb.GlobalContext != nil {
		md.GlobalContext = &GlobalContext{
			UserID:         pb.GetGlobalContext().GetUserId(),
			SessionID:      pb.GetGlobalContext().GetSessionId(),
			Source:         pb.GetGlobalContext().GetSource(),
			DeviceID:       pb.GetGlobalContext().GetDeviceId(),
			OrganizationID: pb.GetGlobalContext().GetOrganizationId(),
			RoleID:         pb.GetGlobalContext().GetRoleId(),
			AuditContext:   pb.GetGlobalContext().GetAuditContext(),
			IPAddress:      pb.GetGlobalContext().GetIpAddress(),
			UserAgent:      pb.GetGlobalContext().GetUserAgent(),
		}
		if isGlobalContextEmpty(md.GlobalContext) {
			md.GlobalContext = nil
		}
	}
	md.Tags = append([]string(nil), pb.GetTags()...)
	md.AIConfidence = pb.GetAiConfidence()
	md.EmbeddingID = pb.GetEmbeddingId()
	md.Categories = append([]string(nil), pb.GetCategories()...)
	md.KnowledgeGraph = pb.GetKnowledgeGraph()
	md.SourceRef = pb.GetSourceRef()
	if pb.GetValidityPeriod() != nil {
		md.ValidityPeriod = &ValidityPeriod{
			EffectiveFrom: pb.GetValidityPeriod().GetEffectiveFrom(),
			EffectiveTo:   pb.GetValidityPeriod().GetEffectiveTo(),
		}
	}
	md.GamificationState = pb.GetGamificationState()
	md.CorrelationID = pb.GetCorrelationId()
	md.CausationID = pb.GetCausationId()
	md.RequestID = pb.GetRequestId()
	md.IdempotencyKey = pb.GetIdempotencyKey()
	md.TraceID = pb.GetTraceId()
	md.SpanID = pb.GetSpanId()
	md.Channel = pb.GetChannel()
	md.Locale = pb.GetLocale()
	md.TenantRegion = pb.GetTenantRegion()
	md.Attributes = copyStringMap(pb.GetAttributes())
	if len(pb.GetExtrasJson()) > 0 {
		if err := json.Unmarshal(pb.GetExtrasJson(), &md.Extras); err != nil {
			return New(), err
		}
	}
	return md, nil
}

func parseStringSliceValue(value extension.Value) []string {
	list, ok := value.ListValue()
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		str, ok := item.StringValue()
		if ok && strings.TrimSpace(str) != "" {
			out = append(out, str)
		}
	}
	return out
}

func pickObject(values extension.Object, keys ...string) extension.Object {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		if object := objectFromValue(raw); object != nil {
			return object
		}
	}
	return nil
}

func pickObjectString(values extension.Object, keys ...string) string {
	for _, key := range keys {
		if raw, ok := values[key]; ok {
			if str, ok := raw.StringValue(); ok {
				return str
			}
		}
	}
	return ""
}

func pickObjectFloat64(values extension.Object, keys ...string) float64 {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		if value, ok := raw.FloatValue(); ok {
			return value
		}
		if value, ok := raw.IntValue(); ok {
			return float64(value)
		}
		if value, ok := raw.UintValue(); ok {
			return float64(value)
		}
	}
	return 0
}

func validateToken(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if !metadataTokenPattern.MatchString(value) {
		return fmt.Errorf("metadata.%s has invalid format", name)
	}
	return nil
}

func parseISODateOrTime(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed, nil
	}
	if parsed, err := time.Parse("2006-01-02", trimmed); err == nil {
		return parsed, nil
	}
	return time.Time{}, errors.New("must be RFC3339 or YYYY-MM-DD")
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(values))
	maps.Copy(out, values)
	return out
}

func isKnownField(key string) bool {
	switch key {
	case "global_context", "globalContext",
		"tags",
		"ai_confidence", "aiConfidence",
		"embedding_id", "embeddingId",
		"categories",
		"knowledge_graph", "knowledgeGraph",
		"source_ref", "sourceRef",
		"validity_period", "validityPeriod",
		"gamification_state", "gamificationState",
		"correlation_id", "correlationId",
		"causation_id", "causationId",
		"request_id", "requestId",
		"idempotency_key", "idempotencyKey",
		"trace_id", "traceId",
		"span_id", "spanId",
		"channel",
		"locale",
		"tenant_region", "tenantRegion",
		"attributes":
		return true
	default:
		return false
	}
}

func isGlobalContextEmpty(gc *GlobalContext) bool {
	if gc == nil {
		return true
	}
	return gc.UserID == "" &&
		gc.SessionID == "" &&
		gc.Source == "" &&
		gc.DeviceID == "" &&
		gc.OrganizationID == "" &&
		gc.RoleID == "" &&
		gc.AuditContext == "" &&
		gc.IPAddress == "" &&
		gc.UserAgent == ""
}
