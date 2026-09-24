package codeowners

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	f "github.com/multimediallc/codeowners-plus/pkg/functional"
)

// githubRule is one line of a GitHub CODEOWNERS file: a path pattern and the
// group of owners that may satisfy it.
type githubRule struct {
	// pattern is the original text, kept for warnings.
	pattern string
	// globs are the doublestar translations of pattern. A file matches the
	// rule when it matches any of them.
	globs    []string
	reviewer *ReviewerGroup
}

func (r *githubRule) matches(path string, warningWriter io.Writer) bool {
	for _, glob := range r.globs {
		match, err := doublestar.Match(glob, path)
		if err != nil {
			_, _ = fmt.Fprintf(warningWriter, "WARNING: PatternError for pattern '%s': %s\n", r.pattern, err)
			continue
		}
		if match {
			return true
		}
	}
	return false
}

// githubPatternToGlobs translates a GitHub CODEOWNERS path pattern into the
// doublestar patterns that match the same set of repo-root-relative paths.
//
// GitHub uses gitignore-style matching:
//   - a pattern containing no "/" (ignoring a trailing one) matches at any
//     depth, so "*.js" matches "a/b/c.js";
//   - a leading or embedded "/" anchors the pattern to the repository root;
//   - a trailing "/" matches only the contents of a directory;
//   - a pattern with no trailing "/" matches a file of that name and, when it
//     names a directory, everything beneath it.
func githubPatternToGlobs(pattern string) []string {
	p := pattern
	dirOnly := strings.HasSuffix(p, "/")
	p = strings.TrimSuffix(p, "/")

	// Anchoring is decided on the pattern before the leading slash is removed,
	// because "/docs" and "docs/team" are both anchored but "docs" is not.
	anchored := strings.HasPrefix(p, "/") || strings.Contains(p, "/")
	p = strings.TrimPrefix(p, "/")

	if p == "" {
		// The pattern was "/" - the whole repository.
		return []string{"**"}
	}
	if !anchored {
		p = "**/" + p
	}
	if dirOnly {
		// Only the contents, recursively. "<p>/**" would also match <p>
		// itself, because doublestar lets "**" match zero segments.
		return []string{p + "/**/*"}
	}

	// A wildcard in the final segment means the pattern names files at that
	// level, so it must not be widened to a directory's contents: GitHub's
	// "docs/*" matches docs/a.md but not docs/b/c.md.
	last := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		last = p[i+1:]
	}
	if strings.ContainsAny(last, "*?[") {
		return []string{p}
	}

	// Otherwise the pattern names a path that may be a directory, and a
	// directory owns everything beneath it.
	return []string{p, p + "/**/*"}
}

// readGitHubCodeowners parses a GitHub CODEOWNERS file into an ordered rule
// list. Order is preserved because GitHub resolves ownership by last match.
func readGitHubCodeowners(
	content []byte,
	reviewerGroupManager ReviewerGroupManager,
	warningWriter io.Writer,
) []*githubRule {
	rules := make([]*githubRule, 0)

	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		// Strip inline comments before splitting, so "* @team # why" does not
		// turn "#" and "why" into owners.
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			_, _ = fmt.Fprintln(warningWriter, "WARNING: Invalid line in CODEOWNERS file:", line)
			continue
		}

		owners := make([]string, 0, len(parts)-1)
		for _, owner := range parts[1:] {
			if !strings.HasPrefix(owner, "@") {
				// Emails and other non-handle owners are not supported by this
				// tool; skip them rather than treating them as a handle.
				_, _ = fmt.Fprintf(warningWriter, "WARNING: Unsupported owner %q in CODEOWNERS file (only @user and @org/team are supported)\n", owner)
				continue
			}
			warnSuspiciousOrgPlaceholder(owner, warningWriter)
			owners = append(owners, owner)
		}
		if len(owners) == 0 {
			continue
		}

		rules = append(rules, &githubRule{
			pattern:  parts[0],
			globs:    githubPatternToGlobs(parts[0]),
			reviewer: reviewerGroupManager.ToReviewerGroup(owners...),
		})
	}

	return rules
}

// newGitHubCodeOwners builds a CodeOwners from a single GitHub CODEOWNERS
// file, using GitHub's last-matching-rule-wins semantics.
func newGitHubCodeOwners(
	root string,
	codeownersPath string,
	files []DiffFile,
	fileReader FileReader,
	warningWriter io.Writer,
	reviewerGroupManager ReviewerGroupManager,
) (CodeOwners, error) {
	if fileReader == nil {
		fileReader = &FilesystemReader{}
	}
	if warningWriter == nil {
		warningWriter = io.Discard
	}

	fullPath := joinRepoPath(root, codeownersPath)

	var rules []*githubRule
	if !fileReader.PathExists(fullPath) {
		_, _ = fmt.Fprintf(warningWriter, "WARNING: CODEOWNERS file not found at %s\n", fullPath)
	} else if content, err := fileReader.ReadFile(fullPath); err != nil {
		_, _ = fmt.Fprintf(warningWriter, "WARNING: Error reading CODEOWNERS file at %s: %v\n", fullPath, err)
	} else {
		rules = readGitHubCodeowners(content, reviewerGroupManager, warningWriter)
	}

	fileNames := f.Map(files, func(file DiffFile) string { return file.FileName })
	owners := make(map[string]fileOwners, len(fileNames))
	nameReviewerMap := make(map[string]ReviewerGroups)
	unownedFiles := make([]string, 0)

	for _, file := range fileNames {
		fileOwner := newFileOwners()

		// GitHub resolves ownership by the last matching rule, not the most
		// specific one.
		var match *ReviewerGroup
		for _, rule := range rules {
			if rule.matches(file, warningWriter) {
				match = rule.reviewer
			}
		}

		if match != nil {
			fileOwner.requiredReviewers = append(fileOwner.requiredReviewers, match)
			for _, name := range match.Names {
				normalizedName := name.Normalized()
				nameReviewerMap[normalizedName] = append(nameReviewerMap[normalizedName], match)
			}
		} else {
			unownedFiles = append(unownedFiles, file)
		}

		owners[file] = *fileOwner
	}

	return &ownersMap{
		fileToOwner:     owners,
		nameReviewerMap: nameReviewerMap,
		unownedFiles:    unownedFiles,
	}, nil
}

// joinRepoPath joins a repository root with a repo-relative path.
func joinRepoPath(root, rel string) string {
	rel = strings.TrimPrefix(rel, "/")
	if root == "" {
		return rel
	}
	if !strings.HasSuffix(root, "/") {
		root += "/"
	}
	return root + rel
}
