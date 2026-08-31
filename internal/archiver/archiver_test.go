package archiver_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/analysis"
	"github.com/sgaunet/tabularium/internal/archiver"
)

// recorder is a script that writes its argv, one argument per line, to a file.
// Reading it back is how a test sees exactly what the external command was
// handed — including whether a value arrived as one argument or several.
type recorder struct {
	command string
	argvLog string
	envLog  string
}

func newRecorder(t *testing.T) recorder {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the recorder is a shell script")
	}

	dir := t.TempDir()
	r := recorder{
		command: filepath.Join(dir, "record-argv"),
		argvLog: filepath.Join(dir, "argv.txt"),
		envLog:  filepath.Join(dir, "env.txt"),
	}

	// NUL-separated, because an argument may contain a newline and the whole
	// point of this fixture is to record exactly what arrived.
	script := "#!/bin/sh\n" +
		": > '" + r.argvLog + "'\n" +
		"for a in \"$@\"; do printf '%s\\000' \"$a\" >> '" + r.argvLog + "'; done\n" +
		"env > '" + r.envLog + "'\n" +
		"echo 'Document 4127 created'\n" +
		"exit 0\n"
	if err := os.WriteFile(r.command, []byte(script), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatalf("writing the recorder: %v", err)
	}
	return r
}

// argv is what the command actually received, one element per NUL.
func (r recorder) argv(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(r.argvLog)
	if err != nil {
		t.Fatalf("reading the recorded argv: %v", err)
	}
	trimmed := strings.TrimSuffix(string(body), "\x00")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\x00")
}

func (r recorder) env(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(r.envLog)
	if err != nil {
		t.Fatalf("reading the recorded environment: %v", err)
	}
	return string(body)
}

// subject is a document with every metadata field populated.
func subject(t *testing.T) archiver.Subject {
	t.Helper()
	md, err := analysis.Decode(`{
		"title":"Facture entretien",
		"type":"facture",
		"correspondent":"Garage Central",
		"tags":["voiture","entretien","urgent"],
		"document_date":"2025-03-14",
		"due_date":"2025-04-14",
		"reference":"FA-2025-0312",
		"description":"Révision annuelle.",
		"amount":"384.50",
		"currency":"EUR",
		"filename":"facture-entretien"
	}`, analysis.Vocabularies{})
	if err != nil {
		t.Fatalf("building the metadata fixture: %v", err)
	}
	return archiver.Subject{Path: "/archive/factures/voiture/2025/facture.pdf", Metadata: md}
}

func build(t *testing.T, o archiver.Options) *archiver.Archiver {
	t.Helper()
	a, err := archiver.New(o)
	if err != nil {
		t.Fatalf("archiver.New: %v", err)
	}
	if a == nil {
		t.Fatal("archiver.New returned nil for a configured command")
	}
	return a
}

func TestEachArgumentIsItsOwnElementOfArgv(t *testing.T) {
	// FR-055: each argument is its own template, rendered separately, and
	// passed as a distinct element. No shell means no quoting rules to get
	// wrong — and a title containing a space, a quote or a semicolon is just a
	// title.
	r := newRecorder(t)
	a := build(t, archiver.Options{
		Command: r.command,
		Args: []string{
			"documents", "upload",
			"--title", "{{.Title}}",
			"--correspondent", "{{.Correspondent}}",
			"--created", "{{.DocumentDate}}",
			"{{.Path}}",
		},
	})

	got := a.Run(context.Background(), subject(t))
	if !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}

	want := []string{
		"documents", "upload",
		"--title", "Facture entretien",
		"--correspondent", "Garage Central",
		"--created", "2025-03-14",
		"/archive/factures/voiture/2025/facture.pdf",
	}
	assertArgv(t, r.argv(t), want)
}

func TestAHostileTitleIsStillOneArgument(t *testing.T) {
	// The reason there is no shell: a model's title is arbitrary text, and
	// under a shell every one of these would be a different kind of disaster.
	hostile := []string{
		"Facture; rm -rf /",
		"Facture $(whoami)",
		"Facture `id`",
		`Facture "quoted" and 'quoted'`,
		"Facture && echo pwned",
		"Facture | tee /tmp/pwned",
		"Facture\nwith a newline",
	}

	for i, title := range hostile {
		// The subtest name becomes part of t.TempDir()'s path, so it stays
		// boring; the interesting string is the payload, not the name.
		t.Run("case-"+strconv.Itoa(i), func(t *testing.T) {
			r := newRecorder(t)
			a := build(t, archiver.Options{
				Command: r.command,
				Args:    []string{"--title", "{{.Title}}", "--end"},
			})

			s := subject(t)
			s.Metadata.Title = title

			if got := a.Run(context.Background(), s); !got.Success {
				t.Fatalf("Run() failed for %q: %+v", title, got)
			}

			// Exactly three arguments, and the middle one is the title
			// character for character. Anything a shell would have done —
			// splitting, substituting, redirecting — shows up as a different
			// count or a different string.
			assertArgv(t, r.argv(t), []string{"--title", title, "--end"})
		})
	}
}

func TestEveryTagArrivesAsItsOwnArgument(t *testing.T) {
	// FR-055's "chaque tag lui parvient comme un argument distinct".
	r := newRecorder(t)
	a := build(t, archiver.Options{
		Command:     r.command,
		Args:        []string{"upload", "{{.Path}}"},
		ArgsEachTag: []string{"--tag", "{{.}}"},
	})

	if got := a.Run(context.Background(), subject(t)); !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}

	want := []string{
		"upload", "/archive/factures/voiture/2025/facture.pdf",
		"--tag", "voiture",
		"--tag", "entretien",
		"--tag", "urgent",
	}
	assertArgv(t, r.argv(t), want)
}

func TestNoTagsMeansNoTagArguments(t *testing.T) {
	r := newRecorder(t)
	a := build(t, archiver.Options{
		Command:     r.command,
		Args:        []string{"upload"},
		ArgsEachTag: []string{"--tag", "{{.}}"},
	})

	s := subject(t)
	s.Metadata.Tags = nil

	if got := a.Run(context.Background(), s); !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}
	assertArgv(t, r.argv(t), []string{"upload"})
}

func TestEveryTemplateFieldOfTheContractIsReachable(t *testing.T) {
	// contracts/cli.md names eleven fields. A template referencing one that
	// does not exist fails at render time, so this reaches all of them.
	r := newRecorder(t)
	a := build(t, archiver.Options{
		Command: r.command,
		Args: []string{
			"{{.Path}}", "{{.Title}}", "{{.Type}}", "{{.Correspondent}}",
			"{{.Reference}}", "{{.Description}}", "{{.Amount}}", "{{.Currency}}",
			"{{.DocumentDate}}", "{{.DueDate}}",
		},
	})

	got := a.Run(context.Background(), subject(t))
	if !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}

	want := []string{
		"/archive/factures/voiture/2025/facture.pdf",
		"Facture entretien", "facture", "Garage Central",
		"FA-2025-0312", "Révision annuelle.", "384.50", "EUR",
		"2025-03-14", "2025-04-14",
	}
	assertArgv(t, r.argv(t), want)
}

func TestAnAbsentValueRendersEmptyRatherThanAsTheWordNil(t *testing.T) {
	// A template reaching a nil pointer prints "<nil>", and the external
	// command receives the word — a document dated "<nil>".
	r := newRecorder(t)
	a := build(t, archiver.Options{
		Command: r.command,
		Args:    []string{"--due", "{{.DueDate}}", "--amount", "{{.Amount}}", "--end"},
	})

	s := subject(t)
	s.Metadata.DueDate = nil
	s.Metadata.Amount = nil

	if got := a.Run(context.Background(), s); !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}

	joined := strings.Join(r.argv(t), "|")
	if strings.Contains(joined, "nil") {
		t.Errorf("an absent value rendered as a nil pointer: %q", joined)
	}
	assertArgv(t, r.argv(t), []string{"--due", "", "--amount", "", "--end"})
}

func TestACommandNotOnPathIsReportedByName(t *testing.T) {
	// FR-060: "command not found" leaves the user guessing which command.
	a := build(t, archiver.Options{Command: "tabularium-no-such-archiver"})

	got := a.Run(context.Background(), subject(t))
	if got.Success {
		t.Fatal("Run() succeeded with no such command")
	}
	if !got.Attempted {
		t.Error("Attempted = false; the step did run and did fail")
	}
	if !strings.Contains(got.Err, "tabularium-no-such-archiver") {
		t.Errorf("Err = %q; want it to name the command", got.Err)
	}
	if got.ExitCode != nil {
		t.Errorf("ExitCode = %v; there was no command to produce one", *got.ExitCode)
	}
}

func TestNoCommandConfiguredMeansNoArchiver(t *testing.T) {
	// FR-062: archiving is on by default whenever it is configured, and this is
	// what "not configured" looks like.
	a, err := archiver.New(archiver.Options{})
	if err != nil {
		t.Fatalf("archiver.New: %v", err)
	}
	if a != nil {
		t.Error("archiver.New returned an archiver for an empty command")
	}
}

func TestAMalformedArgumentTemplateIsAConfigurationError(t *testing.T) {
	// Parsed at construction, so a broken template is caught before any
	// document is touched rather than on the day one is archived.
	tests := []struct {
		name string
		opts archiver.Options
	}{
		{
			name: "an unclosed action in args",
			opts: archiver.Options{Command: "true", Args: []string{"{{.Title"}},
		},
		{
			name: "an unclosed action in args_each_tag",
			opts: archiver.Options{Command: "true", ArgsEachTag: []string{"{{."}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := archiver.New(tt.opts); err == nil {
				t.Error("archiver.New() = nil error for a malformed template")
			}
		})
	}
}

func TestATemplateNamingAFieldThatDoesNotExistFails(t *testing.T) {
	// The template context is exactly the contract's eleven fields. There is no
	// .Folder, and asking for one is an error rather than an empty string.
	r := newRecorder(t)
	a := build(t, archiver.Options{Command: r.command, Args: []string{"{{.Folder}}"}})

	got := a.Run(context.Background(), subject(t))
	if got.Success {
		t.Fatal("Run() succeeded with a template naming a field that does not exist")
	}
	if got.Err == "" {
		t.Error("Err is empty; the failure must say what went wrong")
	}
}

// assertArgv compares what the command received against what was expected.
func assertArgv(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("the command received %d arguments, want %d:\ngot  %q\nwant %q",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTheTimeoutIsExplicit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script")
	}
	dir := t.TempDir()
	slow := filepath.Join(dir, "slow")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 60\n"), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatalf("writing the fixture: %v", err)
	}

	a := build(t, archiver.Options{Command: slow, Timeout: 200 * time.Millisecond})

	start := time.Now()
	got := a.Run(context.Background(), subject(t))
	elapsed := time.Since(start)

	if got.Success {
		t.Error("Run() succeeded against a command that never finishes")
	}
	if elapsed > 30*time.Second {
		t.Errorf("Run() took %v; the timeout was not applied", elapsed)
	}
	if !strings.Contains(got.Err, "did not finish") {
		t.Errorf("Err = %q; want it to say the command timed out", got.Err)
	}
}

func TestCancellingTheRunStopsTheArchiver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script")
	}
	dir := t.TempDir()
	slow := filepath.Join(dir, "slow")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 60\n"), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatalf("writing the fixture: %v", err)
	}

	a := build(t, archiver.Options{Command: slow, Timeout: time.Minute})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	got := a.Run(ctx, subject(t))
	if got.Success {
		t.Error("Run() succeeded despite being interrupted")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("Run() took %v after cancellation", elapsed)
	}
}
