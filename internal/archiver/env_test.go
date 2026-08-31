package archiver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sgaunet/tabularium/internal/archiver"
)

// canary is the value SC-008 hunts for.
const canary = "sk-canary-3f9a17c4e8b2"

func TestACredentialReachesTheCommandThroughTheEnvironmentAndNeverThroughArgv(t *testing.T) {
	// FR-058, FR-073, SC-008. An argv is world-readable through ps and /proc,
	// so a token in one is a token anybody on the machine can read for as long
	// as the command runs.
	t.Setenv("PAPERLESS_TOKEN", canary)
	t.Setenv("PAPERLESS_URL", "https://paperless.example")

	r := newRecorder(t)
	a := build(t, archiver.Options{
		Command: r.command,
		Args:    []string{"documents", "upload", "--title", "{{.Title}}", "{{.Path}}"},
		Env:     []string{"PAPERLESS_URL", "PAPERLESS_TOKEN"},
	})

	if got := a.Run(context.Background(), subject(t)); !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}

	// It arrived.
	env := r.env(t)
	if !strings.Contains(env, "PAPERLESS_TOKEN="+canary) {
		t.Errorf("the credential did not reach the command's environment:\n%s", env)
	}
	if !strings.Contains(env, "PAPERLESS_URL=https://paperless.example") {
		t.Errorf("the configured URL did not reach the command's environment:\n%s", env)
	}

	// And not through argv.
	for i, arg := range r.argv(t) {
		if strings.Contains(arg, canary) {
			t.Errorf("the credential reached argv[%d] = %q", i, arg)
		}
	}
}

func TestAnUnsetConfiguredVariableIsSimplyAbsent(t *testing.T) {
	// Forwarding a name that is not set would put an empty value in the child's
	// environment, which several tools read as "configured, and blank".
	r := newRecorder(t)
	a := build(t, archiver.Options{
		Command: r.command,
		Args:    []string{"upload"},
		Env:     []string{"TABULARIUM_TEST_DEFINITELY_UNSET"},
	})

	if got := a.Run(context.Background(), subject(t)); !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}
	if strings.Contains(r.env(t), "TABULARIUM_TEST_DEFINITELY_UNSET=") {
		t.Error("an unset variable was forwarded as an empty one")
	}
}

func TestTheChildKeepsTheAmbientEnvironment(t *testing.T) {
	// Cmd.Env is built explicitly rather than left nil, but it is built FROM
	// the parent's environment: an archiver that needs HOME or PATH still has
	// them.
	t.Setenv("TABULARIUM_TEST_AMBIENT", "present")

	r := newRecorder(t)
	a := build(t, archiver.Options{Command: r.command, Args: []string{"upload"}})

	if got := a.Run(context.Background(), subject(t)); !got.Success {
		t.Fatalf("Run() failed: %+v", got)
	}

	env := r.env(t)
	if !strings.Contains(env, "TABULARIUM_TEST_AMBIENT=present") {
		t.Errorf("the ambient environment did not reach the command:\n%s", env)
	}
	if !strings.Contains(env, "PATH=") {
		t.Errorf("PATH did not reach the command:\n%s", env)
	}
}
