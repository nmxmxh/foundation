package connector

import (
	"errors"
	"io"
)

// isEOF reports whether err signals a normal end-of-stream. Drivers should
// return io.EOF; some wrap it, so errors.Is is used.
func isEOF(err error) bool {
	return errors.Is(err, io.EOF)
}
