package codeowners

import (
	"fmt"
	"io"
	"strings"
)

// orgPlaceholderPrefix is the owner-token prefix that stands in for "the
// organization this repository belongs to". A team is written "@%/team-slug"
// and resolves to "@<org>/team-slug" at run time, which lets a single
// ownership file be shared across multiple organizations.
//
// "%" is not a legal character in a GitHub organization name, so this can
// never collide with a real owner.
const orgPlaceholderPrefix = "@%/"

// ExpandOrg replaces a leading "@%/" organization placeholder in an owner
// token with the given organization.
//
// Only the exact "@%/" prefix is a placeholder. Plain users ("@alice"),
// already-qualified teams ("@org/team"), emails and any other use of "%" are
// returned unchanged. Expansion is a no-op when org is empty, so callers that
// do not know the organization keep the old behavior.
func ExpandOrg(name, org string) string {
	if org == "" || !strings.HasPrefix(name, orgPlaceholderPrefix) {
		return name
	}
	return "@" + org + "/" + name[len(orgPlaceholderPrefix):]
}

// warnSuspiciousOrgPlaceholder writes a warning for owner tokens that look
// like a mistyped organization placeholder, so typos surface instead of
// silently never matching.
func warnSuspiciousOrgPlaceholder(name string, warningWriter io.Writer) {
	if warningWriter == nil || !strings.Contains(name, "%") {
		return
	}
	if strings.HasPrefix(name, orgPlaceholderPrefix) {
		return
	}
	_, _ = fmt.Fprintf(
		warningWriter,
		"WARNING: owner %q contains '%%' but is not an organization placeholder; expected the form \"%steam-slug\"\n",
		name,
		orgPlaceholderPrefix,
	)
}
