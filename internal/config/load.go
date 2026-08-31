package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/goccy/go-yaml"
)

// Env looks up an environment variable. os.LookupEnv satisfies it directly;
// taking it as a parameter is what lets a test say which environment it depends
// on rather than mutating the one it runs in.
type Env func(key string) (string, bool)

// defaults are the code-level defaults: the bottom layer of the precedence
// order, and deliberately not the embedded starter.
//
// The starter names an archiver, a model, and a full set of filing rules. Those
// are good things to start from and bad things to inherit silently: a fresh
// install must not shell out to a command the user never configured, so the
// defaults stop at the scalars that are safe to assume.
func defaults() *Config {
	return &Config{
		Disposition: "move",
		OCR: OCR{
			Timeout:  120 * time.Second,
			MaxPages: 20,
			DPI:      200,
		},
		Analysis: Analysis{
			Timeout: 120 * time.Second,
			Retries: 3,
		},
	}
}

// DefaultPath is where the configuration lives when --config was not given:
// $XDG_CONFIG_HOME/tabularium/config.yaml, falling back to the platform's own
// configuration directory.
func DefaultPath(env Env) (string, error) {
	if dir, ok := env("XDG_CONFIG_HOME"); ok && dir != "" {
		return filepath.Join(dir, "tabularium", "config.yaml"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating the configuration directory: %w", err)
	}
	return filepath.Join(dir, "tabularium", "config.yaml"), nil
}

// Load resolves the configuration from defaults, then the file, then the
// environment. The flag layer is applied afterwards by the caller, with Apply.
//
// An empty path means the default location, where a missing file is not an
// error. A path given explicitly must exist: silently falling back to defaults
// would hide a typo in the one argument the user was most deliberate about.
func Load(path string, env Env) (*Config, error) {
	cfg := defaults()

	explicit := path != ""
	if !explicit {
		var err error
		if path, err = DefaultPath(env); err != nil {
			return nil, err
		}
	}

	body, err := os.ReadFile(path) //nolint:gosec // the path is the user's own configuration
	switch {
	case err == nil:
		if err := yaml.UnmarshalWithOptions(body, cfg, yaml.Strict()); err != nil {
			return nil, fmt.Errorf("reading configuration %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist) && !explicit:
		// No configuration yet. The defaults stand alone, and Validate will say
		// what is still missing.
	default:
		return nil, fmt.Errorf("reading configuration %s: %w", path, err)
	}

	if err := applyEnv(cfg, env); err != nil {
		return nil, err
	}
	resolveCredentials(cfg, env)
	return cfg, nil
}

// binding maps one environment variable onto one setting.
//
// The table is written out rather than derived from the struct tags because the
// derivation is ambiguous: TABULARIUM_ARCHIVE_ROOT is the top-level
// archive_root, while TABULARIUM_ARCHIVE_COMMAND is archive.command. An
// explicit list is shorter than the rule that would disambiguate them.
type binding struct {
	name string
	set  func(*Config, string) error
}

func envBindings() []binding {
	return []binding{
		{"TABULARIUM_ARCHIVE_ROOT", func(c *Config, v string) error { c.ArchiveRoot = v; return nil }},
		{"TABULARIUM_DISPOSITION", func(c *Config, v string) error { c.Disposition = v; return nil }},

		{"TABULARIUM_OCR_BASE_URL", func(c *Config, v string) error { c.OCR.BaseURL = v; return nil }},
		{"TABULARIUM_OCR_MODEL", func(c *Config, v string) error { c.OCR.Model = v; return nil }},
		{"TABULARIUM_OCR_TIMEOUT", func(c *Config, v string) error { return setDuration(&c.OCR.Timeout, v) }},
		{"TABULARIUM_OCR_MAX_PAGES", func(c *Config, v string) error { return setInt(&c.OCR.MaxPages, v) }},
		{"TABULARIUM_OCR_DPI", func(c *Config, v string) error { return setInt(&c.OCR.DPI, v) }},

		{"TABULARIUM_ANALYSIS_BASE_URL", func(c *Config, v string) error { c.Analysis.BaseURL = v; return nil }},
		{"TABULARIUM_ANALYSIS_MODEL", func(c *Config, v string) error { c.Analysis.Model = v; return nil }},
		{"TABULARIUM_ANALYSIS_TIMEOUT", func(c *Config, v string) error { return setDuration(&c.Analysis.Timeout, v) }},
		{"TABULARIUM_ANALYSIS_RETRIES", func(c *Config, v string) error { return setInt(&c.Analysis.Retries, v) }},

		{"TABULARIUM_ARCHIVE_COMMAND", func(c *Config, v string) error { c.Archive.Command = v; return nil }},
		{"TABULARIUM_ARCHIVE_TIMEOUT", func(c *Config, v string) error { return setDuration(&c.Archive.Timeout, v) }},
	}
}

func applyEnv(cfg *Config, env Env) error {
	for _, b := range envBindings() {
		v, ok := env(b.name)
		if !ok || v == "" {
			continue
		}
		if err := b.set(cfg, v); err != nil {
			return fmt.Errorf("%s: %w", b.name, err)
		}
	}
	return nil
}

func setInt(dst *int, v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("want a whole number, got %q", v)
	}
	*dst = n
	return nil
}

func setDuration(dst *time.Duration, v string) error {
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("want a duration such as 120s, got %q", v)
	}
	*dst = d
	return nil
}

// resolveCredentials reads each API key from the environment variable the
// configuration names.
//
// The dedicated TABULARIUM_*_API_KEY variables are honoured first, so the
// documented names work with no configuration at all; api_key_env then covers
// the case of a credential the user keeps under a name of their own. A key
// written into the file itself wins over neither — it is the layer below both.
func resolveCredentials(cfg *Config, env Env) {
	resolve := func(dst *Credential, standard, named string) {
		if v, ok := env(standard); ok && v != "" {
			*dst = Credential(v)
			return
		}
		if named == "" {
			return
		}
		if v, ok := env(named); ok && v != "" {
			*dst = Credential(v)
		}
	}
	resolve(&cfg.OCR.APIKey, "TABULARIUM_OCR_API_KEY", cfg.OCR.APIKeyEnv)
	resolve(&cfg.Analysis.APIKey, "TABULARIUM_ANALYSIS_API_KEY", cfg.Analysis.APIKeyEnv)
}
