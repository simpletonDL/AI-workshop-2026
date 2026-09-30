// Package skills discovers and parses Claude Code skills (SKILL.md files).
package skills

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the name of the file that defines a skill.
const FileName = "SKILL.md"

// Skill describes a single skill found in a repository.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Path is the SKILL.md path relative to the repository root, slash-separated.
	Path string `json:"path"`
}

// Warning reports a SKILL.md that was found but could not be fully parsed.
// The skill is still included in the results using fallback values.
type Warning struct {
	Path string
	Err  error
}

func (w Warning) String() string {
	return fmt.Sprintf("%s: %v", w.Path, w.Err)
}

// skippedDirs are never descended into during discovery.
var skippedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
}

// Discover walks root and returns all skills sorted by name (then path).
func Discover(root string) ([]Skill, []Warning, error) {
	var (
		found    []Skill
		warnings []Warning
	)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != FileName || !d.Type().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		skill, perr := Parse(data, fallbackName(root, rel))
		skill.Path = rel
		if perr != nil {
			warnings = append(warnings, Warning{Path: rel, Err: perr})
		}
		found = append(found, skill)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].Name != found[j].Name {
			return found[i].Name < found[j].Name
		}
		return found[i].Path < found[j].Path
	})
	return found, warnings, nil
}

// fallbackName returns the name of the directory containing the SKILL.md,
// or the repository directory name when SKILL.md sits at the root.
func fallbackName(root, rel string) string {
	dir := path.Dir(rel)
	if dir == "." {
		abs, err := filepath.Abs(root)
		if err != nil {
			return filepath.Base(root)
		}
		return filepath.Base(abs)
	}
	return path.Base(dir)
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Parse extracts skill data from the contents of a SKILL.md file.
// Missing fields fall back to fallbackName and an empty description. If the
// frontmatter is malformed, a Skill with fallback values is returned together
// with the error.
func Parse(data []byte, fallbackName string) (Skill, error) {
	skill := Skill{Name: fallbackName}

	raw, ok := extractFrontmatter(data)
	if !ok {
		return skill, nil
	}

	var fm frontmatter
	if err := yaml.Unmarshal(raw, &fm); err != nil {
		return skill, fmt.Errorf("invalid frontmatter: %w", err)
	}
	if name := strings.TrimSpace(fm.Name); name != "" {
		skill.Name = name
	}
	skill.Description = strings.TrimSpace(fm.Description)
	return skill, nil
}

// extractFrontmatter returns the YAML between the leading "---" line and the
// next "---" line. ok is false if the file has no frontmatter.
func extractFrontmatter(data []byte) (raw []byte, ok bool) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // UTF-8 BOM
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))

	first, rest, found := bytes.Cut(data, []byte("\n"))
	if !found || string(bytes.TrimRight(first, " \t")) != "---" {
		return nil, false
	}

	for offset := 0; offset <= len(rest); {
		line := rest[offset:]
		end := bytes.IndexByte(line, '\n')
		if end >= 0 {
			line = line[:end]
		}
		if string(bytes.TrimRight(line, " \t")) == "---" {
			return rest[:offset], true
		}
		if end < 0 {
			break
		}
		offset += end + 1
	}
	return nil, false
}
