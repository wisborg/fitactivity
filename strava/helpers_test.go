package strava

import (
	"io"
	"strings"
	"sync"
)

func ioPipe() (*io.PipeReader, *io.PipeWriter) { return io.Pipe() }

// syncWriter is a strings.Builder safe to write from the login and read
// from the test at once.
type syncWriter struct {
	mu sync.Mutex
	b  *strings.Builder
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}
