// Package bootstrap wires project-owned domain services into foundation dispatch.
package bootstrap

// Services is the project-owned domain service container.
//
// Foundation keeps infrastructure wiring in internal/startup. Application domains
// must extend this type and expose handler registration through AllHandlers and
// AllTypedHandlers. Dynamic handlers are compatibility adapters; typed handlers
// are the default internal command/query contract.
type Services struct{}

// AllHandlers returns event or route handlers owned by the project.
func (s *Services) AllHandlers() map[string]any {
	return map[string]any{}
}
