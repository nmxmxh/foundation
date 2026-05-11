package bootstrap

import kitbootstrap "github.com/nmxmxh/ovasabi_foundation/server-kit/go/bootstrap"

// AllTypedHandlers returns generated protobuf-backed handlers for the internal
// frame plane and registry protobuf dispatch. Every project-owned service with
// an api/protos contract must contribute bindings here.
func (s *Services) AllTypedHandlers() kitbootstrap.TypedServiceHandlers {
	return kitbootstrap.TypedServiceHandlers{}
}

// RegisterTypedPlanes projects one typed binding map into both Foundation
// runtime planes: registry protobuf dispatch for ingress/lifecycle work and
// grpcsvc.Frame dispatch for same-process/internal synchronous calls.
func RegisterTypedPlanes(
	registry kitbootstrap.RegistryAdapter,
	router kitbootstrap.FrameRouterAdapter,
	services *Services,
	opts ...kitbootstrap.ConcurrencyOptions,
) error {
	if services == nil {
		return kitbootstrap.RegisterTypedPlanes(registry, router, nil, opts...)
	}
	return kitbootstrap.RegisterTypedPlanes(registry, router, services.AllTypedHandlers(), opts...)
}
