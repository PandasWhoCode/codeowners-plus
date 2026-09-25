package codeowners

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// inMemoryReader is an in-memory FileReader that serves a single .codeowners file.
type inMemoryReader struct {
	content []byte
}

func (r *inMemoryReader) ReadFile(string) ([]byte, error) {
	return r.content, nil
}

func (r *inMemoryReader) PathExists(string) bool {
	return true
}

func TestRead(t *testing.T) {
	tt := []struct {
		name         string
		path         string
		fallback     bool
		fallbackName string
		owners       int
		additional   int
		optional     int
	}{
		{
			name:         "test project root",
			path:         "../../test_project",
			fallback:     true,
			fallbackName: "@base",
			owners:       4,
			additional:   2,
			optional:     1,
		},
		{
			name:         "frontend directory",
			path:         "../../test_project/frontend/",
			fallback:     true,
			fallbackName: "@frontend",
			owners:       2,
			additional:   1,
			optional:     0,
		},
		{
			name:       "non-directory file",
			path:       "../../test_project/a.py",
			fallback:   false,
			owners:     0,
			additional: 0,
			optional:   0,
		},
		{
			name:       "empty directory",
			path:       "../../test_project/empty",
			fallback:   false,
			owners:     0,
			additional: 0,
			optional:   0,
		},
	}

	rgMan := NewReviewerGroupMemo()
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			rules := Read(tc.path, rgMan, nil, io.Discard)

			if !tc.fallback && rules.Fallback != nil {
				t.Errorf("Expected fallback to be nil, got %+v", rules.Fallback)
			}

			if tc.fallback && rules.Fallback.Names[0].Original() != tc.fallbackName {
				t.Errorf("Expected fallback to be %s, got %s", tc.fallbackName, rules.Fallback.Names[0].Original())
			}

			if len(rules.OwnerTests) != tc.owners {
				t.Errorf("Expected %d owner tests, got %d", tc.owners, len(rules.OwnerTests))
			}

			if len(rules.AdditionalReviewerTests) != tc.additional {
				t.Errorf("Expected %d additional tests, got %d", tc.additional, len(rules.AdditionalReviewerTests))
			}

			if len(rules.OptionalReviewerTests) != tc.optional {
				t.Errorf("Expected %d optional tests, got %d", tc.optional, len(rules.OptionalReviewerTests))
			}
		})
	}
}

// TestReadLastMatchWinsSamePriority verifies that within a single priority tier,
// the last-declared rule wins, per the documented "last declared wins" semantics.
// Since ownerTestRecursive returns the first match, same-tier rules must appear in
// reverse declaration order (last-declared first). In a large file the sort must be
// stable to preserve that order for equal-priority patterns — an unstable sort
// scrambles them.
func TestReadLastMatchWinsSamePriority(t *testing.T) {
	rgMan := NewReviewerGroupMemo()

	const globstarRules = 200

	var b strings.Builder
	// Many globstar rules (service0/**, service1/**, ...) interleaved with rules from
	// the other two priority tiers. The mix forces the sort to move elements, which
	// surfaces reordering of the equal-priority globstar rules under an unstable sort.
	for i := 0; i < globstarRules; i++ {
		fmt.Fprintf(&b, "service%d/** @team%d\n", i, i)
		fmt.Fprintf(&b, "file%d.go @file%d\n", i, i) // no-wildcard tier
		fmt.Fprintf(&b, "lib%d/*.go @lib%d\n", i, i) // wildcard tier
	}

	reader := &inMemoryReader{content: []byte(b.String())}
	rules := Read("any/dir", rgMan, reader, io.Discard)

	// Extract the globstar rules in result order. They must be in reverse declaration
	// order (highest index first): for any two same-tier rules, the later-declared one
	// must precede the earlier one so that first-match-wins picks the last declaration.
	prev := globstarRules
	for _, test := range rules.OwnerTests {
		var idx int
		if n, _ := fmt.Sscanf(test.Match, "service%d/**", &idx); n != 1 {
			continue
		}
		if idx >= prev {
			t.Errorf("globstar rules out of last-declared-wins order: service%d/** appears after service%d/** (expected strictly decreasing)", idx, prev)
			break
		}
		prev = idx
	}
}

// TestReadExpandsOrgPlaceholder covers the "@%/" placeholder in the native
// .codeowners format across every rule kind. It uses inMemoryReader rather
// than the test_project fixtures so the existing exact-count assertions in
// this file and codeowners_test.go stay untouched.
func TestReadExpandsOrgPlaceholder(t *testing.T) {
	content := strings.Join([]string{
		"* @%/fallback-team",
		"b.py @%/py-team",
		"or.py @%/first @second @%/third",
		"& models* @%/devops",
		"? a.py @%/juniors",
	}, "\n")

	rgMan := NewReviewerGroupMemoForOrg("acme")
	reader := &inMemoryReader{content: []byte(content)}
	rules := Read("any/dir", rgMan, reader, io.Discard)

	t.Run("fallback", func(t *testing.T) {
		if rules.Fallback == nil {
			t.Fatal("expected a fallback rule")
		}
		if got := rules.Fallback.Names[0].Original(); got != "@acme/fallback-team" {
			t.Errorf("fallback owner = %q, expected @acme/fallback-team", got)
		}
	})

	t.Run("owner rules", func(t *testing.T) {
		found := map[string]string{}
		for _, test := range rules.OwnerTests {
			found[test.Match] = strings.Join(OriginalStrings(test.Reviewer.Names), ",")
		}
		if got := found["b.py"]; got != "@acme/py-team" {
			t.Errorf("b.py owner = %q, expected @acme/py-team", got)
		}
		// Every member of an OR group is expanded; plain handles are untouched.
		if got := found["or.py"]; got != "@acme/first,@second,@acme/third" {
			t.Errorf("or.py owners = %q, expected @acme/first,@second,@acme/third", got)
		}
	})

	t.Run("additional (&) rules", func(t *testing.T) {
		if len(rules.AdditionalReviewerTests) != 1 {
			t.Fatalf("expected 1 additional rule, got %d", len(rules.AdditionalReviewerTests))
		}
		if got := rules.AdditionalReviewerTests[0].Reviewer.Names[0].Original(); got != "@acme/devops" {
			t.Errorf("additional owner = %q, expected @acme/devops", got)
		}
	})

	t.Run("optional (?) rules", func(t *testing.T) {
		if len(rules.OptionalReviewerTests) != 1 {
			t.Fatalf("expected 1 optional rule, got %d", len(rules.OptionalReviewerTests))
		}
		if got := rules.OptionalReviewerTests[0].Reviewer.Names[0].Original(); got != "@acme/juniors" {
			t.Errorf("optional owner = %q, expected @acme/juniors", got)
		}
	})

	t.Run("no expansion without an org", func(t *testing.T) {
		plain := Read("any/dir", NewReviewerGroupMemo(), &inMemoryReader{content: []byte(content)}, io.Discard)
		if got := plain.Fallback.Names[0].Original(); got != "@%/fallback-team" {
			t.Errorf("fallback owner = %q, expected the placeholder to be untouched", got)
		}
	})
}
