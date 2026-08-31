package config_test

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sgaunet/tabularium/internal/config"
)

// stubEnv makes the environment an argument rather than process state, so these
// tests say what they depend on instead of mutating the world.
func stubEnv(vars map[string]string) config.Env {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

func emptyEnv() config.Env { return stubEnv(nil) }

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	return path
}

func TestStrictDecodingRejectsAnUnknownKey(t *testing.T) {
	// FR-072, research.md D14: a typo'd key is a usage error at startup. The
	// failure mode being prevented is a rule that never matches with nothing to
	// explain why.
	dir := t.TempDir()
	path := writeConfig(t, dir, "archive_root: "+dir+"\ndispozition: copy\n")

	_, err := config.Load(path, emptyEnv())
	if err == nil {
		t.Fatal("Load() with a misspelt key = nil error; want a usage error")
	}
	if !strings.Contains(err.Error(), "dispozition") {
		t.Errorf("Load() error = %v; want it to name the offending key", err)
	}
}

func TestLoadAppliesDefaultsWhenTheFileIsSilent(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "archive_root: "+dir+"\n")

	cfg, err := config.Load(path, emptyEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tests := []struct {
		field string
		got   any
		want  any
	}{
		{"disposition", cfg.Disposition, "move"},
		{"ocr.timeout", cfg.OCR.Timeout, 120 * time.Second},
		{"ocr.max_pages", cfg.OCR.MaxPages, 20},
		{"ocr.dpi", cfg.OCR.DPI, 200},
		{"analysis.timeout", cfg.Analysis.Timeout, 120 * time.Second},
		{"analysis.retries", cfg.Analysis.Retries, 3},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want the default %v", tt.field, tt.got, tt.want)
		}
	}
}

func TestResolutionOrderDefaultsFileEnvFlags(t *testing.T) {
	// FR-072. One setting carried through all four layers, because the order is
	// only meaningful when each layer can be seen beating the one below it.
	dir := t.TempDir()

	t.Run("the file beats the defaults", func(t *testing.T) {
		path := writeConfig(t, dir, "archive_root: "+dir+"\ndisposition: copy\n")
		cfg, err := config.Load(path, emptyEnv())
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Disposition != "copy" {
			t.Errorf("disposition = %q, want the file's %q", cfg.Disposition, "copy")
		}
	})

	t.Run("the environment beats the file", func(t *testing.T) {
		path := writeConfig(t, dir, "archive_root: "+dir+"\ndisposition: copy\n")
		env := stubEnv(map[string]string{"TABULARIUM_DISPOSITION": "keep"})
		cfg, err := config.Load(path, env)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Disposition != "keep" {
			t.Errorf("disposition = %q, want the environment's %q", cfg.Disposition, "keep")
		}
	})

	t.Run("a flag beats the environment", func(t *testing.T) {
		path := writeConfig(t, dir, "archive_root: "+dir+"\ndisposition: copy\n")
		env := stubEnv(map[string]string{"TABULARIUM_DISPOSITION": "keep"})
		cfg, err := config.Load(path, env)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		cfg.Apply(overridesFrom(t, []string{"--disposition", "move"}))
		if cfg.Disposition != "move" {
			t.Errorf("disposition = %q, want the flag's %q", cfg.Disposition, "move")
		}
	})
}

// overridesFrom builds the flag layer the way internal/cli does: through
// FlagSet.Visit, which iterates only the flags actually present on the command
// line. Reading the flag variables directly is the bug this guards against.
func overridesFrom(t *testing.T, args []string) config.Overrides {
	t.Helper()

	fs := flag.NewFlagSet("tabularium", flag.ContinueOnError)
	fs.SetOutput(new(strings.Builder))
	disposition := fs.String("disposition", "move", "")
	archiveRoot := fs.String("archive-root", "", "")
	// --output is a flag with no configuration-file counterpart. It has to be
	// declared for the command line to parse, and it maps to no override.
	_ = fs.String("output", "text", "")
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parsing %v: %v", args, err)
	}

	var o config.Overrides
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "disposition":
			o.Disposition = disposition
		case "archive-root":
			o.ArchiveRoot = archiveRoot
		}
	})
	return o
}

func TestAFlagLeftOffDoesNotOverwriteAConfiguredValue(t *testing.T) {
	// research.md D14, the whole reason FlagSet.Visit is mandatory: --disposition
	// defaults to "move". Reading the flag variable directly would silently
	// invert the precedence for every flag the user left off.
	dir := t.TempDir()
	path := writeConfig(t, dir, "archive_root: "+dir+"\ndisposition: copy\n")

	cfg, err := config.Load(path, emptyEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// A command line that mentions no --disposition at all.
	cfg.Apply(overridesFrom(t, []string{"--output", "json"}))

	if cfg.Disposition != "copy" {
		t.Errorf("disposition = %q after a command line with no --disposition; "+
			"want the configured %q to survive", cfg.Disposition, "copy")
	}
}

func TestOverridesLeaveUnmentionedSettingsAlone(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "archive_root: "+dir+"\ndisposition: copy\n")

	cfg, err := config.Load(path, emptyEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Apply(config.Overrides{})

	if cfg.Disposition != "copy" || cfg.ArchiveRoot != dir {
		t.Errorf("an empty Overrides changed the config: disposition=%q archive_root=%q",
			cfg.Disposition, cfg.ArchiveRoot)
	}
}

func TestEnvironmentBindings(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "archive_root: /will/be/replaced\n")

	env := stubEnv(map[string]string{
		"TABULARIUM_ARCHIVE_ROOT":     dir,
		"TABULARIUM_OCR_MODEL":        "llava:13b",
		"TABULARIUM_OCR_BASE_URL":     "http://ocr.example/v1",
		"TABULARIUM_OCR_MAX_PAGES":    "5",
		"TABULARIUM_ANALYSIS_MODEL":   "mistral:7b",
		"TABULARIUM_ANALYSIS_TIMEOUT": "30s",
		"TABULARIUM_ANALYSIS_RETRIES": "1",
		"TABULARIUM_ARCHIVE_COMMAND":  "my-archiver",
	})

	cfg, err := config.Load(path, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tests := []struct {
		field string
		got   any
		want  any
	}{
		{"archive_root", cfg.ArchiveRoot, dir},
		{"ocr.model", cfg.OCR.Model, "llava:13b"},
		{"ocr.base_url", cfg.OCR.BaseURL, "http://ocr.example/v1"},
		{"ocr.max_pages", cfg.OCR.MaxPages, 5},
		{"analysis.model", cfg.Analysis.Model, "mistral:7b"},
		{"analysis.timeout", cfg.Analysis.Timeout, 30 * time.Second},
		{"analysis.retries", cfg.Analysis.Retries, 1},
		{"archive.command", cfg.Archive.Command, "my-archiver"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v from the environment", tt.field, tt.got, tt.want)
		}
	}
}

func TestEnvironmentValueThatWillNotParseIsAUsageError(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "archive_root: "+dir+"\n")
	env := stubEnv(map[string]string{"TABULARIUM_OCR_MAX_PAGES": "many"})

	_, err := config.Load(path, env)
	if err == nil {
		t.Fatal("Load() with an unparseable environment value = nil error")
	}
	if !strings.Contains(err.Error(), "TABULARIUM_OCR_MAX_PAGES") {
		t.Errorf("Load() error = %v; want it to name the variable", err)
	}
}

func TestAConfigFileGivenExplicitlyMustExist(t *testing.T) {
	// Silently falling back to defaults when --config names a file that is not
	// there would hide a typo in the one argument the user was most deliberate
	// about.
	missing := filepath.Join(t.TempDir(), "nope.yaml")

	_, err := config.Load(missing, emptyEnv())
	if err == nil {
		t.Fatal("Load() on a missing explicit path = nil error; want a usage error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("Load() error = %v; want it to name the path", err)
	}
}

func TestDefaultPathPrefersXDGConfigHome(t *testing.T) {
	env := stubEnv(map[string]string{"XDG_CONFIG_HOME": "/xdg"})

	got, err := config.DefaultPath(env)
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join("/xdg", "tabularium", "config.yaml")
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultPathFallsBackToDotConfig(t *testing.T) {
	// With XDG_CONFIG_HOME unset, the file belongs beside the user's other
	// hand-edited configuration. The platform's own directory is not used:
	// os.UserConfigDir would send macOS to ~/Library/Application Support, which
	// is not where anyone puts a YAML file they edit by hand.
	if runtime.GOOS == "windows" {
		t.Skip("HOME is not the home-directory variable on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := config.DefaultPath(emptyEnv())
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(home, ".config", "tabularium", "config.yaml")
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestNoConfigAtTheDefaultLocationNamesThePath(t *testing.T) {
	// archive_root has no default and Validate rejects an unset one, so a run
	// with no configuration was always going to fail. It should fail saying a
	// configuration file is missing and where, rather than two steps later
	// complaining about archive_root.
	if runtime.GOOS == "windows" {
		t.Skip("HOME is not the home-directory variable on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := config.Load("", emptyEnv())
	if err == nil {
		t.Fatal("Load(\"\") with no configuration anywhere = nil error; want a usage error")
	}
	var notFound *config.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Load() error = %v (%T); want a *config.NotFoundError", err, err)
	}
	want := filepath.Join(home, ".config", "tabularium", "config.yaml")
	if !strings.Contains(err.Error(), want) {
		t.Errorf("Load() error = %v; want it to name %q", err, want)
	}
}

func TestTheUsersRulesReplaceTheStarterRulesRatherThanAppending(t *testing.T) {
	// If a user's rules were appended to the embedded starter's, the starter's
	// default rule would sit in front of every rule the user wrote after it and
	// nothing below would ever be reached.
	dir := t.TempDir()
	path := writeConfig(t, dir, `
archive_root: `+dir+`
rules:
  - name: only-mine
    path: "mine/{{.Year}}"
`)

	cfg, err := config.Load(path, emptyEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].Name != "only-mine" {
		t.Fatalf("rules = %+v; want exactly the one rule the user declared", cfg.Rules)
	}
}

func TestStarterConfigIsValid(t *testing.T) {
	// Principle VII: the starter is embedded, so it can be checked here rather
	// than trusted. A starter that does not load is a broken first run.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, config.Starter(), 0o600); err != nil {
		t.Fatalf("writing the starter: %v", err)
	}

	cfg, err := config.Load(path, emptyEnv())
	if err != nil {
		t.Fatalf("the embedded starter config does not load: %v", err)
	}
	cfg.ArchiveRoot = dir // the starter points at the user's home; not this test's business
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the embedded starter config does not validate: %v", err)
	}
}
