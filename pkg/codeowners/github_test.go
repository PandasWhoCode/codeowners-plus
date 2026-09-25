package codeowners

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
)

// githubFileReader serves a GitHub CODEOWNERS file at one exact path.
type githubFileReader struct {
	path    string
	content []byte
}

func (r *githubFileReader) ReadFile(path string) ([]byte, error) {
	if path != r.path {
		return nil, io.ErrUnexpectedEOF
	}
	return r.content, nil
}

func (r *githubFileReader) PathExists(path string) bool {
	return path == r.path
}

func diffFiles(names ...string) []DiffFile {
	files := make([]DiffFile, len(names))
	for i, name := range names {
		files[i] = DiffFile{FileName: name}
	}
	return files
}

// ownersFor returns the flattened owner strings required for a file.
func ownersFor(t *testing.T, co CodeOwners, file string) []string {
	t.Helper()
	groups, ok := co.FileRequired()[file]
	if !ok {
		return nil
	}
	names := OriginalStrings(groups.Flatten())
	slices.Sort(names)
	return names
}

func TestGithubPatternToGlobs(t *testing.T) {
	tt := []struct {
		name    string
		pattern string
		matches []string
		misses  []string
	}{
		{
			name:    "catch-all matches at any depth",
			pattern: "*",
			matches: []string{"a.go", "src/a.go", "a/b/c/d.go"},
		},
		{
			name:    "unanchored extension matches at any depth",
			pattern: "*.js",
			matches: []string{"a.js", "src/a.js", "a/b/c.js"},
			misses:  []string{"a.go", "src/a.ts"},
		},
		{
			name:    "unanchored name matches a file or directory at any depth",
			pattern: "docs",
			matches: []string{"docs", "docs/a.md", "a/docs", "a/docs/b.md"},
			misses:  []string{"documents", "a/docsx/b.md"},
		},
		{
			name:    "leading slash anchors to the root",
			pattern: "/docs",
			matches: []string{"docs", "docs/a.md"},
			misses:  []string{"a/docs", "a/docs/b.md"},
		},
		{
			name:    "embedded slash anchors to the root",
			pattern: "docs/team",
			matches: []string{"docs/team", "docs/team/a.md"},
			misses:  []string{"a/docs/team"},
		},
		{
			name:    "trailing slash matches only directory contents",
			pattern: "docs/",
			matches: []string{"docs/a.md", "docs/b/c.md"},
			misses:  []string{"docs"},
		},
		{
			name:    "single star does not cross a separator",
			pattern: "/docs/*",
			matches: []string{"docs/a.md"},
			misses:  []string{"docs/b/c.md"},
		},
		{
			name:    "globstar crosses separators",
			pattern: "/docs/**",
			matches: []string{"docs/a.md", "docs/b/c.md"},
		},
		{
			name:    "root pattern matches everything",
			pattern: "/",
			matches: []string{"a.go", "a/b.go"},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			rule := &githubRule{pattern: tc.pattern, globs: githubPatternToGlobs(tc.pattern)}
			for _, path := range tc.matches {
				if !rule.matches(path, io.Discard) {
					t.Errorf("pattern %q should match %q (globs %v)", tc.pattern, path, rule.globs)
				}
			}
			for _, path := range tc.misses {
				if rule.matches(path, io.Discard) {
					t.Errorf("pattern %q should not match %q (globs %v)", tc.pattern, path, rule.globs)
				}
			}
		})
	}
}

func TestGitHubCodeownersLastMatchWins(t *testing.T) {
	content := strings.Join([]string{
		"*            @everyone",
		"/docs/       @docs-team",
		"/docs/api/   @api-team",
	}, "\n")

	reader := &githubFileReader{path: "/repo/.github/CODEOWNERS", content: []byte(content)}
	co, err := New(
		"/repo",
		diffFiles("main.go", "docs/readme.md", "docs/api/spec.md"),
		reader,
		io.Discard,
		WithGitHubCodeownersFile(".github/CODEOWNERS"),
	)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	tt := []struct {
		file     string
		expected []string
	}{
		{"main.go", []string{"@everyone"}},
		{"docs/readme.md", []string{"@docs-team"}},
		// Later rule wins even though it is not "more specific" by the native
		// longest-pattern ordering.
		{"docs/api/spec.md", []string{"@api-team"}},
	}
	for _, tc := range tt {
		t.Run(tc.file, func(t *testing.T) {
			got := ownersFor(t, co, tc.file)
			if !slices.Equal(got, tc.expected) {
				t.Errorf("owners for %s = %v, expected %v", tc.file, got, tc.expected)
			}
		})
	}
}

func TestGitHubCodeownersEarlierRuleDoesNotWin(t *testing.T) {
	// The reverse order of the previous test: the broad rule comes last, so by
	// GitHub semantics it owns everything.
	content := strings.Join([]string{
		"/docs/api/   @api-team",
		"*            @everyone",
	}, "\n")

	reader := &githubFileReader{path: "/repo/.github/CODEOWNERS", content: []byte(content)}
	co, err := New("/repo", diffFiles("docs/api/spec.md"), reader, io.Discard,
		WithGitHubCodeownersFile(".github/CODEOWNERS"))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if got := ownersFor(t, co, "docs/api/spec.md"); !slices.Equal(got, []string{"@everyone"}) {
		t.Errorf("owners = %v, expected [@everyone]", got)
	}
}

func TestGitHubCodeownersOrgPlaceholder(t *testing.T) {
	content := "*   @%/platform-ci\n"

	for _, org := range []string{"swirldslabs", "PandasWhoCode"} {
		t.Run(org, func(t *testing.T) {
			reader := &githubFileReader{path: "/repo/.github/CODEOWNERS", content: []byte(content)}
			co, err := New("/repo", diffFiles("main.go"), reader, io.Discard,
				WithGitHubCodeownersFile(".github/CODEOWNERS"), WithOrg(org))
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			expected := []string{"@" + org + "/platform-ci"}
			if got := ownersFor(t, co, "main.go"); !slices.Equal(got, expected) {
				t.Errorf("owners = %v, expected %v", got, expected)
			}
		})
	}
}

func TestGitHubCodeownersParsing(t *testing.T) {
	content := strings.Join([]string{
		"# a leading comment",
		"",
		"*                @%/platform-ci",
		"/docs/           @alice @bob      # an OR group with an inline comment",
		"/legacy/         dev@example.com",
		"/broken/",
	}, "\n")

	warnings := bytes.NewBuffer(nil)
	reader := &githubFileReader{path: "/repo/.github/CODEOWNERS", content: []byte(content)}
	co, err := New(
		"/repo",
		diffFiles("main.go", "docs/a.md", "legacy/b.go", "broken/c.go"),
		reader,
		warnings,
		WithGitHubCodeownersFile(".github/CODEOWNERS"),
		WithOrg("acme"),
	)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	t.Run("expands the placeholder in the catch-all", func(t *testing.T) {
		if got := ownersFor(t, co, "main.go"); !slices.Equal(got, []string{"@acme/platform-ci"}) {
			t.Errorf("owners = %v", got)
		}
	})

	t.Run("strips inline comments and keeps the OR group", func(t *testing.T) {
		if got := ownersFor(t, co, "docs/a.md"); !slices.Equal(got, []string{"@alice", "@bob"}) {
			t.Errorf("owners = %v, expected [@alice @bob]", got)
		}
	})

	t.Run("an email-only rule leaves the file to the catch-all", func(t *testing.T) {
		// The email is unsupported and skipped, so the earlier catch-all is
		// the last rule that still has owners.
		if got := ownersFor(t, co, "legacy/b.go"); !slices.Equal(got, []string{"@acme/platform-ci"}) {
			t.Errorf("owners = %v", got)
		}
		if !strings.Contains(warnings.String(), "Unsupported owner") {
			t.Error("expected a warning about the unsupported email owner")
		}
	})

	t.Run("warns about a rule with no owner", func(t *testing.T) {
		if !strings.Contains(warnings.String(), "Invalid line in CODEOWNERS file") {
			t.Error("expected a warning about the ownerless line")
		}
	})
}

func TestGitHubCodeownersUnownedFiles(t *testing.T) {
	content := "/docs/   @docs-team\n"
	reader := &githubFileReader{path: "/repo/.github/CODEOWNERS", content: []byte(content)}
	co, err := New("/repo", diffFiles("docs/a.md", "src/main.go"), reader, io.Discard,
		WithGitHubCodeownersFile(".github/CODEOWNERS"))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if got := co.UnownedFiles(); !slices.Equal(got, []string{"src/main.go"}) {
		t.Errorf("UnownedFiles() = %v, expected [src/main.go]", got)
	}
}

func TestGitHubCodeownersMissingFile(t *testing.T) {
	reader := &githubFileReader{path: "/repo/.github/CODEOWNERS", content: nil}
	warnings := bytes.NewBuffer(nil)
	co, err := New("/repo", diffFiles("main.go"), reader, warnings,
		WithGitHubCodeownersFile("does/not/exist"))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if got := co.UnownedFiles(); !slices.Equal(got, []string{"main.go"}) {
		t.Errorf("UnownedFiles() = %v, expected [main.go]", got)
	}
	if !strings.Contains(warnings.String(), "CODEOWNERS file not found") {
		t.Error("expected a not-found warning")
	}
}

func TestGitHubCodeownersApplyApprovals(t *testing.T) {
	content := "*   @%/platform-ci\n"
	reader := &githubFileReader{path: "/repo/.github/CODEOWNERS", content: []byte(content)}
	co, err := New("/repo", diffFiles("main.go"), reader, io.Discard,
		WithGitHubCodeownersFile(".github/CODEOWNERS"), WithOrg("acme"))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if len(co.AllRequired()) != 1 {
		t.Fatalf("expected 1 required group before approval, got %d", len(co.AllRequired()))
	}
	// Approval is matched against the resolved slug, proving the nameReviewerMap
	// is keyed on the expanded name.
	co.ApplyApprovals([]Slug{NewSlug("@acme/platform-ci")})
	if len(co.AllRequired()) != 0 {
		t.Errorf("expected the group to be satisfied after approval, got %d remaining", len(co.AllRequired()))
	}
}

func TestJoinRepoPath(t *testing.T) {
	tt := []struct {
		root, rel, expected string
	}{
		{"/repo", ".github/CODEOWNERS", "/repo/.github/CODEOWNERS"},
		{"/repo/", ".github/CODEOWNERS", "/repo/.github/CODEOWNERS"},
		{"/repo", "/.github/CODEOWNERS", "/repo/.github/CODEOWNERS"},
		{"", "CODEOWNERS", "CODEOWNERS"},
	}
	for _, tc := range tt {
		if got := joinRepoPath(tc.root, tc.rel); got != tc.expected {
			t.Errorf("joinRepoPath(%q, %q) = %q, expected %q", tc.root, tc.rel, got, tc.expected)
		}
	}
}
