// Package crossdep provides an annotated callee for cross-package checks.
package crossdep

//hotpath:noalloc
func Exported() int { return 7 }
