package main

import "go/ast"

// collectDeclared records every name this file binds — at package scope and in
// every nested scope. Rule 2 then treats any identifier used but not bound
// here as a reference into another file. Folding all scopes into one flat set
// means a local that shadows another file's symbol is silently accepted: a
// missed violation rather than a false one, which is the right way for a gate
// to be wrong.
func (f *cmdFile) collectDeclared() {
	for _, d := range f.syntax.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				f.bindTop(d.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					f.bindTop(s.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						f.bindTop(n)
					}
				}
			}
		}
	}

	ast.Inspect(f.syntax, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			f.bindFieldNames(n.Recv)
			f.bindFuncType(n.Type)
		case *ast.FuncLit:
			f.bindFuncType(n.Type)
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				f.bindExpr(lhs)
			}
		case *ast.RangeStmt:
			f.bindExpr(n.Key)
			f.bindExpr(n.Value)
		case *ast.TypeSwitchStmt:
			if a, ok := n.Assign.(*ast.AssignStmt); ok {
				for _, lhs := range a.Lhs {
					f.bindExpr(lhs)
				}
			}
		case *ast.LabeledStmt:
			f.bind(n.Label)
		case *ast.ValueSpec:
			for _, name := range n.Names {
				f.bind(name)
			}
		case *ast.TypeSpec:
			f.bind(n.Name)
		}
		return true
	})
}

func (f *cmdFile) bindFuncType(t *ast.FuncType) {
	f.bindFieldNames(t.TypeParams)
	f.bindFieldNames(t.Params)
	f.bindFieldNames(t.Results)
}

func (f *cmdFile) bindFieldNames(list *ast.FieldList) {
	if list == nil {
		return
	}
	for _, field := range list.List {
		for _, name := range field.Names {
			f.bind(name)
		}
	}
}

func (f *cmdFile) bindExpr(e ast.Expr) {
	if ident, ok := e.(*ast.Ident); ok {
		f.bind(ident)
	}
}

func (f *cmdFile) bind(ident *ast.Ident) {
	if ident != nil && ident.Name != "_" {
		f.declared[ident.Name] = true
	}
}

func (f *cmdFile) bindTop(ident *ast.Ident) {
	if ident != nil && ident.Name != "_" {
		f.topLevel[ident.Name] = true
		f.declared[ident.Name] = true
	}
}

// usedIdents lists the identifiers this file reads, skipping the positions
// where an identifier is a field name, a selector's right-hand side, a struct
// literal key, a label, or an import alias rather than a reference to a
// package-scope symbol.
func (f *cmdFile) usedIdents() []*ast.Ident {
	skip := map[ast.Node]bool{}
	var used []*ast.Ident

	ast.Inspect(f.syntax, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.SelectorExpr:
			skip[n.Sel] = true
		case *ast.KeyValueExpr:
			if _, ok := n.Key.(*ast.Ident); ok {
				skip[n.Key] = true
			}
		case *ast.LabeledStmt:
			skip[n.Label] = true
		case *ast.BranchStmt:
			if n.Label != nil {
				skip[n.Label] = true
			}
		case *ast.StructType:
			markFieldNames(skip, n.Fields)
		case *ast.InterfaceType:
			markFieldNames(skip, n.Methods)
		case *ast.Ident:
			if !skip[n] {
				used = append(used, n)
			}
		}
		return true
	})
	return used
}

func markFieldNames(skip map[ast.Node]bool, list *ast.FieldList) {
	for _, field := range list.List {
		for _, name := range field.Names {
			skip[name] = true
		}
	}
}
