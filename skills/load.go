package skills

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
)

const (
	defaultMaxDepth = 3
	maxDirs         = 2000
)

// LoadOption configures LoadAll.
type LoadOption func(*loader)

type loader struct {
	maxDepth int
	strict   bool
}

// WithMaxDepth bounds how deep below the root LoadAll looks for skill
// directories. The default of 3 covers "<root>/<skill>/SKILL.md" and two more
// levels of grouping.
func WithMaxDepth(n int) LoadOption { return func(l *loader) { l.maxDepth = n } }

// WithStrict makes LoadAll skip skills that fail Validate instead of loading
// them and recording the violations as Problems.
func WithStrict() LoadOption { return func(l *loader) { l.strict = true } }

// Load reads the skill whose directory is dir within fsys ("." for the root).
// It parses SKILL.md but does not apply Validate; call that separately when
// strictness matters.
func Load(fsys fs.FS, dir string) (*Skill, error) {
	file := path.Join(dir, skillFile)
	data, err := fs.ReadFile(fsys, file)
	if err != nil {
		return nil, fmt.Errorf("skills: read %s: %w", file, err)
	}
	s, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("skills: parse %s: %w", file, err)
	}
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("skills: open %s: %w", dir, err)
	}
	s.Dir = dir
	s.fsys = sub
	return s, nil
}

// LoadAll scans fsys for directories containing SKILL.md and loads each one.
// It skips dot-directories and node_modules, does not descend into a skill
// directory, and stops after 2000 directories. Skills whose SKILL.md cannot be
// parsed are skipped; skills that violate the specification are loaded and
// noted in Set.Problems unless WithStrict is set. The error is non-nil only
// for I/O failures.
func LoadAll(fsys fs.FS, opts ...LoadOption) (*Set, error) {
	l := &loader{maxDepth: defaultMaxDepth}
	for _, o := range opts {
		o(l)
	}
	set := NewSet()
	dirs := 0
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if skipDir(p) || depth(p) > l.maxDepth {
			return fs.SkipDir
		}
		dirs++
		if dirs > maxDirs {
			return fs.SkipAll
		}
		if _, err := fs.Stat(fsys, path.Join(p, skillFile)); err != nil {
			return nil
		}
		l.add(set, fsys, p)
		return fs.SkipDir
	})
	if err != nil {
		return nil, fmt.Errorf("skills: scan: %w", err)
	}
	return set, nil
}

func (l *loader) add(set *Set, fsys fs.FS, dir string) {
	file := path.Join(dir, skillFile)
	s, err := Load(fsys, dir)
	if err != nil {
		set.Problems = append(set.Problems, Problem{Path: file, Err: err})
		return
	}
	if errs := Validate(s); len(errs) > 0 {
		set.Problems = append(set.Problems, Problem{Path: file, Err: errors.Join(errs...)})
		if l.strict {
			return
		}
	}
	set.Add(s)
}

func skipDir(p string) bool {
	base := path.Base(p)
	return p != "." && (strings.HasPrefix(base, ".") || base == "node_modules")
}

func depth(p string) int {
	if p == "." {
		return 0
	}
	return strings.Count(p, "/") + 1
}

func readAll(f fs.File) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxFileBytes {
		return nil, ErrFileTooLarge
	}
	return b, nil
}
