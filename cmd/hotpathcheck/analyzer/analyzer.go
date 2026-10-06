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
//	any(x), I(x)               explicit interface boxing
//	a + b, s += t (strings)    string concatenation
//	m[k] = v, m[k]++           map assignment may grow the map
//	func literal               closure allocation
//	go / defer                 goroutine + deferred-call records
//	call to unannotated func   unverified — could contain any of the above
//
// Calls into math, math/bits, and sync/atomic (except atomic.Value) are
// treated as verified: those packages do not allocate.
//
// The analyzer sees syntax, not escape analysis: `make([]byte, 8)` that the
// compiler would promote to the stack is still flagged. It also cannot see
// implicit interface conversions (passing a concrete value to an `any`
// parameter), method values, or calls through function values and
// interfaces it cannot resolve. testing.AllocsPerRun and guard.Exact remain
// the ground truth — this tool catches regressions at write time, the
// runtime checks verify at run time.
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

// isDirective reports whether a comment is `//hotpath:<name>`, optionally
// followed by a space and a reason. `//hotpath:noallocx` does not match.
func isDirective(c *ast.Comment, name string) bool {
	text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
	d := "hotpath:" + name
	return text == d || strings.HasPrefix(text, d+" ")
}

// hasDirective reports whether a doc comment carries `//hotpath:<name>`.
func hasDirective(doc *ast.CommentGroup, name string) bool {
	if doc == nil {
		return false
	}
	for _, c := range doc.List {
		if isDirective(c, name) {
			return true
		}
	}
	return false
}

// allowedLines collects the source lines covered by a //hotpath:allow
// comment. A trailing comment covers its own line; a comment alone on its
// line covers the line below it.
func allowedLines(pass *analysis.Pass) map[posKey]bool {
	allowed := make(map[posKey]bool)
	for _, f := range pass.Files {
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if !isDirective(c, "allow") {
					continue
				}
				p := pass.Fset.Position(c.Pos())
				line := p.Line
				if standalone(pass, c.Pos()) {
					line++
				}
				allowed[posKey{p.Filename, line}] = true
			}
		}
	}
	return allowed
}

// standalone reports whether only whitespace precedes pos on its line. If
// the source can't be read, the comment is treated as standalone.
func standalone(pass *analysis.Pass, pos token.Pos) bool {
	tf := pass.Fset.File(pos)
	if tf == nil || pass.ReadFile == nil {
		return true
	}
	src, err := pass.ReadFile(tf.Name())
	if err != nil {
		return true
	}
	line := tf.Line(pos)
	start, end := tf.Offset(tf.LineStart(line)), tf.Offset(pos)
	if end > len(src) {
		return true
	}
	return strings.TrimSpace(string(src[start:end])) == ""
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
	concatSeen := make(map[*ast.BinaryExpr]bool)
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BinaryExpr:
			if !concatSeen[node] && c.isStringConcat(node) {
				c.markConcat(node, concatSeen)
				c.report(node.Pos(), "allocation site: string concatenation")
			}
		case *ast.AssignStmt:
			if node.Tok == token.ADD_ASSIGN && len(node.Lhs) == 1 && isString(c.pass.TypesInfo.TypeOf(node.Lhs[0])) {
				c.report(node.Pos(), "allocation site: string concatenation")
			}
			for _, lhs := range node.Lhs {
				c.mapWrite(lhs)
			}
		case *ast.IncDecStmt:
			c.mapWrite(node.X)
		case *ast.UnaryExpr:
			if node.Op == token.AND {
				if cl, ok := node.X.(*ast.CompositeLit); ok {
					addrOf[cl] = true
					c.report(node.Pos(), "allocation site: address of composite literal &%s{...}",
						c.typeName(c.pass.TypesInfo.TypeOf(cl)))
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
				c.report(node.Pos(), "allocation site: map literal %s", c.typeName(t))
			case *types.Slice:
				c.report(node.Pos(), "possible allocation: slice literal %s", c.typeName(t))
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
		return // indirect calls (func values, interface dispatch we can't see) are out of scope
	}
	fn = fn.Origin() // methods of instantiated generic types resolve to their declaration
	if nonAllocatingStd(fn) {
		return
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
	c.report(n.Pos(), "unverified call: %s is not marked //hotpath:noalloc", c.funcName(fn))
}

// nonAllocatingStd reports calls into standard packages that never allocate.
// atomic.Value is excluded: Store/Swap/CompareAndSwap box their argument.
func nonAllocatingStd(fn *types.Func) bool {
	if fn.Pkg() == nil {
		return false
	}
	switch fn.Pkg().Path() {
	case "math", "math/bits":
		return true
	case "sync/atomic":
		recv := fn.Type().(*types.Signature).Recv()
		if recv == nil {
			return true
		}
		t := recv.Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		named, ok := t.(*types.Named)
		return !ok || named.Obj().Name() != "Value"
	}
	return false
}

// mapWrite reports an assignment target that writes into a map.
func (c *checker) mapWrite(lhs ast.Expr) {
	ix, ok := ast.Unparen(lhs).(*ast.IndexExpr)
	if !ok {
		return
	}
	if t := c.pass.TypesInfo.TypeOf(ix.X); t != nil {
		if _, ok := t.Underlying().(*types.Map); ok {
			c.report(lhs.Pos(), "possible allocation: map assignment may grow the map")
		}
	}
}

// isStringConcat reports a non-constant string + expression.
func (c *checker) isStringConcat(b *ast.BinaryExpr) bool {
	if b.Op != token.ADD {
		return false
	}
	tv, ok := c.pass.TypesInfo.Types[b]
	return ok && tv.Value == nil && isString(tv.Type)
}

// markConcat marks the nested operands of a concatenation chain so that
// a + b + c is reported once, at the outermost expression.
func (c *checker) markConcat(b *ast.BinaryExpr, seen map[*ast.BinaryExpr]bool) {
	seen[b] = true
	for _, op := range []ast.Expr{b.X, b.Y} {
		if inner, ok := ast.Unparen(op).(*ast.BinaryExpr); ok && c.isStringConcat(inner) {
			c.markConcat(inner, seen)
		}
	}
}

func isString(t types.Type) bool {
	if t == nil {
		return false
	}
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

func (c *checker) conversion(n *ast.CallExpr) {
	target := c.pass.TypesInfo.TypeOf(n)
	if target == nil {
		return
	}
	switch t := target.Underlying().(type) {
	case *types.Basic:
		if t.Kind() == types.String {
			c.report(n.Pos(), "allocation site: conversion to %s copies bytes", c.typeName(target))
		}
	case *types.Slice:
		c.report(n.Pos(), "allocation site: conversion to %s may allocate", c.typeName(target))
	case *types.Interface:
		c.report(n.Pos(), "allocation site: conversion to %s boxes the value", c.typeName(target))
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

// typeName renders t relative to the package under analysis: OrderA rather
// than example.com/app/book.OrderA.
func (c *checker) typeName(t types.Type) string {
	if t == nil {
		return "?"
	}
	return types.TypeString(t, types.RelativeTo(c.pass.Pkg))
}

// funcName renders a callee as Name or (Recv).Name.
func (c *checker) funcName(fn *types.Func) string {
	if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
		return fmt.Sprintf("(%s).%s", c.typeName(recv.Type()), fn.Name())
	}
	if fn.Pkg() != nil && fn.Pkg() != c.pass.Pkg {
		return fn.Pkg().Name() + "." + fn.Name()
	}
	return fn.Name()
}
