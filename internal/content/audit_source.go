package content

import (
	"gopkg.in/yaml.v3"
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

// LoadNotes reads every `note:` of the content files with the row it sits on (the nearest enclosing mapping that has a
// `code`), for the audit's check of notes that admit a gap.
func LoadNotes(dir string) ([]NoteRow, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil {
		return nil, err
	}
	var out []NoteRow
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			continue
		}
		kind := strings.TrimSuffix(filepath.Base(f), ".yml")
		var walk func(n *yaml.Node, section string)
		walk = func(n *yaml.Node, section string) {
			switch n.Kind {
			case yaml.DocumentNode, yaml.SequenceNode:
				for _, c := range n.Content {
					walk(c, section)
				}
			case yaml.MappingNode:
				code, note := "", ""
				for i := 0; i+1 < len(n.Content); i += 2 {
					k, v := n.Content[i].Value, n.Content[i+1]
					switch {
					case k == "code" && v.Kind == yaml.ScalarNode:
						code = v.Value
					case k == "note" && v.Kind == yaml.ScalarNode:
						note = v.Value
					case v.Kind == yaml.SequenceNode || v.Kind == yaml.MappingNode:
						walk(v, k)
					}
				}
				if code == "" && section != "" {
					code = section // a block with a note and no code of its own: named by its key
				}
				if code != "" && note != "" {
					out = append(out, NoteRow{Kind: kind + "/" + section, Code: code, Text: note, Always: section != "building_functions" && section != "settlement_buildings"})
				}
			}
		}
		walk(&doc, "")
	}
	return out, nil
}
