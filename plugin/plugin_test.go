package plugin

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedSkillMatchesDisk(t *testing.T) {
	count := 0
	err := filepath.WalkDir("skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		disk, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		embedded, err := Skill.ReadFile(filepath.ToSlash(path))
		if err != nil {
			t.Errorf("%s is not embedded: %v", path, err)
			return nil
		}
		if !bytes.Equal(disk, embedded) {
			t.Errorf("%s differs from its embedded copy", path)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("no skill files found")
	}
	embeddedCount := 0
	_ = fs.WalkDir(Skill, "skills", func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			embeddedCount++
		}
		return err
	})
	if embeddedCount != count {
		t.Fatalf("embedded %d files, disk has %d", embeddedCount, count)
	}
}

func TestSkillFrontMatter(t *testing.T) {
	data, err := Skill.ReadFile("skills/gravity/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	head, rest, ok := strings.Cut(strings.TrimPrefix(string(data), "---\n"), "\n---\n")
	if !strings.HasPrefix(string(data), "---\n") || !ok {
		t.Fatal("SKILL.md has no front matter")
	}
	if !strings.Contains("\n"+head+"\n", "\nname: gravity\n") {
		t.Fatalf("front matter lacks name: gravity:\n%s", head)
	}
	if !strings.Contains(head, "\ndescription: ") {
		t.Fatal("front matter lacks a description")
	}
	for _, link := range []string{"references/manifest.md", "references/structure.md", "references/passes.md", "references/i18n.md", "references/troubleshooting.md"} {
		if !strings.Contains(rest, link) {
			t.Errorf("SKILL.md does not link %s", link)
		}
		if _, err := Skill.ReadFile("skills/gravity/" + link); err != nil {
			t.Errorf("%s missing: %v", link, err)
		}
	}
}
