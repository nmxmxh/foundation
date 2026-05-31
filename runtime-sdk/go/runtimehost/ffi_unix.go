//go:build cgo && (linux || darwin)

package runtimehost

/*
#cgo linux LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>

typedef uint32_t (*ovrt_abi_version_fn)(void);
typedef int32_t (*ovrt_create_fn)(uintptr_t, void**, char*, uintptr_t);
typedef void (*ovrt_destroy_fn)(void*);
typedef int32_t (*ovrt_process_fn)(void*, const uint8_t*, uintptr_t, uint8_t*, uintptr_t, char*, uintptr_t);

static uint32_t ovrt_call_abi_version(void* fn) {
	return ((ovrt_abi_version_fn)fn)();
}

static int32_t ovrt_call_create(void* fn, uintptr_t workers, void** host, char* err, uintptr_t err_cap) {
	return ((ovrt_create_fn)fn)(workers, host, err, err_cap);
}

static void ovrt_call_destroy(void* fn, void* host) {
	((ovrt_destroy_fn)fn)(host);
}

static int32_t ovrt_call_process(void* fn, void* host, const uint8_t* unit_id, uintptr_t unit_id_len, uint8_t* buffer, uintptr_t buffer_len, char* err, uintptr_t err_cap) {
	return ((ovrt_process_fn)fn)(host, unit_id, unit_id_len, buffer, buffer_len, err, err_cap);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
)

const ffiABIVersion = 1

type FFIPoolOptions struct {
	LibraryPath string
	Workers     int
	Logger      logger.Logger
}

type FFIPool struct {
	logger logger.Logger

	mu         sync.RWMutex
	bufferPool sync.Pool
	backend    ffiBackend
}

type ffiBackend interface {
	Process(unitID string, buffer []byte, errBuf []byte) (int32, string)
	Close() error
}

type cgoFFIBackend struct {
	library      unsafe.Pointer
	host         unsafe.Pointer
	abiVersionFn unsafe.Pointer
	createFn     unsafe.Pointer
	destroyFn    unsafe.Pointer
	processFn    unsafe.Pointer
}

func NewFFIPool(opts FFIPoolOptions) (*FFIPool, error) {
	if opts.Workers <= 0 {
		opts.Workers = 1
	}
	if opts.Logger == nil {
		opts.Logger, _ = logger.NewDefault()
	}
	opts.Logger = opts.Logger.With("component", "runtime_ffi_pool")
	if opts.LibraryPath == "" {
		return nil, errors.New("runtime ffi library path is required")
	}

	pool := &FFIPool{
		bufferPool: sync.Pool{New: func() any {
			buffer := make([]byte, generated.BUFFER_TOTAL_BYTES)
			return &buffer
		}},
		logger: opts.Logger,
	}
	backend, err := openFFIBackend(opts.LibraryPath, opts.Workers)
	if err != nil {
		return nil, err
	}
	pool.backend = backend
	return pool, nil
}

func (p *FFIPool) Execute(ctx context.Context, req ProcessRequest) (ProcessResponse, error) {
	if p == nil {
		return ProcessResponse{}, errors.New("ffi runtime pool is nil")
	}
	if stringsTrim(req.UnitID) == "" {
		return ProcessResponse{}, errors.New("unit id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ProcessResponse{}, err
	}

	rawPtr := p.bufferPool.Get().(*[]byte)
	raw := *rawPtr
	defer func() {
		*rawPtr = raw
		p.bufferPool.Put(rawPtr)
	}()
	buffer, err := NewBuffer(raw)
	if err != nil {
		return ProcessResponse{}, err
	}
	buffer.Reset()
	buffer.Initialize(req.ModuleVersion)
	if err := buffer.SetHeaderInt(generated.INT_IDX_CONTEXT_HASH, req.ContextHash); err != nil {
		return ProcessResponse{}, err
	}
	if err := buffer.SetInputBytesFast(req.Input); err != nil {
		return ProcessResponse{}, err
	}
	if _, err := buffer.AddEpoch(generated.IDX_INPUT_WRITTEN, 1); err != nil {
		return ProcessResponse{}, err
	}

	backend := p.currentBackend()
	if backend == nil {
		return ProcessResponse{}, errors.New("ffi runtime host is closed")
	}

	errBuf := make([]byte, 4096)
	clear(errBuf)
	status, message := backend.Process(req.UnitID, raw, errBuf)
	if status != 0 {
		if message == "" {
			message = "ffi runtime process failed"
		}
		return ProcessResponse{}, errors.New(message)
	}

	statusCode, err := buffer.HeaderInt(generated.INT_IDX_STATUS_CODE)
	if err != nil {
		return ProcessResponse{}, err
	}
	output, err := buffer.OutputBytesView()
	if err != nil {
		return ProcessResponse{}, err
	}

	response := ProcessResponse{
		Output:      append([]byte(nil), output...),
		Diagnostics: stringsTrim(buffer.DiagnosticsText()),
		StatusCode:  statusCode,
		OutputEpoch: buffer.LoadEpoch(generated.IDX_OUTPUT_WRITTEN),
	}
	_, _ = buffer.AddEpoch(generated.IDX_OUTPUT_CONSUMED, 1)
	if statusCode != 0 {
		if response.Diagnostics == "" {
			response.Diagnostics = "ffi runtime returned non-zero status"
		}
		return response, errors.New(response.Diagnostics)
	}
	return response, nil
}

func (p *FFIPool) Close() error {
	if p == nil {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.backend == nil {
		return nil
	}
	err := p.backend.Close()
	p.backend = nil
	return err
}

func (p *FFIPool) currentBackend() ffiBackend {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.backend
}

func openFFIBackend(library string, workers int) (*cgoFFIBackend, error) {
	libraryPath := C.CString(library)
	defer C.free(unsafe.Pointer(libraryPath))

	handle := C.dlopen(libraryPath, C.RTLD_NOW|C.RTLD_LOCAL)
	if handle == nil {
		return nil, errors.New(dlError("dlopen runtime ffi library"))
	}

	backend := &cgoFFIBackend{library: handle}
	var err error
	if backend.abiVersionFn, err = lookupSymbol(handle, "ovrt_runtime_abi_version"); err != nil {
		_ = backend.Close()
		return nil, err
	}
	if backend.createFn, err = lookupSymbol(handle, "ovrt_runtime_create"); err != nil {
		_ = backend.Close()
		return nil, err
	}
	if backend.destroyFn, err = lookupSymbol(handle, "ovrt_runtime_destroy"); err != nil {
		_ = backend.Close()
		return nil, err
	}
	if backend.processFn, err = lookupSymbol(handle, "ovrt_runtime_process_buffer"); err != nil {
		_ = backend.Close()
		return nil, err
	}

	if version := uint32(C.ovrt_call_abi_version(backend.abiVersionFn)); version != ffiABIVersion {
		_ = backend.Close()
		return nil, fmt.Errorf("runtime ffi abi mismatch: %d != %d", version, ffiABIVersion)
	}

	var host unsafe.Pointer
	errBuf := make([]byte, 4096)
	status := int32(C.ovrt_call_create(
		backend.createFn,
		C.uintptr_t(workers),
		(*unsafe.Pointer)(unsafe.Pointer(&host)),
		(*C.char)(unsafe.Pointer(unsafe.SliceData(errBuf))),
		C.uintptr_t(len(errBuf)),
	))
	if status != 0 {
		_ = backend.Close()
		return nil, errors.New(cStringBytes(errBuf))
	}
	if host == nil {
		_ = backend.Close()
		return nil, errors.New("runtime ffi host returned a nil handle")
	}

	backend.host = host
	return backend, nil
}

func (b *cgoFFIBackend) Process(unitID string, buffer []byte, errBuf []byte) (int32, string) {
	if b == nil || b.host == nil || b.processFn == nil {
		return 1, "ffi runtime host is closed"
	}
	unitIDData := unsafe.StringData(unitID)
	status := int32(C.ovrt_call_process(
		b.processFn,
		b.host,
		(*C.uint8_t)(unsafe.Pointer(unitIDData)),
		C.uintptr_t(len(unitID)),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(buffer))),
		C.uintptr_t(len(buffer)),
		(*C.char)(unsafe.Pointer(unsafe.SliceData(errBuf))),
		C.uintptr_t(len(errBuf)),
	))
	return status, cStringBytes(errBuf)
}

func (b *cgoFFIBackend) Close() error {
	if b == nil {
		return nil
	}
	if b.destroyFn != nil && b.host != nil {
		C.ovrt_call_destroy(b.destroyFn, b.host)
		b.host = nil
	}
	if b.library != nil {
		if result := C.dlclose(b.library); result != 0 {
			return errors.New(dlError("dlclose runtime ffi library"))
		}
		b.library = nil
	}
	b.abiVersionFn = nil
	b.createFn = nil
	b.destroyFn = nil
	b.processFn = nil
	return nil
}

func lookupSymbol(handle unsafe.Pointer, name string) (unsafe.Pointer, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	symbol := C.dlsym(handle, cName)
	if symbol == nil {
		return nil, errors.New(dlError("dlsym " + name))
	}
	return symbol, nil
}

func dlError(action string) string {
	message := C.dlerror()
	if message == nil {
		return action + " failed"
	}
	return action + ": " + C.GoString(message)
}
