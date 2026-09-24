package codeowners

import (
	"bytes"
	"strings"
	"testing"
)

func TestExpandOrg(t *testing.T) {
	tt := []struct {
		name     string
		owner    string
		org      string
		expected string
	}{
		{"expands the placeholder", "@%/platform-ci", "swirldslabs", "@swirldslabs/platform-ci"},
		{"expands against another org", "@%/platform-ci", "PandasWhoCode", "@PandasWhoCode/platform-ci"},
		{"expands a nested slug", "@%/a/b", "acme", "@acme/a/b"},
		{"leaves a plain user alone", "@alice", "acme", "@alice"},
		{"leaves a qualified team alone", "@other-org/team", "acme", "@other-org/team"},
		{"leaves an email alone", "dev@example.com", "acme", "dev@example.com"},
		{"leaves a stray percent alone", "@foo%bar", "acme", "@foo%bar"},
		{"leaves a trailing percent alone", "@org/%", "acme", "@org/%"},
		{"leaves @% without a slash alone", "@%", "acme", "@%"},
		{"leaves a bare percent alone", "%", "acme", "%"},
		{"leaves %/team without the @ alone", "%/team", "acme", "%/team"},
		{"is a no-op with an empty org", "@%/platform-ci", "", "@%/platform-ci"},
		{"is a no-op on an empty string", "", "acme", ""},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExpandOrg(tc.owner, tc.org); got != tc.expected {
				t.Errorf("ExpandOrg(%q, %q) = %q, expected %q", tc.owner, tc.org, got, tc.expected)
			}
		})
	}
}

func TestWarnSuspiciousOrgPlaceholder(t *testing.T) {
	tt := []struct {
		name       string
		owner      string
		expectWarn bool
	}{
		{"no percent", "@alice", false},
		{"valid placeholder", "@%/team", false},
		{"percent in the middle", "@foo%bar", true},
		{"percent without a slash", "@%", true},
		{"percent without the at sign", "%/team", true},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			buf := bytes.NewBuffer(nil)
			warnSuspiciousOrgPlaceholder(tc.owner, buf)
			warned := strings.Contains(buf.String(), "WARNING")
			if warned != tc.expectWarn {
				t.Errorf("warned = %v, expected %v (output: %q)", warned, tc.expectWarn, buf.String())
			}
		})
	}
}

func TestNewReviewerGroupMemoForOrg(t *testing.T) {
	t.Run("expands names before building slugs", func(t *testing.T) {
		rgm := NewReviewerGroupMemoForOrg("swirldslabs")
		group := rgm.ToReviewerGroup("@%/platform-ci")
		if len(group.Names) != 1 {
			t.Fatalf("expected 1 name, got %d", len(group.Names))
		}
		// Original is used for @-mentions and reviewer requests, so it must be
		// the resolved value, not the placeholder.
		if got := group.Names[0].Original(); got != "@swirldslabs/platform-ci" {
			t.Errorf("Original() = %q, expected %q", got, "@swirldslabs/platform-ci")
		}
		if got := group.Names[0].Normalized(); got != "@swirldslabs/platform-ci" {
			t.Errorf("Normalized() = %q, expected %q", got, "@swirldslabs/platform-ci")
		}
	})

	t.Run("placeholder and explicit org memoize to the same group", func(t *testing.T) {
		rgm := NewReviewerGroupMemoForOrg("acme")
		viaPlaceholder := rgm.ToReviewerGroup("@%/team")
		viaExplicit := rgm.ToReviewerGroup("@acme/team")
		if viaPlaceholder != viaExplicit {
			t.Error("expected the placeholder and the explicit org to resolve to the same memoized group")
		}
	})

	t.Run("expands every name in an OR group", func(t *testing.T) {
		rgm := NewReviewerGroupMemoForOrg("acme")
		group := rgm.ToReviewerGroup("@%/a", "@bob", "@%/b")
		expected := []string{"@acme/a", "@bob", "@acme/b"}
		if len(group.Names) != len(expected) {
			t.Fatalf("expected %d names, got %d", len(expected), len(group.Names))
		}
		for i, want := range expected {
			if got := group.Names[i].Original(); got != want {
				t.Errorf("name %d = %q, expected %q", i, got, want)
			}
		}
	})

	t.Run("empty org behaves like the plain memo", func(t *testing.T) {
		rgm := NewReviewerGroupMemoForOrg("")
		group := rgm.ToReviewerGroup("@%/team")
		if got := group.Names[0].Original(); got != "@%/team" {
			t.Errorf("Original() = %q, expected the token to be untouched", got)
		}
	})
}
