package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repo writes a throwaway repository whose cmd/ holds the given files, keyed
// by base name, and returns its root.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, cmdDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, cmdDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func check(t *testing.T, root string) []Finding {
	t.Helper()
	got, err := Check(root)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return got
}

// wantOne asserts exactly one finding, whose message contains substr.
func wantOne(t *testing.T, got []Finding, substr string) Finding {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("got %d findings %v, want exactly one mentioning %q", len(got), got, substr)
	}
	if !strings.Contains(got[0].Msg, substr) {
		t.Fatalf("finding = %q, want it to mention %q", got[0].Msg, substr)
	}
	return got[0]
}

const cmdImport = `import "github.com/spf13/cobra"` + "\n"

func command(name string, extra ...string) string {
	return "package cmd\n\n" + cmdImport +
		"\nfunc " + name + "() *cobra.Command {\n\treturn &cobra.Command{}\n}\n" +
		strings.Join(extra, "\n")
}

func TestCleanCommandDirectoryPasses(t *testing.T) {
	root := repo(t, map[string]string{
		"root.go": command("newRootCmd"),
		"list.go": command("newListCmd"),
	})
	if got := check(t, root); len(got) != 0 {
		t.Errorf("findings = %v, want none", got)
	}
}

func TestFileWithNoCommandIsReported(t *testing.T) {
	root := repo(t, map[string]string{
		"list.go":   command("newListCmd"),
		"helper.go": "package cmd\n\nfunc helper() int { return 1 }\n",
	})
	f := wantOne(t, check(t, root), "declares no function returning *cobra.Command")
	if filepath.Base(f.File) != "helper.go" {
		t.Errorf("file = %s, want helper.go", f.File)
	}
}

func TestFileWithTwoCommandsIsReported(t *testing.T) {
	root := repo(t, map[string]string{
		"sync.go": "package cmd\n\n" + cmdImport + `
func newSyncCmd() *cobra.Command { return &cobra.Command{} }
func newSyncAllCmd() *cobra.Command { return &cobra.Command{} }
`,
	})
	f := wantOne(t, check(t, root), "declares 2 functions returning *cobra.Command")
	if !strings.Contains(f.Msg, "newSyncCmd, newSyncAllCmd") {
		t.Errorf("msg = %q, want both constructors named", f.Msg)
	}
}

func TestCrossFileSymbolIsReported(t *testing.T) {
	root := repo(t, map[string]string{
		"list.go":   command("newListCmd", "func useIt() int { return shared() }"),
		"status.go": command("newStatusCmd", "func shared() int { return 1 }"),
	})
	f := wantOne(t, check(t, root), `uses "shared", declared in status.go`)
	if filepath.Base(f.File) != "list.go" {
		t.Errorf("file = %s, want list.go", f.File)
	}
}

func TestCommandConstructorsMayBeRegisteredByAParent(t *testing.T) {
	root := repo(t, map[string]string{
		"root.go": "package cmd\n\n" + cmdImport + `
func newRootCmd() *cobra.Command {
	c := &cobra.Command{}
	c.AddCommand(newListCmd())
	return c
}
`,
		"list.go": command("newListCmd"),
	})
	if got := check(t, root); len(got) != 0 {
		t.Errorf("findings = %v, want the parent/child registration allowed", got)
	}
}

func TestSharedTypesVarsAndConstsAreReported(t *testing.T) {
	root := repo(t, map[string]string{
		"a.go": command("newACmd", `
type shape struct{ n int }

var pick = 1

const limit = 2
`),
		"b.go": command("newBCmd", "func use() int { return pick + limit + shape{}.n }"),
	})
	got := check(t, root)
	if len(got) != 3 {
		t.Fatalf("got %d findings %v, want one each for pick, limit and shape", len(got), got)
	}
	for _, name := range []string{"pick", "limit", "shape"} {
		found := false
		for _, f := range got {
			found = found || strings.Contains(f.Msg, `uses "`+name+`"`)
		}
		if !found {
			t.Errorf("no finding for %q in %v", name, got)
		}
	}
}

// A local binding that happens to share a name with another file's symbol is
// deliberately not reported: the checker folds every scope into one set, so it
// under-reports rather than reporting a use that is not one.
func TestLocalsAreNotMistakenForSharedSymbols(t *testing.T) {
	root := repo(t, map[string]string{
		"a.go": command("newACmd", "func helper() int { return 1 }"),
		"b.go": command("newBCmd", `
func other() int {
	helper := 2
	for helper := range 3 {
		_ = helper
	}
	return helper
}
`),
	})
	if got := check(t, root); len(got) != 0 {
		t.Errorf("findings = %v, want locals ignored", got)
	}
}

func TestStructFieldsAndLabelsAreNotTreatedAsReferences(t *testing.T) {
	root := repo(t, map[string]string{
		"a.go": command("newACmd", "func shared() int { return 1 }"),
		"b.go": command("newBCmd", `
type box struct{ shared int }

func other() int {
	b := box{shared: 4}
	shared:
	for range 2 {
		break shared
	}
	return b.shared
}
`),
	})
	if got := check(t, root); len(got) != 0 {
		t.Errorf("findings = %v, want field names, keys and labels ignored", got)
	}
}

func TestEachSharedSymbolIsReportedOncePerFile(t *testing.T) {
	root := repo(t, map[string]string{
		"a.go": command("newACmd", "func shared() int { return 1 }"),
		"b.go": command("newBCmd", "func one() int { return shared() }\nfunc two() int { return shared() }"),
	})
	wantOne(t, check(t, root), `uses "shared"`)
}

func TestTestFilesAreIgnored(t *testing.T) {
	root := repo(t, map[string]string{
		"list.go":      command("newListCmd"),
		"list_test.go": "package cmd\n\nfunc helper() int { return 1 }\n",
	})
	if got := check(t, root); len(got) != 0 {
		t.Errorf("findings = %v, want _test.go files skipped", got)
	}
}

func TestAliasedCobraImportIsRecognised(t *testing.T) {
	root := repo(t, map[string]string{
		"list.go": "package cmd\n\nimport c \"github.com/spf13/cobra\"\n\nfunc newListCmd() *c.Command { return nil }\n",
	})
	if got := check(t, root); len(got) != 0 {
		t.Errorf("findings = %v, want the aliased import recognised", got)
	}
}

func TestOutsideImportOfCmdIsReported(t *testing.T) {
	root := repo(t, map[string]string{"list.go": command("newListCmd")})
	pkg := filepath.Join(root, "internal", "thing")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "package thing\n\nimport _ \"" + cmdPkgPath + "\"\n"
	if err := os.WriteFile(filepath.Join(pkg, "thing.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f := wantOne(t, check(t, root), "the command package is a leaf")
	if f.File != filepath.Join("internal", "thing", "thing.go") {
		t.Errorf("file = %s, want the internal package path", f.File)
	}
}

func TestRootMainMayImportCmd(t *testing.T) {
	root := repo(t, map[string]string{"list.go": command("newListCmd")})
	body := "package main\n\nimport _ \"" + cmdPkgPath + "\"\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := check(t, root); len(got) != 0 {
		t.Errorf("findings = %v, want the root main package allowed", got)
	}
}

func TestSkippedDirectoriesAreNotScanned(t *testing.T) {
	root := repo(t, map[string]string{"list.go": command("newListCmd")})
	for _, dir := range []string{"bin", ".claude/worktrees/x", "macos", "node_modules", ".git"} {
		full := filepath.Join(root, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "package junk\n\nimport _ \"" + cmdPkgPath + "\"\n"
		if err := os.WriteFile(filepath.Join(full, "junk.go"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := check(t, root); len(got) != 0 {
		t.Errorf("findings = %v, want vendored and generated trees skipped", got)
	}
}

func TestFindingsAreOrderedByFileThenLine(t *testing.T) {
	root := repo(t, map[string]string{
		"a.go": command("newACmd", "func shared() int { return 1 }\nfunc alsoShared() int { return 2 }"),
		"b.go": command("newBCmd", "func one() int { return shared() }\nfunc two() int { return alsoShared() }"),
		"c.go": "package cmd\n",
	})
	got := check(t, root)
	if len(got) < 3 {
		t.Fatalf("got %v, want at least three findings", got)
	}
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if prev.File > cur.File || (prev.File == cur.File && prev.Line > cur.Line) {
			t.Errorf("findings out of order at %d: %v then %v", i, prev, cur)
		}
	}
}

func TestFindingStringIsACompilerStyleLocation(t *testing.T) {
	f := Finding{File: "cmd/a.go", Line: 12, Msg: "boom"}
	if got, want := f.String(), "cmd/a.go:12: boom"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestMissingCommandDirectoryIsAnError(t *testing.T) {
	if _, err := Check(t.TempDir()); err == nil {
		t.Error("expected an error when cmd/ is absent")
	}
}

func TestUnparseableSourceIsAnError(t *testing.T) {
	root := repo(t, map[string]string{"broken.go": "package cmd\n\nfunc ("})
	if _, err := Check(root); err == nil {
		t.Error("expected a parse error")
	}
}

func TestUnparseableImportsOutsideCmdAreAnError(t *testing.T) {
	root := repo(t, map[string]string{"list.go": command("newListCmd")})
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package !"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(root); err == nil {
		t.Error("expected a parse error from the import scan")
	}
}

func TestFileWithoutTheCobraImportDeclaresNoCommand(t *testing.T) {
	root := repo(t, map[string]string{"plain.go": "package cmd\n\nfunc helper() int { return 1 }\n"})
	wantOne(t, check(t, root), "declares no function returning *cobra.Command")
}

// The real repository is the checker's own acceptance case: it must run to
// completion against it without erroring, whatever it finds.
func TestCheckRunsAgainstThisRepository(t *testing.T) {
	if _, err := Check("../.."); err != nil {
		t.Fatalf("Check on the repository: %v", err)
	}
}

// Every binding form the collector understands, in one file: generic type
// parameters, named results, a method receiver, a type switch binding, a
// closure's parameters, and function-scoped var/const/type declarations. None
// of them may be mistaken for a reference into a.go.
func TestEveryLocalBindingFormIsRecognised(t *testing.T) {
	root := repo(t, map[string]string{
		"a.go": command("newACmd", `
func item() int { return 1 }
func recv() int { return 2 }
type held struct{}
func closed() int { return 3 }
func named() int { return 4 }
func kind() int { return 5 }
`),
		"b.go": command("newBCmd", `
type carrier struct{ n int }

func (recv carrier) size() int { return recv.n }

func generic[item any](v item) item { return v }

func named() (named int) { return 3 }

func other(closed func(held int) int) int {
	var kind any = 1
	switch kind := kind.(type) {
	case int:
		_ = kind
	}
	type held int
	const limit = 2
	var item = 3
	return closed(int(held(item))) + limit
}
`),
	})
	if got := check(t, root); len(got) != 0 {
		t.Errorf("findings = %v, want every local binding form recognised", got)
	}
}

// Interface method names and an embedded interface are field names, not
// references to a.go's symbols.
func TestInterfaceMethodNamesAreNotReferences(t *testing.T) {
	root := repo(t, map[string]string{
		"a.go": command("newACmd", "func size() int { return 1 }"),
		"b.go": command("newBCmd", `
type sizer interface {
	size() int
}
`),
	})
	if got := check(t, root); len(got) != 0 {
		t.Errorf("findings = %v, want interface method names ignored", got)
	}
}

// An unreadable directory inside the tree surfaces as an error rather than a
// silent pass.
func TestUnreadableDirectoryIsAnError(t *testing.T) {
	root := repo(t, map[string]string{"list.go": command("newListCmd")})
	blocked := filepath.Join(root, "internal")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Skipf("cannot drop directory permissions here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if _, err := Check(root); err == nil {
		t.Error("expected the walk to surface the unreadable directory")
	}
}
