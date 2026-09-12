// One binary, six commands.
//
// # Why this exists
//
// The container image used to carry six separate Go binaries: the API, the
// worker, the migrator, the bootstrap, the regulatory recorder and the source
// ingester. Every one of them links its own copy of the Go runtime and of
// nearly the same set of internal packages — `db`, `config`, `logging`,
// `registry` — so the image paid for that six times over. Measured on the
// layers: 22 MB, 12 MB, 11 MB, 10 MB, 10 MB and 11 MB, for 76 MB of binaries
// that share most of their content.
//
// Compiled together they are one binary about the size of the largest, because
// the linker keeps one copy of everything they have in common. That is the
// whole of the saving and it is worth having twice over on a small server: less
// disk, and a much smaller pull the first time a deployment comes up on a
// metered link.
//
// # Why the commands still exist under cmd/
//
// `cmd/api`, `cmd/worker` and the rest are now four-line wrappers around the
// same packages. They are kept because `go run ./cmd/api` is what the Makefile,
// the tests and every runbook in this repository say to type, and because a
// developer running one service should not have to know that production packs
// them together.
//
// # Dispatch
//
// `biz1core <command> [flags]`. The command is consumed and the rest is handed
// on untouched, so `biz1core api -healthcheck` reaches the API exactly as
// `api -healthcheck` did — `os.Args` is rewritten before the command runs so
// that the `flag` package, which reads it directly, sees what it expects.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mahedi-emon/Biz1core/backend/internal/build"
	"github.com/mahedi-emon/Biz1core/backend/internal/cmd/api"
	"github.com/mahedi-emon/Biz1core/backend/internal/cmd/backup"
	"github.com/mahedi-emon/Biz1core/backend/internal/cmd/bootstrap"
	"github.com/mahedi-emon/Biz1core/backend/internal/cmd/ingest"
	"github.com/mahedi-emon/Biz1core/backend/internal/cmd/migrate"
	"github.com/mahedi-emon/Biz1core/backend/internal/cmd/regulatory"
	"github.com/mahedi-emon/Biz1core/backend/internal/cmd/worker"
)

// commands are what this binary can be. The description is what `biz1core` with
// no argument prints, so it has to say what the thing does rather than repeat
// its name.
var commands = map[string]struct {
	run  func()
	what string
}{
	"api": {api.Main,
		"serve the HTTP API. `-healthcheck` asks a running one whether it is ready"},
	"worker": {worker.Main,
		"drain the job queue: submissions, sweeps, scheduled reports, alerts"},
	"migrate": {migrate.Main,
		"apply the migration chain, then stop. Runs before the API on every deploy"},
	"bootstrap": {bootstrap.Main,
		"create the first platform operator on an installation that has none"},
	"regulatory": {regulatory.Main,
		"record legal values from an attestation file"},
	"ingest": {ingest.Main,
		"retrieve the document a legal value is published in, read it, record it"},
	"backup": {backup.Main,
		"take a backup, list them, prove one restores, restore one, prune"},
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	name := os.Args[1]
	switch name {
	case "-h", "--help", "help":
		usage()
		return
	case "-version", "--version", "version":
		fmt.Println(build.Version)
		return
	}

	cmd, known := commands[name]
	if !known {
		fmt.Fprintf(os.Stderr, "biz1core: no command %q\n\n", name)
		usage()
		os.Exit(2)
	}

	// The command reads os.Args itself, through the flag package and in one
	// case by scanning for `-healthcheck` before any configuration is loaded.
	// Rewriting it here is what makes `biz1core api -healthcheck` and
	// `api -healthcheck` the same thing.
	os.Args = append([]string{os.Args[0] + " " + name}, os.Args[2:]...)
	cmd.run()
}

func usage() {
	var names []string
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	fmt.Fprintf(&b, "biz1core %s\n\nusage: biz1core <command> [flags]\n\n",
		build.Version)
	for _, name := range names {
		fmt.Fprintf(&b, "  %-11s %s\n", name, commands[name].what)
	}
	fmt.Fprintf(&b, "\nEach command takes its own flags; `biz1core <command> -h`"+
		" lists them.\n")
	fmt.Fprint(os.Stderr, b.String())
}
