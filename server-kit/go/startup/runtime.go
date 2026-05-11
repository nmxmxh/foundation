package startup

import (
	"errors"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	eventcontract "github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

type CloseFunc func() error

type Runtime struct {
	Logger      logger.Logger
	Registry    *registry.ServiceRegistry
	FrameRouter *grpcsvc.Router
	Bus         eventcontract.Bus
	Redis       redis.Client
	Handler     *graceful.Handler
	JWT         *auth.JWTManager
	RBAC        *security.Authorizer

	closeFns []CloseFunc
}

type Options struct {
	Service       string
	Version       string
	Logger        logger.Logger
	Registry      *registry.ServiceRegistry
	FrameRouter   *grpcsvc.Router
	Bus           eventcontract.Bus
	Redis         redis.Client
	BusCloser     CloseFunc
	EventEmitter  graceful.EventEmitter
	Scheduler     graceful.Scheduler
	Cache         graceful.Cache
	EventEnabled  bool
	JWTSecret     string
	JWTManager    *auth.JWTManager
	Authorizer    *security.Authorizer
	RoleTemplates []security.RoleTemplate
}

func NewRuntime(opts Options) (*Runtime, error) {
	service := strings.TrimSpace(opts.Service)
	if service == "" {
		return nil, errors.New("service is required")
	}

	l := opts.Logger
	if l == nil {
		var err error
		l, err = logger.NewDefault()
		if err != nil {
			return nil, err
		}
	}

	jwtManager := opts.JWTManager
	if jwtManager == nil {
		if strings.TrimSpace(opts.JWTSecret) == "" {
			return nil, errors.New("jwt secret is required when JWT manager is not provided")
		}
		created, err := auth.NewJWTManager(opts.JWTSecret)
		if err != nil {
			return nil, err
		}
		jwtManager = created
	}

	authorizer := opts.Authorizer
	if authorizer == nil {
		authorizer = security.NewAuthorizer(opts.RoleTemplates)
	}

	eventEnabled := opts.EventEnabled
	eventEmitter := opts.EventEmitter
	if eventEmitter == nil && opts.Redis != nil {
		eventEmitter = graceful.NewRedisEventEmitter(eventcontract.Bus(opts.Bus)) // Fallback to redis
		eventEnabled = true
	}

	handler := graceful.NewHandler(
		graceful.WithLogger(l),
		graceful.WithService(service),
		graceful.WithVersion(normalizeVersion(opts.Version)),
		graceful.WithEventEnabled(eventEnabled),
		graceful.WithEventEmitter(eventEmitter),
		graceful.WithScheduler(opts.Scheduler),
		graceful.WithCache(opts.Cache),
	)

	reg := opts.Registry
	if reg == nil {
		reg = registry.New(opts.Redis, handler, l)
	}
	frameRouter := opts.FrameRouter
	if frameRouter == nil {
		frameRouter = grpcsvc.NewRouter()
	}

	runtime := &Runtime{
		Logger:      l,
		Registry:    reg,
		FrameRouter: frameRouter,
		Bus:         opts.Bus,
		Redis:       opts.Redis,
		Handler:     handler,
		JWT:         jwtManager,
		RBAC:        authorizer,
		closeFns:    make([]CloseFunc, 0, 4),
	}
	if opts.BusCloser != nil {
		runtime.AddCloser(opts.BusCloser)
	}
	return runtime, nil
}

func (r *Runtime) AddCloser(fn CloseFunc) {
	if r == nil || fn == nil {
		return
	}
	r.closeFns = append(r.closeFns, fn)
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	var firstErr error
	for i := len(r.closeFns) - 1; i >= 0; i-- {
		if err := r.closeFns[i](); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return "v1"
	}
	return version
}
