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

func main() {
	multichecker.Main(analyzer.Analyzer)
}
