package skills

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Skill
		wantErr bool
	}{
		{
			name:  "name and description",
			input: "---\nname: deploy\ndescription: Deploy the service to staging\n---\n# Deploy\n",
			want:  Skill{Name: "deploy", Description: "Deploy the service to staging"},
		},
		{
			name:  "missing name falls back to directory",
			input: "---\ndescription: Something\n---\nbody\n",
			want:  Skill{Name: "dir", Description: "Something"},
		},
		{
			name:  "blank name falls back to directory",
			input: "---\nname: \"  \"\n---\n",
			want:  Skill{Name: "dir"},
		},
		{
			name:  "no frontmatter",
			input: "# Just markdown\n",
			want:  Skill{Name: "dir"},
		},
		{
			name:  "unterminated frontmatter is ignored",
			input: "---\nname: x\n",
			want:  Skill{Name: "dir"},
		},
		{
			name:  "empty file",
			input: "",
			want:  Skill{Name: "dir"},
		},
		{
			name:  "CRLF line endings and BOM",
			input: "\xef\xbb\xbf---\r\nname: win\r\ndescription: Windows file\r\n---\r\n",
			want:  Skill{Name: "win", Description: "Windows file"},
		},
		{
			name:  "frontmatter terminated at EOF without newline",
			input: "---\nname: eof\n---",
			want:  Skill{Name: "eof"},
		},
		{
			name:  "multiline description",
			input: "---\nname: multi\ndescription: >\n  Line one\n  line two\n---\n",
			want:  Skill{Name: "multi", Description: "Line one line two"},
		},
		{
			name:  "extra fields are ignored",
			input: "---\nname: extra\ndescription: d\nallowed-tools: [Bash]\n---\n",
			want:  Skill{Name: "extra", Description: "d"},
		},
		{
			name:    "invalid yaml",
			input:   "---\nname: [unclosed\n---\n",
			want:    Skill{Name: "dir"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.input), "dir")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Parse() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".claude/skills/deploy/SKILL.md", "---\nname: deploy\ndescription: Deploy the service to staging\n---\n")
	writeFile(t, root, "skills/code-review/SKILL.md", "---\ndescription: Review the current diff for correctness bugs\n---\n")
	writeFile(t, root, "plugins/p/skills/broken/SKILL.md", "---\nname: [oops\n---\n")
	writeFile(t, root, "plugins/p/skills/broken/README.md", "not a skill")
	writeFile(t, root, "docs/skill.md", "wrong case, not a skill")
	writeFile(t, root, ".git/SKILL.md", "---\nname: in-git\n---\n")
	writeFile(t, root, "node_modules/x/SKILL.md", "---\nname: in-node-modules\n---\n")

	got, warnings, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	want := []Skill{
		{Name: "broken", Path: "plugins/p/skills/broken/SKILL.md"},
		{Name: "code-review", Description: "Review the current diff for correctness bugs", Path: "skills/code-review/SKILL.md"},
		{Name: "deploy", Description: "Deploy the service to staging", Path: ".claude/skills/deploy/SKILL.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discover() =\n%+v\nwant\n%+v", got, want)
	}
	if len(warnings) != 1 || warnings[0].Path != "plugins/p/skills/broken/SKILL.md" {
		t.Errorf("warnings = %v, want one for broken skill", warnings)
	}
}

func TestDiscoverSortsDuplicateNamesByPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "b/same/SKILL.md", "---\nname: same\n---\n")
	writeFile(t, root, "a/same/SKILL.md", "---\nname: same\n---\n")

	got, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "a/same/SKILL.md" || got[1].Path != "b/same/SKILL.md" {
		t.Errorf("Discover() = %+v, want sorted by path for equal names", got)
	}
}

func TestDiscoverRootSkill(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "my-skill")
	writeFile(t, root, "SKILL.md", "no frontmatter")

	got, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Skill{{Name: "my-skill", Path: "SKILL.md"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discover() = %+v, want %+v", got, want)
	}
}

func TestDiscoverEmpty(t *testing.T) {
	got, warnings, err := Discover(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || len(warnings) != 0 {
		t.Errorf("Discover() = %v, %v; want empty", got, warnings)
	}
}

func TestDiscoverMissingRoot(t *testing.T) {
	if _, _, err := Discover(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("Discover() on missing root: want error")
	}
}
