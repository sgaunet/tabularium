package config_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/sgaunet/tabularium/internal/config"
)

// canary is the value SC-008 hunts for. If it appears anywhere in any rendering
// of a Config, redaction has failed.
const canary = "sk-canary-3f9a17c4e8b2"

func TestCredentialRedactsInEveryRendering(t *testing.T) {
	c := config.Credential(canary)

	renderings := map[string]func() string{
		"String()": c.String,
		"%v":       func() string { return fmt.Sprintf("%v", c) },
		"%s":       func() string { return fmt.Sprintf("key=%s", c) },
		"%q":       func() string { return fmt.Sprintf("%q", c) },
		"%#v":      func() string { return fmt.Sprintf("%#v", c) },
		"MarshalJSON": func() string {
			b, err := json.Marshal(c)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			return string(b)
		},
		"MarshalYAML": func() string {
			b, err := yaml.Marshal(c)
			if err != nil {
				t.Fatalf("yaml.Marshal: %v", err)
			}
			return string(b)
		},
	}

	for name, render := range renderings {
		t.Run(name, func(t *testing.T) {
			got := render()
			if strings.Contains(got, canary) {
				t.Fatalf("%s leaked the credential: %s", name, got)
			}
			if !strings.Contains(got, "[redacted]") {
				t.Errorf("%s = %s; want it to say [redacted]", name, got)
			}
		})
	}
}

func TestCredentialSecretIsTheOnlyWayOut(t *testing.T) {
	c := config.Credential(canary)
	if got := c.Secret(); got != canary {
		t.Errorf("Secret() = %q, want the credential back verbatim", got)
	}
	if config.Credential("").Secret() != "" {
		t.Error("Secret() on an unset credential must be empty, not the redaction text")
	}
}

// TestWholeConfigRedacts is the test that matters for SC-008: a credential must
// survive no rendering of the struct that contains it, because the leak that
// actually happens is a %v of a whole config in a debug log.
func TestWholeConfigRedacts(t *testing.T) {
	cfg := &config.Config{
		ArchiveRoot: "/tmp/archive",
		OCR:         config.OCR{BaseURL: "http://localhost:11434/v1", APIKey: config.Credential(canary)},
		Analysis:    config.Analysis{BaseURL: "http://localhost:11434/v1", APIKey: config.Credential(canary)},
	}

	renderings := map[string]func() string{
		"%v of the struct":  func() string { return fmt.Sprintf("%v", *cfg) },
		"%+v of the struct": func() string { return fmt.Sprintf("%+v", *cfg) },
		"%v of the pointer": func() string { return fmt.Sprintf("%v", cfg) },
		"json.Marshal": func() string {
			b, err := json.Marshal(cfg)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			return string(b)
		},
		"yaml.Marshal": func() string {
			b, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatalf("yaml.Marshal: %v", err)
			}
			return string(b)
		},
	}

	for name, render := range renderings {
		t.Run(name, func(t *testing.T) {
			if got := render(); strings.Contains(got, canary) {
				t.Fatalf("%s leaked the credential:\n%s", name, got)
			}
		})
	}
}

func TestCredentialFromTheEnvironmentIsRedactedToo(t *testing.T) {
	// The credential a run actually carries arrives from the environment named
	// by api_key_env. It must be the same redacting type, not a bare string.
	env := stubEnv(map[string]string{"MY_OCR_KEY": canary})
	dir := t.TempDir()
	path := writeConfig(t, dir, `
archive_root: `+dir+`
ocr:
  api_key_env: MY_OCR_KEY
`)

	cfg, err := config.Load(path, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.OCR.APIKey.Secret(); got != canary {
		t.Fatalf("OCR.APIKey.Secret() = %q, want the value of MY_OCR_KEY", got)
	}
	if got := fmt.Sprintf("%v", *cfg); strings.Contains(got, canary) {
		t.Fatalf("a config loaded from the environment leaked the credential:\n%s", got)
	}
}
