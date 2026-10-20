#!/usr/bin/env bash
# validate_go.sh [task-id ...]: proves every frozen Go baseline task is
# well-formed, mirroring validate.sh. For each task: copy its project from
# go/, add the hidden acceptance tests to the target package and check they
# fail (a compile failure counts); apply the reference solution; then gofmt,
# `go vet ./...` and `go test ./...` must all be clean.
set -u
here="$(cd "$(dirname "$0")" && pwd)"

field() { sed -n "s/^$2: *//p" "$1" | head -1; }

# The task set is frozen: any edit to a task shows up here.
if ! (cd "$here/go/tasks" && sha256sum --quiet -c MANIFEST); then
  echo "FAIL: go/tasks/ differs from go/tasks/MANIFEST (the task set is frozen)"
  exit 1
fi

ids=("$@")
if [ ${#ids[@]} -eq 0 ]; then
  for d in "$here"/go/tasks/*/; do ids+=("$(basename "$d")"); done
fi

fail=0
for id in "${ids[@]}"; do
  t="$here/go/tasks/$id"
  project="$(field "$t/task.md" project)"
  pkg="$(field "$t/task.md" package)"
  work="$(mktemp -d)"
  cp -r "$here/go/$project/." "$work/"
  cp "$t/accept_test.go" "$work/$pkg/accept_test.go"
  if (cd "$work" && go test -count=1 -run '^TestAccept' "./$pkg" >/dev/null 2>&1); then
    echo "FAIL $id: acceptance passes before the change"
    fail=1; rm -rf "$work"; continue
  fi
  if ! (cd "$work" && patch -s -p1 < "$t/solution.patch"); then
    echo "FAIL $id: solution does not apply"
    fail=1; rm -rf "$work"; continue
  fi
  if out="$(cd "$work" && gofmt -l .)" && [ -n "$out" ]; then
    echo "FAIL $id: gofmt after the solution"; echo "$out" | sed 's/^/    /'
    fail=1; rm -rf "$work"; continue
  fi
  if ! out="$(cd "$work" && go vet ./... 2>&1)"; then
    echo "FAIL $id: go vet after the solution"; echo "$out" | sed 's/^/    /'
    fail=1; rm -rf "$work"; continue
  fi
  if ! out="$(cd "$work" && go test -count=1 ./... 2>&1)"; then
    echo "FAIL $id: go test after the solution"; echo "$out" | sed 's/^/    /'
    fail=1; rm -rf "$work"; continue
  fi
  echo "ok   $id ($project)"
  rm -rf "$work"
done
exit $fail
