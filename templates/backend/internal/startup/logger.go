// Package startup initializes infrastructure dependencies for the application.
package startup

import (
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
)

// NewLogger installs the Foundation logger with application runtime scope.
func NewLogger(env, level string) logger.Logger {
	return logger.Install(logger.RuntimeConfig(env, level, "{{PROJECT_NAME}}", "startup"))
}
