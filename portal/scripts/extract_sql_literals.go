//go:build ignore

// extract_sql_literals walks the Go source tree and prints every string
// literal that looks like SQL. It is used by scripts/check_schema_vs_code.sh
// to build the set of SQL statements that must be checked against the schema.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var sqlHint = regexp.MustCompile(`\b(INSERT|UPDATE|DELETE|SELECT|ALTER|CREATE|DROP|SET|FROM|WHERE|JOIN|RETURNING|TABLE)\b`)

func looksLikeSQL(s string) bool {
	return sqlHint.MatchString(s)
}

func extractStringsFromFile(path string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
	if err != nil {
		// Ignore parse errors caused by build tags or partial files; we only
		// need string literals.
		return nil
	}

	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v := lit.Value
		var content string
		if strings.HasPrefix(v, "`") && strings.HasSuffix(v, "`") && len(v) >= 2 {
			content = v[1 : len(v)-1]
		} else {
			// Interpreted string literal.
			if unq, err := strconv.Unquote(v); err == nil {
				content = unq
			} else {
				content = v
			}
		}
		if looksLikeSQL(content) {
			fmt.Println(content)
		}
		return true
	})
	return nil
}

func main() {
	for _, arg := range os.Args[1:] {
		info, err := os.Stat(arg)
		if err != nil {
			continue
		}
		if info.IsDir() {
			_ = filepath.Walk(arg, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				if !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
					return nil
				}
				_ = extractStringsFromFile(path)
				return nil
			})
		} else {
			_ = extractStringsFromFile(arg)
		}
	}
}
