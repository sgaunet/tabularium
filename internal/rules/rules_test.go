package rules_test

import (
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/rules"
)

// compile is the fixture builder: it turns the readable form a configuration
// file uses into the compiled rules Match works on.
func compile(t *testing.T, specs ...rules.Rule) []rules.Rule {
	t.Helper()
	out := make([]rules.Rule, len(specs))
	for i, s := range specs {
		tmpl, err := rules.Parse(s.Name, s.PathText)
		if err != nil {
			t.Fatalf("rules.Parse(%q, %q): %v", s.Name, s.PathText, err)
		}
		s.Path = tmpl
		out[i] = s
	}
	return out
}

// ruleSet mirrors contracts/config.example.yaml: three increasingly general
// facture rules, then a default. Order is the whole point.
func ruleSet(t *testing.T) []rules.Rule {
	t.Helper()
	return compile(t,
		rules.Rule{Name: "factures-voiture", Type: "facture", Tags: []string{"voiture"},
			PathText: "factures/voiture/{{.Year}}"},
		rules.Rule{Name: "factures-sante", Type: "facture", Tags: []string{"sante"},
			PathText: "factures/sante/{{.Year}}"},
		rules.Rule{Name: "factures", Type: "facture",
			PathText: "factures/{{.Correspondent}}/{{.Year}}"},
		rules.Rule{Name: "contrats-axa", Type: "contrat", Correspondent: "axa",
			PathText: "contrats/axa"},
		rules.Rule{Name: "divers", PathText: "divers/{{.Year}}"},
	)
}

func TestMatchFirstWinsInFileOrder(t *testing.T) {
	rs := ruleSet(t)

	tests := []struct {
		name    string
		subject rules.Subject
		want    string
	}{
		{
			name:    "the first matching rule wins even though later ones also match",
			subject: rules.Subject{Type: "facture", Tags: []string{"voiture", "entretien"}},
			want:    "factures-voiture",
		},
		{
			name:    "a later specific rule is reached when the earlier one does not match",
			subject: rules.Subject{Type: "facture", Tags: []string{"sante"}},
			want:    "factures-sante",
		},
		{
			name:    "the general rule catches a facture with no known tag",
			subject: rules.Subject{Type: "facture", Correspondent: "edf"},
			want:    "factures",
		},
		{
			name:    "correspondent is an equality condition",
			subject: rules.Subject{Type: "contrat", Correspondent: "axa"},
			want:    "contrats-axa",
		},
		{
			name:    "a contrat from another correspondent falls through to the default",
			subject: rules.Subject{Type: "contrat", Correspondent: "maif"},
			want:    "divers",
		},
		{
			name: "the vocabulary fallback type falls to the default, never to an invented rule",
			// FR-029, SC-013: `autre` is what the model chooses when it cannot
			// place a document. It must stay recognisable as unplaced.
			subject: rules.Subject{Type: "autre", Tags: []string{"voiture"}},
			want:    "divers",
		},
		{
			name: "a tag phrased differently from what a rule expects falls to the default",
			// SC-013: "automobile" is not "voiture". The document is not
			// misfiled under factures/voiture; it lands in divers.
			subject: rules.Subject{Type: "facture", Correspondent: "garage", Tags: []string{"automobile"}},
			want:    "factures",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rules.Match(rs, tt.subject)
			if !ok {
				t.Fatalf("Match(%+v): no rule matched, want %q", tt.subject, tt.want)
			}
			if got.Name != tt.want {
				t.Errorf("Match(%+v) = %q, want %q", tt.subject, got.Name, tt.want)
			}
		})
	}
}

func TestMatchTagsAreSetContainmentNotEquality(t *testing.T) {
	rs := compile(t, rules.Rule{
		Name: "two-tags", Type: "facture", Tags: []string{"voiture", "entretien"},
		PathText: "factures/voiture/entretien",
	})

	tests := []struct {
		name    string
		tags    []string
		wantHit bool
	}{
		{name: "exactly the required tags", tags: []string{"voiture", "entretien"}, wantHit: true},
		{name: "a superset matches — containment, not equality", tags: []string{"entretien", "voiture", "urgent"}, wantHit: true},
		{name: "order is irrelevant", tags: []string{"entretien", "voiture"}, wantHit: true},
		{name: "a subset does not match: ALL listed tags must be present", tags: []string{"voiture"}, wantHit: false},
		{name: "no tags at all does not match", tags: nil, wantHit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := rules.Match(rs, rules.Subject{Type: "facture", Tags: tt.tags})
			if ok != tt.wantHit {
				t.Errorf("Match with tags %v = %v, want %v", tt.tags, ok, tt.wantHit)
			}
		})
	}
}

func TestDefaultRuleIsTheOneWithNoCondition(t *testing.T) {
	rs := ruleSet(t)

	for _, r := range rs {
		wantDefault := r.Name == "divers"
		if got := r.IsDefault(); got != wantDefault {
			t.Errorf("%q.IsDefault() = %v, want %v", r.Name, got, wantDefault)
		}
	}

	// A rule with no condition matches anything at all.
	got, ok := rules.Match(rs, rules.Subject{Type: "quelque-chose-dinconnu"})
	if !ok || got.Name != "divers" {
		t.Errorf("Match(unknown type) = %q, %v; want the default rule", got.Name, ok)
	}
}

func TestMatchWithoutDefaultReportsNoMatch(t *testing.T) {
	// FR-040: no matching rule and no default rule is a failure the caller has
	// to handle, not a silent fallback to some invented destination.
	rs := compile(t, rules.Rule{Name: "factures", Type: "facture", PathText: "factures"})

	if got, ok := rules.Match(rs, rules.Subject{Type: "contrat"}); ok {
		t.Errorf("Match(contrat) = %q, true; want no match", got.Name)
	}
}

func TestRender(t *testing.T) {
	full := rules.PathContext{
		Type: "facture", Correspondent: "garage-central", Reference: "FA-2025-0312",
		Title: "Facture entretien", Year: "2025", Month: "03", Day: "14",
		Tags: []string{"voiture", "entretien"},
	}

	tests := []struct {
		name    string
		path    string
		ctx     rules.PathContext
		want    string
		wantErr string
	}{
		{
			name: "literal segments and a date part",
			path: "factures/voiture/{{.Year}}", ctx: full, want: "factures/voiture/2025",
		},
		{
			name: "month and day are zero-padded strings so paths sort correctly",
			path: "{{.Year}}/{{.Month}}/{{.Day}}", ctx: full, want: "2025/03/14",
		},
		{
			name: "every declared field is reachable",
			path: "{{.Type}}/{{.Correspondent}}/{{.Reference}}", ctx: full,
			want: "facture/garage-central/FA-2025-0312",
		},
		{
			name: "a path with no template at all is a literal directory",
			path: "contrats/axa", ctx: full, want: "contrats/axa",
		},
		{
			name: "an empty middle segment fails loudly instead of collapsing",
			// FR-039: `factures//2025` must never be produced.
			path:    "factures/{{.Correspondent}}/{{.Year}}",
			ctx:     rules.PathContext{Year: "2025"},
			wantErr: "empty",
		},
		{
			name:    "an empty trailing segment fails too",
			path:    "factures/{{.Year}}",
			ctx:     rules.PathContext{Correspondent: "edf"},
			wantErr: "empty",
		},
		{
			name:    "a traversal segment is refused",
			path:    "factures/{{.Correspondent}}",
			ctx:     rules.PathContext{Correspondent: ".."},
			wantErr: "..",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := rules.Parse("r", tt.path)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.path, err)
			}
			r := rules.Rule{Name: "r", PathText: tt.path, Path: tmpl}

			got, err := r.Render(tt.ctx)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Render() = %q, nil; want an error mentioning %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Render() error = %v; want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Render(): %v", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPathContextHasNoFolderField(t *testing.T) {
	// FR-039 and SC-014: the model produces a bounded type and tags; it can
	// never name a folder, because there is no field through which to name one.
	// Every directory level is literal text in the configuration.
	tmpl, err := rules.Parse("r", "{{.Folder}}")
	if err == nil {
		r := rules.Rule{Name: "r", Path: tmpl}
		if got, err := r.Render(rules.PathContext{Year: "2025"}); err == nil {
			t.Fatalf("Render() = %q, nil; a .Folder reference must fail", got)
		}
		return
	}
	// Failing at Parse is just as good — it fails before any document is read.
}

func TestParseRejectsAMalformedTemplate(t *testing.T) {
	// A malformed template is a configuration error, and it is caught at load
	// rather than at match time, before any document has been touched.
	if _, err := rules.Parse("bad", "factures/{{.Year"); err == nil {
		t.Fatal("Parse() with an unclosed action = nil error; want a parse failure")
	}
}

func TestRenderIsPure(t *testing.T) {
	// The spec's determinism assumption: matching and rendering are functions
	// of the metadata and the configuration alone — no clock, no I/O.
	rs := ruleSet(t)
	subject := rules.Subject{Type: "facture", Tags: []string{"voiture"}}
	ctx := rules.PathContext{Year: "2025"}

	first, ok := rules.Match(rs, subject)
	if !ok {
		t.Fatal("Match(): no rule matched")
	}
	want, err := first.Render(ctx)
	if err != nil {
		t.Fatalf("Render(): %v", err)
	}

	for range 100 {
		got, ok := rules.Match(rs, subject)
		if !ok {
			t.Fatal("Match(): stopped matching on a later call")
		}
		out, err := got.Render(ctx)
		if err != nil {
			t.Fatalf("Render(): %v", err)
		}
		if got.Name != first.Name || out != want {
			t.Fatalf("Match/Render drifted: got %q %q, want %q %q", got.Name, out, first.Name, want)
		}
	}
}
