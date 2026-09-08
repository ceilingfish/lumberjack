package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	cmdDir      = "cmd"
	cmdPkgPath  = "github.com/ceilingfish/lumberjack/cmd"
	cobraPkg    = "github.com/spf13/cobra"
	cobraCmdTyp = "Command"
)

// Finding is one rule violation, reported as a compiler-style location so
// editors and CI logs can jump to it.
type Finding struct {
	File string
	Line int
	Msg  string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Msg)
}

// cmdFile is one parsed non-test file under cmd/.
type cmdFile struct {
	path     string
	fset     *token.FileSet
	syntax   *ast.File
	commands []string        // top-level funcs returning *cobra.Command
	declared map[string]bool // every name bound anywhere in this file
	topLevel map[string]bool // names bound at package scope by this file
}

// Check runs both rules against the repository rooted at root and returns
// every violation, ordered by file then line.
func Check(root string) ([]Finding, error) {
	files, err := parseCmdDir(filepath.Join(root, cmdDir))
	if err != nil {
		return nil, err
	}
	findings := append(checkOneCommandPerFile(files), checkNoSharedSymbols(files)...)

	imports, err := checkNoOutsideImports(root)
	if err != nil {
		return nil, err
	}
	findings = append(findings, imports...)

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

func parseCmdDir(dir string) ([]*cmdFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var files []*cmdFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parseCmdFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

func parseCmdFile(path string) (*cmdFile, error) {
	fset := token.NewFileSet()
	syntax, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	f := &cmdFile{
		path:     path,
		fset:     fset,
		syntax:   syntax,
		declared: map[string]bool{},
		topLevel: map[string]bool{},
	}
	f.collectDeclared()
	f.commands = f.commandConstructors()
	return f, nil
}

// commandConstructors lists the top-level functions returning *cobra.Command.
// The cobra import's local name is resolved per file so an aliased import is
// still recognised.
func (f *cmdFile) commandConstructors() []string {
	cobra := importAlias(f.syntax, cobraPkg)
	if cobra == "" {
		return nil
	}
	var names []string
	for _, d := range f.syntax.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Type.Results == nil {
			continue
		}
		for _, res := range fn.Type.Results.List {
			if isPointerTo(res.Type, cobra, cobraCmdTyp) {
				names = append(names, fn.Name.Name)
				break
			}
		}
	}
	return names
}

// importAlias returns the name pkgPath is referred to by inside file, or ""
// when the file does not import it.
func importAlias(file *ast.File, pkgPath string) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != pkgPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return filepath.Base(path)
	}
	return ""
}

func isPointerTo(expr ast.Expr, pkg, name string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}
