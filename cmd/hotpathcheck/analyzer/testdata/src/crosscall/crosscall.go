// Package crosscall verifies that the noalloc annotation survives a package
// boundary via analysis facts.
package crosscall

import "crossdep"

//hotpath:noalloc
func callsAnnotatedInOtherPkg() int { // want callsAnnotatedInOtherPkg:"noalloc"
	return crossdep.Exported() // ok — fact proves it
}
