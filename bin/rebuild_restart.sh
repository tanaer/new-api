#!/bin/bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WEB_DIR="$ROOT_DIR/web"
LOG_DIR="$ROOT_DIR/logs"
PID_FILE="$ROOT_DIR/.new-api.pid"
GO_BUILD_CACHE=${GO_BUILD_CACHE:-$ROOT_DIR/.gocache-temp}

PORT=${PORT:-3333}
BUILD_FRONTEND=1
BUILD_BACKEND=1
START_SERVER=1
USE_BUN_INSTALL=0

usage() {
  cat <<USAGE
Usage: $(basename "$0") [options]

Rebuild and restart new-api from source.

Options:
  --port <port>         Port to start new-api on (default: 3333)
  --log-dir <dir>       Log directory (default: $ROOT_DIR/logs)
  --skip-frontend       Skip frontend build
  --skip-backend        Skip backend build
  --build-only          Build only, do not start server
  --bun-install         Run bun install before frontend build
  -h, --help            Show this help message

Environment:
  PORT=<port>           Same as --port
  GO_BUILD_CACHE=<dir>  Go build cache directory
USAGE
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --port)
      PORT="$2"
      shift 2
      ;;
    --log-dir)
      LOG_DIR="$2"
      shift 2
      ;;
    --skip-frontend)
      BUILD_FRONTEND=0
      shift
      ;;
    --skip-backend)
      BUILD_BACKEND=0
      shift
      ;;
    --build-only)
      START_SERVER=0
      shift
      ;;
    --bun-install)
      USE_BUN_INSTALL=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

timestamp() {
  date '+%Y%m%d%H%M%S'
}

kill_running_processes() {
  local pids=""
  pids=$(ps -eo pid=,args= | awk -v root="$ROOT_DIR/new-api" '$0 ~ root && $0 !~ /awk/ {print $1}')

  if [[ -f "$PID_FILE" ]]; then
    local stored_pid
    stored_pid=$(cat "$PID_FILE" 2>/dev/null || true)
    if [[ -n "$stored_pid" ]] && kill -0 "$stored_pid" 2>/dev/null; then
      pids=$(printf '%s\n%s\n' "$pids" "$stored_pid" | awk 'NF' | sort -u)
    fi
  fi

  if [[ -z "$pids" ]]; then
    echo "No running new-api process found."
    return
  fi

  echo "Stopping existing new-api process(es): $(echo "$pids" | xargs)"
  while read -r pid; do
    [[ -z "$pid" ]] && continue
    kill "$pid" 2>/dev/null || true
  done <<< "$pids"

  sleep 2

  while read -r pid; do
    [[ -z "$pid" ]] && continue
    if kill -0 "$pid" 2>/dev/null; then
      echo "Force killing PID $pid"
      kill -9 "$pid" 2>/dev/null || true
    fi
  done <<< "$pids"
}

build_frontend() {
  require_command bun
  echo "Building frontend..."
  cd "$WEB_DIR"
  if [[ "$USE_BUN_INSTALL" -eq 1 ]]; then
    bun install
  fi
  DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION=$(cat "$ROOT_DIR/VERSION") bun run build
}

build_backend() {
  require_command go
  echo "Building backend..."
  mkdir -p "$GO_BUILD_CACHE"
  cd "$ROOT_DIR"
  GOCACHE="$GO_BUILD_CACHE" go build -buildvcs=false -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=$(cat VERSION)'" -o new-api
}

start_server() {
  mkdir -p "$LOG_DIR"
  local restart_log="$LOG_DIR/new-api-restart-$(timestamp).log"

  echo "Starting new-api on port $PORT..."
  cd "$ROOT_DIR"
  nohup "$ROOT_DIR/new-api" --port "$PORT" --log-dir "$LOG_DIR" > "$restart_log" 2>&1 &
  local new_pid=$!
  echo "$new_pid" > "$PID_FILE"

  sleep 2
  if kill -0 "$new_pid" 2>/dev/null; then
    echo "new-api started successfully."
    echo "PID: $new_pid"
    echo "Port: $PORT"
    echo "Restart log: $restart_log"
  else
    echo "new-api failed to start. Check log: $restart_log" >&2
    exit 1
  fi
}

main() {
  mkdir -p "$LOG_DIR"

  kill_running_processes

  if [[ "$BUILD_FRONTEND" -eq 1 ]]; then
    build_frontend
  fi

  if [[ "$BUILD_BACKEND" -eq 1 ]]; then
    build_backend
  fi

  if [[ "$START_SERVER" -eq 1 ]]; then
    start_server
  else
    echo "Build finished. Start skipped because --build-only was used."
  fi
}

main
