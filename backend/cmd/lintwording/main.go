// Command lintwording fails the build on compliance claims the product must
// never make.
//
// Blueprint A1 forbids "ZATCA-certified", "certified compliant", "guaranteed
// compliant" and "never at legal risk". This is a legal exposure, not a style
// preference: passing ZATCA's SDK validation is a technical self-check, not
// ZATCA approval, and claiming otherwise misrepresents the product to a Saudi
// client.
//
// # Why this is a program and not a grep
//
// A naive grep flags the documentation that TEACHES the rule. The line
//
//	| Never write "ZATCA-certified" | Always write "supports ZATCA requirements" |
//
// is the rule being stated, not broken. So the check distinguishes two things:
//
//   - Product output (Go, SQL, HTML, TS, locale files) — any occurrence fails.
//     These strings can reach a user.
//   - Internal documents (docs/, memories, the blueprint) — an occurrence fails
//     only when the line does not also carry a negation marker. A line saying
//     "never write X" is fine; a line asserting X is not.
//
// The blueprint itself is excluded entirely: it is the frozen source
// specification, quoted verbatim, and not ours to edit.
package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// forbidden phrases, lower-cased for comparison.
var forbidden = []string{
	"zatca-certified",
	"zatca certified",
	"certified compliant",
	"guaranteed compliant",
	"never at legal risk",
}

// A line in an internal document may quote a forbidden phrase when it is
// clearly stating the prohibition.
// Strikethrough is the canonical marker in tables: a crossed-out phrase reads
// as "do not use this" in the rendered document, so the marker carries meaning
// for a human reader rather than existing only to satisfy this tool.
var negationMarkers = []string{
	"~~", "❌",
	"never", "not ", "don't", "do not", "forbidden", "banned", "avoid",
	"must not", "cannot", "instead of", "rather than", "prohibited",
	"wording", "lint",
}

// Extensions whose content can reach a user. Any occurrence fails.
var productOutput = map[string]bool{
	".go": true, ".sql": true, ".html": true, ".htm": true,
	".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".json": true, ".yaml": true, ".yml": true, ".txt": true,
}

// Extensions treated as internal documentation.
var internalDoc = map[string]bool{".md": true}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "bin": true, "dist": true,
	"vendor": true, ".next": true, "target": true,
}

// The frozen source specification. Quoted verbatim, not ours to edit.
const blueprintName = "RawSyst-POS-Blueprint-v2.4-FINAL.md"

type finding struct {
	path string
	line int
	text string
	why  string
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	var findings []finding
	scanned := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == blueprintName {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		isProduct := productOutput[ext]
		isDoc := internalDoc[ext]
		if !isProduct && !isDoc {
			return nil
		}

		// This file necessarily contains every forbidden phrase.
		if filepath.Base(path) == "main.go" && strings.Contains(path, "lintwording") {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		scanned++

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			lower := strings.ToLower(line)

			for _, phrase := range forbidden {
				if !strings.Contains(lower, phrase) {
					continue
				}
				if isDoc && hasNegation(lower) {
					// Stating the rule, not breaking it.
					continue
				}
				why := "product output must never make this claim"
				if isDoc {
					why = "documentation asserts this claim without stating it as prohibited"
				}
				findings = append(findings, finding{path, n, strings.TrimSpace(line), why})
				break
			}
		}
		return sc.Err()
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "lintwording: %v\n", err)
		os.Exit(2)
	}

	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "\nForbidden compliance wording found in %d place(s):\n\n", len(findings))
		for _, f := range findings {
			fmt.Fprintf(os.Stderr, "  %s:%d\n    %s\n    → %s\n\n", f.path, f.line, f.text, f.why)
		}
		fmt.Fprint(os.Stderr,
			"The software SUPPORTS compliance; it never guarantees it.\n"+
				"Use \"supports ZATCA requirements\", \"built to support ZATCA and PDPL\n"+
				"requirements\", or \"WPS/Mudad-ready\".\n\n")
		os.Exit(1)
	}

	fmt.Printf("wording check passed (%d files scanned)\n", scanned)
}

func hasNegation(lowerLine string) bool {
	for _, m := range negationMarkers {
		if strings.Contains(lowerLine, m) {
			return true
		}
	}
	return false
}
