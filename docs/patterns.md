# Code Patterns & Best Practices

No Go source exists yet. These are the patterns the constitution commits this project to,
so the first code written already conforms rather than being retrofitted.

## Error Handling

Wrap, never flatten. A flattened error string destroys `errors.Is` and `errors.As` for
every caller above.

```go
// good
if err := store.Write(ctx, path); err != nil {
    return fmt.Errorf("filing %s: %w", path, err)
}

// forbidden — the chain is gone
return fmt.Errorf("filing failed: %s", err.Error())
```

Sentinel errors and typed errors belong in the domain package that owns the failure, so
the CLI layer can map them onto exit codes without importing the whole world.

## Exit Codes

`0` success, `1` runtime failure, `2` usage error — documented in `--help` and covered by
a test. The command layer sets none of its own: `cli.Run` returns an error and a
scaffolded `main` exits 1 for everything, so `main` must classify explicitly.

```go
func main() {
    if err := cli.Execute(); err != nil {
        if errors.As(err, &usageErr) {
            os.Exit(2)
        }
        os.Exit(1)
    }
}
```

## The Output Contract

stdout carries data only, selectable with `--output=text|json`. Logs, errors and progress
go to stderr. This split is what makes the tool safe inside a pipe, and it is the reason
every writer is injected rather than being `os.Stdout` reached for directly — a test can
then assert on both streams.

Colour, spinners and progress bars are suppressed when `NO_COLOR` is set or stdout is not
a TTY.

## Testing Patterns

- **Naming**: `foo_test.go` beside `foo.go`
- **Package**: `package foo_test` — black box, always
- **Internals**: an `export_test.go` inside `package foo` re-exports what a test needs:

```go
// export_test.go
package slug

var Sanitise = sanitise
```

- **End to end**: at least one test builds the binary and invokes it, asserting on
  stdout, stderr and the exit code together
- **Run**: `go test -count=2 -race ./...`

## Package Organisation

Packages are named for the domain they serve. `utils`, `helpers`, `common` and `base` are
forbidden — they attract unrelated code and grow into import cycles.

Business logic imports no CLI package. The test for this is mechanical: if a package
cannot be exercised without constructing a `flag.FlagSet`, it is in the wrong place.

## Concurrency and Cancellation

Every long-running function takes `ctx context.Context` as its first parameter and honours
it. `main` builds the context with `signal.NotifyContext` for SIGINT and SIGTERM, so an
interrupted run leaves the filesystem coherent — never a half-written file under its final
name.

Every I/O gets an explicit timeout. Retries are bounded and backed off; an unbounded retry
turns a transient fault into an outage for whatever is on the other end.

## Generics

Prefer concrete types. A generic is introduced only once the same concrete implementation
exists for three or more types. Generics cost readability, and most Go code never needs
them.

## Code Generation

Generators are Go tool dependencies (`go get -tool <module>`, the Go 1.24+ `tool`
directive), never installed globally, so every checkout runs the same versions. Each is
invoked by a `//go:generate go tool <name> ...` directive next to the file it produces,
and `go generate ./...` regenerates the whole tree — `task lint` runs it first. Generated
files are committed, so a clean checkout builds without running any generator.

## Embedding

Runtime assets — templates, default configuration, static files — are embedded with
`//go:embed`. The binary never depends on files sitting next to it.
