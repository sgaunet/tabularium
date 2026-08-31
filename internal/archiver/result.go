package archiver

import "time"

// Result is the outcome of the external hand-off.
//
// It is deliberately not parsed for an identifier: the archiver is a black box
// described entirely by configuration, and presuming an output format would tie
// the tool to one product (FR-057, FR-059).
type Result struct {
	// Attempted is false when the archiver is unconfigured or disabled by flag
	// (FR-062). It is what distinguishes "did not run" from "ran and failed".
	Attempted bool
	Success   bool

	// ExitCode is the command's own code. Nil when the step failed before the
	// command produced one — a missing binary, or a timeout.
	ExitCode *int

	// Output is stdout and stderr combined, verbatim, truncated to 8 KiB.
	Output string

	// Err says why the step failed when it failed before producing an exit
	// code (FR-056, FR-060).
	Err string

	AttemptedAt *time.Time
}

// truncate caps captured output, marking the cut so a reader knows there was
// more rather than wondering why the message stops mid-sentence.
func truncate(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return s[:maxOutput] + "\n… (truncated at 8 KiB)"
}
