package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// OpenAPISpec represents OpenAPI 3.0 specification.
type OpenAPISpec struct {
	OpenAPI    string                `json:"openapi"`
	Info       Info                  `json:"info"`
	Paths      map[string]PathItem   `json:"paths"`
	Components Components            `json:"components"`
	Security   []map[string][]string `json:"security,omitempty"`
	Tags       []Tag                 `json:"tags,omitempty"`
}

type Info struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

type Tag struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type PathItem map[string]Operation

type Operation struct {
	OperationID string                 `json:"operationId,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	Parameters  []Parameter            `json:"parameters,omitempty"`
	RequestBody *RequestBody           `json:"requestBody,omitempty"`
	Responses   map[string]Response    `json:"responses"`
	Security    *[]map[string][]string `json:"security,omitempty"`
	QueryHints  *QueryRequirements     `json:"x-query-requirements,omitempty"`
}

type QueryRequirements struct {
	Required []string   `json:"required,omitempty"`
	AnyOf    [][]string `json:"anyOf,omitempty"`
}

type Parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Schema      Schema `json:"schema"`
}

type RequestBody struct {
	Required bool                 `json:"required,omitempty"`
	Content  map[string]MediaType `json:"content"`
}

type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

type MediaType struct {
	Schema Schema `json:"schema"`
}

type SecurityScheme struct {
	Type         string `json:"type"`
	Name         string `json:"name,omitempty"`
	In           string `json:"in,omitempty"`
	Scheme       string `json:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty"`
	Description  string `json:"description,omitempty"`
}

type Schema struct {
	Type                 string            `json:"type,omitempty"`
	Format               string            `json:"format,omitempty"`
	Description          string            `json:"description,omitempty"`
	MinLength            *int              `json:"minLength,omitempty"`
	Properties           map[string]Schema `json:"properties,omitempty"`
	Items                *Schema           `json:"items,omitempty"`
	AdditionalProperties *Schema           `json:"additionalProperties,omitempty"`
	Required             []string          `json:"required,omitempty"`
	Enum                 []string          `json:"enum,omitempty"`
	Ref                  string            `json:"$ref,omitempty"`
	OneOf                []Schema          `json:"oneOf,omitempty"`
	Nullable             bool              `json:"nullable,omitempty"`
	Example              interface{}       `json:"example,omitempty"`
}

type Components struct {
	Schemas         map[string]Schema         `json:"schemas"`
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes,omitempty"`
}

type schemaGenerator struct {
	schemas      map[string]Schema
	byFullName   map[protoreflect.FullName]string
	bySchemaName map[string]protoreflect.FullName
}

// Config holds docgen configuration
type Config struct {
	Title           string
	Version         string
	Description     string
	PublicPaths     []string
	SecuritySchemes map[string]SecurityScheme
	Routes          []registry.HTTPRoute
}

// Generate creates OpenAPI spec from routes.
//
//nolint:gocognit,gocyclo // OpenAPI generation intentionally keeps route, schema, security, and response assembly in one pass.
func Generate(cfg Config) OpenAPISpec {
	sort.Slice(cfg.Routes, func(i, j int) bool {
		if cfg.Routes[i].Path == cfg.Routes[j].Path {
			return strings.ToLower(cfg.Routes[i].Method) < strings.ToLower(cfg.Routes[j].Method)
		}
		return cfg.Routes[i].Path < cfg.Routes[j].Path
	})

	spec := OpenAPISpec{
		OpenAPI: "3.0.0",
		Info: Info{
			Title:       cfg.Title,
			Version:     cfg.Version,
			Description: cfg.Description,
		},
		Paths: make(map[string]PathItem),
		Components: Components{
			Schemas:         make(map[string]Schema),
			SecuritySchemes: make(map[string]SecurityScheme),
		},
	}
	spec.Components.SecuritySchemes["bearerAuth"] = SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "JWT",
		Description:  "JWT bearer token. Example: Authorization: Bearer <token>",
	}
	for name, scheme := range cfg.SecuritySchemes {
		spec.Components.SecuritySchemes[name] = scheme
	}
	if _, ok := spec.Components.SecuritySchemes["bearerAuth"]; ok {
		spec.Security = []map[string][]string{{"bearerAuth": {}}}
	}

	addStandardSchemas(spec.Components.Schemas)
	generator := newSchemaGenerator(spec.Components.Schemas)
	tagSet := make(map[string]struct{})

	for _, route := range cfg.Routes {
		if _, exists := spec.Paths[route.Path]; !exists {
			spec.Paths[route.Path] = make(PathItem)
		}

		method := strings.ToLower(route.Method)
		tag := deriveTag(route.Path)
		tagSet[tag] = struct{}{}

		op := Operation{
			OperationID: deriveOperationID(method, route.Path),
			Tags:        []string{tag},
			Summary:     route.Description,
			Description: route.Description,
			Responses: map[string]Response{
				"400": buildErrorResponse("Bad request"),
				"500": buildErrorResponse("Internal server error"),
			},
		}

		if route.IsPublic || !requiresAuthentication(route.Path, cfg.PublicPaths) {
			op.Security = emptySecurityRequirement()
		} else if len(route.AuthRequirements) > 0 {
			op.Security = securityRequirement(route.AuthRequirements)
			op.Responses["401"] = buildErrorResponse("Unauthorized")
		} else {
			op.Responses["401"] = buildErrorResponse("Unauthorized")
		}

		if route.RequestSchema != "" && method != "get" && method != "delete" {
			op.RequestBody = &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {
						Schema: Schema{Ref: "#/components/schemas/" + route.RequestSchema},
					},
				},
			}
		} else if route.RequestType != nil {
			requestSchemaName := generator.generateSchema(route.RequestType)
			if method == "get" || method == "delete" {
				op.Parameters = buildQueryParameters(route.RequestType, route.RequiredQueryParams)
				if len(route.RequiredQueryParams) > 0 || len(route.AnyOfQueryParams) > 0 {
					op.QueryHints = &QueryRequirements{
						Required: dedupeStrings(route.RequiredQueryParams),
						AnyOf:    normalizeAnyOfQueryParams(route.AnyOfQueryParams),
					}
				}
			} else {
				op.RequestBody = &RequestBody{
					Required: true,
					Content: map[string]MediaType{
						"application/json": {
							Schema: Schema{Ref: "#/components/schemas/" + requestSchemaName},
						},
					},
				}
			}
		}

		successStatus := route.SuccessStatusCode
		if successStatus == 0 {
			successStatus = 200
		}
		successCode := fmt.Sprintf("%d", successStatus)
		successDescription := route.SuccessDescription
		if successDescription == "" {
			successDescription = "Successful response"
		}

		if route.NoContentResponse {
			op.Responses[successCode] = Response{Description: successDescription}
		} else if route.ResponseSchema != "" {
			op.Responses[successCode] = Response{
				Description: successDescription,
				Content: map[string]MediaType{
					"application/json": {
						Schema: Schema{Ref: "#/components/schemas/" + route.ResponseSchema},
					},
				},
			}
		} else if route.ResponseType != nil {
			responseSchemaName := generator.generateSchema(route.ResponseType)
			op.Responses[successCode] = Response{
				Description: successDescription,
				Content: map[string]MediaType{
					"application/json": {
						Schema: Schema{Ref: "#/components/schemas/" + responseSchemaName},
					},
				},
			}
		} else {
			defaultSchemaRef := "#/components/schemas/StandardSuccessResponse"
			if isLikelyListEndpoint(route.Path, route.Description) {
				defaultSchemaRef = "#/components/schemas/PaginatedResult"
			}
			op.Responses[successCode] = Response{
				Description: successDescription,
				Content: map[string]MediaType{
					"application/json": {
						Schema: Schema{Ref: defaultSchemaRef},
					},
				},
			}
		}

		spec.Paths[route.Path][method] = op
	}

	for tag := range tagSet {
		spec.Tags = append(spec.Tags, Tag{Name: tag})
	}
	sort.Slice(spec.Tags, func(i, j int) bool { return spec.Tags[i].Name < spec.Tags[j].Name })

	return spec
}

func newSchemaGenerator(schemas map[string]Schema) *schemaGenerator {
	g := &schemaGenerator{
		schemas:      schemas,
		byFullName:   make(map[protoreflect.FullName]string),
		bySchemaName: make(map[string]protoreflect.FullName),
	}
	for name := range schemas {
		g.bySchemaName[name] = ""
	}
	return g
}

func securityRequirement(requirements []registry.HTTPSecurityRequirement) *[]map[string][]string {
	result := make([]map[string][]string, 0, len(requirements))
	for _, requirement := range requirements {
		if requirement.Scheme == "" {
			continue
		}
		scopes := requirement.Scopes
		if scopes == nil {
			scopes = []string{}
		}
		result = append(result, map[string][]string{requirement.Scheme: scopes})
	}
	if len(result) == 0 {
		return nil
	}
	return &result
}

func (g *schemaGenerator) generateSchema(msg proto.Message) string {
	if msg == nil {
		return ""
	}
	return g.generateMessage(msg.ProtoReflect().Descriptor())
}

func (g *schemaGenerator) generateMessage(md protoreflect.MessageDescriptor) string {
	if md == nil {
		return ""
	}
	if existing, ok := g.byFullName[md.FullName()]; ok {
		return existing
	}

	schemaName := g.uniqueSchemaName(md)
	g.byFullName[md.FullName()] = schemaName
	g.schemas[schemaName] = Schema{Type: "object"} // recursion guard

	properties := make(map[string]Schema)
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		if field.JSONName() == "" {
			continue
		}
		properties[field.JSONName()] = g.schemaForField(field)
	}

	g.schemas[schemaName] = Schema{
		Type:       "object",
		Properties: properties,
	}

	return schemaName
}

func (g *schemaGenerator) uniqueSchemaName(md protoreflect.MessageDescriptor) string {
	base := string(md.Name())
	if existing, ok := g.bySchemaName[base]; !ok || existing == md.FullName() {
		g.bySchemaName[base] = md.FullName()
		return base
	}

	pkgPrefix := packagePrefix(md.ParentFile().Package())
	candidate := pkgPrefix + base
	if existing, ok := g.bySchemaName[candidate]; !ok || existing == md.FullName() {
		g.bySchemaName[candidate] = md.FullName()
		return candidate
	}

	for i := 2; ; i++ {
		candidateN := fmt.Sprintf("%s%d", candidate, i)
		if existing, ok := g.bySchemaName[candidateN]; !ok || existing == md.FullName() {
			g.bySchemaName[candidateN] = md.FullName()
			return candidateN
		}
	}
}

func packagePrefix(pkg protoreflect.FullName) string {
	parts := strings.Split(string(pkg), ".")
	var out strings.Builder
	for _, part := range parts {
		if part == "" || isVersionSegment(part) {
			continue
		}
		out.WriteString(toPascalCase(part))
	}
	if out.Len() == 0 {
		return "Proto"
	}
	return out.String()
}

func isVersionSegment(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (g *schemaGenerator) schemaForField(fd protoreflect.FieldDescriptor) Schema {
	if fd.IsMap() {
		valueSchema := g.singularFieldSchema(fd.MapValue(), fd.MapValue().JSONName())
		return Schema{Type: "object", AdditionalProperties: &valueSchema}
	}
	if fd.Cardinality() == protoreflect.Repeated {
		item := g.singularFieldSchema(fd, fd.JSONName())
		return Schema{Type: "array", Items: &item}
	}
	return g.singularFieldSchema(fd, fd.JSONName())
}

func (g *schemaGenerator) singularFieldSchema(fd protoreflect.FieldDescriptor, jsonName string) Schema {
	var schema Schema
	switch fd.Kind() {
	case protoreflect.BoolKind:
		schema = Schema{Type: "boolean"}
	case protoreflect.StringKind:
		schema = Schema{Type: "string"}
	case protoreflect.BytesKind:
		schema = Schema{Type: "string", Format: "byte"}
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		schema = Schema{Type: "integer", Format: "int32"}
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		schema = Schema{Type: "integer", Format: "int32"}
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		schema = Schema{Type: "integer", Format: "int64"}
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		schema = Schema{Type: "integer", Format: "int64"}
	case protoreflect.FloatKind:
		schema = Schema{Type: "number", Format: "float"}
	case protoreflect.DoubleKind:
		schema = Schema{Type: "number", Format: "double"}
	case protoreflect.EnumKind:
		values := make([]string, 0, fd.Enum().Values().Len())
		for i := 0; i < fd.Enum().Values().Len(); i++ {
			values = append(values, string(fd.Enum().Values().Get(i).Name()))
		}
		schema = Schema{Type: "string", Enum: values}
	case protoreflect.MessageKind, protoreflect.GroupKind:
		schema = g.schemaForMessageKind(fd.Message())
	default:
		schema = Schema{Type: "string"}
	}
	applyFieldValidationHints(jsonName, &schema)
	return schema
}

func (g *schemaGenerator) schemaForMessageKind(md protoreflect.MessageDescriptor) Schema {
	if md == nil {
		return Schema{Type: "object"}
	}

	switch string(md.FullName()) {
	case "google.protobuf.Timestamp":
		return Schema{Type: "string", Format: "date-time"}
	case "google.protobuf.Duration":
		return Schema{Type: "string"}
	case "google.protobuf.StringValue":
		return Schema{Type: "string", Nullable: true}
	case "google.protobuf.BoolValue":
		return Schema{Type: "boolean", Nullable: true}
	case "google.protobuf.Int32Value", "google.protobuf.UInt32Value":
		return Schema{Type: "integer", Format: "int32", Nullable: true}
	case "google.protobuf.Int64Value", "google.protobuf.UInt64Value":
		return Schema{Type: "integer", Format: "int64", Nullable: true}
	case "google.protobuf.FloatValue":
		return Schema{Type: "number", Format: "float", Nullable: true}
	case "google.protobuf.DoubleValue":
		return Schema{Type: "number", Format: "double", Nullable: true}
	case "google.protobuf.BytesValue":
		return Schema{Type: "string", Format: "byte", Nullable: true}
	case "google.protobuf.Struct":
		anySchema := Schema{}
		return Schema{Type: "object", AdditionalProperties: &anySchema}
	case "google.protobuf.ListValue":
		anySchema := Schema{}
		return Schema{Type: "array", Items: &anySchema}
	case "google.protobuf.Value", "google.protobuf.Any":
		return Schema{Type: "object"}
	}

	name := g.generateMessage(md)
	return Schema{Ref: "#/components/schemas/" + name}
}

func buildQueryParameters(msg proto.Message, requiredQueryParams []string) []Parameter {
	if msg == nil {
		return nil
	}

	requiredFields := make(map[string]bool, len(requiredQueryParams))
	for _, name := range requiredQueryParams {
		normalized := strings.TrimSpace(name)
		if normalized == "" {
			continue
		}
		requiredFields[normalized] = true
	}
	msgType := reflect.TypeOf(msg)
	if msgType.Kind() == reflect.Ptr {
		msgType = msgType.Elem()
	}
	if msgType.Kind() != reflect.Struct {
		return nil
	}

	params := make([]Parameter, 0, msgType.NumField())
	for i := 0; i < msgType.NumField(); i++ {
		field := msgType.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		name := strings.Split(jsonTag, ",")[0]
		if name == "" || name == "metadata" {
			continue
		}

		schema, ok := querySchemaForGoType(field.Type)
		if !ok {
			continue
		}
		applyFieldValidationHints(name, &schema)

		params = append(params, Parameter{
			Name:     name,
			In:       "query",
			Required: requiredFields[name],
			Schema:   schema,
		})
	}

	sort.Slice(params, func(i, j int) bool {
		if params[i].Required != params[j].Required {
			return params[i].Required
		}
		return params[i].Name < params[j].Name
	})

	return params
}

//nolint:exhaustive // Unsupported reflection kinds intentionally fall through to false.
func querySchemaForGoType(t reflect.Type) (Schema, bool) {
	if t.Kind() == reflect.Ptr {
		return Schema{}, false
	}
	switch t.Kind() {
	case reflect.Bool:
		return Schema{Type: "boolean"}, true
	case reflect.String:
		return Schema{Type: "string"}, true
	case reflect.Int32, reflect.Uint32:
		return Schema{Type: "integer", Format: "int32"}, true
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint64:
		return Schema{Type: "integer", Format: "int64"}, true
	case reflect.Float32:
		return Schema{Type: "number", Format: "float"}, true
	case reflect.Float64:
		return Schema{Type: "number", Format: "double"}, true
	default:
		return Schema{}, false
	}
}

func applyFieldValidationHints(fieldName string, schema *Schema) {
	if schema == nil || schema.Ref != "" || schema.Type != "string" {
		return
	}

	name := strings.ToLower(fieldName)
	switch {
	case strings.Contains(name, "email"):
		schema.Format = "email"
	case strings.HasSuffix(name, "url") || strings.Contains(name, "_url"):
		schema.Format = "uri"
	case strings.Contains(name, "password"):
		schema.MinLength = intPtr(8)
	case strings.HasSuffix(name, "_at") || strings.Contains(name, "timestamp"):
		schema.Format = "date-time"
	case strings.Contains(name, "date"):
		schema.Format = "date"
	}
}

func intPtr(v int) *int {
	return &v
}

func dedupeStrings(items []string) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func normalizeAnyOfQueryParams(groups [][]string) [][]string {
	normalized := make([][]string, 0, len(groups))
	seenGroups := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		deduped := dedupeStrings(group)
		if len(deduped) == 0 {
			continue
		}
		key := strings.Join(deduped, "|")
		if _, exists := seenGroups[key]; exists {
			continue
		}
		seenGroups[key] = struct{}{}
		normalized = append(normalized, deduped)
	}
	return normalized
}

func buildErrorResponse(description string) Response {
	return Response{
		Description: description,
		Content: map[string]MediaType{
			"application/json": {
				Schema: Schema{Ref: "#/components/schemas/ErrorResponse"},
			},
		},
	}
}

func emptySecurityRequirement() *[]map[string][]string {
	empty := []map[string][]string{}
	return &empty
}

func addStandardSchemas(schemas map[string]Schema) {
	anySchema := Schema{}

	schemas["ErrorResponse"] = Schema{
		Type:     "object",
		Required: []string{"error"},
		Properties: map[string]Schema{
			"error": {
				Type:    "string",
				Example: "validation failed",
			},
			"code": {
				Type:    "string",
				Example: "BAD_REQUEST",
			},
			"details": {
				Type:                 "object",
				AdditionalProperties: &anySchema,
			},
		},
	}

	schemas["Pagination"] = Schema{
		Type: "object",
		Properties: map[string]Schema{
			"page_size":       {Type: "integer", Format: "int32", Example: 50},
			"page_token":      {Type: "string", Example: "cursor:abc123"},
			"next_page_token": {Type: "string", Example: "cursor:def456"},
			"total_count":     {Type: "integer", Format: "int64", Example: 120},
		},
	}

	schemas["StandardSuccessResponse"] = Schema{
		Type: "object",
		Properties: map[string]Schema{
			"status":  {Type: "string", Example: "ok"},
			"message": {Type: "string", Example: "Successful response"},
			"data": {
				Type:                 "object",
				AdditionalProperties: &anySchema,
			},
		},
	}

	itemAny := Schema{}
	schemas["PaginatedResult"] = Schema{
		Type: "object",
		Properties: map[string]Schema{
			"items": {
				Type:  "array",
				Items: &itemAny,
			},
			"pagination": {
				Ref: "#/components/schemas/Pagination",
			},
		},
	}
}

func requiresAuthentication(path string, publicPaths []string) bool {
	for _, prefix := range publicPaths {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}
	return true
}

func deriveTag(path string) string {
	segments := pathSegments(path)
	if len(segments) >= 2 && segments[0] == "v1" {
		return segments[1]
	}
	if len(segments) > 0 {
		return segments[0]
	}
	return "default"
}

func deriveOperationID(method, path string) string {
	segments := pathSegments(path)
	parts := []string{strings.ToLower(method)}
	for _, seg := range segments {
		if seg == "" || seg == "v1" {
			continue
		}
		seg = strings.Trim(seg, "{}")
		parts = append(parts, toPascalCase(seg))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return parts[0] + strings.Join(parts[1:], "")
}

func pathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func toPascalCase(s string) string {
	if s == "" {
		return ""
	}
	splitter := func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	}
	parts := strings.FieldsFunc(s, splitter)
	var out strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = unicode.ToUpper(runes[0])
		out.WriteString(string(runes))
	}
	if out.Len() == 0 {
		return s
	}
	return out.String()
}

func isLikelyListEndpoint(path, description string) bool {
	lowerPath := strings.ToLower(path)
	lowerDesc := strings.ToLower(description)
	if strings.HasPrefix(lowerDesc, "list ") || strings.Contains(lowerDesc, " list ") {
		return true
	}
	if strings.Contains(lowerPath, "/list") || strings.Contains(lowerPath, "/entries") || strings.HasSuffix(lowerPath, "s") {
		return true
	}
	return false
}

// Example main function - customize for your project
func main() {
	// TODO: Import your domain handlers and collect routes
	// Example:
	// var allRoutes []registry.HTTPRoute
	// allRoutes = append(allRoutes, geo.GetHTTPHandlers(nil)...)
	// allRoutes = append(allRoutes, report.GetHTTPHandlers(nil)...)

	cfg := Config{
		Title:       "{{PROJECT_NAME}} API",
		Version:     "{{FOUNDATION_VERSION}}",
		Description: "API documentation for {{PROJECT_NAME}}",
		PublicPaths: []string{
			"/v1/health",
			"/v1/status",
		},
		Routes: []registry.HTTPRoute{}, // Add your routes here
	}

	spec := Generate(cfg)

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(spec); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding spec: %v\n", err)
		os.Exit(1)
	}
}
