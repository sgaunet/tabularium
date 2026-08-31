package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/cli"
)

// configFile writes a minimal valid configuration and returns its path, so a
// run gets past loading and into the part of Run under test.
func configFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "archive_root: " + dir + `
types:
  fallback: autre
  values: [facture, autre]
rules:
  - name: divers
    path: "divers"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

// document copies a corpus file into a temp directory, so a test that files it
// cannot disturb the repository.
func document(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("reading corpus file %s: %v", name, err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestStdoutCarriesNoDiagnostics(t *testing.T) {
	// FR-067, SC-005. Run takes both writers as parameters precisely so this is
	// assertable without capturing process-global state — and so that no
	// package below can reach for os.Stdout in the first place.
	cfg := configFile(t)
	doc := document(t, "notes.txt")

	var stdout, stderr strings.Builder
	_ = cli.Run(context.Background(), []string{"--config", cfg, "--verbose", "--dry-run", doc},
		&stdout, &stderr)

	for _, diagnostic := range []string{"level=", "msg=", "DEBUG", "INFO", "WARN", "ERROR"} {
		if strings.Contains(stdout.String(), diagnostic) {
			t.Errorf("stdout carried the diagnostic marker %q:\n%s", diagnostic, stdout.String())
		}
	}
}

func TestVerboseSetsTheLevelToDebug(t *testing.T) {
	cfg := configFile(t)
	doc := document(t, "notes.txt")

	var stdout, stderr strings.Builder
	_ = cli.Run(context.Background(), []string{"--config", cfg, "--verbose", "--dry-run", doc},
		&stdout, &stderr)

	if !strings.Contains(stderr.String(), "DEBUG") {
		t.Errorf("--verbose produced no debug record on stderr; stderr was:\n%s", stderr.String())
	}
}

func TestQuietSetsTheLevelToError(t *testing.T) {
	cfg := configFile(t)
	doc := document(t, "notes.txt")

	var stdout, stderr strings.Builder
	_ = cli.Run(context.Background(), []string{"--config", cfg, "--quiet", "--dry-run", doc},
		&stdout, &stderr)

	for _, suppressed := range []string{"DEBUG", "INFO", "WARN"} {
		if strings.Contains(stderr.String(), suppressed) {
			t.Errorf("--quiet let a %s record through:\n%s", suppressed, stderr.String())
		}
	}
}

func TestDefaultLevelIsBetweenQuietAndVerbose(t *testing.T) {
	cfg := configFile(t)
	doc := document(t, "notes.md")

	var stdout, stderr strings.Builder
	_ = cli.Run(context.Background(), []string{"--config", cfg, "--dry-run", doc}, &stdout, &stderr)

	if strings.Contains(stderr.String(), "DEBUG") {
		t.Errorf("the default level let a DEBUG record through:\n%s", stderr.String())
	}
}

func TestAMissingDocumentIsAUsageError(t *testing.T) {
	// FR-005: unreadable or non-regular is the user's mistake, not a runtime
	// fault, so it is exit 2 and will fail the same way on every retry.
	cfg := pipelineConfig(t, stubEndpoint(t, metadataReply()))
	missing := filepath.Join(t.TempDir(), "nope.pdf")

	_, err := runWith(t, "--config", cfg, "--dry-run", missing)
	if err == nil {
		t.Fatal("Run() on a missing document = nil error")
	}
	if !isUsage(err) {
		t.Errorf("Run() on a missing document = %v; want a *cli.UsageError", err)
	}
}

func TestANonRegularFileIsAUsageError(t *testing.T) {
	cfg := pipelineConfig(t, stubEndpoint(t, metadataReply()))

	tests := []struct {
		name string
		path func(*testing.T) string
	}{
		{name: "a directory", path: tempDir},
		{name: "a character device", path: func(*testing.T) string { return os.DevNull }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runWith(t, "--config", cfg, "--dry-run", tt.path(t))
			if err == nil {
				t.Fatal("Run() = nil error")
			}
			if !isUsage(err) {
				t.Errorf("Run() = %v; want a *cli.UsageError", err)
			}
		})
	}
}

func tempDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestAnUnsupportedTypeIsAUsageErrorNamingIt(t *testing.T) {
	// FR-004: "unsupported file" tells the user nothing; naming the detected
	// type is what lets them see that their .pdf is actually a zip.
	cfg := pipelineConfig(t, stubEndpoint(t, metadataReply()))
	doc := document(t, "unsupported.bin")

	stderr, err := runWith(t, "--config", cfg, "--dry-run", doc)
	if err == nil {
		t.Fatal("Run() on an unsupported type = nil error")
	}
	if !isUsage(err) {
		t.Errorf("Run() = %v; want a *cli.UsageError", err)
	}
	if !strings.Contains(stderr, "application/octet-stream") {
		t.Errorf("stderr does not name the detected type:\n%s", stderr)
	}
}

func TestAnInvalidConfigurationIsAUsageError(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(bad, []byte("archive_root: "+dir+"\nnot_a_key: 1\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	doc := document(t, "notes.txt")

	_, err := runWith(t, "--config", bad, "--dry-run", doc)
	if err == nil {
		t.Fatal("Run() with an invalid configuration = nil error")
	}
	if !isUsage(err) {
		t.Errorf("Run() with an invalid configuration = %v; want a *cli.UsageError", err)
	}
}

func TestCancellingTheContextStopsTheRun(t *testing.T) {
	// Principle V: the tool is always safe to interrupt.
	cfg := configFile(t)
	doc := document(t, "notes.txt")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stdout, stderr strings.Builder
	err := cli.Run(ctx, []string{"--config", cfg, "--dry-run", doc}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run() with an already-cancelled context = nil error")
	}
}

// runWith is run() with an explicit *testing.T, for the cases that want the
// streams thrown away.
func runWith(t *testing.T, args ...string) (stderr string, err error) {
	t.Helper()
	var out, errOut strings.Builder
	err = cli.Run(context.Background(), args, &out, &errOut)
	return errOut.String(), err
}

func TestAMissingModelConfigurationIsAUsageError(t *testing.T) {
	// The message names the settings to add and will say the same thing on
	// every retry, so it belongs on exit 2 with the other configuration errors
	// rather than on exit 1 with the transient ones.
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	body := "archive_root: " + dir + `
analysis:
  base_url: http://127.0.0.1:1/v1
  model: stub
types:
  fallback: autre
  values: [autre]
rules:
  - name: divers
    path: "divers"
`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	// A scan: it needs a vision model, and no ocr section describes one.
	doc := document(t, "scanned.pdf")

	stderr, err := runWith(t, "--config", cfg, "--dry-run", doc)
	if err == nil {
		t.Fatal("Run() with no vision model configured = nil error")
	}
	if !isUsage(err) {
		t.Errorf("Run() = %v; want a *cli.UsageError so main exits 2", err)
	}
	for _, want := range []string{"ocr.base_url", "ocr.model"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not name %s:\n%s", want, stderr)
		}
	}
}
