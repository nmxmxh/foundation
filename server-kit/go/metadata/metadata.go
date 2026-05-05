package metadata

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	transportpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/transport/v1"
)

var metadataTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

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
	Extras            map[string]any    `json:"extras,omitempty"`
}

type contextKey string

const metadataKey contextKey = "ovasabi_server_kit_metadata"

func New() EnvelopeMetadata {
	return EnvelopeMetadata{
		Tags:       []string{},
		Categories: []string{},
		Attributes: map[string]string{},
		Extras:     map[string]any{},
	}
}

func NewCorrelationID() string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err == nil {
		return "corr_" + time.Now().UTC().Format("20060102T150405.000000000") + "_" + hex.EncodeToString(random[:])
	}
	return "corr_" + time.Now().UTC().Format("20060102T150405.000000000")
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
	if ctx == nil {
		return New()
	}
	value, ok := ctx.Value(metadataKey).(EnvelopeMetadata)
	if !ok {
		return New()
	}
	if value.Attributes == nil {
		value.Attributes = map[string]string{}
	}
	if value.Extras == nil {
		value.Extras = map[string]any{}
	}
	return value
}

func NewContext(ctx context.Context, raw map[string]any) context.Context {
	return IntoContext(ctx, FromMap(raw))
}

func FromMap(raw map[string]any) EnvelopeMetadata {
	md := New()
	if raw == nil {
		return md
	}

	if gc := pickMap(raw, "global_context", "globalContext"); gc != nil {
		metaGC := &GlobalContext{
			UserID:         pickString(gc, "user_id", "userId"),
			SessionID:      pickString(gc, "session_id", "sessionId"),
			Source:         pickString(gc, "source"),
			DeviceID:       pickString(gc, "device_id", "deviceId"),
			OrganizationID: pickString(gc, "organization_id", "organizationId"),
			RoleID:         pickString(gc, "role_id", "roleId"),
			AuditContext:   pickString(gc, "audit_context", "auditContext"),
			IPAddress:      pickString(gc, "ip_address", "ipAddress"),
			UserAgent:      pickString(gc, "user_agent", "userAgent"),
		}
		if !isGlobalContextEmpty(metaGC) {
			md.GlobalContext = metaGC
		}
	}

	md.Tags = parseStringSlice(raw["tags"])
	md.Categories = parseStringSlice(raw["categories"])
	md.AIConfidence = pickFloat64(raw, "ai_confidence", "aiConfidence")
	md.EmbeddingID = pickString(raw, "embedding_id", "embeddingId")
	md.KnowledgeGraph = pickString(raw, "knowledge_graph", "knowledgeGraph")
	md.SourceRef = pickString(raw, "source_ref", "sourceRef")
	md.GamificationState = pickString(raw, "gamification_state", "gamificationState")
	md.CorrelationID = pickString(raw, "correlation_id", "correlationId")
	md.CausationID = pickString(raw, "causation_id", "causationId")
	md.RequestID = pickString(raw, "request_id", "requestId")
	md.IdempotencyKey = pickString(raw, "idempotency_key", "idempotencyKey")
	md.TraceID = pickString(raw, "trace_id", "traceId")
	md.SpanID = pickString(raw, "span_id", "spanId")
	md.Channel = pickString(raw, "channel")
	md.Locale = pickString(raw, "locale")
	md.TenantRegion = pickString(raw, "tenant_region", "tenantRegion")

	if vp := pickMap(raw, "validity_period", "validityPeriod"); vp != nil {
		period := &ValidityPeriod{
			EffectiveFrom: pickString(vp, "effective_from", "effectiveFrom"),
			EffectiveTo:   pickString(vp, "effective_to", "effectiveTo"),
		}
		if period.EffectiveFrom != "" || period.EffectiveTo != "" {
			md.ValidityPeriod = period
		}
	}

	if attrs := pickMap(raw, "attributes"); attrs != nil {
		md.Attributes = map[string]string{}
		for key, value := range attrs {
			md.Attributes[key] = fmt.Sprintf("%v", value)
		}
	}

	for key, value := range raw {
		if isKnownField(key) {
			continue
		}
		md.Extras[key] = value
	}
	return md
}

func (m EnvelopeMetadata) ToMap() map[string]any {
	result := map[string]any{}

	m.appendGlobalContext(result)
	m.appendCollections(result)
	m.appendScalarFields(result)
	m.appendValidityPeriod(result)
	m.appendAttributes(result)
	for key, value := range m.Extras {
		if _, exists := result[key]; !exists {
			result[key] = value
		}
	}
	return result
}

func (m EnvelopeMetadata) appendGlobalContext(result map[string]any) {
	if m.GlobalContext == nil {
		return
	}
	result["global_context"] = map[string]any{
		"user_id":         m.GlobalContext.UserID,
		"session_id":      m.GlobalContext.SessionID,
		"source":          m.GlobalContext.Source,
		"device_id":       m.GlobalContext.DeviceID,
		"organization_id": m.GlobalContext.OrganizationID,
		"role_id":         m.GlobalContext.RoleID,
		"audit_context":   m.GlobalContext.AuditContext,
		"ip_address":      m.GlobalContext.IPAddress,
		"user_agent":      m.GlobalContext.UserAgent,
	}
}

func (m EnvelopeMetadata) appendCollections(result map[string]any) {
	if len(m.Tags) > 0 {
		result["tags"] = m.Tags
	}
	if len(m.Categories) > 0 {
		result["categories"] = m.Categories
	}
}

func (m EnvelopeMetadata) appendScalarFields(result map[string]any) {
	setIfNotZero(result, "ai_confidence", m.AIConfidence)
	setIfNotEmpty(result, "embedding_id", m.EmbeddingID)
	setIfNotEmpty(result, "knowledge_graph", m.KnowledgeGraph)
	setIfNotEmpty(result, "source_ref", m.SourceRef)
	setIfNotEmpty(result, "gamification_state", m.GamificationState)
	setIfNotEmpty(result, "correlation_id", m.CorrelationID)
	setIfNotEmpty(result, "causation_id", m.CausationID)
	setIfNotEmpty(result, "request_id", m.RequestID)
	setIfNotEmpty(result, "idempotency_key", m.IdempotencyKey)
	setIfNotEmpty(result, "trace_id", m.TraceID)
	setIfNotEmpty(result, "span_id", m.SpanID)
	setIfNotEmpty(result, "channel", m.Channel)
	setIfNotEmpty(result, "locale", m.Locale)
	setIfNotEmpty(result, "tenant_region", m.TenantRegion)
}

// PrepareForEmit restores routing fields from request context before emission.
func PrepareForEmit(ctx context.Context, raw map[string]any) map[string]any {
	md := FromContext(ctx)
	emitted := mergeMaps(md.ToMap(), raw)

	if emitted["correlation_id"] == nil || emitted["correlation_id"] == "" {
		if corr := correlationFromGlobalContext(emitted); corr != "" {
			emitted["correlation_id"] = corr
		}
	}
	return emitted
}

func mergeMaps(base, overrides map[string]any) map[string]any {
	result := map[string]any{}
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overrides {
		if str, ok := v.(string); ok && str == "" {
			continue
		}
		result[k] = v
	}
	return result
}

func correlationFromGlobalContext(meta map[string]any) string {
	gc, ok := meta["global_context"].(map[string]any)
	if !ok || gc == nil {
		return ""
	}

	if corr, ok := gc["correlation_id"].(string); ok && corr != "" {
		return corr
	}
	return ""
}

func (m EnvelopeMetadata) appendValidityPeriod(result map[string]any) {
	if m.ValidityPeriod == nil {
		return
	}
	result["validity_period"] = map[string]any{
		"effective_from": m.ValidityPeriod.EffectiveFrom,
		"effective_to":   m.ValidityPeriod.EffectiveTo,
	}
}

func (m EnvelopeMetadata) appendAttributes(result map[string]any) {
	if len(m.Attributes) > 0 {
		result["attributes"] = m.Attributes
	}
}

func setIfNotEmpty(target map[string]any, key, value string) {
	if value != "" {
		target[key] = value
	}
}

func setIfNotZero(target map[string]any, key string, value float64) {
	if value != 0 {
		target[key] = value
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
		m.Extras = map[string]any{}
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
	return json.Marshal(m.ToMap())
}

func (m EnvelopeMetadata) ToTransportProto() (*transportpb.Metadata, error) {
	pb := &transportpb.Metadata{
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
		pb.GlobalContext = &transportpb.GlobalContext{
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
		pb.ValidityPeriod = &transportpb.ValidityPeriod{
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
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return New(), err
	}
	return FromMap(raw), nil
}

func FromTransportProto(pb *transportpb.Metadata) (EnvelopeMetadata, error) {
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

func parseStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			str, ok := item.(string)
			if ok && strings.TrimSpace(str) != "" {
				out = append(out, str)
			}
		}
		return out
	default:
		return []string{}
	}
}

func pickMap(values map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		switch typed := raw.(type) {
		case map[string]any:
			return typed
		case map[string]string:
			out := map[string]any{}
			for subKey, subValue := range typed {
				out[subKey] = subValue
			}
			return out
		}
	}
	return nil
}

func pickString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if raw, ok := values[key]; ok {
			if str, ok := raw.(string); ok {
				return str
			}
		}
	}
	return ""
}

func pickFloat64(values map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if raw, ok := values[key]; ok {
			switch value := raw.(type) {
			case float64:
				return value
			case float32:
				return float64(value)
			case int:
				return float64(value)
			case int64:
				return float64(value)
			}
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
	for key, value := range values {
		out[key] = value
	}
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
