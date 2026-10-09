package content

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	literalRe = regexp.MustCompile(`"([a-z][a-z0-9_.:\-]*)"`)
	fieldRe   = regexp.MustCompile(`\.([A-Z][A-Za-z0-9]*)`)
)

// ScanSource reads the Go source under the given roots (tests and the content package itself left out) and returns every
// short string literal and every selected field name in it: what the audit's reader check looks the content up in.
func ScanSource(roots ...string) (literals, fields map[string]bool, err error) {
	literals, fields = map[string]bool{}, map[string]bool{}
	for _, root := range roots {
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				if strings.HasSuffix(filepath.ToSlash(path), "internal/content") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".gen.go") {
				return nil
			}
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			for _, m := range literalRe.FindAllStringSubmatch(string(raw), -1) {
				literals[m[1]] = true
			}
			for _, m := range fieldRe.FindAllStringSubmatch(string(raw), -1) {
				fields[m[1]] = true
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return literals, fields, nil
}
