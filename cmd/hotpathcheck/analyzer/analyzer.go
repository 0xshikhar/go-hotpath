// Package analyzer implements hotpathcheck: diagnostics for syntactic
// allocation sites inside functions annotated //hotpath:noalloc.
//
// Directives
//
//	//hotpath:noalloc   on a function doc comment: the function must not
//	                   allocate, and may only call other annotated
//	                   functions. Transitive by design — "noalloc" is a
//	                   property of the whole call tree.
//
//	//hotpath:allow     on the same line as a site or the line above it:
//	                   suppresses the diagnostic at that position. Use it
//	                   for bounded appends, stack-promoted allocations you
//	                   have verified, and calls to code you cannot annotate.
//
// What counts as a site (heuristic — syntactic, pre-optimization):
//
//	new(T)                     allocation site
//	make(slice/map/chan)       allocation site
//	append(...)                may reallocate
//	&T{...}                    address of literal: heap pointer
//	[]T{...}, map{...}         composite literals of ref types
//	string(x), []byte(x)       conversions that copy data
//	any(x), I(x)               interface boxing
//	func literal               closure allocation
//	go / defer                 goroutine + deferred-call records
//	call to unannotated func   unverified — could contain any of the above
//
// The analyzer sees syntax, not escape analysis: `make([]byte, 8)` that the
// compiler would promote to the stack is still flagged. testing.AllocsPerRun
// and guard.Exact remain the ground truth — this tool catches regressions at
// write time, the runtime checks verify at run time.
package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer is the //hotpath:noalloc checker.
var Analyzer = &analysis.Analyzer{
	Name:      "hotpathcheck",
	Doc:       "reports syntactic allocation sites inside functions marked //hotpath:noalloc",
	Run:       run,
	Requires:  []*analysis.Analyzer{inspect.Analyzer},
	FactTypes: []analysis.Fact{(*noallocFact)(nil)},
}

// noallocFact marks a function object whose declaration carries
// //hotpath:noalloc, so callers in other packages can be verified.
type noallocFact struct{}

func (*noallocFact) AFact()         {}
func (*noallocFact) String() string { return "noalloc" }

func run(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	annotated := make(map[*types.Func]bool)
	var decls []*ast.FuncDecl
	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil)}, func(n ast.Node) {
		fd := n.(*ast.FuncDecl)
		decls = append(decls, fd)
		fn, _ := pass.TypesInfo.Defs[fd.Name].(*types.Func)
		if fn != nil && hasDirective(fd.Doc, "noalloc") {
			annotated[fn] = true
			pass.ExportObjectFact(fn, &noallocFact{})
		}
	})
	if len(annotated) == 0 {
		return nil, nil
	}

	c := &checker{
		pass:      pass,
		annotated: annotated,
		allowed:   allowedLines(pass),
	}
	for _, fd := range decls {
		if fd.Body == nil {
			continue
		}
		fn, _ := pass.TypesInfo.Defs[fd.Name].(*types.Func)
		if annotated[fn] {
			c.funcDecl(fd)
		}
	}
	return nil, nil
}

// hasDirective reports whether a doc comment contains `//hotpath:<name>`.
// A space after // is tolerated.
func hasDirective(doc *ast.CommentGroup, name string) bool {
	if doc == nil {
		return false
	}
	for _, c := range doc.List {
		text := strings.TrimPrefix(c.Text, "//")
		text = strings.TrimSpace(text)
		if strings.HasPrefix(text, "hotpath:"+name) {
			return true
		}
	}
	return false
}

// allowedLines collects the set of source lines carrying a
// //hotpath:allow comment anywhere in the file (doc comments included).
// A line is allowed if it carries the comment itself — trailing style — and
// the line *below* a standalone allow comment is also allowed.
func allowedLines(pass *analysis.Pass) map[posKey]bool {
	allowed := make(map[posKey]bool)
	for _, f := range pass.Files {
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
				if strings.HasPrefix(text, "hotpath:allow") {
					p := pass.Fset.Position(c.Pos())
					allowed[posKey{p.Filename, p.Line}] = true
					allowed[posKey{p.Filename, p.Line + 1}] = true
				}
			}
		}
	}
	return allowed
}

type posKey struct {
	file string
	line int
}

type checker struct {
	pass      *analysis.Pass
	annotated map[*types.Func]bool
	allowed   map[posKey]bool
}

func (c *checker) report(pos token.Pos, format string, args ...any) {
	p := c.pass.Fset.Position(pos)
	if c.allowed[posKey{p.Filename, p.Line}] {
		return
	}
	c.pass.Reportf(pos, format, args...)
}

func (c *checker) funcDecl(fd *ast.FuncDecl) {
	addrOf := make(map[*ast.CompositeLit]bool)
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.UnaryExpr:
			if node.Op == token.AND {
				if cl, ok := node.X.(*ast.CompositeLit); ok {
					addrOf[cl] = true
					c.report(node.Pos(), "allocation site: address of composite literal &%s{...}",
						typeName(c.pass.TypesInfo.TypeOf(cl)))
				}
			}
		case *ast.CompositeLit:
			if addrOf[node] {
				return true // already reported at the &
			}
			t := c.pass.TypesInfo.TypeOf(node)
			if t == nil {
				return true
			}
			switch t.Underlying().(type) {
			case *types.Map:
				c.report(node.Pos(), "allocation site: map literal %s", typeName(t))
			case *types.Slice:
				c.report(node.Pos(), "possible allocation: slice literal %s", typeName(t))
			}
		case *ast.FuncLit:
			c.report(node.Pos(), "possible allocation: func literal (closure)")
		case *ast.GoStmt:
			c.report(node.Pos(), "allocation: go statement starts a goroutine")
		case *ast.DeferStmt:
			c.report(node.Pos(), "allocation: defer may allocate a deferred call")
		case *ast.CallExpr:
			c.call(node)
		}
		return true
	})
}

func (c *checker) call(n *ast.CallExpr) {
	// Type conversions appear as CallExpr: Fun's type is a type.
	if tv, ok := c.pass.TypesInfo.Types[n.Fun]; ok && tv.IsType() {
		c.conversion(n)
		return
	}

	// Builtins: only new/make/append can allocate. len/cap/copy/delete/
	// min/max/clear/clear/print/println/recover etc. cannot.
	if id, ok := n.Fun.(*ast.Ident); ok {
		if b, ok := c.pass.TypesInfo.ObjectOf(id).(*types.Builtin); ok {
			switch b.Name() {
			case "new":
				c.report(n.Pos(), "allocation site: new(T)")
			case "make":
				c.report(n.Pos(), "allocation site: make(...)")
			case "append":
				c.report(n.Pos(), "possible allocation: append may reallocate the slice")
			}
			return
		}
	}

	fn := calleeFunc(c.pass.TypesInfo, n.Fun)
	if fn == nil {
		return // indirect calls (func values, interface dispatch we can't see) — out of scope for v1
	}
	if fn.Pkg() != nil && fn.Pkg() != c.pass.Pkg {
		var f noallocFact
		if c.pass.ImportObjectFact(fn, &f) {
			return
		}
	} else if c.annotated[fn] {
		return
	}
	// An annotated function may only call annotated functions.
	c.report(n.Pos(), "unverified call: %s is not marked //hotpath:noalloc", fn.Name())
}

func (c *checker) conversion(n *ast.CallExpr) {
	target := c.pass.TypesInfo.TypeOf(n)
	if target == nil {
		return
	}
	switch t := target.Underlying().(type) {
	case *types.Basic:
		if t.Kind() == types.String {
			c.report(n.Pos(), "allocation site: conversion to %s copies bytes", typeName(target))
		}
	case *types.Slice:
		c.report(n.Pos(), "allocation site: conversion to %s may allocate", typeName(target))
	case *types.Interface:
		c.report(n.Pos(), "allocation site: conversion to %s boxes the value", typeName(target))
	}
}

// calleeFunc resolves a call's Fun expression to its *types.Func, unwrapping
// generic instantiation. Returns nil for builtins, methods expressions we
// can't resolve, and indirect calls.
func calleeFunc(info *types.Info, expr ast.Expr) *types.Func {
	switch f := expr.(type) {
	case *ast.Ident:
		fn, _ := info.ObjectOf(f).(*types.Func)
		return fn
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[f]; ok {
			fn, _ := sel.Obj().(*types.Func)
			return fn
		}
		fn, _ := info.ObjectOf(f.Sel).(*types.Func)
		return fn
	case *ast.IndexExpr: // f[T]
		return calleeFunc(info, f.X)
	case *ast.IndexListExpr: // f[T,U]
		return calleeFunc(info, f.X)
	}
	return nil
}

func typeName(t types.Type) string {
	if t == nil {
		return "?"
	}
	return fmt.Sprintf("%s", t)
}
