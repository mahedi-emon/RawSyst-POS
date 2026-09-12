// regulatory, as its own command.
//
// The body lives in `internal/cmd/regulatory` so it can be compiled into the single
// `biz1core` binary the container image ships as well as into this one. Keeping
// this wrapper means `go run ./cmd/regulatory` still works, which is what the
// Makefile, the tests and every runbook in this repository say to type.
package main

import "github.com/mahedi-emon/Biz1core/backend/internal/cmd/regulatory"

func main() { regulatory.Main() }
