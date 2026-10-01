#!/bin/sh -l

# Prints a Markdown test coverage report to stdout. Nothing in the repository
# is modified; the profile is written to a temporary directory.

set -e

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

go test ./... -covermode=count -coverprofile="${tmp}/coverage.out" >&2
go tool cover -func="${tmp}/coverage.out" > "${tmp}/coverage.txt"

total="$(awk '/^total:/ { print $NF }' "${tmp}/coverage.txt")"

echo "## Test Coverage: ${total}"
echo ""
echo "<details><summary>Coverage by function</summary>"
echo ""
echo '```'
cat "${tmp}/coverage.txt"
echo '```'
echo ""
echo "</details>"
