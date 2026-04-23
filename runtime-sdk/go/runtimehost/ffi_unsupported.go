//go:build !cgo || (!linux && !darwin)

package runtimehost

import (
	"context"
	"errors"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
)

type FFIPoolOptions struct {
	LibraryPath string
	Workers     int
	Logger      logger.Logger
}

type FFIPool struct{}

func NewFFIPool(FFIPoolOptions) (*FFIPool, error) {
	return nil, errors.Join(ErrFFITransportUnsupported, errors.New("ffi runtime transport is not supported on this build"))
}

func (p *FFIPool) Execute(context.Context, ProcessRequest) (ProcessResponse, error) {
	return ProcessResponse{}, errors.Join(ErrFFITransportUnsupported, errors.New("ffi runtime transport is not supported on this build"))
}

func (p *FFIPool) Close() error {
	return nil
}
