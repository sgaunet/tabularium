package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/sgaunet/tabularium/internal/analysis"
	"github.com/sgaunet/tabularium/internal/archiver"
	"github.com/sgaunet/tabularium/internal/chat"
	"github.com/sgaunet/tabularium/internal/config"
	"github.com/sgaunet/tabularium/internal/document"
	"github.com/sgaunet/tabularium/internal/extract"
	"github.com/sgaunet/tabularium/internal/filing"
	"github.com/sgaunet/tabularium/internal/triage"
)

// Run is the whole command. It takes both writers as parameters rather than
// reaching for os.Stdout and os.Stderr, which is what makes the stream split
// (FR-067) a structural property: no package below this one is given a way to
// write to stdout at all, and a test can assert on each stream independently
// without capturing process-global state.
//
// The returned error decides the exit code. A *UsageError is exit 2, anything
// else is exit 1, and nil is exit 0; main does that classification and nothing
// in this package calls os.Exit.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	err := run(ctx, args, stdout, stderr)
	if err != nil {
		// The error report is the command's own output to the user, not a log
		// record: slog's key=value framing mangles a message meant to be read.
		// It goes to stderr like everything else diagnostic.
		fmt.Fprintf(stderr, "tabularium: %v\n", err)
	}
	return err
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	o, err := parse(args)
	switch {
	case errors.Is(err, errHelpRequested):
		// The user asked for help and got it: a successful invocation, and the
		// help belongs on stdout where `tabularium --help | less` expects it.
		writeUsage(stdout)
		return nil
	case err != nil:
		// Help alongside a usage error goes to stderr, so a caller parsing
		// stdout never finds a help screen mixed into its input.
		writeUsage(stderr)
		return err
	}

	if o.version {
		_, _ = io.WriteString(stdout, versionText())
		return nil
	}

	log := newLogger(stderr, o)
	log.Debug("starting", "document", o.document, "config", o.configPath)

	cfg, err := config.Load(o.configPath, os.LookupEnv)
	if err != nil {
		return Usagef("%w", err)
	}
	cfg.Apply(o.overrides)
	if err := cfg.Validate(); err != nil {
		return Usagef("%w", err)
	}
	log.Debug("configuration resolved",
		"archive_root", cfg.ArchiveRoot,
		"disposition", cfg.Disposition,
		"rules", len(cfg.RuleSet()))

	if err := ctx.Err(); err != nil {
		return err
	}

	deps, err := build(cfg, o, log)
	if err != nil {
		return Usagef("%w", err)
	}

	// FR-052: the second entry point. An already-filed document whose sidecar
	// records a hand-off that did not succeed needs only that hand-off — no
	// extraction, no analysis, no move. It is checked before sniffing, because
	// the point is to do none of the work that already succeeded.
	if !o.dryRun && !o.noAnalysis {
		handled, replayErr := replay(ctx, cfg, o, deps, stdout, stderr, log)
		if handled {
			return classify(report(stderr, o, replayErr))
		}
	}

	plan, err := triage.Build(ctx, o.document, deps, triage.Options{
		Disposition: triage.Disposition(cfg.Disposition),
		NoAnalysis:  o.noAnalysis,
	})
	if err != nil {
		return classify(err)
	}

	res := result{Plan: plan, Action: ActionPlanned, IncludeText: o.noAnalysis}

	switch {
	case o.noAnalysis:
		// FR-019: the extracted text and nothing else. Without metadata no rule
		// can match and no name can be computed, so there is nothing to file.
		res.Action = ActionExtracted
	case o.dryRun:
		// FR-043: the plan is the answer, and Build already guaranteed that
		// producing it wrote nothing.
		res.Action = ActionPlanned
	default:
		if err := execute(ctx, cfg, o, plan, &res, log, stderr); err != nil {
			// A filed document whose hand-off failed is still filed, and the
			// output says so before the error decides the exit code (FR-063).
			if res.Action != ActionPlanned {
				_ = write(stdout, o.output, res)
			}
			return classify(report(stderr, o, err))
		}
	}

	return write(stdout, o.output, res)
}

// replay finishes work an earlier run left undone, if there is any.
//
// The second return is false when there is nothing to replay and the document
// should be processed from the start.
func replay(ctx context.Context, cfg *config.Config, o *options, deps triage.Deps,
	stdout, stderr io.Writer, log *slog.Logger,
) (bool, error) {
	filer, arch, err := writers(cfg, o, log, stderr)
	if err != nil || filer == nil {
		return false, err
	}
	defer func() { _ = filer.Close() }()

	out, handled, replayErr := triage.Replay(ctx, o.document, deps,
		triage.FileOptions{Filer: filer, Archiver: arch})
	if !handled {
		return false, nil
	}

	// The document is filed either way, so the output says so before the error
	// decides the exit code.
	res := result{Action: ActionFiled, Archive: &out.Archive}
	res.Plan.Source = sourceOf(o.document)
	res.Plan.Dest = out.Dest
	if writeErr := write(stdout, o.output, res); writeErr != nil && replayErr == nil {
		return true, writeErr
	}
	return true, replayErr
}

// sourceOf re-reads the document for the output's source block. A replay has
// not sniffed it, and the block is part of the contract either way.
func sourceOf(path string) document.Source {
	src, err := document.Open(path)
	if err != nil {
		// Replay only runs on a document it has already opened successfully;
		// an error here means it vanished between the two calls, and an empty
		// block is more honest than a fabricated one.
		return document.Source{Path: path}
	}
	return src
}

// execute carries out the plan and records what happened in res.
func execute(ctx context.Context, cfg *config.Config, o *options, plan triage.Plan,
	res *result, log *slog.Logger, stderr io.Writer,
) error {
	filer, arch, err := writers(cfg, o, log, stderr)
	if err != nil {
		return err
	}
	if filer == nil {
		// FR-062: filing is disabled by flag. Nothing is written, and the
		// output says the destination was computed but not used.
		res.Action = ActionNotFiled
		return nil
	}
	defer func() { _ = filer.Close() }()

	out, execErr := triage.Execute(ctx, plan, triage.FileOptions{Filer: filer, Archiver: arch})

	// The outcome is recorded even when the hand-off failed, because the
	// document really is filed and the output must not pretend otherwise.
	res.Archive = &out.Archive
	switch {
	case out.Duplicate:
		res.Action = ActionDuplicate
		res.Plan.Dest = out.Dest
		// FR-049, FR-069: the user is told, on stderr, and the exit code stays
		// 0 — a parser reading stdout is not disturbed.
		log.Warn("a document with identical content is already filed; nothing was written",
			"dest", out.Dest)
	case out.Filed:
		res.Action = ActionFiled
		res.Plan.Dest = out.Dest
	default:
		res.Action = ActionNotFiled
	}
	return execErr
}

// writers builds the two write-side collaborators, or nil for each that is
// switched off. Both are on by default whenever they are configured (FR-061).
//
// stderr is where the archiver's own output is echoed, so the user can see why a
// hand-off was refused (FR-057a).
func writers(cfg *config.Config, o *options, log *slog.Logger, stderr io.Writer,
) (*filing.Filer, *archiver.Archiver, error) {
	if o.noFile {
		return nil, nil, nil
	}
	filer, err := filing.Open(cfg.ArchiveRoot, log)
	if err != nil {
		return nil, nil, err
	}

	if o.noArchive || !cfg.Archive.Configured() {
		return filer, nil, nil
	}
	// --quiet is the silent mode (FR-071), and a successful archiver's chatter is
	// not an error. A failing one still gets through: report() prints what was
	// captured when nothing was echoed.
	echo := stderr
	if o.quiet {
		echo = nil
	}
	arch, err := archiver.New(archiver.Options{
		Command:     cfg.Archive.Command,
		Args:        cfg.Archive.Args,
		ArgsEachTag: cfg.Archive.ArgsEachTag,
		Timeout:     cfg.Archive.Timeout,
		Env:         cfg.Archive.Env,
		Echo:        echo,
		Log:         log,
	})
	if err != nil {
		_ = filer.Close()
		return nil, nil, err
	}
	return filer, arch, nil
}

// build assembles the pipeline's collaborators from the resolved configuration.
//
// The two clients are constructed independently, because FR-017 makes OCR and
// analysis independently configurable — a vision model on this machine and
// analysis wherever you like. Either may be absent; a document that needs the
// missing one then fails by name.
func build(cfg *config.Config, o *options, log *slog.Logger) (triage.Deps, error) {
	deps := triage.Deps{Config: cfg, Log: log}
	deps.Extract = extract.Options{MaxPages: cfg.OCR.MaxPages, DPI: cfg.OCR.DPI, Log: log}

	if cfg.OCR.BaseURL != "" && cfg.OCR.Model != "" {
		vision, err := chat.New(chat.Options{
			BaseURL: cfg.OCR.BaseURL, Model: cfg.OCR.Model,
			APIKey: cfg.OCR.APIKey, Timeout: cfg.OCR.Timeout,
		})
		if err != nil {
			return triage.Deps{}, err
		}
		deps.Extract.Vision = vision
	}

	if o.noAnalysis {
		return deps, nil
	}
	if cfg.Analysis.BaseURL == "" || cfg.Analysis.Model == "" {
		return triage.Deps{}, errors.New("no analysis endpoint is configured: " +
			"set analysis.base_url and analysis.model, or pass --no-analysis")
	}
	client, err := chat.New(chat.Options{
		BaseURL: cfg.Analysis.BaseURL, Model: cfg.Analysis.Model,
		APIKey: cfg.Analysis.APIKey, Timeout: cfg.Analysis.Timeout,
		Retries: cfg.Analysis.Retries,
	})
	if err != nil {
		return triage.Deps{}, err
	}
	deps.Analyser = analysis.New(client,
		analysis.Vocabularies{Types: cfg.Types, Tags: cfg.Tags}, cfg.Analysis.Retries)

	return deps, nil
}

// report shows what a refusing archiver said when it was not echoed live.
//
// Only under --quiet, which is the one case where the echo was switched off: the
// command's own words are what tell the user what to fix, and a failure is an
// error, which even the silent mode reports (FR-057a, FR-071). Gated on the same
// flag that switched the echo off, so what was streamed is never printed twice.
func report(stderr io.Writer, o *options, err error) error {
	var failed *triage.ArchiveFailedError
	if !o.quiet || !errors.As(err, &failed) || failed.Result.Output == "" {
		return err
	}
	prefix := filepath.Base(failed.Command) + "| "
	for line := range strings.SplitSeq(strings.TrimRight(failed.Result.Output, "\n"), "\n") {
		fmt.Fprintf(stderr, "%s%s\n", prefix, strings.TrimSuffix(line, "\r"))
	}
	return err
}

// classify maps a pipeline failure to the right exit code.
//
// The distinction the user cares about is whether the same command will fail
// the same way tomorrow. A configuration they can fix, a file they named
// wrongly, an endpoint that will not honour the schema — all exit 2. Everything
// else is a runtime failure and exits 1.
func classify(err error) error {
	var (
		archiveFailed *triage.ArchiveFailedError
		badSource     *document.SourceError
		unsupported   *document.UnsupportedError
		missingTool   *extract.MissingToolError
		unconfigured  *extract.NotConfiguredError
		schema        *chat.SchemaUnsupportedError
		bomb          *extract.BombError
	)
	switch {
	case errors.As(err, &archiveFailed):
		// FR-063: the document is filed and the external step is not done. That
		// is a runtime failure to retry, not a configuration error to fix.
		return err
	case errors.As(err, &badSource),
		errors.As(err, &unsupported),
		errors.As(err, &missingTool),
		errors.As(err, &unconfigured),
		errors.As(err, &schema),
		errors.As(err, &bomb),
		errors.Is(err, fs.ErrNotExist),
		errors.Is(err, fs.ErrPermission):
		return &UsageError{Err: err}
	default:
		return err
	}
}

// write renders the result in the requested format (FR-064).
func write(stdout io.Writer, format string, r result) error {
	if format == "json" {
		return writeJSON(stdout, r)
	}
	return writeText(stdout, r)
}

// newLogger builds the diagnostic logger. It writes to stderr, always: FR-067
// and SC-005 both depend on nothing diagnostic reaching stdout.
//
// --quiet and --verbose are the silent and detailed modes of FR-071, and they
// move this level rather than gating individual call sites.
func newLogger(stderr io.Writer, o *options) *slog.Logger {
	level := slog.LevelInfo
	switch {
	case o.quiet:
		level = slog.LevelError
	case o.verbose:
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
}

// Decorated reports whether colour, spinners and progress bars may be written
// to w.
//
// Both conditions must hold — a character device *and* an unset NO_COLOR. Not
// either-or: a piped stdout suppresses decoration even on a colour-capable
// terminal, and that is the case that actually corrupts somebody's data.
func Decorated(w io.Writer, env config.Env) bool {
	if _, set := env("NO_COLOR"); set {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
