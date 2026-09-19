package skills_test

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/richardwooding/agentkit/skills"
)

func skillMD(name, desc string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("---\nname: " + name + "\ndescription: " + desc + "\n---\n# " + name + "\n\nDo the thing.\n")}
}

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"pdf-processing/SKILL.md":                skillMD("pdf-processing", "Extract PDF text. Use when handling PDFs."),
		"pdf-processing/scripts/extract.py":      {Data: []byte("print('hi')")},
		"pdf-processing/references/REFERENCE.md": {Data: []byte("# Reference")},
		"pdf-processing/assets/logo.png":         {Data: []byte{0x89, 'P', 'N', 'G'}},
		"misnamed/SKILL.md":                      skillMD("other-name", "Loaded with a problem."),
		"nodesc/SKILL.md":                        {Data: []byte("---\nname: nodesc\n---\n")},
		"group/sub/deep-skill/SKILL.md":          skillMD("deep-skill", "Depth three."),
		"group/sub/deeper/too-deep/SKILL.md":     skillMD("too-deep", "Depth four."),
		"node_modules/pkg/SKILL.md":              skillMD("pkg", "Ignored."),
		".hidden/SKILL.md":                       skillMD("hidden", "Ignored."),
		"pdf-processing/nested-ignored/SKILL.md": skillMD("nested-ignored", "Inside a skill dir."),
		"README.md":                              {Data: []byte("not a skill")},
	}
}

func TestLoadAll(t *testing.T) {
	set, err := skills.LoadAll(testFS())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"deep-skill", "other-name", "pdf-processing"}
	if got := set.Names(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("names = %v, want %v", got, want)
	}
	if len(set.Problems) != 2 {
		t.Fatalf("problems = %v", set.Problems)
	}
	var mismatch, missing bool
	for _, p := range set.Problems {
		mismatch = mismatch || errors.Is(p, skills.ErrNameMismatch)
		missing = missing || errors.Is(p, skills.ErrMissingField)
	}
	if !mismatch || !missing {
		t.Fatalf("problems = %v", set.Problems)
	}
	pdf, ok := set.Lookup("pdf-processing")
	if !ok || pdf.Dir != "pdf-processing" || pdf.Body != "# pdf-processing\n\nDo the thing." {
		t.Fatalf("pdf = %+v", pdf)
	}

	strict, err := skills.LoadAll(testFS(), skills.WithStrict())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := strict.Lookup("other-name"); ok || strict.Len() != 2 {
		t.Fatalf("strict names = %v", strict.Names())
	}
	shallow, _ := skills.LoadAll(testFS(), skills.WithMaxDepth(1))
	if shallow.Len() != 2 {
		t.Fatalf("shallow names = %v", shallow.Names())
	}
}

func TestLoadRootIsSkill(t *testing.T) {
	root := fstest.MapFS{"SKILL.md": skillMD("solo", "A single skill at the root."), "references/a.md": {Data: []byte("a")}}
	set, err := skills.LoadAll(root)
	if err != nil || set.Len() != 1 {
		t.Fatalf("set = %v err = %v", set.Names(), err)
	}
	s, _ := set.Lookup("solo")
	if s.Dir != "." || len(skills.Validate(s)) != 0 {
		t.Fatalf("root skill = %+v", s)
	}
	if _, err := skills.Load(root, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load missing = %v", err)
	}
}

func TestResourcesAndFiles(t *testing.T) {
	pdf, err := skills.Load(testFS(), "pdf-processing")
	if err != nil {
		t.Fatal(err)
	}
	files, truncated, err := pdf.Resources(0)
	if err != nil || truncated {
		t.Fatalf("resources err = %v truncated = %v", err, truncated)
	}
	if len(files) != 4 || files[0] != "assets/logo.png" || files[3] != "scripts/extract.py" {
		t.Fatalf("files = %v", files)
	}
	capped, truncated, _ := pdf.Resources(2)
	if len(capped) != 2 || !truncated {
		t.Fatalf("capped = %v truncated = %v", capped, truncated)
	}
	if b, err := pdf.ReadFile("references/REFERENCE.md"); err != nil || string(b) != "# Reference" {
		t.Fatalf("ReadFile = %q, %v", b, err)
	}
	for _, bad := range []string{"../misnamed/SKILL.md", "/etc/passwd", ".", ""} {
		if _, err := pdf.ReadFile(bad); err == nil {
			t.Fatalf("ReadFile(%q) should fail", bad)
		}
	}
	parsed, _ := skills.Parse([]byte("---\nname: x\ndescription: d\n---\n"))
	if _, err := parsed.ReadFile("a"); !errors.Is(err, skills.ErrNoFiles) {
		t.Fatalf("parsed ReadFile = %v", err)
	}
}

func TestMergePrecedence(t *testing.T) {
	user := skills.NewSet(&skills.Skill{Name: "review", Description: "user"}, &skills.Skill{Name: "only-user", Description: "u"})
	project := skills.NewSet(&skills.Skill{Name: "review", Description: "project"})
	project.Problems = append(project.Problems, skills.Problem{Path: "p", Err: skills.ErrInvalidName})
	merged := skills.Merge(user, project, nil)
	if merged.Len() != 2 || len(merged.Problems) != 1 {
		t.Fatalf("merged = %v problems = %v", merged.Names(), merged.Problems)
	}
	if r, _ := merged.Lookup("review"); r.Description != "project" {
		t.Fatalf("review = %+v", r)
	}
	if names := merged.Names(); names[0] != "review" || names[1] != "only-user" {
		t.Fatalf("order = %v", names)
	}
	var empty *skills.Set
	if empty.Len() != 0 || empty.Prompt() != "" || len(skills.Tools(empty)) != 0 {
		t.Fatal("nil set must be inert")
	}
}
