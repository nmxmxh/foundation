package bootstrap

// Services is the project-owned domain service container.
//
// Foundation keeps infrastructure wiring in internal/startup. Application domains
// should extend this type and expose handler registration through AllHandlers.
type Services struct{}

// AllHandlers returns event or route handlers owned by the project.
func (s *Services) AllHandlers() map[string]any {
	return map[string]any{}
}
