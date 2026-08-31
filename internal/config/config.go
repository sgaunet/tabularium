package config

import (
	_ "embed"
	"slices"
	"time"

	"github.com/sgaunet/tabularium/internal/rules"
)

// starter is the commented configuration a user starts from. Embedding it is
// what keeps the binary self-contained: it never depends on a file sitting
// beside it (Principle VII).
//
//go:embed default.yaml
var starter []byte

// Starter returns the embedded starter configuration, verbatim.
func Starter() []byte { return slices.Clone(starter) }

// Credential holds a secret. Every way of rendering it says "[redacted]", and
// the only way to the value is Secret.
//
// This is deliberately a type and not a convention: the leak that actually
// happens is a %v of a whole config struct in a debug log, and no amount of
// reviewer discipline catches that reliably. Making redaction a property of the
// type is what turns SC-008 into something the compiler helps with.
type Credential string

// String implements fmt.Stringer, which is what redacts the value inside a %v
// of any struct that contains it.
func (c Credential) String() string { return "[redacted]" }

// GoString implements fmt.GoStringer, so %#v redacts as well.
func (c Credential) GoString() string { return `"[redacted]"` }

// MarshalJSON implements json.Marshaler.
func (c Credential) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }

// MarshalYAML implements the yaml interface marshaler.
func (c Credential) MarshalYAML() (any, error) { return "[redacted]", nil }

// Secret returns the credential itself. Every call site is a place to check.
func (c Credential) Secret() string { return string(c) }

// OCR configures the endpoint that transcribes rasterised pages.
type OCR struct {
	BaseURL   string        `yaml:"base_url"`
	Model     string        `yaml:"model"`
	Timeout   time.Duration `yaml:"timeout"`
	MaxPages  int           `yaml:"max_pages"`
	DPI       int           `yaml:"dpi"`
	APIKeyEnv string        `yaml:"api_key_env"`
	APIKey    Credential    `yaml:"api_key"`
}

// Analysis configures the endpoint that infers metadata from text. It is
// configured independently of OCR: a vision model on this machine and analysis
// anywhere is the point of FR-017.
type Analysis struct {
	BaseURL   string        `yaml:"base_url"`
	Model     string        `yaml:"model"`
	Timeout   time.Duration `yaml:"timeout"`
	Retries   int           `yaml:"retries"`
	APIKeyEnv string        `yaml:"api_key_env"`
	APIKey    Credential    `yaml:"api_key"`
}

// Vocabulary is a closed set a field may take.
//
// Fallback is required for types and unused for tags: it is what keeps a
// document the model could not place recognisable as unplaced, rather than
// filed somewhere plausible and wrong.
type Vocabulary struct {
	Fallback string   `yaml:"fallback"`
	Values   []string `yaml:"values"`
}

// Bounded reports whether the vocabulary constrains anything. An empty tag
// vocabulary leaves tags free (FR-030).
func (v Vocabulary) Bounded() bool { return len(v.Values) > 0 }

// Allows reports whether s is acceptable. An unbounded vocabulary allows
// everything.
func (v Vocabulary) Allows(s string) bool {
	if !v.Bounded() {
		return true
	}
	return slices.Contains(v.Values, s)
}

// RuleSpec is a filing rule as it is written in the configuration file. It
// becomes a rules.Rule — with its path template compiled — at Validate.
type RuleSpec struct {
	Name          string   `yaml:"name"`
	Type          string   `yaml:"type"`
	Tags          []string `yaml:"tags"`
	Correspondent string   `yaml:"correspondent"`
	Path          string   `yaml:"path"`
}

// Archive describes the external archiving command. The tool presumes no
// particular product: the command is a black box described entirely here.
type Archive struct {
	Command     string        `yaml:"command"`
	Args        []string      `yaml:"args"`
	ArgsEachTag []string      `yaml:"args_each_tag"`
	Timeout     time.Duration `yaml:"timeout"`
	Env         []string      `yaml:"env"`
}

// Configured reports whether an archiver was described at all. Archiving is on
// by default whenever it is configured (FR-061).
func (a Archive) Configured() bool { return a.Command != "" }

// Config is the whole of the tool's configuration, after all four layers have
// been resolved.
type Config struct {
	ArchiveRoot string     `yaml:"archive_root"`
	Disposition string     `yaml:"disposition"`
	OCR         OCR        `yaml:"ocr"`
	Analysis    Analysis   `yaml:"analysis"`
	Types       Vocabulary `yaml:"types"`
	Tags        Vocabulary `yaml:"tags"`
	Rules       []RuleSpec `yaml:"rules"`
	Archive     Archive    `yaml:"archive"`

	// ruleSet holds the compiled rules. It is populated by Validate, so a
	// malformed template is a usage error before any document is read.
	ruleSet []rules.Rule
}

// RuleSet returns the compiled rules, in file order. It is empty until Validate
// has run.
func (c *Config) RuleSet() []rules.Rule { return c.ruleSet }

// Overrides carries the settings a caller wants applied on top of every other
// layer. A nil field means "not given" — which is the whole mechanism behind
// the flag layer, because a flag left off the command line must not overwrite a
// configured value with its own default.
type Overrides struct {
	ArchiveRoot *string
	Disposition *string
}

// Apply lays the override layer over the config.
func (c *Config) Apply(o Overrides) {
	if o.ArchiveRoot != nil {
		c.ArchiveRoot = *o.ArchiveRoot
	}
	if o.Disposition != nil {
		c.Disposition = *o.Disposition
	}
}
