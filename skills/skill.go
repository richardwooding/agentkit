package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

const (
	skillFile           = "SKILL.md"
	maxNameLen          = 64
	maxDescriptionLen   = 1024
	maxCompatibilityLen = 500
)

// Errors reported by Parse, Validate and the loaders.
var (
	ErrNoFrontmatter        = errors.New("skills: missing YAML frontmatter")
	ErrMissingField         = errors.New("skills: missing required field")
	ErrInvalidName          = errors.New("skills: invalid name")
	ErrNameMismatch         = errors.New("skills: name does not match directory")
	ErrInvalidDescription   = errors.New("skills: invalid description")
	ErrInvalidCompatibility = errors.New("skills: invalid compatibility")
	ErrNoFiles              = errors.New("skills: skill has no files")
	ErrUnknownSkill         = errors.New("skills: unknown skill")
)

// Skill is one parsed SKILL.md and, when loaded from a filesystem, its directory.
type Skill struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	Metadata      map[string]string
	AllowedTools  []string
	// Body is the Markdown after the frontmatter, trimmed.
	Body string
	// Dir is the skill directory within the filesystem it was loaded from; "."
	// when the filesystem root is the skill directory, "" for a Parse result.
	Dir string

	fsys fs.FS
}

// Open opens a file bundled with the skill by its path relative to the skill
// directory. Paths that escape the directory are rejected.
func (s *Skill) Open(name string) (fs.File, error) {
	if s.fsys == nil {
		return nil, ErrNoFiles
	}
	if !fs.ValidPath(name) || name == "." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	return s.fsys.Open(name)
}

// ReadFile reads a bundled file by its path relative to the skill directory.
func (s *Skill) ReadFile(name string) ([]byte, error) {
	f, err := s.Open(name)
	if err != nil {
		return nil, err
	}
	data, err := readAll(f)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	return data, err
}

// Resources lists the files bundled with the skill, relative to its directory
// and sorted, excluding SKILL.md. At most limit entries are returned; truncated
// reports whether more exist. A limit of 0 means no limit.
func (s *Skill) Resources(limit int) (files []string, truncated bool, err error) {
	if s.fsys == nil {
		return nil, false, nil
	}
	err = fs.WalkDir(s.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || p == skillFile {
			return nil
		}
		if limit > 0 && len(files) >= limit {
			truncated = true
			return fs.SkipAll
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("skills: list resources: %w", err)
	}
	return files, truncated, nil
}

// Validate checks s against the specification and returns every violation. The
// loaders record these as Problems but still load the skill unless WithStrict
// is set, mirroring how other clients treat skills written for each other.
func Validate(s *Skill) []error {
	var errs []error
	if err := validateName(s.Name); err != nil {
		errs = append(errs, err)
	}
	if s.Dir != "" && s.Dir != "." && s.Name != path.Base(s.Dir) {
		errs = append(errs, fmt.Errorf("%w: %q in %q", ErrNameMismatch, s.Name, s.Dir))
	}
	if n := len(s.Description); n == 0 || n > maxDescriptionLen {
		errs = append(errs, fmt.Errorf("%w: %d characters, want 1-%d", ErrInvalidDescription, n, maxDescriptionLen))
	}
	if n := len(s.Compatibility); n > maxCompatibilityLen {
		errs = append(errs, fmt.Errorf("%w: %d characters, want at most %d", ErrInvalidCompatibility, n, maxCompatibilityLen))
	}
	return errs
}

func validateName(name string) error {
	switch {
	case name == "" || len(name) > maxNameLen:
		return fmt.Errorf("%w: %q must be 1-%d characters", ErrInvalidName, name, maxNameLen)
	case strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-"):
		return fmt.Errorf("%w: %q must not start or end with a hyphen", ErrInvalidName, name)
	case strings.Contains(name, "--"):
		return fmt.Errorf("%w: %q must not contain consecutive hyphens", ErrInvalidName, name)
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Errorf("%w: %q may only contain a-z, 0-9 and hyphens", ErrInvalidName, name)
		}
	}
	return nil
}

// Problem is a skill the loader skipped or loaded with reservations.
type Problem struct {
	// Path is the SKILL.md within the scanned filesystem.
	Path string
	Err  error
}

// Error implements error.
func (p Problem) Error() string { return p.Path + ": " + p.Err.Error() }

// Unwrap returns the underlying error.
func (p Problem) Unwrap() error { return p.Err }

// Set is an ordered collection of skills with unique names.
type Set struct {
	skills []*Skill
	index  map[string]int
	// Problems records skills that were skipped or loaded despite violations.
	Problems []Problem
}

// NewSet builds a set from skills; later duplicates replace earlier ones.
func NewSet(skills ...*Skill) *Set {
	s := &Set{index: map[string]int{}}
	s.Add(skills...)
	return s
}

// Add inserts skills. A skill whose name is already present replaces the
// earlier one in place, so a project set merged after a user set wins.
func (s *Set) Add(skills ...*Skill) {
	if s.index == nil {
		s.index = map[string]int{}
	}
	for _, sk := range skills {
		if i, ok := s.index[sk.Name]; ok {
			s.skills[i] = sk
			continue
		}
		s.index[sk.Name] = len(s.skills)
		s.skills = append(s.skills, sk)
	}
}

// Lookup returns the skill with the given name.
func (s *Set) Lookup(name string) (*Skill, bool) {
	if s == nil {
		return nil, false
	}
	i, ok := s.index[name]
	if !ok {
		return nil, false
	}
	return s.skills[i], true
}

// All returns the skills in insertion order.
func (s *Set) All() []*Skill {
	if s == nil {
		return nil
	}
	return append([]*Skill(nil), s.skills...)
}

// Len returns the number of skills.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.skills)
}

// Names returns the skill names in insertion order.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	names := make([]string, len(s.skills))
	for i, sk := range s.skills {
		names[i] = sk.Name
	}
	return names
}

// Merge combines sets; on a name collision the skill from the later set wins,
// so pass lower-precedence sets first (user, then project). Problems are kept.
func Merge(sets ...*Set) *Set {
	out := NewSet()
	for _, s := range sets {
		if s == nil {
			continue
		}
		out.Add(s.skills...)
		out.Problems = append(out.Problems, s.Problems...)
	}
	return out
}
