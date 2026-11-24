#!/usr/bin/env bash
# Print the CHANGELOG section for a version, or a commit summary when no section exists.
set -euo pipefail
cd "$(dirname "$0")/.."

version="${1:-}"
if [ -z "${version}" ]; then
  echo "usage: $0 <version>" >&2
  exit 2
fi
version="${version#v}"

if grep -q "^## \[${version}\]" CHANGELOG.md; then
  awk -v version="${version}" '
    $0 ~ "^## \\[" version "\\]" { printing = 1; print; next }
    printing && /^## \[/ { exit }
    printing { print }
  ' CHANGELOG.md
else
  echo "## ${version}"
  echo
  echo "Changes since the previous release:"
  echo
  git log --no-merges --pretty='- %s (%h)' "$(git describe --tags --abbrev=0 2>/dev/null || echo HEAD~50)..HEAD"
fi
