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
	"go.uber.org/zap"
)

const ffiABIVersion = 1

type FFIPoolOptions struct {
	LibraryPath string
	Workers     int
	Logger      logger.Logger
}

type FFIPool struct {
	logger logger.Logger

	mu           sync.RWMutex
	bufferPool   sync.Pool
	errBufPool   sync.Pool
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
		opts.Logger = opts.Logger.With(zap.String("component", "runtime_ffi_pool"))
	}
	if opts.LibraryPath == "" {
		return nil, errors.New("runtime ffi library path is required")
	}

	libraryPath := C.CString(opts.LibraryPath)
	defer C.free(unsafe.Pointer(libraryPath))

	handle := C.dlopen(libraryPath, C.RTLD_NOW|C.RTLD_LOCAL)
	if handle == nil {
		return nil, errors.New(dlError("dlopen runtime ffi library"))
	}

	pool := &FFIPool{
		logger:  opts.Logger,
		library: handle,
		bufferPool: sync.Pool{New: func() any {
			buffer := make([]byte, generated.BUFFER_TOTAL_BYTES)
			return &buffer
		}},
		errBufPool: sync.Pool{New: func() any {
			buffer := make([]byte, 4096)
			return &buffer
		}},
	}

	var err error
	if pool.abiVersionFn, err = lookupSymbol(handle, "ovrt_runtime_abi_version"); err != nil {
		_ = pool.Close()
		return nil, err
	}
	if pool.createFn, err = lookupSymbol(handle, "ovrt_runtime_create"); err != nil {
		_ = pool.Close()
		return nil, err
	}
	if pool.destroyFn, err = lookupSymbol(handle, "ovrt_runtime_destroy"); err != nil {
		_ = pool.Close()
		return nil, err
	}
	if pool.processFn, err = lookupSymbol(handle, "ovrt_runtime_process_buffer"); err != nil {
		_ = pool.Close()
		return nil, err
	}

	if version := uint32(C.ovrt_call_abi_version(pool.abiVersionFn)); version != ffiABIVersion {
		_ = pool.Close()
		return nil, fmt.Errorf("runtime ffi abi mismatch: %d != %d", version, ffiABIVersion)
	}

	var host unsafe.Pointer
	errBuf := make([]byte, 4096)
	status := int32(C.ovrt_call_create(
		pool.createFn,
		C.uintptr_t(opts.Workers),
		(*unsafe.Pointer)(unsafe.Pointer(&host)),
		(*C.char)(unsafe.Pointer(unsafe.SliceData(errBuf))),
		C.uintptr_t(len(errBuf)),
	))
	if status != 0 {
		_ = pool.Close()
		return nil, errors.New(cStringBytes(errBuf))
	}
	if host == nil {
		_ = pool.Close()
		return nil, errors.New("runtime ffi host returned a nil handle")
	}

	pool.host = host
	return pool, nil
}

func (p *FFIPool) Execute(ctx context.Context, req ProcessRequest) (ProcessResponse, error) {
	if p == nil {
		return ProcessResponse{}, errors.New("ffi runtime pool is nil")
	}
	if stringsTrim(req.UnitID) == "" {
		return ProcessResponse{}, errors.New("unit id is required")
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
	if err := buffer.Initialize(req.ModuleVersion); err != nil {
		return ProcessResponse{}, err
	}
	if err := buffer.SetHeaderInt(generated.INT_IDX_CONTEXT_HASH, req.ContextHash); err != nil {
		return ProcessResponse{}, err
	}
	if err := buffer.SetInputBytesFast(req.Input); err != nil {
		return ProcessResponse{}, err
	}
	if _, err := buffer.AddEpoch(generated.IDX_INPUT_WRITTEN, 1); err != nil {
		return ProcessResponse{}, err
	}

	p.mu.RLock()
	host := p.host
	processFn := p.processFn
	p.mu.RUnlock()
	if host == nil || processFn == nil {
		return ProcessResponse{}, errors.New("ffi runtime host is closed")
	}

	errBufPtr := p.errBufPool.Get().(*[]byte)
	errBuf := *errBufPtr
	defer func() {
		*errBufPtr = errBuf
		p.errBufPool.Put(errBufPtr)
	}()
	clear(errBuf)
	unitID := unsafe.StringData(req.UnitID)
	status := int32(C.ovrt_call_process(
		processFn,
		host,
		(*C.uint8_t)(unsafe.Pointer(unitID)),
		C.uintptr_t(len(req.UnitID)),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(raw))),
		C.uintptr_t(len(raw)),
		(*C.char)(unsafe.Pointer(unsafe.SliceData(errBuf))),
		C.uintptr_t(len(errBuf)),
	))
	if status != 0 {
		message := cStringBytes(errBuf)
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

	if p.destroyFn != nil && p.host != nil {
		C.ovrt_call_destroy(p.destroyFn, p.host)
		p.host = nil
	}
	if p.library != nil {
		if result := C.dlclose(p.library); result != 0 {
			return errors.New(dlError("dlclose runtime ffi library"))
		}
		p.library = nil
	}
	p.abiVersionFn = nil
	p.createFn = nil
	p.destroyFn = nil
	p.processFn = nil
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

func cStringBytes(raw []byte) string {
	for index, value := range raw {
		if value == 0 {
			return string(raw[:index])
		}
	}
	return string(raw)
}

func stringsTrim(value string) string {
	start := 0
	for start < len(value) && (value[start] == ' ' || value[start] == '\n' || value[start] == '\t' || value[start] == '\r') {
		start++
	}
	end := len(value)
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\t' || value[end-1] == '\r') {
		end--
	}
	return value[start:end]
}
