// Command tabularium reads one document, works out what it is, names it, files
// it into a local archive tree, and hands it to a configured external archiver.
//
// One document per invocation. Batch with `find … | xargs -n1 tabularium` and
// parallelise with `xargs -P`, which is safe by construction: destination names
// are claimed atomically.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/sgaunet/tabularium/internal/cli"
)

func main() {
	// Every long operation below takes this context, so SIGINT and SIGTERM
	// unwind an in-flight model request or a half-written copy cleanly rather
	// than leaving the archive tree in a state nobody chose.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		// The whole of the exit-code contract lives here. cli.Run has already
		// reported the error on stderr; this decides only what the shell sees.
		var usage *cli.UsageError
		if errors.As(err, &usage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
