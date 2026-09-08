package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// checkOneCommandPerFile enforces rule 1: every non-test file under cmd/
// declares exactly one constructor returning *cobra.Command. A file with none
// is shared code that belongs in internal/; a file with several is more than
// one command sharing a file.
func checkOneCommandPerFile(files []*cmdFile) []Finding {
	var findings []Finding
	for _, f := range files {
		switch len(f.commands) {
		case 1:
			continue
		case 0:
			findings = append(findings, Finding{
				File: f.path,
				Line: 1,
				Msg: "declares no function returning *cobra.Command; a file under cmd/ must " +
					"implement exactly one command — move shared code to a package under internal/",
			})
		default:
			findings = append(findings, Finding{
				File: f.path,
				Line: f.fset.Position(f.syntax.Pos()).Line,
				Msg: fmt.Sprintf("declares %d functions returning *cobra.Command (%s); "+
					"give each command its own file",
					len(f.commands), strings.Join(f.commands, ", ")),
			})
		}
	}
	return findings
}

// checkNoSharedSymbols enforces rule 2 within cmd/: no file may reference a
// package-scope symbol declared by another file. The one exception is a
// command constructor, which its parent command has to call to register it.
func checkNoSharedSymbols(files []*cmdFile) []Finding {
	owner := map[string]*cmdFile{}
	constructor := map[string]bool{}
	for _, f := range files {
		for name := range f.topLevel {
			owner[name] = f
		}
		for _, name := range f.commands {
			constructor[name] = true
		}
	}

	var findings []Finding
	for _, f := range files {
		reported := map[string]bool{}
		for _, ident := range f.usedIdents() {
			name := ident.Name
			if f.declared[name] || constructor[name] || reported[name] {
				continue
			}
			home, ok := owner[name]
			if !ok || home == f {
				continue
			}
			reported[name] = true
			findings = append(findings, Finding{
				File: f.path,
				Line: f.fset.Position(ident.Pos()).Line,
				Msg: fmt.Sprintf("uses %q, declared in %s; commands must not share code — "+
					"move it to a package under internal/", name, filepath.Base(home.path)),
			})
		}
	}
	return findings
}

// checkNoOutsideImports enforces rule 2's other half: nothing may import the
// cmd package except the root main package, which exists to call it.
func checkNoOutsideImports(root string) ([]Finding, error) {
	var findings []Finding
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel := strings.TrimPrefix(path, root+string(filepath.Separator))
		if rel == "main.go" || strings.HasPrefix(rel, cmdDir+string(filepath.Separator)) {
			return nil
		}

		file, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if perr != nil {
			return fmt.Errorf("parsing %s: %w", path, perr)
		}
		for _, spec := range file.Imports {
			imported, uerr := strconv.Unquote(spec.Path.Value)
			if uerr != nil || imported != cmdPkgPath {
				continue
			}
			findings = append(findings, Finding{
				File: rel,
				Line: fset.Position(spec.Pos()).Line,
				Msg: fmt.Sprintf("imports %s; the command package is a leaf — "+
					"move the shared code to a package under internal/", cmdPkgPath),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

func skipDir(name string) bool {
	return name == ".git" || name == "bin" || name == "node_modules" ||
		name == "macos" || strings.HasPrefix(name, ".claude")
}
