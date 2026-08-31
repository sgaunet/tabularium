package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/config"
)

// loadBody writes body to a temp config, loads it, and returns the config. Any
// load error is fatal — these tests are about Validate, not about parsing.
func loadBody(t *testing.T, body string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Load(writeConfig(t, dir, body), emptyEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ArchiveRoot == "" {
		cfg.ArchiveRoot = dir
	}
	return cfg
}

func TestValidateTypeVocabulary(t *testing.T) {
	// FR-029: the fallback exists so a document the model cannot place stays
	// RECOGNISABLE as unplaced. A fallback outside the vocabulary would be
	// rejected by the very validation it exists to survive.
	tests := []struct {
		name    string
		types   string
		wantErr string
	}{
		{
			name:  "a fallback that is a member is fine",
			types: "types:\n  fallback: autre\n  values: [facture, autre]\n",
		},
		{
			name:    "a fallback outside the vocabulary is a usage error",
			types:   "types:\n  fallback: inconnu\n  values: [facture, autre]\n",
			wantErr: "inconnu",
		},
		{
			name:    "a missing fallback is a usage error",
			types:   "types:\n  values: [facture, autre]\n",
			wantErr: "fallback",
		},
		{
			name:    "an empty type vocabulary is a usage error",
			types:   "types:\n  fallback: autre\n  values: []\n",
			wantErr: "values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := loadBody(t, "archive_root: "+dir+"\n"+tt.types+
				"rules:\n  - name: divers\n    path: divers\n")

			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error mentioning %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() error = %v; want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAtMostOneDefaultRule(t *testing.T) {
	// FR-038: a second unconditional rule is unreachable. Accepting it would
	// leave the author with a rule that silently never fires.
	dir := t.TempDir()
	cfg := loadBody(t, "archive_root: "+dir+`
types:
  fallback: autre
  values: [facture, autre]
rules:
  - name: divers
    path: "divers/{{.Year}}"
  - name: aussi-divers
    path: "autre/{{.Year}}"
`)

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() with two unconditional rules = nil; want a usage error")
	}
	if !strings.Contains(err.Error(), "aussi-divers") {
		t.Errorf("Validate() error = %v; want it to name the unreachable rule", err)
	}
}

func TestValidateParsesEveryRuleTemplateAtLoad(t *testing.T) {
	// research.md D9: a malformed template is a usage error caught before any
	// document is read — not on the day a document happens to match that rule.
	dir := t.TempDir()
	cfg := loadBody(t, "archive_root: "+dir+`
types:
  fallback: autre
  values: [facture, autre]
rules:
  - name: factures
    type: facture
    path: "factures/{{.Year"
  - name: divers
    path: "divers"
`)

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() with an unclosed template action = nil; want a usage error")
	}
	if !strings.Contains(err.Error(), "factures") {
		t.Errorf("Validate() error = %v; want it to name the rule", err)
	}
}

func TestValidateCompilesRulesInFileOrder(t *testing.T) {
	dir := t.TempDir()
	cfg := loadBody(t, "archive_root: "+dir+`
types:
  fallback: autre
  values: [facture, autre]
rules:
  - name: first
    type: facture
    path: "a"
  - name: second
    type: autre
    path: "b"
  - name: third
    path: "c"
`)

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	got := cfg.RuleSet()
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("RuleSet() has %d rules, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("RuleSet()[%d].Name = %q, want %q", i, got[i].Name, name)
		}
		if got[i].Path == nil {
			t.Errorf("RuleSet()[%d] (%q) has no compiled template", i, name)
		}
	}
}

func TestValidateRejectsARuleWithNoName(t *testing.T) {
	// The rule name is what --dry-run and the sidecar report (FR-043, FR-066).
	// An unnamed rule makes both useless.
	dir := t.TempDir()
	cfg := loadBody(t, "archive_root: "+dir+`
types:
  fallback: autre
  values: [autre]
rules:
  - path: "divers"
`)

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() with an unnamed rule = nil; want a usage error")
	}
}

func TestValidateRejectsADuplicateRuleName(t *testing.T) {
	dir := t.TempDir()
	cfg := loadBody(t, "archive_root: "+dir+`
types:
  fallback: autre
  values: [facture, autre]
rules:
  - name: factures
    type: facture
    path: "a"
  - name: factures
    path: "b"
`)

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() with two rules of the same name = nil; want a usage error")
	}
	if !strings.Contains(err.Error(), "factures") {
		t.Errorf("Validate() error = %v; want it to name the duplicate", err)
	}
}

func TestValidateResolvesTheArchiveRoot(t *testing.T) {
	dir := t.TempDir()
	cfg := loadBody(t, "archive_root: "+dir+`
types:
  fallback: autre
  values: [autre]
rules:
  - name: divers
    path: "divers"
`)

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !filepath.IsAbs(cfg.ArchiveRoot) {
		t.Errorf("archive_root = %q after Validate; want an absolute path", cfg.ArchiveRoot)
	}
}

func TestValidateRejectsAnUnsetArchiveRoot(t *testing.T) {
	cfg := loadBody(t, `
types:
  fallback: autre
  values: [autre]
rules:
  - name: divers
    path: "divers"
`)
	cfg.ArchiveRoot = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() with no archive_root = nil; want a usage error")
	}
}

func TestValidateRejectsAnUnknownDisposition(t *testing.T) {
	dir := t.TempDir()
	cfg := loadBody(t, "archive_root: "+dir+`
disposition: teleport
types:
  fallback: autre
  values: [autre]
rules:
  - name: divers
    path: "divers"
`)

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() with disposition: teleport = nil; want a usage error")
	}
	if !strings.Contains(err.Error(), "teleport") {
		t.Errorf("Validate() error = %v; want it to quote the offending value", err)
	}
}

func TestVocabularyMembership(t *testing.T) {
	types := config.Vocabulary{Fallback: "autre", Values: []string{"facture", "autre"}}

	if !types.Allows("facture") {
		t.Error(`Allows("facture") = false, want true`)
	}
	if types.Allows("invente") {
		t.Error(`Allows("invente") = true; a value outside the vocabulary must be refused`)
	}
	if !types.Bounded() {
		t.Error("Bounded() = false for a vocabulary that declares values")
	}

	// FR-030: an omitted tag vocabulary leaves tags free.
	free := config.Vocabulary{}
	if free.Bounded() {
		t.Error("Bounded() = true for an empty vocabulary; tags must be free when unbounded")
	}
	if !free.Allows("anything-at-all") {
		t.Error("an unbounded vocabulary must allow any value")
	}
}
