#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  echo "Usage: $0 [up|down|restart|logs|ps] [--platform] [--playground]"
  echo ""
  echo "Commands:"
  echo "  up        Start services (default)"
  echo "  down      Stop services"
  echo "  restart   Restart services"
  echo "  logs      Tail logs"
  echo "  ps        Show running services"
  echo ""
  echo "Flags:"
  echo "  --platform    Only platform stack"
  echo "  --playground  Only playground stack"
  echo "  (no flag)     Both stacks"
  exit 1
}

CMD="up"
PLATFORM=false
PLAYGROUND=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    up|down|restart|logs|ps) CMD="$1" ;;
    --platform)   PLATFORM=true ;;
    --playground) PLAYGROUND=true ;;
    -h|--help)    usage ;;
    *) echo "Unknown option: $1"; usage ;;
  esac
  shift
done

# Default: both stacks
if ! $PLATFORM && ! $PLAYGROUND; then
  PLATFORM=true
  PLAYGROUND=true
fi

run_compose() {
  local dir="$1"
  local name="$2"
  shift 2

  echo "==> $name: docker compose $*"
  docker compose -f "$dir/docker-compose.yml" "$@"
}

case "$CMD" in
  up)
    $PLATFORM   && run_compose "$SCRIPT_DIR/platform"   "Platform"   up -d
    $PLAYGROUND && run_compose "$SCRIPT_DIR/playground"  "Playground" up -d
    ;;
  down)
    $PLAYGROUND && run_compose "$SCRIPT_DIR/playground"  "Playground" down
    $PLATFORM   && run_compose "$SCRIPT_DIR/platform"    "Platform"   down
    ;;
  restart)
    $PLAYGROUND && run_compose "$SCRIPT_DIR/playground"  "Playground" down
    $PLATFORM   && run_compose "$SCRIPT_DIR/platform"    "Platform"   down
    $PLATFORM   && run_compose "$SCRIPT_DIR/platform"    "Platform"   up -d
    $PLAYGROUND && run_compose "$SCRIPT_DIR/playground"   "Playground" up -d
    ;;
  logs)
    if $PLATFORM && $PLAYGROUND; then
      docker compose -f "$SCRIPT_DIR/platform/docker-compose.yml" -f "$SCRIPT_DIR/playground/docker-compose.yml" logs -f
    elif $PLATFORM; then
      run_compose "$SCRIPT_DIR/platform" "Platform" logs -f
    else
      run_compose "$SCRIPT_DIR/playground" "Playground" logs -f
    fi
    ;;
  ps)
    $PLATFORM   && run_compose "$SCRIPT_DIR/platform"   "Platform"   ps
    $PLAYGROUND && run_compose "$SCRIPT_DIR/playground"  "Playground" ps
    ;;
esac
