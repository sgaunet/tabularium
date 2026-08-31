package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sgaunet/tabularium/internal/rules"
)

// dispositions are what may become of the source once it has been filed.
var dispositions = []string{"move", "copy", "keep"}

// Validate checks the configuration and compiles the filing rules.
//
// Everything here is checked before any document is read, because every failure
// it can find is a configuration error the user can fix — and finding it on the
// day a document happens to match the broken rule is finding it too late.
//
// The caller maps the returned error to exit code 2.
func (c *Config) Validate() error {
	if err := c.resolveArchiveRoot(); err != nil {
		return err
	}
	if !slices.Contains(dispositions, c.Disposition) {
		return fmt.Errorf("disposition %q: want one of %s",
			c.Disposition, strings.Join(dispositions, ", "))
	}
	if err := c.validateTypes(); err != nil {
		return err
	}
	return c.compileRules()
}

func (c *Config) resolveArchiveRoot() error {
	if c.ArchiveRoot == "" {
		return errors.New("archive_root is not set: nowhere to file documents")
	}

	path, err := expandHome(c.ArchiveRoot)
	if err != nil {
		return err
	}
	if path, err = filepath.Abs(path); err != nil {
		return fmt.Errorf("resolving archive_root %q: %w", c.ArchiveRoot, err)
	}
	c.ArchiveRoot = path
	return nil
}

// expandHome resolves a leading ~ the way a shell would. A configuration file
// is hand-written, and ~/Documents/Archive is what a person writes.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expanding %q: %w", path, err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

// validateTypes enforces FR-029: the type vocabulary must declare a fallback,
// and that fallback must be a member of the vocabulary.
//
// The fallback exists so a document the model cannot place stays recognisable
// as unplaced. A fallback outside the vocabulary would be rejected by the very
// validation it exists to survive, which is a failure with no symptom until a
// document needs it.
func (c *Config) validateTypes() error {
	if !c.Types.Bounded() {
		return errors.New("types.values is empty: the type vocabulary is what keeps the " +
			"model from inventing a document type")
	}
	if c.Types.Fallback == "" {
		return errors.New("types.fallback is not set: it is what a document the model " +
			"cannot place is filed as")
	}
	if !c.Types.Allows(c.Types.Fallback) {
		return fmt.Errorf("types.fallback %q is not one of types.values (%s)",
			c.Types.Fallback, strings.Join(c.Types.Values, ", "))
	}
	return nil
}

// compileRules parses every path template and enforces the two structural rules
// on the rule list: names are unique, and there is at most one default rule.
func (c *Config) compileRules() error {
	compiled := make([]rules.Rule, 0, len(c.Rules))
	seen := make(map[string]struct{}, len(c.Rules))
	defaultRule := ""

	for i, spec := range c.Rules {
		if spec.Name == "" {
			return fmt.Errorf("rules[%d] has no name: the name is what --dry-run and "+
				"the sidecar report", i)
		}
		if _, dup := seen[spec.Name]; dup {
			return fmt.Errorf("rules[%d]: duplicate rule name %q", i, spec.Name)
		}
		seen[spec.Name] = struct{}{}

		if spec.Path == "" {
			return fmt.Errorf("rule %q has no path", spec.Name)
		}
		tmpl, err := rules.Parse(spec.Name, spec.Path)
		if err != nil {
			return err
		}

		r := rules.Rule{
			Name:          spec.Name,
			Type:          spec.Type,
			Tags:          spec.Tags,
			Correspondent: spec.Correspondent,
			PathText:      spec.Path,
			Path:          tmpl,
		}
		if r.IsDefault() {
			if defaultRule != "" {
				return fmt.Errorf("rule %q is a second rule with no condition: it is "+
					"unreachable behind %q", spec.Name, defaultRule)
			}
			defaultRule = spec.Name
		}
		if spec.Type != "" && !c.Types.Allows(spec.Type) {
			return fmt.Errorf("rule %q conditions on type %q, which is not one of "+
				"types.values (%s): it can never match", spec.Name, spec.Type,
				strings.Join(c.Types.Values, ", "))
		}

		compiled = append(compiled, r)
	}

	c.ruleSet = compiled
	return nil
}
