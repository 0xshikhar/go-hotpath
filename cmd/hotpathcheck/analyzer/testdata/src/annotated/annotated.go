// Package annotated exercises hotpathcheck diagnostics.
package annotated

import (
	"math"
	"math/bits"
	"sync/atomic"
)

type T struct{ x int }

func unannotatedHelper() {}

//hotpath:noalloc
func makesSlice() []byte { // want makesSlice:"noalloc"
	return make([]byte, 8) // want `allocation site: make`
}

//hotpath:noalloc
func makesMap() map[int]int { // want makesMap:"noalloc"
	return make(map[int]int) // want `allocation site: make`
}

//hotpath:noalloc
func news() *int { // want news:"noalloc"
	p := new(int) // want `allocation site: new`
	return p
}

//hotpath:noalloc
func appends(s []int) []int { // want appends:"noalloc"
	return append(s, 1) // want `possible allocation: append`
}

//hotpath:noalloc
func addrOfLit() *T { // want addrOfLit:"noalloc"
	return &T{x: 1} // want `allocation site: address of composite literal`
}

//hotpath:noalloc
func sliceLit() []int { // want sliceLit:"noalloc"
	return []int{1, 2, 3} // want `possible allocation: slice literal`
}

//hotpath:noalloc
func mapLit() map[string]int { // want mapLit:"noalloc"
	return map[string]int{"a": 1} // want `allocation site: map literal`
}

//hotpath:noalloc
func convStr(b []byte) string { // want convStr:"noalloc"
	return string(b) // want `allocation site: conversion`
}

//hotpath:noalloc
func convSlice(s string) []byte { // want convSlice:"noalloc"
	return []byte(s) // want `allocation site: conversion`
}

//hotpath:noalloc
func boxing() any { // want boxing:"noalloc"
	return any(42) // want `allocation site: conversion.*boxes`
}

//hotpath:noalloc
func closure() func() { // want closure:"noalloc"
	return func() {} // want `possible allocation: func literal`
}

//hotpath:noalloc
func goStmt() { // want goStmt:"noalloc"
	go unannotatedHelper() // want `allocation: go statement` `unverified call`
}

//hotpath:noalloc
func deferStmt() { // want deferStmt:"noalloc"
	defer unannotatedHelper() // want `allocation: defer` `unverified call`
}

//hotpath:noalloc
func callsUnannotated() { // want callsUnannotated:"noalloc"
	unannotatedHelper() // want `unverified call`
}

//hotpath:noalloc
func callsAnnotated() int { // want callsAnnotated:"noalloc"
	return cleanCallee() // ok — callee is annotated
}

//hotpath:noalloc
func cleanCallee() int { // want cleanCallee:"noalloc"
	return 42
}

//hotpath:noalloc
func builtinsOK(s []int, d []int, m map[int]int) int { // want builtinsOK:"noalloc"
	n := copy(d, s)
	delete(m, 1)
	clear(m)
	return len(s) + cap(s) + n + min(n, 3) + max(n, 2)
}

//hotpath:noalloc
func allowedStandalone(s []int) []int { // want allowedStandalone:"noalloc"
	//hotpath:allow free-list append; capacity is reserved at Reset
	return append(s, 1)
}

//hotpath:noalloc
func allowedInline(s []int) []int { // want allowedInline:"noalloc"
	return append(s, 1) //hotpath:allow bounded
}

// Unannotated functions are never checked, even if they allocate.
func allocatingButUnannotated() []byte {
	return make([]byte, 1<<20)
}

//hotpath:noalloc
func concat(a, b string) string { // want concat:"noalloc"
	const c = "x" + "y" // constant: folded at compile time, no allocation
	return a + b + c    // want `allocation site: string concatenation`
}

//hotpath:noalloc
func concatAssign(a, b string) string { // want concatAssign:"noalloc"
	a += b // want `allocation site: string concatenation`
	return a
}

//hotpath:noalloc
func mapWrites(m map[int]int) { // want mapWrites:"noalloc"
	m[1] = 2 // want `possible allocation: map assignment`
	m[2]++   // want `possible allocation: map assignment`
}

//hotpath:noalloc
func sliceWritesOK(s []int) { // want sliceWritesOK:"noalloc"
	s[0] = 1
	s[1]++
}

//hotpath:noalloc
func stdNonAllocating(x float64, u uint64, n *int64, a *atomic.Int64) float64 { // want stdNonAllocating:"noalloc"
	atomic.AddInt64(n, 1)
	a.Add(1)
	return math.Sqrt(x) + float64(bits.Len64(u))
}

//hotpath:noalloc
func atomicValueIsNot(v *atomic.Value) any { // want atomicValueIsNot:"noalloc"
	return v.Load() // want `unverified call: \(\*sync/atomic.Value\).Load`
}

//hotpath:noalloc
func trailingAllowDoesNotLeak(s []int) []int { // want trailingAllowDoesNotLeak:"noalloc"
	s = append(s, 1)    //hotpath:allow bounded
	return append(s, 2) // want `possible allocation: append`
}

//hotpath:noallocx
func notADirective() []byte { return make([]byte, 1) } // near-miss spelling: not annotated

type box[T any] struct{ v T }

//hotpath:noalloc
func (b *box[T]) get() T { return b.v } // want get:"noalloc"

//hotpath:noalloc
func useGenericMethod(b *box[int]) int { // want useGenericMethod:"noalloc"
	return b.get() // ok — resolves to the annotated generic declaration
}

//hotpath:noalloc
func namesAreRelative() *T { // want namesAreRelative:"noalloc"
	return &T{} // want `address of composite literal &T\{\.\.\.\}`
}
