// Package bootstrap wires project-owned domain services into foundation dispatch.
package bootstrap

// Services is the project-owned domain service container.
//
// Foundation keeps infrastructure wiring in internal/startup. Application domains
// must extend this type and expose handler registration through AllHandlers using
// Foundation handler types. Do not add local wrapper or adapter layers here.
type Services struct{}

// AllHandlers returns event or route handlers owned by the project.
func (s *Services) AllHandlers() map[string]any {
	return map[string]any{}
}
