package cli_test

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/cli"
	"github.com/sgaunet/tabularium/internal/config"
)

func env(vars map[string]string) config.Env {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

// charDevice opens /dev/null, which is a character device on every platform
// this tool targets. It stands in for a terminal without needing a pty.
func charDevice(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no character device available: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("stat %s: %v", os.DevNull, err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		t.Skipf("%s is not a character device here", os.DevNull)
	}
	return f
}

// pipe is a *os.File that is emphatically not a terminal.
func pipe(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return w
}

func TestDecoratedRequiresBothConditions(t *testing.T) {
	// FR-070: colour and progress need a character device AND an unset
	// NO_COLOR. Conjunction, not disjunction — a piped stdout suppresses them
	// even on a colour-capable terminal, which is the case that actually
	// corrupts someone's data.
	tests := []struct {
		name    string
		writer  func(*testing.T) io.Writer
		environ map[string]string
		want    bool
	}{
		{
			name:    "a character device with NO_COLOR unset decorates",
			writer:  charDeviceWriter,
			environ: nil,
			want:    true,
		},
		{
			name:    "a character device with NO_COLOR set does not",
			writer:  charDeviceWriter,
			environ: map[string]string{"NO_COLOR": "1"},
			want:    false,
		},
		{
			name:    "NO_COLOR set to the empty string still means set",
			writer:  charDeviceWriter,
			environ: map[string]string{"NO_COLOR": ""},
			want:    false,
		},
		{
			name:    "a pipe does not decorate even with NO_COLOR unset",
			writer:  pipeWriter,
			environ: nil,
			want:    false,
		},
		{
			name:    "a pipe with NO_COLOR set certainly does not",
			writer:  pipeWriter,
			environ: map[string]string{"NO_COLOR": "1"},
			want:    false,
		},
		{
			name:    "an in-memory buffer is not a terminal",
			writer:  bufferWriter,
			environ: nil,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cli.Decorated(tt.writer(t), env(tt.environ)); got != tt.want {
				t.Errorf("Decorated() = %v, want %v", got, tt.want)
			}
		})
	}
}

func charDeviceWriter(t *testing.T) io.Writer {
	t.Helper()
	return charDevice(t)
}

func pipeWriter(t *testing.T) io.Writer {
	t.Helper()
	return pipe(t)
}

func bufferWriter(t *testing.T) io.Writer {
	t.Helper()
	return new(strings.Builder)
}
