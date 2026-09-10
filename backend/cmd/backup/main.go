// backup, as its own command.
//
// The body lives in `internal/cmd/backup` so it can be compiled into the single
// `rawsyst` binary the container image ships as well as into this one. Keeping
// this wrapper means `go run ./cmd/backup` still works.
package main

import "github.com/mahedi-emon/rawsyst-pos/backend/internal/cmd/backup"

func main() { backup.Main() }
