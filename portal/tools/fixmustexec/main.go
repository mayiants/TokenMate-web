package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

const (
	mustExec   = "MustExec"
	mustExecTx = "MustExecTx"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		log.Fatalf("abs root: %v", err)
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedModule,
		Dir:  absRoot,
	}
	pkgs, err := packages.Load(cfg, "./internal/...")
	if err != nil {
		log.Fatalf("packages.Load: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		log.Fatalf("package load errors")
	}

	// Force eager type-checking of all packages before we rewrite any source files.
	for _, pkg := range pkgs {
		_ = pkg.Syntax
		_ = pkg.TypesInfo
	}

	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			continue
		}
		for _, file := range pkg.Syntax {
			fpath := pkg.Fset.File(file.Pos()).Name()
			if strings.HasSuffix(fpath, "_test.go") && filepath.Base(fpath) == "db_test.go" {
				continue
			}
			src, err := os.ReadFile(fpath)
			if err != nil {
				log.Fatalf("read %s: %v", fpath, err)
			}
			repls := rewriteFile(src, file, pkg.TypesInfo, pkg.Fset)
			if len(repls) == 0 {
				continue
			}
			applyReplacements(fpath, src, repls)
		}
	}

	// Process test setup files separately: packages.Load does not expose them in Syntax.
	err = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" || d.Name() == "node_modules" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "db_test.go" {
			return nil
		}
		if !strings.Contains(path, string(os.PathSeparator)+"internal"+string(os.PathSeparator)) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		repls := rewriteTestFile(src, file, fset)
		if len(repls) == 0 {
			return nil
		}
		applyReplacements(path, src, repls)
		return nil
	})
	if err != nil {
		log.Fatalf("walk test files: %v", err)
	}
}

func applyReplacements(fpath string, src []byte, repls []replacement) {
	// Apply replacements from the end of the file forward so offsets stay valid.
	sort.Slice(repls, func(i, j int) bool {
		return repls[i].start > repls[j].start
	})
	for _, r := range repls {
		src = append(src[:r.start], append([]byte(r.text), src[r.end:]...)...)
	}

	out, err := imports.Process(fpath, src, &imports.Options{
		FormatOnly: false,
		Comments:   true,
		TabIndent:  true,
		TabWidth:   8,
	})
	if err != nil {
		log.Fatalf("imports %s: %v", fpath, err)
	}
	if err := os.WriteFile(fpath, out, 0o644); err != nil {
		log.Fatalf("write %s: %v", fpath, err)
	}
	log.Printf("rewrote %s", fpath)
}

type replacement struct {
	start int
	end   int
	text  string
}

func rewriteFile(src []byte, file *ast.File, info *types.Info, fset *token.FileSet) []replacement {
	var repls []replacement
	var stack []ast.Node
	astutil.Apply(file, func(cursor *astutil.Cursor) bool {
		n := cursor.Node()
		stack = append(stack, n)
		stmt, ok := n.(*ast.ExprStmt)
		if !ok {
			return true
		}
		call, ok := stmt.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		name := sel.Sel.Name
		if name != mustExec && name != mustExecTx {
			return true
		}

		fn := enclosingFunc(stack)
		if fn == nil {
			log.Printf("skipping top-level call at %s", fset.Position(call.Pos()))
			return true
		}

		var repl ast.Stmt
		if name == mustExecTx {
			repl = buildTxIfStmt(call, fn, info)
		} else {
			repl = buildIfStmt(sel, call, fn, info)
		}

		// Format the replacement in isolation so surrounding comments are not absorbed.
		var buf strings.Builder
		if err := format.Node(&buf, token.NewFileSet(), repl); err != nil {
			log.Fatalf("format replacement at %s: %v", fset.Position(stmt.Pos()), err)
		}
		replText := strings.TrimRight(buf.String(), "\n")

		baseIndent := leadingIndent(src, fset, stmt.Pos())
		lines := strings.Split(replText, "\n")
		for i := 1; i < len(lines); i++ {
			lines[i] = baseIndent + lines[i]
		}
		text := strings.Join(lines, "\n")

		tf := fset.File(stmt.Pos())
		start := tf.Offset(stmt.Pos())
		end := tf.Offset(stmt.End())
		repls = append(repls, replacement{start: start, end: end, text: text})
		return true
	}, func(cursor *astutil.Cursor) bool {
		stack = stack[:len(stack)-1]
		return true
	})
	return repls
}

func leadingIndent(src []byte, fset *token.FileSet, pos token.Pos) string {
	tf := fset.File(pos)
	line := tf.Line(pos)
	lineStart := tf.LineStart(line)
	off := tf.Offset(pos)
	return string(src[tf.Offset(lineStart):off])
}

func rewriteTestFile(src []byte, file *ast.File, fset *token.FileSet) []replacement {
	var repls []replacement
	var stack []ast.Node
	astutil.Apply(file, func(cursor *astutil.Cursor) bool {
		n := cursor.Node()
		stack = append(stack, n)
		var call *ast.CallExpr
		var stmtEnd token.Pos
		var isDefer bool
		switch s := n.(type) {
		case *ast.ExprStmt:
			call = s.X.(*ast.CallExpr)
			stmtEnd = s.End()
		case *ast.DeferStmt:
			call = s.Call
			stmtEnd = s.End()
			isDefer = true
		default:
			return true
		}
		if call == nil {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		name := sel.Sel.Name
		if name != mustExec && name != mustExecTx {
			return true
		}

		tName := testingTName(stack)
		if tName == "" {
			return true
		}

		var repl ast.Stmt
		if name == mustExecTx {
			repl = buildTxTestIfStmt(call, tName)
		} else {
			repl = buildTestIfStmt(sel, call, tName)
		}

		if isDefer {
			repl = &ast.DeferStmt{Call: &ast.CallExpr{
				Fun: &ast.FuncLit{
					Type: &ast.FuncType{},
					Body: &ast.BlockStmt{List: []ast.Stmt{repl}},
				},
			}}
		}

		var buf strings.Builder
		if err := format.Node(&buf, token.NewFileSet(), repl); err != nil {
			log.Fatalf("format test replacement at %s: %v", fset.Position(call.Pos()), err)
		}
		replText := strings.TrimRight(buf.String(), "\n")

		baseIndent := leadingIndent(src, fset, n.Pos())
		lines := strings.Split(replText, "\n")
		for i := 1; i < len(lines); i++ {
			lines[i] = baseIndent + lines[i]
		}
		text := strings.Join(lines, "\n")

		tf := fset.File(n.Pos())
		start := tf.Offset(n.Pos())
		end := tf.Offset(stmtEnd)
		repls = append(repls, replacement{start: start, end: end, text: text})
		return true
	}, func(cursor *astutil.Cursor) bool {
		stack = stack[:len(stack)-1]
		return true
	})
	return repls
}

func testingTName(stack []ast.Node) string {
	for i := len(stack) - 1; i >= 0; i-- {
		var sig *ast.FuncType
		switch n := stack[i].(type) {
		case *ast.FuncDecl:
			sig = n.Type
		case *ast.FuncLit:
			sig = n.Type
		}
		if sig == nil {
			continue
		}
		if name := tParamName(sig); name != "" {
			return name
		}
	}
	return ""
}

func tParamName(sig *ast.FuncType) string {
	if sig == nil || sig.Params == nil {
		return ""
	}
	for _, field := range sig.Params.List {
		if types.ExprString(field.Type) == "*testing.T" {
			if len(field.Names) > 0 {
				return field.Names[0].Name
			}
			return "t"
		}
	}
	return ""
}

func buildTestIfStmt(sel *ast.SelectorExpr, call *ast.CallExpr, tName string) ast.Stmt {
	execCall := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   sel.X,
			Sel: &ast.Ident{Name: "Exec"},
		},
		Args: call.Args,
	}
	return buildTestErrorHandling(execCall, tName, false)
}

func buildTxTestIfStmt(call *ast.CallExpr, tName string) ast.Stmt {
	if len(call.Args) < 3 {
		log.Fatalf("MustExecTx call has too few args at %v", call.Pos())
	}
	tx := call.Args[2]
	ctx := call.Args[0]
	rest := make([]ast.Expr, len(call.Args)-3)
	copy(rest, call.Args[3:])
	execArgs := append([]ast.Expr{ctx}, rest...)
	execCall := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   tx,
			Sel: &ast.Ident{Name: "Exec"},
		},
		Args: execArgs,
	}
	return buildTestErrorHandling(execCall, tName, true)
}

func buildTestErrorHandling(execCall *ast.CallExpr, tName string, txExec bool) ast.Stmt {
	lhs := []ast.Expr{&ast.Ident{Name: "err"}}
	if txExec {
		lhs = []ast.Expr{&ast.Ident{Name: "_"}, &ast.Ident{Name: "err"}}
	}
	return &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: lhs,
			Tok: token.DEFINE,
			Rhs: []ast.Expr{execCall},
		},
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "err"},
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{X: &ast.Ident{Name: tName}, Sel: &ast.Ident{Name: "Fatalf"}},
			Args: []ast.Expr{
				&ast.BasicLit{Kind: token.STRING, Value: `"database operation failed: %v"`},
				&ast.Ident{Name: "err"},
			},
		}}}},
	}
}

func enclosingFunc(stack []ast.Node) ast.Node {
	for i := len(stack) - 1; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			return n
		}
	}
	return nil
}

func funcSignature(fn ast.Node) *ast.FuncType {
	switch n := fn.(type) {
	case *ast.FuncDecl:
		return n.Type
	case *ast.FuncLit:
		return n.Type
	}
	return nil
}

func buildIfStmt(sel *ast.SelectorExpr, call *ast.CallExpr, fn ast.Node, info *types.Info) ast.Stmt {
	receiver := sel.X
	execCall := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   receiver,
			Sel: &ast.Ident{Name: "Exec"},
		},
		Args: call.Args,
	}
	return buildErrorHandling(execCall, call, fn, info, nil, receiver, false)
}

func buildTxIfStmt(call *ast.CallExpr, fn ast.Node, info *types.Info) ast.Stmt {
	if len(call.Args) < 3 {
		log.Fatalf("MustExecTx call has too few args at %v", call.Pos())
	}
	tx := call.Args[2]
	ctx := call.Args[0]
	rest := make([]ast.Expr, len(call.Args)-3)
	copy(rest, call.Args[3:])
	execArgs := append([]ast.Expr{ctx}, rest...)
	execCall := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   tx,
			Sel: &ast.Ident{Name: "Exec"},
		},
		Args: execArgs,
	}
	logger := call.Args[1]
	return buildErrorHandling(execCall, call, fn, info, logger, nil, true)
}

func buildErrorHandling(execCall, original *ast.CallExpr, fn ast.Node, info *types.Info, explicitLogger ast.Expr, recv ast.Expr, txExec bool) ast.Stmt {
	sig := funcSignature(fn)
	hasGin, ginName := hasGinContext(sig, info)
	if hasGin && ginName == "" {
		ginName = "c"
	}
	hasTest, tName := hasTestingT(sig, info)
	if hasTest && tName == "" {
		tName = "t"
	}
	errReturn, single := returnsError(sig, info)
	logger := explicitLogger
	if logger == nil {
		logger = findLogger(fn, sig, recv, info)
	}

	var body []ast.Stmt
	if hasGin {
		body = []ast.Stmt{
			&ast.ExprStmt{X: logCall(logger, "database operation failed", "error")},
			&ast.ExprStmt{X: &ast.CallExpr{
				Fun: &ast.SelectorExpr{X: &ast.Ident{Name: ginName}, Sel: &ast.Ident{Name: "JSON"}},
				Args: []ast.Expr{
					&ast.SelectorExpr{X: &ast.Ident{Name: "http"}, Sel: &ast.Ident{Name: "StatusInternalServerError"}},
					&ast.CompositeLit{
						Type: &ast.SelectorExpr{X: &ast.Ident{Name: "gin"}, Sel: &ast.Ident{Name: "H"}},
						Elts: []ast.Expr{
							&ast.KeyValueExpr{Key: &ast.BasicLit{Kind: token.STRING, Value: `"error"`}, Value: &ast.CompositeLit{
								Type: &ast.SelectorExpr{X: &ast.Ident{Name: "gin"}, Sel: &ast.Ident{Name: "H"}},
								Elts: []ast.Expr{
									&ast.KeyValueExpr{Key: &ast.BasicLit{Kind: token.STRING, Value: `"message"`}, Value: &ast.BasicLit{Kind: token.STRING, Value: `"Database operation failed"`}},
								},
							}},
						},
					},
				},
			}},
			&ast.ReturnStmt{},
		}
	} else if hasTest {
		body = []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{X: &ast.Ident{Name: tName}, Sel: &ast.Ident{Name: "Fatalf"}},
			Args: []ast.Expr{
				&ast.BasicLit{Kind: token.STRING, Value: `"database operation failed: %v"`},
				&ast.Ident{Name: "err"},
			},
		}}}
	} else if errReturn {
		var ret ast.Expr
		if single {
			ret = &ast.CallExpr{
				Fun: &ast.SelectorExpr{X: &ast.Ident{Name: "fmt"}, Sel: &ast.Ident{Name: "Errorf"}},
				Args: []ast.Expr{
					&ast.BasicLit{Kind: token.STRING, Value: `"database operation failed: %w"`},
					&ast.Ident{Name: "err"},
				},
			}
		} else {
			ret = &ast.Ident{Name: "err"}
		}
		body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{ret}}}
	} else {
		body = []ast.Stmt{&ast.ExprStmt{X: logCall(logger, "database operation failed", "error")}}
	}

	lhs := []ast.Expr{&ast.Ident{Name: "err"}}
	if txExec {
		lhs = []ast.Expr{&ast.Ident{Name: "_"}, &ast.Ident{Name: "err"}}
	}
	assign := &ast.AssignStmt{
		Lhs: lhs,
		Tok: token.DEFINE,
		Rhs: []ast.Expr{execCall},
	}
	return &ast.IfStmt{
		Init: assign,
		Cond: &ast.BinaryExpr{
			X:  &ast.Ident{Name: "err"},
			Op: token.NEQ,
			Y:  &ast.Ident{Name: "nil"},
		},
		Body: &ast.BlockStmt{List: body},
	}
}

func logCall(logger ast.Expr, msg, level string) ast.Expr {
	method := "Error"
	if level != "error" {
		method = strings.Title(level)
	}
	return &ast.CallExpr{
		Fun: &ast.SelectorExpr{X: logger, Sel: &ast.Ident{Name: method}},
		Args: []ast.Expr{
			&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", msg)},
			&ast.CallExpr{
				Fun:  &ast.SelectorExpr{X: &ast.Ident{Name: "zap"}, Sel: &ast.Ident{Name: "Error"}},
				Args: []ast.Expr{&ast.Ident{Name: "err"}},
			},
		},
	}
}

func hasGinContext(sig *ast.FuncType, info *types.Info) (bool, string) {
	if sig == nil || sig.Params == nil {
		return false, ""
	}
	for _, field := range sig.Params.List {
		t := info.TypeOf(field.Type)
		if t == nil {
			continue
		}
		ts := types.TypeString(t, nil)
		if ts == "*github.com/gin-gonic/gin.Context" {
			if len(field.Names) > 0 {
				return true, field.Names[0].Name
			}
			return true, ""
		}
	}
	return false, ""
}

func hasTestingT(sig *ast.FuncType, info *types.Info) (bool, string) {
	if sig == nil || sig.Params == nil {
		return false, ""
	}
	for _, field := range sig.Params.List {
		t := info.TypeOf(field.Type)
		if t == nil {
			continue
		}
		ts := types.TypeString(t, nil)
		if ts == "*testing.T" {
			if len(field.Names) > 0 {
				return true, field.Names[0].Name
			}
			return true, ""
		}
	}
	return false, ""
}

func returnsError(sig *ast.FuncType, info *types.Info) (has bool, single bool) {
	if sig == nil || sig.Results == nil {
		return false, false
	}
	total := 0
	for _, field := range sig.Results.List {
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		total += n
		t := info.TypeOf(field.Type)
		if t != nil && types.TypeString(t, nil) == "error" {
			has = true
		}
	}
	single = total == 1 && has
	return
}

func findLogger(fn ast.Node, sig *ast.FuncType, recv ast.Expr, info *types.Info) ast.Expr {
	// Method receiver (e.g. the route struct).logger, if such a field exists in this package.
	if decl, ok := fn.(*ast.FuncDecl); ok && decl.Recv != nil && len(decl.Recv.List) > 0 {
		recvName := ""
		for _, n := range decl.Recv.List[0].Names {
			if n.Name != "" {
				recvName = n.Name
				break
			}
		}
		if recvName != "" {
			recvType := info.TypeOf(decl.Recv.List[0].Type)
			if structHasLogger(recvType) {
				return &ast.SelectorExpr{X: &ast.Ident{Name: recvName}, Sel: &ast.Ident{Name: "logger"}}
			}
		}
	}
	// logger parameter
	if sig != nil && sig.Params != nil {
		for _, field := range sig.Params.List {
			if len(field.Names) == 0 {
				continue
			}
			name := field.Names[0].Name
			if name == "logger" {
				return &ast.Ident{Name: name}
			}
		}
	}
	return &ast.CallExpr{Fun: &ast.SelectorExpr{X: &ast.Ident{Name: "zap"}, Sel: &ast.Ident{Name: "L"}}}
}

func structHasLogger(t types.Type) bool {
	if t == nil {
		return false
	}
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	str, ok := named.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := 0; i < str.NumFields(); i++ {
		f := str.Field(i)
		if f.Name() == "logger" {
			ts := types.TypeString(f.Type(), nil)
			if ts == "*go.uber.org/zap.Logger" {
				return true
			}
		}
	}
	return false
}

func receiverHasLogger(recv ast.Expr, info *types.Info) bool {
	if recv == nil || info == nil {
		return false
	}
	t := info.TypeOf(recv)
	if t == nil {
		return false
	}
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	str, ok := named.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := 0; i < str.NumFields(); i++ {
		f := str.Field(i)
		if f.Name() == "logger" {
			ts := types.TypeString(f.Type(), nil)
			if ts == "*go.uber.org/zap.Logger" {
				return true
			}
		}
	}
	return false
}
