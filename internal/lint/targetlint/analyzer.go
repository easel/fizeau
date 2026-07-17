package targetlint

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Finding struct {
	Path   string
	Line   int
	Column int
	Text   string
}

type Options struct {
	Root string
}

func Scan(opts Options) ([]Finding, error) {
	root := opts.Root
	if root == "" {
		root = "."
	}

	paths, err := scanPaths(root)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, rel := range paths {
		fileFindings, err := scanFile(root, rel)
		if err != nil {
			return nil, err
		}
		findings = append(findings, fileFindings...)
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

func ScanContent(path string, content []byte) []Finding {
	var findings []Finding
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		col := strings.Index(line, "target")
		if col < 0 || allowedTargetLine(path, line) {
			continue
		}
		findings = append(findings, Finding{
			Path:   filepath.ToSlash(path),
			Line:   lineNo,
			Column: col + 1,
			Text:   strings.TrimSpace(line),
		})
	}
	return findings
}

func scanPaths(root string) ([]string, error) {
	var paths []string
	if _, err := os.Stat(filepath.Join(root, "service.go")); err == nil {
		paths = append(paths, "service.go")
	}
	serviceFiles, err := filepath.Glob(filepath.Join(root, "service_*.go"))
	if err != nil {
		return nil, err
	}
	for _, path := range serviceFiles {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, "_test.go") {
			paths = append(paths, rel)
		}
	}
	for _, dir := range []string{"internal/routing", "internal/modelcatalog"} {
		if err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(rel))
			return nil
		}); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func scanFile(root, rel string) ([]Finding, error) {
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) // #nosec G304 -- rel is selected from scoped repo paths.
	if err != nil {
		return nil, fmt.Errorf("targetlint: read %s: %w", rel, err)
	}
	return ScanContent(rel, content), nil
}

func allowedTargetLine(path, line string) bool {
	rel := filepath.ToSlash(path)
	if strings.Contains(line, "HealthTarget") {
		return true
	}
	// CONTRACT-003 uses target as the normative term for the portable
	// runtime's GOOS/GOARCH and guest mount destination. This file owns only
	// that public facade; routing destination vocabulary remains forbidden in
	// every other root service file.
	if rel == "service_portable_runtime.go" {
		return true
	}
	if strings.HasSuffix(rel, "internal/routing/errors.go") {
		trimmed := strings.TrimSpace(line)
		return strings.Contains(trimmed, "Is(target error)") ||
			strings.Contains(trimmed, "target.(type)") ||
			strings.Contains(trimmed, ", target)")
	}
	return false
}
