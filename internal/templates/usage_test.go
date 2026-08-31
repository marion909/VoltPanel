package templates_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryRendererHasACaller findet Vorlagen, die erzeugt, aber nie
// ausgeliefert werden.
//
// Das ist hier zweimal passiert: die Site-Einstellungen standen in der
// Vorlage, ohne dass es einen Weg gab, sie zu setzen — und RenderShared
// erzeugte den Standardserver für die ACME-Prüfung, den niemand schrieb.
// Beides kompiliert einwandfrei und fällt erst im Betrieb auf, im zweiten
// Fall als 404 mitten in einer Zertifikatsanforderung.
func TestEveryRendererHasACaller(t *testing.T) {
	renderers := exportedRenderers(t)
	if len(renderers) == 0 {
		t.Fatal("keine Render-Funktionen gefunden — prüft der Test noch das Richtige?")
	}

	callers := sourceOutsideTemplates(t)
	for _, name := range renderers {
		needle := "templates." + name + "("
		if !strings.Contains(callers, needle) {
			t.Errorf("templates.%s wird nirgends aufgerufen — die Vorlage erreicht den Server nie", name)
		}
	}
}

func exportedRenderers(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				// Methoden zählen nicht: sie hängen an einem Typ, der selbst
				// einen Aufrufer braucht.
				if !ok || fn.Recv != nil || !fn.Name.IsExported() {
					continue
				}
				if strings.HasPrefix(fn.Name.Name, "Render") {
					names = append(names, fn.Name.Name)
				}
			}
		}
	}
	return names
}

// sourceOutsideTemplates sammelt den Quelltext des restlichen Baums. Ein
// Aufruf innerhalb des Pakets zählt nicht — er würde nur bedeuten, dass sich
// die Vorlagen gegenseitig aufrufen.
func sourceOutsideTemplates(t *testing.T) string {
	t.Helper()

	var b strings.Builder
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "bin", "web":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.Contains(path, filepath.Join("internal", "templates")) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b.Write(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
