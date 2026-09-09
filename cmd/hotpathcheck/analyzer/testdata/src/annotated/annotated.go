// Package annotated exercises hotpathcheck diagnostics.
package annotated

type T struct{ x int }

func unannotatedHelper() {}

//hotpath:noalloc
func makesSlice() []byte {
	return make([]byte, 8) // want `allocation site: make`
}

//hotpath:noalloc
func makesMap() map[int]int {
	return make(map[int]int) // want `allocation site: make`
}

//hotpath:noalloc
func news() *int {
	p := new(int) // want `allocation site: new`
	return p
}

//hotpath:noalloc
func appends(s []int) []int {
	return append(s, 1) // want `possible allocation: append`
}

//hotpath:noalloc
func addrOfLit() *T {
	return &T{x: 1} // want `allocation site: address of composite literal`
}

//hotpath:noalloc
func sliceLit() []int {
	return []int{1, 2, 3} // want `possible allocation: slice literal`
}

//hotpath:noalloc
func mapLit() map[string]int {
	return map[string]int{"a": 1} // want `allocation site: map literal`
}

//hotpath:noalloc
func convStr(b []byte) string {
	return string(b) // want `allocation site: conversion`
}

//hotpath:noalloc
func convSlice(s string) []byte {
	return []byte(s) // want `allocation site: conversion`
}

//hotpath:noalloc
func boxing() any {
	return any(42) // want `allocation site: conversion.*boxes`
}

//hotpath:noalloc
func closure() func() {
	return func() {} // want `possible allocation: func literal`
}

//hotpath:noalloc
func goStmt() {
	go unannotatedHelper() // want `allocation: go statement` `unverified call`
}

//hotpath:noalloc
func deferStmt() {
	defer unannotatedHelper() // want `allocation: defer` `unverified call`
}

//hotpath:noalloc
func callsUnannotated() {
	unannotatedHelper() // want `unverified call`
}

//hotpath:noalloc
func callsAnnotated() int {
	return cleanCallee() // ok — callee is annotated
}

//hotpath:noalloc
func cleanCallee() int {
	return 42
}

//hotpath:noalloc
func builtinsOK(s []int, d []int, m map[int]int) int {
	n := copy(d, s)
	delete(m, 1)
	clear(m)
	return len(s) + cap(s) + n + min(n, 3) + max(n, 2)
}

//hotpath:noalloc
func allowedStandalone(s []int) []int {
	//hotpath:allow free-list append; capacity is reserved at Reset
	return append(s, 1)
}

//hotpath:noalloc
func allowedInline(s []int) []int {
	return append(s, 1) //hotpath:allow bounded
}

// Unannotated functions are never checked, even if they allocate.
func allocatingButUnannotated() []byte {
	return make([]byte, 1<<20)
}
