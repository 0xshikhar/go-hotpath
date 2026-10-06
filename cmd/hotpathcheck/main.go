// Command hotpathcheck is a go/analysis vettool that enforces
// //hotpath:noalloc annotations: an annotated function may not contain a
// syntactic allocation site or call an unannotated function.
//
// Usage:
//
//	go install github.com/0xshikhar/go-hotpath/cmd/hotpathcheck@latest
//	go vet -vettool=$(which hotpathcheck) ./...
//
// Or directly:
//
//	hotpathcheck ./...
package main

import (
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/0xshikhar/go-hotpath/cmd/hotpathcheck/analyzer"
)

// multichecker, not singlechecker: under `go vet -vettool` (Go 1.26) a
// singlechecker binary's diagnostics come back as raw JSON with exit status 0,
// which would silently pass CI. multichecker prints them and exits non-zero.
func main() {
	multichecker.Main(analyzer.Analyzer)
}
