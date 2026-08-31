package archiver

import (
	"bytes"
	"io"
)

// prefixWriter forwards whole lines, marking each with the command's name.
//
// The archiver's output lands on the same stream as our own diagnostics, and a
// reader has to be able to tell them apart at a glance: "unknown flag: --created"
// alone reads like tabularium's complaint, "archivis| unknown flag: --created"
// does not.
//
// Lines are held until their newline arrives, so a command that writes a line in
// several pieces still produces one prefixed line rather than three.
type prefixWriter struct {
	w      io.Writer
	prefix string
	buf    []byte
}

// Write never reports an error.
//
// The echo is a courtesy to whoever is watching: a terminal that has gone away,
// or a stderr closed by the caller, must not turn into a failed hand-off. Nothing
// is lost by staying quiet either — the same bytes are captured into
// Result.Output and reach the sidecar regardless.
func (p *prefixWriter) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		p.emit(p.buf[:i])
		p.buf = p.buf[i+1:]
	}
	return len(b), nil
}

// Flush emits a last line the command left without a newline. A usage message
// that ends bare is exactly the kind a failing command produces, and it is the
// line the user most needs.
func (p *prefixWriter) Flush() {
	if len(p.buf) > 0 {
		p.emit(p.buf)
		p.buf = nil
	}
}

func (p *prefixWriter) emit(line []byte) {
	// A carriage return from a command that draws a progress bar would otherwise
	// park the cursor back over our prefix.
	line = bytes.TrimSuffix(line, []byte("\r"))
	_, _ = p.w.Write(append(append([]byte(p.prefix), line...), '\n'))
}
