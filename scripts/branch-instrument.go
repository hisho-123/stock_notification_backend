//go:build ignore

// branch-instrument instruments a temporary source copy, never the checkout.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type branch struct {
	ID    string   `json:"id"`
	File  string   `json:"file"`
	Line  int      `json:"line"`
	Kind  string   `json:"kind"`
	Edges []string `json:"edges"`
}

var branches []branch
var fset = token.NewFileSet()
var currentFile string

func register(pos token.Pos, kind string, edges ...string) string {
	line := fset.Position(pos).Line
	id := fmt.Sprintf("%s:%d:%d:%s", currentFile, line, fset.Position(pos).Column, kind)
	branches = append(branches, branch{id, currentFile, line, kind, edges})
	return id
}
func literal(s string) *ast.BasicLit {
	return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(s)}
}
func hit(id, edge string) ast.Stmt {
	return &ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("__coverageBranchHit"), Args: []ast.Expr{literal(id + ":" + edge)}}}
}
func boolean(id string, expr ast.Expr) ast.Expr {
	return &ast.CallExpr{Fun: ast.NewIdent("__coverageBranchBool"), Args: []ast.Expr{literal(id), expr}}
}

func boolResult(t *ast.FuncType) bool {
	if t.Results == nil || len(t.Results.List) != 1 || len(t.Results.List[0].Names) > 1 {
		return false
	}
	id, ok := t.Results.List[0].Type.(*ast.Ident)
	return ok && id.Name == "bool"
}

// Tests in this repository run sequentially within each test process. Setenv
// tracks subtest names, restores their parent label, and reaches CLI children.
func instrumentTests(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		var body *ast.BlockStmt
		var typ *ast.FuncType
		switch n := n.(type) {
		case *ast.FuncDecl:
			body, typ = n.Body, n.Type
		case *ast.FuncLit:
			body, typ = n.Body, n.Type
		}
		if body == nil || typ.Params == nil || len(typ.Params.List) != 1 {
			return true
		}
		param := typ.Params.List[0]
		ptr, ok := param.Type.(*ast.StarExpr)
		if !ok || len(param.Names) != 1 {
			return true
		}
		sel, ok := ptr.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "T" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "testing" {
			return true
		}
		t := param.Names[0]
		name := &ast.CallExpr{Fun: &ast.SelectorExpr{X: t, Sel: ast.NewIdent("Name")}}
		set := &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: t, Sel: ast.NewIdent("Setenv")}, Args: []ast.Expr{literal("STOCK_BRANCH_TEST"), name}}}
		body.List = append([]ast.Stmt{set}, body.List...)
		return true
	})
}

func instrument(file *ast.File) {
	// Fail explicitly on constructs whose edges this project-specific audit
	// does not model, rather than silently reporting complete coverage.
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.TypeSwitchStmt, *ast.LabeledStmt:
			panic("type switches and labels need explicit instrumentation")
		case *ast.BranchStmt:
			if n.Tok == token.GOTO || n.Tok == token.FALLTHROUGH || n.Label != nil {
				panic("goto, fallthrough and labeled exits need explicit instrumentation")
			}
		}
		return true
	})
	// Boolean return values are decisions too (e.g. session expiration).
	ast.Inspect(file, func(n ast.Node) bool {
		var body *ast.BlockStmt
		var typ *ast.FuncType
		switch n := n.(type) {
		case *ast.FuncDecl:
			body, typ = n.Body, n.Type
		case *ast.FuncLit:
			body, typ = n.Body, n.Type
		}
		if body == nil || !boolResult(typ) {
			return true
		}
		ast.Inspect(body, func(n ast.Node) bool {
			if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			if ret, ok := n.(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
				if id, ok := ret.Results[0].(*ast.Ident); ok && (id.Name == "true" || id.Name == "false") {
					return true
				}
				ret.Results[0] = boolean(register(ret.Pos(), "return", "true", "false"), ret.Results[0])
			}
			return true
		})
		return true
	})
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.IfStmt:
			n.Cond = boolean(register(n.Pos(), "if", "true", "false"), n.Cond)
		case *ast.ForStmt:
			if n.Cond != nil {
				n.Cond = boolean(register(n.Pos(), "for", "true", "false"), n.Cond)
			}
		case *ast.SwitchStmt:
			// Include the no-match edge when no default was written.
			hasDefault := false
			for _, item := range n.Body.List {
				if item.(*ast.CaseClause).List == nil {
					hasDefault = true
				}
			}
			if !hasDefault {
				n.Body.List = append(n.Body.List, &ast.CaseClause{})
			}
			for i, item := range n.Body.List {
				c := item.(*ast.CaseClause)
				kind := fmt.Sprintf("switch-case-%d", i)
				if c.List == nil {
					kind = "switch-default"
				}
				id := register(n.Pos(), kind, "taken")
				c.Body = append([]ast.Stmt{hit(id, "taken")}, c.Body...)
			}
		case *ast.SelectStmt:
			for i, item := range n.Body.List {
				c := item.(*ast.CommClause)
				id := register(n.Pos(), fmt.Sprintf("select-case-%d", i), "taken")
				c.Body = append([]ast.Stmt{hit(id, "taken")}, c.Body...)
			}
		}
		return true
	})
	// A range has an entry and exhaustion edge. A break must not count as
	// exhaustion; an early return naturally bypasses the trailing probe.
	ast.Inspect(file, func(n ast.Node) bool {
		var list *[]ast.Stmt
		switch n := n.(type) {
		case *ast.BlockStmt:
			list = &n.List
		case *ast.CaseClause:
			list = &n.Body
		case *ast.CommClause:
			list = &n.Body
		}
		if list == nil {
			return true
		}
		var result []ast.Stmt
		for _, stmt := range *list {
			r, ok := stmt.(*ast.RangeStmt)
			if !ok {
				result = append(result, stmt)
				continue
			}
			id := register(r.Pos(), "range", "true", "false")
			flag := "__coverageRangeExhausted" + strconv.Itoa(len(branches))
			targets := map[*ast.BranchStmt]bool{}
			ast.Inspect(r.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.FuncLit:
					return false
				case *ast.BranchStmt:
					if n.Tok == token.GOTO || n.Label != nil {
						panic("labeled exits need explicit range instrumentation")
					}
					if n.Tok == token.BREAK {
						targets[n] = true
					}
				}
				return true
			})
			hasBreak := len(targets) != 0
			if hasBreak {
				ast.Inspect(r.Body, func(n ast.Node) bool {
					var list *[]ast.Stmt
					switch n := n.(type) {
					case *ast.BlockStmt:
						list = &n.List
					case *ast.CaseClause:
						list = &n.Body
					case *ast.CommClause:
						list = &n.Body
					}
					if list == nil {
						return true
					}
					for i, stmt := range *list {
						if b, ok := stmt.(*ast.BranchStmt); ok && targets[b] {
							delete(targets, b)
							(*list)[i] = &ast.BlockStmt{List: []ast.Stmt{&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(flag)}, Tok: token.ASSIGN, Rhs: []ast.Expr{ast.NewIdent("false")}}, b}}
						}
					}
					return true
				})
				result = append(result, &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(flag)}, Tok: token.DEFINE, Rhs: []ast.Expr{ast.NewIdent("true")}})
			}
			r.Body.List = append([]ast.Stmt{hit(id, "true")}, r.Body.List...)
			result = append(result, r)
			if hasBreak {
				result = append(result, &ast.IfStmt{Cond: ast.NewIdent(flag), Body: &ast.BlockStmt{List: []ast.Stmt{hit(id, "false")}}})
			} else {
				result = append(result, hit(id, "false"))
			}
		}
		*list = result
		return true
	})
}

const helper = `
import (
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "os"
 "path/filepath"
 "sync"
)
var __coverageBranchMu sync.Mutex
var __coverageBranchSeen=map[string]bool{}
func __coverageBranchHit(id string){
 __coverageBranchMu.Lock()
 defer __coverageBranchMu.Unlock()
 test:=os.Getenv("STOCK_BRANCH_TEST")
 if test==""{test="<process>"}
 key:=test+"\n"+id
 if __coverageBranchSeen[key]{return}
 dir:=os.Getenv("STOCK_BRANCH_COVERDIR")
 if dir==""{panic("STOCK_BRANCH_COVERDIR is required")}
 digest:=sha256.Sum256([]byte(key))
 name:=hex.EncodeToString(digest[:])
 data,_:=json.Marshal(struct{ID string ` + "`json:\"id\"`" + `;Test string ` + "`json:\"test\"`" + `}{id,test})
 if err:=os.WriteFile(filepath.Join(dir,name),data,0600);err!=nil{panic(err)}
 __coverageBranchSeen[key]=true
}
func __coverageBranchBool(id string,value bool)bool{
 if value{__coverageBranchHit(id+":true")}else{__coverageBranchHit(id+":false")}
 return value
}
`

func main() {
	root, manifest := os.Args[1], os.Args[2]
	packages := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		currentFile, err = filepath.Rel(root, path)
		if err != nil {
			return err
		}
		currentFile = filepath.ToSlash(currentFile)
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, "_test.go") {
			instrumentTests(file)
		} else {
			instrument(file)
			packages[filepath.Dir(path)] = file.Name.Name
		}
		out, err := os.Create(path)
		if err != nil {
			return err
		}
		err = format.Node(out, fset, file)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		panic(err)
	}
	for dir, name := range packages {
		if err := os.WriteFile(filepath.Join(dir, "zz_branch_audit.go"), []byte("package "+name+helper), 0600); err != nil {
			panic(err)
		}
	}
	data, err := json.MarshalIndent(branches, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		panic(err)
	}
}
