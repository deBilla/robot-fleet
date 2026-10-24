#!/usr/bin/env bash
# Every kubectl/helm call made for the local k3s cluster must name the colima
# context explicitly, and nothing may switch the user's current context: the
# default context on a dev machine can be a production cluster.
#
# Runs k3s-setup.sh (full and --images) and the k3s/k8s Make targets against
# fake kubectl/helm/colima/docker binaries that record their arguments.
# Usage: platform/scripts/test_k3s_context.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLATFORM_DIR="$(dirname "$SCRIPT_DIR")"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
CALLS="$WORK/calls.log"
mkdir -p "$WORK/bin"

for tool in kubectl helm docker kind; do
    cat >"$WORK/bin/$tool" <<EOF
#!/usr/bin/env bash
echo "$tool \$*" >>"$CALLS"
EOF
done
# colima list reports a running VM so start_cluster takes its "already up" path.
cat >"$WORK/bin/colima" <<EOF
#!/usr/bin/env bash
echo "colima \$*" >>"$CALLS"
if [ "\${1:-}" = list ]; then echo '{"status":"Running","address":"192.0.2.1"}'; fi
EOF
chmod +x "$WORK/bin/"*
export PATH="$WORK/bin:$PATH"

failures=0
# Run a command; on failure show its output (the checks below need it to finish).
run() {
    if ! "$@" >"$WORK/out.log" 2>&1; then
        echo "FAIL running: $*"; sed 's/^/    /' "$WORK/out.log"
        exit 1
    fi
}
check() {
    local label="$1" expected="$2"
    local bad
    bad=$(grep -E '^(kubectl|helm) ' "$CALLS" | grep -v -- "$expected" || true)
    if [ -n "$bad" ]; then
        echo "FAIL [$label] calls without $expected:"; echo "$bad" | sed 's/^/    /'
        failures=$((failures + 1))
    fi
    if grep -qE '^kubectl .*config use-context' "$CALLS"; then
        echo "FAIL [$label] switches the user's current kube context"
        failures=$((failures + 1))
    fi
    if ! grep -qE '^(kubectl|helm) ' "$CALLS"; then
        echo "FAIL [$label] made no kubectl/helm calls (test is not exercising it)"
        failures=$((failures + 1))
    fi
    : >"$CALLS"
}

: >"$CALLS"
run "$SCRIPT_DIR/k3s-setup.sh"
check "k3s-setup.sh" "--context colima\|--kube-context colima"

run "$SCRIPT_DIR/k3s-setup.sh" --images
check "k3s-setup.sh --images" "--context colima"

run make -s -C "$PLATFORM_DIR" k3s-train TASK=locomotion TIMESTEPS=10
check "make k3s-train" "--context colima"

for target in k8s-train k8s-sync k8s-status "k8s-logs SVC=api" "k8s-reload SVC=api"; do
    # shellcheck disable=SC2086  # target may carry a variable assignment
    run make -s -C "$PLATFORM_DIR" $target TASK=locomotion
    check "make $target" "--context kind-fleetos"
done

if [ "$failures" -gt 0 ]; then
    echo "$failures check(s) failed"
    exit 1
fi
echo "ok: every kubectl/helm call names its cluster; no context switches"
