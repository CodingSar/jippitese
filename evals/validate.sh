#!/usr/bin/env bash
# validate.sh [task-id ...]: proves every frozen task is well-formed. For
# each task: copy its project from examples/, append the hidden acceptance
# scenarios to the target module's scenarios.jip and check they fail; apply
# the reference solution; then `jip check --write-interfaces` (accepting the
# break the task declares) and `jip test` must both be green.
set -u
here="$(cd "$(dirname "$0")" && pwd)"
root="$(dirname "$here")"
jip="$here/out/bin/jip"
mkdir -p "$here/out/bin"
(cd "$root/compiler" && go build -o "$jip" ./cmd/jip) || exit 3

field() { sed -n "s/^$2: *//p" "$1" | head -1; }

# The task set is frozen: any edit to a task shows up here.
if ! (cd "$here/tasks" && sha256sum --quiet -c MANIFEST); then
  echo "FAIL: tasks/ differs from tasks/MANIFEST (the task set is frozen)"
  exit 1
fi

ids=("$@")
if [ ${#ids[@]} -eq 0 ]; then
  for d in "$here"/tasks/*/; do ids+=("$(basename "$d")"); done
fi

fail=0
for id in "${ids[@]}"; do
  t="$here/tasks/$id"
  project="$(field "$t/task.md" project)"
  module="$(field "$t/task.md" module)"
  breaking="$(field "$t/task.md" breaking)"
  work="$(mktemp -d)"
  cp -r "$root/examples/$project/." "$work/"
  rm -rf "$work/.jip"
  scen="$work/$(echo "$module" | tr . /)/scenarios.jip"
  { echo; cat "$t/accept.jip"; } >> "$scen"
  if (cd "$work" && "$jip" test --filter accept >/dev/null 2>&1); then
    echo "FAIL $id: acceptance passes before the change"
    fail=1; rm -rf "$work"; continue
  fi
  if ! (cd "$work" && patch -s -p1 < "$t/solution.patch"); then
    echo "FAIL $id: solution does not apply"
    fail=1; rm -rf "$work"; continue
  fi
  accept=()
  [ -n "$breaking" ] && accept=(--accept-breaking "$breaking")
  if ! out="$(cd "$work" && "$jip" check --write-interfaces "${accept[@]}" 2>&1)"; then
    echo "FAIL $id: jip check after the solution"; echo "$out" | sed 's/^/    /'
    fail=1; rm -rf "$work"; continue
  fi
  if ! out="$(cd "$work" && "$jip" test 2>&1)"; then
    echo "FAIL $id: jip test after the solution"; echo "$out" | sed 's/^/    /'
    fail=1; rm -rf "$work"; continue
  fi
  echo "ok   $id ($project)"
  rm -rf "$work"
done
exit $fail
