#!/usr/bin/env bash
# Sets up FleetOS on a single-node k3s cluster inside Colima (macOS) — the
# lightweight alternative to the kind + ArgoCD setup in k8s-setup.sh.
#
# Focus is the MLOps path: platform services + MinIO + Temporal + Argo
# Workflows + training templates + Kubeflow Trainer/Katib. ArgoCD, the simulator
# and dashboards are off.
# k3s shares Colima's Docker runtime, so locally built images need no loading.
#
# Prerequisites: colima, kubectl, helm, docker
# Usage:
#   scripts/k3s-setup.sh           # Full setup (idempotent)
#   scripts/k3s-setup.sh --images  # Rebuild images + restart platform pods
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLATFORM_DIR="$(dirname "$SCRIPT_DIR")"
K8S_DIR="$PLATFORM_DIR/deploy/k8s"
HELM_DIR="$PLATFORM_DIR/deploy/helm/fleetos"
# Every kubectl/helm call targets this context explicitly; the user's current
# context (possibly a production cluster) is never used or changed.
KUBE_CONTEXT="${KUBE_CONTEXT:-colima}"
RELEASE="${RELEASE:-fleetos-platform}"
NAMESPACE="${NAMESPACE:-fleetos}"
K3S_CPUS="${K3S_CPUS:-8}"
K3S_MEMORY="${K3S_MEMORY:-14}"
ARGO_WF_VERSION="${ARGO_WF_VERSION:-v3.5.5}"
# Kubeflow training backend (Trainer v2 TrainJobs + Katib HPO). Set
# K3S_KUBEFLOW=false to skip it and also pass --set kubeflow.enabled=false.
K3S_KUBEFLOW="${K3S_KUBEFLOW:-true}"
KUBEFLOW_TRAINER_VERSION="${KUBEFLOW_TRAINER_VERSION:-v2.3.0}"
KATIB_VERSION="${KATIB_VERSION:-v0.19.0}"
MIN_FREE_DISK_GB=15

PLATFORM_IMAGES=(api worker inference ingestion processor)

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${GREEN}[k3s]${NC} $*"; }
warn() { echo -e "${YELLOW}[k3s]${NC} $*"; }
err()  { echo -e "${RED}[k3s]${NC} $*" >&2; }
step() { echo -e "\n${CYAN}--- $* ---${NC}"; }

kubectl() { command kubectl --context "$KUBE_CONTEXT" "$@"; }
helm() { command helm --kube-context "$KUBE_CONTEXT" "$@"; }

# --- Preflight ---
preflight() {
    step "Preflight checks"
    for cmd in colima kubectl helm docker; do
        if ! command -v "$cmd" &>/dev/null; then
            err "$cmd is required but not installed (brew install $cmd)"
            exit 1
        fi
        log "  $cmd OK"
    done

    # Images (~5GB) + the Colima VM disk grow on the host; running out mid-build
    # leaves half-written layers behind.
    local free_gb
    free_gb=$(df -k / | awk 'NR==2 {print int($4/1024/1024)}')
    if [ "${free_gb:-0}" -lt "$MIN_FREE_DISK_GB" ]; then
        warn "Only ${free_gb}GB free on / (recommended >= ${MIN_FREE_DISK_GB}GB)."
        warn "Free space with: docker builder prune -af && colima ssh -- sudo fstrim -av"
    fi
}

# Return freed VM blocks to the host. Docker data lives on Colima's data disk
# (/mnt/lima-colima), so trim every filesystem, not just /.
trim_vm_disk() {
    colima ssh -- sudo fstrim -a >/dev/null 2>&1 || warn "fstrim failed (non-fatal)"
}

# --- Colima VM with k3s ---
start_cluster() {
    step "Colima + k3s"
    local status
    status=$(colima list -j 2>/dev/null | head -1 || true)
    if echo "$status" | grep -q '"status":"Running"' && kubectl get nodes >/dev/null 2>&1; then
        log "Colima is running with k3s"
    else
        log "Starting Colima (cpu=${K3S_CPUS}, memory=${K3S_MEMORY}GiB) with k3s..."
        colima start --kubernetes --cpu "$K3S_CPUS" --memory "$K3S_MEMORY" --network-address
    fi
    kubectl wait --for=condition=Ready node --all --timeout=180s
}

vm_ip() {
    colima list -j 2>/dev/null | head -1 | sed -n 's/.*"address":"\([^"]*\)".*/\1/p'
}

# --- Images (built straight into Colima's Docker, which k3s uses) ---
build_images() {
    step "Docker images"
    cd "$PLATFORM_DIR"
    for svc in "${PLATFORM_IMAGES[@]}"; do
        local img="fleetos/${svc}:latest"
        log "  Building ${img}..."
        docker build -q -t "${img}" -f "deploy/docker/Dockerfile.${svc}" . >/dev/null
    done
    log "  Building fleetos/training:latest..."
    docker build -q -t "fleetos/training:latest" ./training >/dev/null
    docker builder prune -af >/dev/null
    trim_vm_disk
}

# --- Kubeflow Trainer v2 + Katib (must precede the Helm release, which
# creates a ClusterTrainingRuntime) ---
install_kubeflow() {
    [ "$K3S_KUBEFLOW" = "true" ] || { log "Skipping Kubeflow (K3S_KUBEFLOW=false)"; return; }
    step "Kubeflow Trainer ${KUBEFLOW_TRAINER_VERSION} + Katib ${KATIB_VERSION}"
    # Only the controller (+ JobSet); FleetOS ships its own small runtime instead
    # of the stock ones, which pull multi-GB CUDA images.
    kubectl apply --server-side \
        -k "https://github.com/kubeflow/trainer.git/manifests/overlays/manager?ref=${KUBEFLOW_TRAINER_VERSION}" >/dev/null
    kubectl -n kubeflow-system rollout status deployment/kubeflow-trainer-controller-manager --timeout=300s
    kubectl -n kubeflow-system rollout status deployment/jobset-controller-manager --timeout=300s

    kubectl apply --server-side \
        -k "https://github.com/kubeflow/katib.git/manifests/v1beta1/installs/katib-standalone-postgres?ref=${KATIB_VERSION}" >/dev/null
    for d in katib-controller katib-db-manager katib-ui; do
        kubectl -n kubeflow rollout status "deployment/$d" --timeout=400s
    done
}

# --- FleetOS Helm release ---
deploy_platform() {
    step "FleetOS Helm release"
    cd "$HELM_DIR"
    if ! ls charts/*.tgz >/dev/null 2>&1; then
        log "Fetching Helm subcharts..."
        helm dependency build . >/dev/null
    fi
    helm upgrade --install "$RELEASE" . \
        -n "$NAMESPACE" --create-namespace \
        -f values-local.yaml -f values-k3s.yaml \
        --wait --timeout 10m
    cd "$PLATFORM_DIR"
}

# --- Argo Workflows (training) ---
install_argo_workflows() {
    step "Argo Workflows"
    kubectl create namespace argo 2>/dev/null || true
    if kubectl get deployment workflow-controller -n argo >/dev/null 2>&1; then
        log "Argo Workflows already installed"
    else
        log "Installing Argo Workflows ${ARGO_WF_VERSION}..."
        kubectl apply --server-side -n argo \
            -f "https://github.com/argoproj/argo-workflows/releases/download/${ARGO_WF_VERSION}/install.yaml" >/dev/null
    fi

    # Server auth mode: the api/worker call the Argo REST API without a token
    # locally. Use client auth + ARGO_TOKEN in shared clusters.
    kubectl -n argo patch deployment argo-server --type json \
        -p '[{"op":"replace","path":"/spec/template/spec/containers/0/args","value":["server","--auth-mode=server"]}]' >/dev/null
    kubectl patch svc argo-server -n argo --type merge \
        -p '{"spec":{"type":"NodePort","ports":[{"port":2746,"targetPort":2746,"nodePort":30746}]}}' >/dev/null

    kubectl apply -f "$K8S_DIR/argo-rbac.yaml"
    kubectl apply -f "$K8S_DIR/training-secrets.yaml"
    kubectl apply -n argo -f "$K8S_DIR/workflows/"

    kubectl -n argo rollout status deployment/workflow-controller --timeout=180s
    kubectl -n argo rollout status deployment/argo-server --timeout=180s
}

restart_platform() {
    step "Restarting platform pods"
    for svc in "${PLATFORM_IMAGES[@]}"; do
        kubectl -n "$NAMESPACE" rollout restart "deployment/${RELEASE}-${svc}"
    done
    for svc in "${PLATFORM_IMAGES[@]}"; do
        kubectl -n "$NAMESPACE" rollout status "deployment/${RELEASE}-${svc}" --timeout=180s
    done
}

# --- Summary ---
print_summary() {
    local ip
    ip=$(vm_ip)
    ip="${ip:-<colima-vm-ip>}"
    step "FleetOS on k3s ready"
    log "  Context:       ${KUBE_CONTEXT} (pass --context ${KUBE_CONTEXT}; your current context is unchanged)"
    log "  API:           http://${ip}:30880   (X-API-Key: dev-key-001)"
    log "  Inference:     http://${ip}:30081"
    log "  Argo Wf UI:    https://${ip}:30746"
    log "  MinIO Console: http://${ip}:30901   (fleetos / fleetos123)"
    if [ "$K3S_KUBEFLOW" = "true" ]; then
        log "  Katib UI:      kubectl --context ${KUBE_CONTEXT} -n kubeflow port-forward svc/katib-ui 8090:80  → http://localhost:8090/katib/"
    fi
    echo ""
    log "  Pipeline (Katib HPO → Kubeflow TrainJob → eval → gate):"
    log "    curl -X POST -H 'X-API-Key: dev-key-001' http://${ip}:30880/api/v1/training/pipelines \\"
    log "      -d '{\"task_type\":\"locomotion\",\"timesteps\":20000,\"use_katib\":true}'"
    log "  Train:  make k3s-train TASK=locomotion TIMESTEPS=20000"
    log "  Stop:   make k3s-down   (colima stop; data on the MinIO PVC survives)"
}

# --- Main ---
preflight

case "${1:-}" in
    --images)
        build_images
        restart_platform
        ;;
    *)
        start_cluster
        build_images
        install_argo_workflows
        install_kubeflow
        deploy_platform
        print_summary
        ;;
esac
