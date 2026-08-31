package archiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// maxOutput bounds what is kept of the command's output.
//
// 8 KiB is enough for a stack trace or a usage message and not enough for a
// runaway process to fill the sidecar. The output is kept for a human to read
// and is never parsed (FR-057).
const maxOutput = 8 << 10

// defaultTimeout applies when the configuration is silent. Every I/O has an
// explicit timeout, and a subprocess is I/O (FR-075).
const defaultTimeout = 60 * time.Second

// Options describe the external command entirely.
//
// The tool presumes no particular product: the archiver is a black box, and
// everything it needs is here (FR-054).
type Options struct {
	Command     string
	Args        []string
	ArgsEachTag []string
	Timeout     time.Duration

	// Env names environment variables to forward. Credentials reach the
	// command this way and never through argv, which is world-readable through
	// ps and /proc (FR-058, FR-073).
	Env []string

	// Echo receives the command's output as it arrives, one prefixed line at a
	// time (FR-057a). It is the caller's diagnostic stream; nil captures without
	// showing anything, which is what --quiet asks for.
	Echo io.Writer

	Log *slog.Logger
}

// Archiver runs the configured command.
type Archiver struct {
	command     string
	args        []*template.Template
	argsEachTag []*template.Template
	timeout     time.Duration
	env         []string
	echo        io.Writer
	log         *slog.Logger
}

// New compiles the argv templates.
//
// They are parsed here rather than at run time, so a malformed one is a
// configuration error caught before any document is touched. A nil Archiver
// and a nil error mean no archiver is configured, which is not a failure.
func New(o Options) (*Archiver, error) {
	if o.Command == "" {
		return nil, nil //nolint:nilnil // "no archiver configured" is not a failure
	}

	args, err := compile("args", o.Args)
	if err != nil {
		return nil, err
	}
	eachTag, err := compile("args_each_tag", o.ArgsEachTag)
	if err != nil {
		return nil, err
	}

	timeout := o.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Archiver{
		command: o.Command, args: args, argsEachTag: eachTag,
		timeout: timeout, env: o.Env, echo: o.Echo, log: log,
	}, nil
}

func compile(field string, in []string) ([]*template.Template, error) {
	out := make([]*template.Template, 0, len(in))
	for i, text := range in {
		tmpl, err := template.New(fmt.Sprintf("%s[%d]", field, i)).
			Option("missingkey=error").Parse(text)
		if err != nil {
			return nil, fmt.Errorf("archive.%s[%d] %q: %w", field, i, text, err)
		}
		out = append(out, tmpl)
	}
	return out, nil
}

// Command is the configured command, for a message that needs to name it.
func (a *Archiver) Command() string { return a.command }

// Run hands the document to the external command.
//
// Every failure is reported in the Result rather than returned: the caller has
// to record the attempt in the sidecar either way, and an error return would
// make it too easy to file a document and forget to say the hand-off failed.
func (a *Archiver) Run(ctx context.Context, s Subject) Result {
	at := time.Now()
	result := Result{Attempted: true, AttemptedAt: &at}

	// exec.LookPath before the first use, so a missing binary is reported by
	// name rather than as a bare "command not found" (FR-060).
	path, err := exec.LookPath(a.command)
	if err != nil {
		result.Err = a.command + " is not installed or not on PATH"
		return result
	}

	argv, err := a.render(s)
	if err != nil {
		result.Err = err.Error()
		return result
	}

	// The timeout is the archiver's own, and it is bounded independently of the
	// run's overall context so a slow archiver cannot hang the terminal.
	runCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, path, argv...) //nolint:gosec // the command the user configured
	// Built explicitly rather than inherited by leaving Env nil, so what the
	// child receives is a decision this code made and can be read here.
	cmd.Env = a.environ()
	configureProcessGroup(cmd)

	// Captured for the sidecar, and echoed for whoever is watching (FR-057).
	//
	// Both streams get the SAME writer value, not two equivalent ones: os/exec
	// gives them one shared pipe and one copying goroutine only when
	// cmd.Stdout == cmd.Stderr as interface values. Building the MultiWriter
	// twice would start two goroutines racing on the prefixWriter's held line.
	var output bytes.Buffer
	sink := io.Writer(&output)
	var echo *prefixWriter
	if a.echo != nil {
		// The base name, because a command configured by absolute path would
		// otherwise put its whole path in front of every line it writes.
		echo = &prefixWriter{w: a.echo, prefix: filepath.Base(a.command) + "| "}
		sink = io.MultiWriter(&output, echo)
	}
	cmd.Stdout, cmd.Stderr = sink, sink

	a.log.Debug("handing the document to the archiver",
		"command", a.command, "args", len(argv))

	runErr := cmd.Run()
	if echo != nil {
		echo.Flush()
	}
	result.Output = truncate(output.String())

	switch {
	case runErr == nil:
		result.Success = true
		code := 0
		result.ExitCode = &code
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		result.Err = fmt.Sprintf("%s did not finish within %s and its process group was killed",
			a.command, a.timeout)
	case ctx.Err() != nil:
		result.Err = a.command + " was interrupted"
	default:
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			// FR-059: a non-zero exit code is the failure signal, and it is
			// recorded verbatim.
			code := exit.ExitCode()
			result.ExitCode = &code
			result.Err = fmt.Sprintf("%s exited %d", a.command, code)
			return result
		}
		result.Err = fmt.Sprintf("%s: %v", a.command, runErr)
	}
	return result
}

// environ builds the child's environment.
//
// The configured names are re-read from this process's environment and appended
// explicitly. That is what documents which variables the command depends on,
// and it is where a credential travels — never in argv.
func (a *Archiver) environ() []string {
	env := os.Environ()
	for _, name := range a.env {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// render turns the configured templates into an argv.
//
// Each argument is its own template, rendered separately, and becomes a
// distinct element of the slice. No shell is involved, so there are no quoting
// rules to get wrong and nothing a title containing a semicolon can do (FR-055).
func (a *Archiver) render(s Subject) ([]string, error) {
	ctx := s.context()

	argv := make([]string, 0, len(a.args)+len(a.argsEachTag)*len(s.Metadata.Tags))
	for _, tmpl := range a.args {
		rendered, err := execute(tmpl, ctx)
		if err != nil {
			return nil, err
		}
		argv = append(argv, rendered)
	}

	// Repeated once per tag, so every tag arrives as its own argument (FR-055).
	for _, tag := range s.Metadata.Tags {
		for _, tmpl := range a.argsEachTag {
			rendered, err := execute(tmpl, tag)
			if err != nil {
				return nil, err
			}
			argv = append(argv, rendered)
		}
	}
	return argv, nil
}

func execute(tmpl *template.Template, data any) (string, error) {
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("rendering the archiver argument %s: %w", tmpl.Name(), err)
	}
	return b.String(), nil
}
