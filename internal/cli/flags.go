package cli

import (
	"errors"
	"flag"
	"io"
	"slices"

	"github.com/sgaunet/tabularium/internal/config"
)

// options is the command line, parsed and checked.
type options struct {
	configPath  string
	output      string
	dryRun      bool
	disposition string
	noAnalysis  bool
	noFile      bool
	noArchive   bool
	quiet       bool
	verbose     bool
	version     bool

	// document is the single positional argument.
	document string

	// overrides carries only the settings the user actually named on the
	// command line, so the layer below is not overwritten by a flag's default.
	overrides config.Overrides
}

var (
	outputFormats = []string{"text", "json"}
	dispositions  = []string{"move", "copy", "keep"}
)

// errHelpRequested signals that the user asked for help and got it. It is not a
// usage error: --help is a successful invocation and exits 0.
var errHelpRequested = errors.New("help requested")

// parse reads the command line.
//
// flag.ContinueOnError is mandatory: flag.ExitOnError calls os.Exit(2) itself,
// which would bypass the exit-code classification in main entirely — including
// every case that must exit 1.
func parse(args []string) (*options, error) {
	var o options

	fs := flag.NewFlagSet("tabularium", flag.ContinueOnError)
	// The flag package's own usage output is a generated list; ours is the
	// hand-written contract, and it is printed by the caller on the right
	// stream. Silence the built-in one rather than let both appear.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	fs.StringVar(&o.configPath, "config", "", "configuration file")
	// FR-064: stdout carries data only, in a form the caller selects.
	fs.StringVar(&o.output, "output", "text", "text | json")
	fs.BoolVar(&o.dryRun, "dry-run", false, "compute and print the plan; write nothing")
	fs.StringVar(&o.disposition, "disposition", "move", "move | copy | keep")
	fs.BoolVar(&o.noAnalysis, "no-analysis", false, "extract text only")
	fs.BoolVar(&o.noFile, "no-file", false, "skip local filing")
	fs.BoolVar(&o.noArchive, "no-archive", false, "skip the external archiver")
	// FR-071: a silent mode and a detailed one.
	fs.BoolVar(&o.quiet, "quiet", false, "errors only")
	fs.BoolVar(&o.verbose, "verbose", false, "debug diagnostics")
	fs.BoolVar(&o.version, "version", false, "print version and exit")

	// -h and --help are left undefined so the flag package produces
	// flag.ErrHelp for them, which maps to exit 0 rather than exit 2.
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelpRequested
		}
		return nil, Usagef("%w", err)
	}

	if o.version {
		return &o, nil
	}

	if err := o.check(fs); err != nil {
		return nil, err
	}
	return &o, nil
}

// check validates the parsed command line and builds the override layer.
func (o *options) check(fs *flag.FlagSet) error {
	if o.quiet && o.verbose {
		return Usagef("--quiet and --verbose contradict each other: choose one")
	}
	if !slices.Contains(outputFormats, o.output) {
		return Usagef("--output %q: want text or json", o.output)
	}
	if !slices.Contains(dispositions, o.disposition) {
		return Usagef("--disposition %q: want move, copy or keep", o.disposition)
	}

	switch n := fs.NArg(); {
	case n == 0:
		return Usagef("no document given: tabularium takes exactly one file " +
			"(batch with `find … | xargs -n1 tabularium`)")
	case n > 1:
		return Usagef("%d documents given: tabularium takes exactly one file "+
			"(batch with `find … | xargs -n1 tabularium`)", n)
	}
	o.document = fs.Arg(0)

	// FlagSet.Visit iterates only the flags actually present on the command
	// line. Reading the flag variables directly would let an unset
	// --disposition overwrite a configured value with its own default, which
	// silently inverts the precedence order for every flag left off.
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "disposition" {
			o.overrides.Disposition = &o.disposition
		}
	})
	return nil
}
