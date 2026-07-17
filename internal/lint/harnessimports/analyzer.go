package harnessimports

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const contractMessage = "CONTRACT-004 invariant #1"

var forbiddenImports = []string{
	"github.com/easel/fizeau/internal/harnesses/claude",
	"github.com/easel/fizeau/internal/harnesses/claude-tui",
	"github.com/easel/fizeau/internal/harnesses/codex",
	"github.com/easel/fizeau/internal/harnesses/gemini",
	"github.com/easel/fizeau/internal/harnesses/opencode",
	"github.com/easel/fizeau/internal/harnesses/pi",
}

type Finding struct {
	Path       string
	Line       int
	Column     int
	ImportPath string
	Message    string
}

type Options struct {
	Root string
}

func Scan(opts Options) ([]Finding, error) {
	root := opts.Root
	if root == "" {
		root = "."
	}

	var findings []Finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", ".ddx", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		fileFindings, err := scanFile(path, rel)
		if err != nil {
			return err
		}
		findings = append(findings, fileFindings...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("harnessimports: scan %s: %w", root, err)
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Column < findings[j].Column
	})
	return findings, nil
}

func scanFile(path, rel string) ([]Finding, error) {
	if allowedImportSite(rel) {
		return nil, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}

	var findings []Finding
	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if !slices.Contains(forbiddenImports, importPath) {
			continue
		}
		position := fset.Position(imp.Pos())
		findings = append(findings, Finding{
			Path:       rel,
			Line:       position.Line,
			Column:     position.Column,
			ImportPath: importPath,
			Message:    formatMessage(rel, importPath),
		})
	}
	return findings, nil
}

func formatMessage(path, importPath string) string {
	return fmt.Sprintf("%s: %s imports %s; only packages under internal/harnesses/ may import concrete per-harness packages", contractMessage, path, importPath)
}

func allowedImportSite(rel string) bool {
	rel = filepath.ToSlash(rel)
	return strings.HasPrefix(rel, "internal/harnesses/") || strings.HasSuffix(rel, "_test.go")
}
