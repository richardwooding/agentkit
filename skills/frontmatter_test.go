package skills_test

import (
	"errors"
	"testing"

	"github.com/richardwooding/agentkit/skills"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want skills.Skill
		err  error
	}{
		{
			name: "minimal",
			in:   "---\nname: pdf\ndescription: Work with PDFs.\n---\n# PDF\n\nBody.\n",
			want: skills.Skill{Name: "pdf", Description: "Work with PDFs.", Body: "# PDF\n\nBody."},
		},
		{
			name: "quoted, comments, metadata and allowed-tools",
			in: "---\n# a comment\nname: \"data-analysis\"\ndescription: 'It''s for data' # trailing\nlicense: Apache-2.0\ncompatibility: Requires python\n" +
				"metadata:\n  author: example-org\n  version: \"1.0\"\n  nested:\n    too: deep\nallowed-tools: Bash(git:*) Read\nunknown: ignored\n---\nbody",
			want: skills.Skill{
				Name: "data-analysis", Description: "It's for data", License: "Apache-2.0", Compatibility: "Requires python",
				Metadata: map[string]string{"author": "example-org", "version": "1.0"}, AllowedTools: []string{"Bash(git:*)", "Read"}, Body: "body",
			},
		},
		{
			name: "unquoted colon is taken verbatim",
			in:   "---\nname: x\ndescription: Use this skill when: the user asks about PDFs\n---\n",
			want: skills.Skill{Name: "x", Description: "Use this skill when: the user asks about PDFs"},
		},
		{
			name: "literal block scalar",
			in:   "---\nname: x\ndescription: |\n  Line one.\n  Line two.\n\n---\nbody",
			want: skills.Skill{Name: "x", Description: "Line one.\nLine two.", Body: "body"},
		},
		{
			name: "folded block scalar with CRLF and BOM",
			in:   "\uFEFF---\r\nname: x\r\ndescription: >-\r\n  Folded\r\n  text.\r\n\r\n  New paragraph.\r\n---\r\nbody\r\n",
			want: skills.Skill{Name: "x", Description: "Folded text.\nNew paragraph.", Body: "body"},
		},
		{
			name: "empty metadata key then body",
			in:   "---\nname: x\ndescription: d\nmetadata:\n---\nbody",
			want: skills.Skill{Name: "x", Description: "d", Body: "body"},
		},
		{name: "no frontmatter", in: "# just markdown", err: skills.ErrNoFrontmatter},
		{name: "unterminated frontmatter", in: "---\nname: x\ndescription: d\n", err: skills.ErrNoFrontmatter},
		{name: "missing description", in: "---\nname: x\n---\n", err: skills.ErrMissingField},
		{name: "empty name", in: "---\nname: \"\"\ndescription: d\n---\n", err: skills.ErrMissingField},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := skills.Parse([]byte(tc.in))
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != tc.want.Name || got.Description != tc.want.Description || got.License != tc.want.License ||
				got.Compatibility != tc.want.Compatibility || got.Body != tc.want.Body {
				t.Fatalf("got %+v\nwant %+v", *got, tc.want)
			}
			if len(got.Metadata) != len(tc.want.Metadata) {
				t.Fatalf("metadata = %v, want %v", got.Metadata, tc.want.Metadata)
			}
			for k, v := range tc.want.Metadata {
				if got.Metadata[k] != v {
					t.Fatalf("metadata[%s] = %q, want %q", k, got.Metadata[k], v)
				}
			}
			if len(got.AllowedTools) != len(tc.want.AllowedTools) {
				t.Fatalf("allowed-tools = %v, want %v", got.AllowedTools, tc.want.AllowedTools)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	long := make([]byte, 1025)
	for i := range long {
		long[i] = 'a'
	}
	tests := []struct {
		name string
		s    skills.Skill
		want []error
	}{
		{name: "ok", s: skills.Skill{Name: "pdf-processing", Description: "d", Dir: "x/pdf-processing"}},
		{name: "root dir ok", s: skills.Skill{Name: "anything", Description: "d", Dir: "."}},
		{name: "uppercase", s: skills.Skill{Name: "PDF", Description: "d"}, want: []error{skills.ErrInvalidName}},
		{name: "hyphens", s: skills.Skill{Name: "-pdf--x-", Description: "d"}, want: []error{skills.ErrInvalidName}},
		{name: "mismatch", s: skills.Skill{Name: "pdf", Description: "d", Dir: "skills/other"}, want: []error{skills.ErrNameMismatch}},
		{name: "long description", s: skills.Skill{Name: "pdf", Description: string(long)}, want: []error{skills.ErrInvalidDescription}},
		{name: "long compatibility", s: skills.Skill{Name: "pdf", Description: "d", Compatibility: string(long)}, want: []error{skills.ErrInvalidCompatibility}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := skills.Validate(&tc.s)
			if len(errs) != len(tc.want) {
				t.Fatalf("errs = %v, want %d", errs, len(tc.want))
			}
			for i, w := range tc.want {
				if !errors.Is(errs[i], w) {
					t.Fatalf("errs[%d] = %v, want %v", i, errs[i], w)
				}
			}
		})
	}
}
