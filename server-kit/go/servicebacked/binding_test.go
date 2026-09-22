//go:build servicebacked

package servicebacked

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/placement"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// This fixture uses PostgreSQL truth, Redis placement, and an authenticated TCP service.
func serviceBindingFixture(tb testing.TB) (context.Context, *placement.BindingRegistry, placement.RuntimeBindingRequest, placement.BindingFrameDispatcher) {
	tb.Helper()
	env := requireServiceEnv(tb)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	tb.Cleanup(cancel)
	org := uniqueName(env.prefix, "binding-org")
	ctx = security.ContextWithOrganizationID(ctx, org)
	md := metadata.New()
	md.CorrelationID = "binding-service"
	ctx = metadata.IntoContext(ctx, md)
	state := openPostgres(tb, env, serviceBackedPoolOptions(2))
	tb.Cleanup(state.Close)
	applyStateSchema(tb, ctx, state)
	cleanupOrganization(tb, ctx, state, org)
	record := database.DomainRecord{Domain: "compute", Collection: "resources", OrganizationID: org, RecordID: "resident", Data: serviceRecordData(map[string]any{"value": int64(37)})}
	if _, err := state.UpsertRecord(ctx, record); err != nil {
		tb.Fatal(err)
	}
	saved, found, err := state.GetRecord(ctx, "compute", "resources", org, "resident")
	if err != nil || !found {
		tb.Fatalf("source: %v %v", found, err)
	}
	field, ok := saved.Data.Get("value")
	if !ok {
		tb.Fatal("missing typed source value")
	}
	value, err := strconv.ParseUint(field.Text, 10, 64)
	if err != nil {
		tb.Fatal(err)
	}
	var input [8]byte
	binary.LittleEndian.PutUint64(input[:], value)
	r, err := placement.NewBindingRegistry(11, placement.BindingLimits{Resources: 4, Bindings: 4, ResidentBytes: 1024, InFlight: 4},
		func(ctx context.Context, _ placement.BindingAction, resource, binding uint64) error {
			if security.GetOrganizationIDFromContext(ctx) != org || resource != 7 || binding != 0 && binding != 9 {
				return placement.ErrBindingForbidden
			}
			return nil
		}, placement.BindingType{ID: 101, Validate: func(data []byte) error {
			if len(data) != 8 {
				return placement.ErrBindingInvalid
			}
			return nil
		}})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := r.Close(); err != nil {
			tb.Error(err)
		}
	})
	if _, err := r.PublishResource(ctx, 7, 101, 0, input[:]); err != nil {
		tb.Fatal(err)
	}
	spec := placement.BindingSpec{InputCopiesKnown: true, ID: 9, ResourceID: 7, ResourceGeneration: 1, InputTypeID: 101, OutputTypeID: 101, MaxInputBytes: 8, MaxOutputBytes: 8, MaxConcurrency: 4,
		Operation: func(ctx context.Context, input, output []byte) (uint32, error) {
			if md := metadata.FromContext(ctx); md.CorrelationID == "" {
				return 0, placement.ErrBindingInvalid
			}
			binary.LittleEndian.PutUint64(output, binary.LittleEndian.Uint64(input)*2)
			return 8, nil
		}}
	request, err := r.Bind(ctx, spec, 0)
	if err != nil {
		tb.Fatal(err)
	}
	redis := openRedis(tb, env)
	tb.Cleanup(func() {
		if err := redis.Close(); err != nil {
			tb.Error(err)
		}
	})
	sink := &collectingSink{}
	stop, err := placement.ListenMirrors(ctx, redis, env.prefix+"-bindings", sink, func(_ int, err error) { tb.Error(err) })
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(stop)
	if err := placement.PublishLaneMirrors(ctx, redis, env.prefix+"-bindings", "resource-owner", "local", placement.LaneClassHub,
		[]placement.LaneMirrorUpdate{{Lane: 1, Generation: 1, MaxConcurrency: 4, EwmaNs: 20000, TickSeen: 1}}); err != nil {
		tb.Fatal(err)
	}
	waitForCondition(tb, 5*time.Second, func() bool { return sink.updates() == 1 })
	// The authenticated endpoint is configured locally. Redis never supplies its address.
	if sink.lastUpdate().Lane != 1 {
		tb.Fatal("unexpected owner lane")
	}
	router := grpcsvc.NewRouter()
	if err := router.RegisterFrame(placement.BindingRequestedEvent, placement.BindingFrameHandler(r)); err != nil {
		tb.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	server := grpcsvc.NewServer(router, grpcsvc.ServerOptions{AuthToken: "binding-test-token", MaxMessageBytes: 4096,
		UnaryInterceptors: []grpc.UnaryServerInterceptor{func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
			return next(security.ContextWithOrganizationID(ctx, org), req)
		}}})
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	tb.Cleanup(func() {
		server.Stop()
		select {
		case err := <-finished:
			if err != nil {
				tb.Error(err)
			}
		case <-time.After(2 * time.Second):
			tb.Error("server did not stop")
		}
	})
	opts := grpcsvc.ClientOptions{AuthToken: "binding-test-token", MaxMessageBytes: 4096, DialOptions: []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}}
	conn, err := grpcsvc.Dial(ctx, listener.Addr().String(), opts)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := conn.Close(); err != nil {
			tb.Error(err)
		}
	})
	client := grpcsvc.NewClient(conn, opts)
	dispatch := func(ctx context.Context, frame grpcsvc.Frame) (grpcsvc.Frame, error) {
		return client.DispatchFrame(ctx, frame)
	}
	return ctx, r, request, dispatch
}

func TestServiceBackedResidentBinding(t *testing.T) {
	ctx, r, request, dispatch := serviceBindingFixture(t)
	receipt, output, err := placement.ExecuteRemoteBinding(ctx, dispatch, request, "service-execution")
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint64(output) != 74 || receipt.InputCopiedBytes != 0 || receipt.TransferredBytes != 184 {
		t.Fatalf("result: %+v %x", receipt, output)
	}
	var local [8]byte
	if _, err := r.Execute(ctx, request, "local-oracle", local[:]); err != nil || binary.LittleEndian.Uint64(local[:]) != 74 {
		t.Fatalf("local: %x %v", local, err)
	}
	if _, err := r.PublishResource(ctx, 7, 101, 1, local[:]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := placement.ExecuteRemoteBinding(ctx, dispatch, request, "stale"); !errors.Is(err, placement.ErrBindingStale) {
		t.Fatalf("stale remote: %v", err)
	}
}

func BenchmarkServiceBackedResidentBinding(b *testing.B) {
	ctx, r, request, dispatch := serviceBindingFixture(b)
	var output [8]byte
	b.Run("native", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := r.Execute(ctx, request, "binding-service", output[:]); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("grpc", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := placement.ExecuteRemoteBinding(ctx, dispatch, request, "benchmark"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
