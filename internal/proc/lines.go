package proc

import (
	"bytes"
	"strings"
	"sync"
)

// lineWriter turns a stream of arbitrary chunks into whole lines.
//
// A child's output arrives in whatever sizes the pipe hands over, so a line
// can be split across two writes. Splitting each chunk on newline — which is
// what the PHP does — emits those halves as separate lines. That was harmless
// while the log was a transcript and is not now: half a line on a live status
// line is a flicker, and the tail of it never arrives because the next write
// overwrites it.
type lineWriter struct {
	mu        sync.Mutex
	residual  []byte
	emit      func(string)
	redactor  redactor
	maxBuffer int
}

type redactor interface{ Redact(string) string }

// maxLineBuffer bounds what is held waiting for a newline. A child emitting
// megabytes without one — a progress bar redrawing with carriage returns, or
// binary — must not grow this without limit.
const maxLineBuffer = 64 * 1024

func (w *lineWriter) Write(p []byte) (int, error) {
	if w.emit == nil {
		return len(p), nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.residual = append(w.residual, p...)
	for {
		index := bytes.IndexByte(w.residual, '\n')
		if index < 0 {
			break
		}
		w.send(string(w.residual[:index]))
		w.residual = w.residual[index+1:]
	}

	if len(w.residual) > maxLineBuffer {
		w.send(string(w.residual))
		w.residual = nil
	}

	return len(p), nil
}

// flush emits whatever never got a newline, which is how a prompt or a
// progress line that ends without one still reaches the log.
func (w *lineWriter) flush() {
	if w.emit == nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.residual) > 0 {
		w.send(string(w.residual))
		w.residual = nil
	}
}

func (w *lineWriter) send(line string) {
	line = strings.TrimRight(line, "\r")
	if w.redactor != nil {
		line = w.redactor.Redact(line)
	}

	// Indented, so child output is distinguishable from upkeep's own.
	w.emit("  " + line)
}
