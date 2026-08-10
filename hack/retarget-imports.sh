#!/usr/bin/env bash
#
# Rewrites upstream's import paths onto this fork's.
#
# The fork exists because `replace github.com/gocql/gocql => ...` cannot point at
# the Apache driver: that module imports its own internal/ packages under its real
# path, so Go rejects it with "used for two different module paths". Rewriting the
# imports is the only way to combine the two. See FORK.md.
#
# This script is idempotent and is the whole of the mechanical fork delta. When
# merging upstream produces a swamp of import conflicts, the escape hatch is:
#
#     git revert --no-commit <retarget commit>   # back to upstream paths
#     git merge <upstream tag>                   # merges against upstream shape
#     ./hack/retarget-imports.sh                 # regenerate the rewrite
#
set -euo pipefail

cd "$(dirname "$0")/.."

readonly OLD_DRIVER="github.com/gocql/gocql"
readonly NEW_DRIVER="github.com/apache/cassandra-gocql-driver/v2"
readonly OLD_MODULE="github.com/scylladb/gocqlx/v3"
# Keeps the /v3 major suffix: this fork tracks upstream v3.x, and Go's semantic
# import versioning would otherwise ignore every v3 tag we publish.
readonly NEW_MODULE="github.com/playneta/gocqlx/v3"

# perl rather than sed: -i behaves the same on macOS and Linux.
find . -path ./.git -prune -o \
	\( -name '*.go' -o -name '*.tmpl' \) -print0 |
	xargs -0 perl -pi -e "s{\Q${OLD_DRIVER}\E}{${NEW_DRIVER}}g; s{\Q${OLD_MODULE}\E}{${NEW_MODULE}}g"

go mod edit -module "${NEW_MODULE}"
go mod edit -droprequire "${OLD_DRIVER}"
go mod edit -dropreplace "${OLD_DRIVER}"
go mod edit -require "${NEW_DRIVER}@v2.1.2"

# cmd/schemagen/testdata is its own module: it type-checks the generated models
# against this one, so it needs the same retarget. The ScyllaDB gocql replace goes
# away with it — that is the whole point of the fork.
readonly TESTDATA=cmd/schemagen/testdata/go.mod
if [ -f "${TESTDATA}" ]; then
	go mod edit -modfile "${TESTDATA}" \
		-droprequire "${OLD_DRIVER}" \
		-dropreplace "${OLD_DRIVER}" \
		-droprequire "${OLD_MODULE}" \
		-dropreplace "${OLD_MODULE}" \
		-require "${NEW_DRIVER}@v2.1.2" \
		-require "${NEW_MODULE}@v3.0.0" \
		-replace "${NEW_MODULE}=../../.."
fi

echo "retargeted ${OLD_DRIVER} -> ${NEW_DRIVER}"
echo "retargeted ${OLD_MODULE} -> ${NEW_MODULE}"
echo
echo "NOTE: the tree does not compile on this commit alone — the Apache driver"
echo "removed APIs upstream still calls. The follow-up commit adapts to them."
