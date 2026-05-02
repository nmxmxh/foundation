package grpcsvc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
)

const (
	CodecName                  = "foundation-json"
	BinaryCodecName            = "foundation-binary"
	dispatchMethod             = "/foundation.runtime.v1.RuntimeService/Dispatch"
	dispatchFrameMethod        = "/foundation.runtime.v1.RuntimeService/DispatchFrame"
	defaultMaxEventTypeBytes   = 256
	defaultMaxCorrelationBytes = 512
)

type Envelope struct {
	EventType     string         `json:"event_type"`
	Payload       map[string]any `json:"payload"`
	CorrelationID string         `json:"correlation_id"`
	SchemaVersion string         `json:"schema_version"`
}

type Handler func(context.Context, Envelope) (Envelope, error)
type FrameHandler func(context.Context, Frame) (Frame, error)

type Frame struct {
	EventType     string
	Payload       []byte
	CorrelationID string
	SchemaVersion string
}

type FrameView struct {
	EventType     []byte
	Payload       []byte
	CorrelationID []byte
	SchemaVersion []byte
}

type Client struct {
	conn          grpc.ClientConnInterface
	envelopeOpts  []grpc.CallOption
	frameOpts     []grpc.CallOption
	maxEventBytes int
	maxCorrBytes  int
}

type DirectFrameClient struct {
	router        *Router
	maxEventBytes int
	maxCorrBytes  int
}

type Router struct {
	handlers      map[string]Handler
	frameHandlers map[string]FrameHandler
}

type ServerOptions struct {
	AuthToken           string
	MaxMessageBytes     int
	MaxEventTypeBytes   int
	MaxCorrelationBytes int
	UnaryInterceptors   []grpc.UnaryServerInterceptor
}

type ClientOptions struct {
	AuthToken       string
	MaxMessageBytes int
	DialOptions     []grpc.DialOption
}

func init() {
	encoding.RegisterCodec(jsonCodec{})
	encoding.RegisterCodec(binaryFrameCodec{})
}

var (
	jsonForceCodecOption         = grpc.ForceCodec(jsonCodec{})
	binaryFrameForceCodecOption  = grpc.ForceCodec(binaryFrameCodec{})
	jsonForceCodecOptions        = []grpc.CallOption{jsonForceCodecOption}
	binaryFrameForceCodecOptions = []grpc.CallOption{binaryFrameForceCodecOption}
)

func NewRouter() *Router {
	return &Router{
		handlers:      map[string]Handler{},
		frameHandlers: map[string]FrameHandler{},
	}
}

func (r *Router) Register(eventType string, handler Handler) error {
	if r == nil {
		return errors.New("grpc router is nil")
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return errors.New("event type is required")
	}
	if handler == nil {
		return errors.New("handler is required")
	}
	if _, exists := r.handlers[eventType]; exists {
		return fmt.Errorf("duplicate grpc handler for %s", eventType)
	}
	r.handlers[eventType] = handler
	return nil
}

func (r *Router) RegisterFrame(eventType string, handler FrameHandler) error {
	if r == nil {
		return errors.New("grpc router is nil")
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return errors.New("event type is required")
	}
	if handler == nil {
		return errors.New("handler is required")
	}
	if _, exists := r.frameHandlers[eventType]; exists {
		return fmt.Errorf("duplicate grpc frame handler for %s", eventType)
	}
	r.frameHandlers[eventType] = handler
	return nil
}

func (r *Router) DispatchFrame(ctx context.Context, frame Frame) (Frame, error) {
	if r == nil {
		return Frame{}, errors.New("grpc router is nil")
	}
	h := r.frameHandlers[frame.EventType]
	if h == nil {
		return Frame{}, fmt.Errorf("no grpc frame handler for %s", frame.EventType)
	}
	return h(ctx, frame)
}

func NewServer(router *Router, opts ServerOptions) *grpc.Server {
	interceptors := append([]grpc.UnaryServerInterceptor{}, opts.UnaryInterceptors...)
	if opts.AuthToken != "" {
		interceptors = append(interceptors, authUnaryInterceptor(opts.AuthToken))
	}
	interceptors = append(interceptors, envelopeGuardUnaryInterceptor(opts))
	serverOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(interceptors...),
	}
	if opts.MaxMessageBytes > 0 {
		serverOpts = append(serverOpts, grpc.MaxRecvMsgSize(opts.MaxMessageBytes), grpc.MaxSendMsgSize(opts.MaxMessageBytes))
	}
	server := grpc.NewServer(serverOpts...)
	RegisterRuntimeService(server, router)
	return server
}

func Serve(ctx context.Context, listener net.Listener, router *Router, opts ServerOptions) error {
	server := NewServer(router, opts)
	go func() {
		<-ctx.Done()
		server.GracefulStop()
	}()
	err := server.Serve(listener)
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	return err
}

func Dial(ctx context.Context, target string, opts ClientOptions) (*grpc.ClientConn, error) {
	dialOpts := []grpc.DialOption{
		grpc.WithDefaultCallOptions(grpc.ForceCodec(jsonCodec{})),
	}
	if opts.MaxMessageBytes > 0 {
		dialOpts = append(dialOpts, grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(opts.MaxMessageBytes),
			grpc.MaxCallSendMsgSize(opts.MaxMessageBytes),
		))
	}
	if opts.AuthToken != "" {
		dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(clientAuthUnaryInterceptor(opts.AuthToken)))
	}
	dialOpts = append(dialOpts, opts.DialOptions...)
	return grpc.NewClient(target, dialOpts...)
}

func NewClient(conn grpc.ClientConnInterface, opts ClientOptions) *Client {
	envelopeOpts := append([]grpc.CallOption{}, jsonForceCodecOptions...)
	frameOpts := append([]grpc.CallOption{}, binaryFrameForceCodecOptions...)
	if opts.MaxMessageBytes > 0 {
		limits := []grpc.CallOption{
			grpc.MaxCallRecvMsgSize(opts.MaxMessageBytes),
			grpc.MaxCallSendMsgSize(opts.MaxMessageBytes),
		}
		envelopeOpts = append(envelopeOpts, limits...)
		frameOpts = append(frameOpts, limits...)
	}
	return &Client{
		conn:          conn,
		envelopeOpts:  envelopeOpts,
		frameOpts:     frameOpts,
		maxEventBytes: defaultMaxEventTypeBytes,
		maxCorrBytes:  defaultMaxCorrelationBytes,
	}
}

func NewDirectFrameClient(router *Router, opts ServerOptions) *DirectFrameClient {
	maxEventBytes := opts.MaxEventTypeBytes
	if maxEventBytes <= 0 {
		maxEventBytes = defaultMaxEventTypeBytes
	}
	maxCorrBytes := opts.MaxCorrelationBytes
	if maxCorrBytes <= 0 {
		maxCorrBytes = defaultMaxCorrelationBytes
	}
	return &DirectFrameClient{
		router:        router,
		maxEventBytes: maxEventBytes,
		maxCorrBytes:  maxCorrBytes,
	}
}

func (c *DirectFrameClient) DispatchFrame(ctx context.Context, frame Frame) (Frame, error) {
	if c == nil || c.router == nil {
		return Frame{}, errors.New("direct frame client router is nil")
	}
	if err := validateFrame(frame, c.maxEventBytes, c.maxCorrBytes); err != nil {
		return Frame{}, err
	}
	return c.router.DispatchFrame(ctx, frame)
}

func (c *Client) Dispatch(ctx context.Context, envelope Envelope, opts ...grpc.CallOption) (Envelope, error) {
	var out Envelope
	callOpts := c.envelopeOpts
	if len(opts) > 0 {
		callOpts = append(append([]grpc.CallOption{}, c.envelopeOpts...), opts...)
	}
	err := c.conn.Invoke(ctx, dispatchMethod, envelope, &out, callOpts...)
	return out, err
}

func (c *Client) DispatchFrame(ctx context.Context, frame Frame, opts ...grpc.CallOption) (Frame, error) {
	if err := validateFrame(frame, c.maxEventBytes, c.maxCorrBytes); err != nil {
		return Frame{}, err
	}
	var out Frame
	callOpts := c.frameOpts
	if len(opts) > 0 {
		callOpts = append(append([]grpc.CallOption{}, c.frameOpts...), opts...)
	}
	err := c.conn.Invoke(ctx, dispatchFrameMethod, frame, &out, callOpts...)
	return out, err
}

type runtimeService struct {
	router *Router
}

type RuntimeServiceServer interface {
	mustEmbedRuntimeServiceServer()
}

func (*runtimeService) mustEmbedRuntimeServiceServer() {}

func RegisterRuntimeService(registrar grpc.ServiceRegistrar, router *Router) {
	registrar.RegisterService(&grpc.ServiceDesc{
		ServiceName: "foundation.runtime.v1.RuntimeService",
		HandlerType: (*RuntimeServiceServer)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "Dispatch",
			Handler:    dispatchHandler,
		}, {
			MethodName: "DispatchFrame",
			Handler:    dispatchFrameHandler,
		}},
		Streams:  []grpc.StreamDesc{},
		Metadata: "foundation/runtime/v1/runtime.proto",
	}, &runtimeService{router: router})
}

func Dispatch(ctx context.Context, conn grpc.ClientConnInterface, envelope Envelope, opts ...grpc.CallOption) (Envelope, error) {
	var out Envelope
	callOpts := jsonForceCodecOptions
	if len(opts) > 0 {
		callOpts = append(append([]grpc.CallOption{}, jsonForceCodecOptions...), opts...)
	}
	err := conn.Invoke(ctx, dispatchMethod, envelope, &out, callOpts...)
	return out, err
}

func DispatchFrame(ctx context.Context, conn grpc.ClientConnInterface, frame Frame, opts ...grpc.CallOption) (Frame, error) {
	var out Frame
	callOpts := binaryFrameForceCodecOptions
	if len(opts) > 0 {
		callOpts = append(append([]grpc.CallOption{}, binaryFrameForceCodecOptions...), opts...)
	}
	err := conn.Invoke(ctx, dispatchFrameMethod, frame, &out, callOpts...)
	return out, err
}

func dispatchHandler(service any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var req Envelope
	if err := decode(&req); err != nil {
		return nil, err
	}
	info := &grpc.UnaryServerInfo{
		Server:     service,
		FullMethod: dispatchMethod,
	}
	handler := func(ctx context.Context, request any) (any, error) {
		svc := service.(*runtimeService)
		if svc.router == nil {
			return nil, errors.New("grpc router is nil")
		}
		req := request.(Envelope)
		h := svc.router.handlers[req.EventType]
		if h == nil {
			return nil, fmt.Errorf("no grpc handler for %s", req.EventType)
		}
		return h(ctx, req)
	}
	if interceptor == nil {
		return handler(ctx, req)
	}
	return interceptor(ctx, req, info, handler)
}

func dispatchFrameHandler(service any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var req Frame
	if err := decode(&req); err != nil {
		return nil, err
	}
	info := &grpc.UnaryServerInfo{
		Server:     service,
		FullMethod: dispatchFrameMethod,
	}
	handler := func(ctx context.Context, request any) (any, error) {
		svc := service.(*runtimeService)
		if svc.router == nil {
			return nil, errors.New("grpc router is nil")
		}
		req := request.(Frame)
		h := svc.router.frameHandlers[req.EventType]
		if h == nil {
			return nil, fmt.Errorf("no grpc frame handler for %s", req.EventType)
		}
		return h(ctx, req)
	}
	if interceptor == nil {
		return handler(ctx, req)
	}
	return interceptor(ctx, req, info, handler)
}

func authUnaryInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok || first(md.Get("authorization")) != "Bearer "+token {
			return nil, errors.New("grpc unauthorized")
		}
		return handler(ctx, req)
	}
}

func clientAuthUnaryInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func envelopeGuardUnaryInterceptor(opts ServerOptions) grpc.UnaryServerInterceptor {
	maxEventTypeBytes := opts.MaxEventTypeBytes
	if maxEventTypeBytes <= 0 {
		maxEventTypeBytes = defaultMaxEventTypeBytes
	}
	maxCorrelationBytes := opts.MaxCorrelationBytes
	if maxCorrelationBytes <= 0 {
		maxCorrelationBytes = defaultMaxCorrelationBytes
	}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		envelope, ok := req.(Envelope)
		if ok {
			if envelope.EventType == "" {
				return nil, errors.New("grpc event type is required")
			}
			if len(envelope.EventType) > maxEventTypeBytes {
				return nil, fmt.Errorf("grpc event type too large: %d > %d", len(envelope.EventType), maxEventTypeBytes)
			}
			if len(envelope.CorrelationID) > maxCorrelationBytes {
				return nil, fmt.Errorf("grpc correlation id too large: %d > %d", len(envelope.CorrelationID), maxCorrelationBytes)
			}
			return handler(ctx, req)
		}
		frame, ok := req.(Frame)
		if ok {
			if err := validateFrame(frame, maxEventTypeBytes, maxCorrelationBytes); err != nil {
				return nil, err
			}
		}
		return handler(ctx, req)
	}
}

func validateFrame(frame Frame, maxEventTypeBytes, maxCorrelationBytes int) error {
	if frame.EventType == "" {
		return errors.New("grpc event type is required")
	}
	if len(frame.EventType) > maxEventTypeBytes {
		return fmt.Errorf("grpc event type too large: %d > %d", len(frame.EventType), maxEventTypeBytes)
	}
	if len(frame.CorrelationID) > maxCorrelationBytes {
		return fmt.Errorf("grpc correlation id too large: %d > %d", len(frame.CorrelationID), maxCorrelationBytes)
	}
	return nil
}

type jsonCodec struct{}

func (jsonCodec) Name() string { return CodecName }

func (jsonCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (jsonCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

type binaryFrameCodec struct{}

func (binaryFrameCodec) Name() string { return BinaryCodecName }

func (binaryFrameCodec) Marshal(v any) ([]byte, error) {
	frame, ok := v.(Frame)
	if !ok {
		if framePtr, ok := v.(*Frame); ok && framePtr != nil {
			frame = *framePtr
			ok = true
		}
	}
	if !ok {
		return nil, fmt.Errorf("foundation binary codec expects grpcsvc.Frame, got %T", v)
	}
	return marshalFrame(frame), nil
}

func (binaryFrameCodec) Unmarshal(data []byte, v any) error {
	frame, ok := v.(*Frame)
	if !ok || frame == nil {
		return fmt.Errorf("foundation binary codec expects *grpcsvc.Frame, got %T", v)
	}
	decoded, err := unmarshalFrame(data)
	if err != nil {
		return err
	}
	*frame = decoded
	return nil
}

func marshalFrame(frame Frame) []byte {
	size := 16 + len(frame.EventType) + len(frame.Payload) + len(frame.CorrelationID) + len(frame.SchemaVersion)
	out := make([]byte, size)
	offset := putStringField(out, 0, frame.EventType)
	offset = putField(out, offset, frame.Payload)
	offset = putStringField(out, offset, frame.CorrelationID)
	putStringField(out, offset, frame.SchemaVersion)
	return out
}

func AppendMarshalFrame(dst []byte, frame Frame) []byte {
	size := 16 + len(frame.EventType) + len(frame.Payload) + len(frame.CorrelationID) + len(frame.SchemaVersion)
	offset := len(dst)
	dst = append(dst, make([]byte, size)...)
	offset = putStringField(dst, offset, frame.EventType)
	offset = putField(dst, offset, frame.Payload)
	offset = putStringField(dst, offset, frame.CorrelationID)
	putStringField(dst, offset, frame.SchemaVersion)
	return dst
}

func unmarshalFrame(data []byte) (Frame, error) {
	var frame Frame
	var eventType []byte
	var err error
	offset := 0
	eventType, offset, err = readField(data, offset)
	if err != nil {
		return Frame{}, err
	}
	frame.Payload, offset, err = readField(data, offset)
	if err != nil {
		return Frame{}, err
	}
	correlationID, offset, err := readField(data, offset)
	if err != nil {
		return Frame{}, err
	}
	schemaVersion, offset, err := readField(data, offset)
	if err != nil {
		return Frame{}, err
	}
	if offset != len(data) {
		return Frame{}, errors.New("trailing bytes in foundation binary frame")
	}
	frame.EventType = string(eventType)
	frame.CorrelationID = string(correlationID)
	frame.SchemaVersion = string(schemaVersion)
	return frame, nil
}

func UnmarshalFrameView(data []byte) (FrameView, error) {
	var view FrameView
	var err error
	offset := 0
	view.EventType, offset, err = readField(data, offset)
	if err != nil {
		return FrameView{}, err
	}
	view.Payload, offset, err = readField(data, offset)
	if err != nil {
		return FrameView{}, err
	}
	view.CorrelationID, offset, err = readField(data, offset)
	if err != nil {
		return FrameView{}, err
	}
	view.SchemaVersion, offset, err = readField(data, offset)
	if err != nil {
		return FrameView{}, err
	}
	if offset != len(data) {
		return FrameView{}, errors.New("trailing bytes in foundation binary frame")
	}
	return view, nil
}

func putField(out []byte, offset int, value []byte) int {
	binary.BigEndian.PutUint32(out[offset:offset+4], uint32(len(value)))
	offset += 4
	copy(out[offset:offset+len(value)], value)
	return offset + len(value)
}

func putStringField(out []byte, offset int, value string) int {
	binary.BigEndian.PutUint32(out[offset:offset+4], uint32(len(value)))
	offset += 4
	copy(out[offset:offset+len(value)], value)
	return offset + len(value)
}

func readField(data []byte, offset int) ([]byte, int, error) {
	if len(data)-offset < 4 {
		return nil, 0, errors.New("truncated foundation binary frame")
	}
	length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if length < 0 || len(data)-offset < length {
		return nil, 0, errors.New("invalid foundation binary frame length")
	}
	return data[offset : offset+length], offset + length, nil
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
