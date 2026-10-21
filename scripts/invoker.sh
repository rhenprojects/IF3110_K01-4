#!/bin/sh
set -e

PORT="${PORT:-9000}"
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SERVER_DIR="$ROOT_DIR/server"
BIN_PATH="$SERVER_DIR/bin/server"

build_server() {
  echo "Building server..."
  mkdir -p "$SERVER_DIR/bin"
  (cd "$SERVER_DIR" && go build -o bin/server app/main.go)
  echo "Built: server/bin/server"
}

case "$1" in
  build)
    build_server
    ;;

  run)
    [ -f "$BIN_PATH" ] || build_server
    echo "Running server on port $PORT..."
    PORT="$PORT" "$BIN_PATH"
    ;;

  dev)
    echo "Starting dev server on port $PORT..."
    (cd "$SERVER_DIR" && PORT="$PORT" go run app/main.go)
    ;;

  test|test-conn)
    PAYLOAD="$2"
    if [ -z "$PAYLOAD" ] || [ "$PAYLOAD" = "json" ]; then
      PAYLOAD='{"message": "ping", "port": 9000, "status": "connected"}'
    fi

    # Check if server is already running on port
    WAS_RUNNING=1
    if ! curl -s "http://localhost:$PORT/api/v1/health" >/dev/null 2>&1; then
      WAS_RUNNING=0
      echo "Server is not running on port $PORT. Starting server..."
      [ -f "$BIN_PATH" ] || build_server
      PORT="$PORT" "$BIN_PATH" > /tmp/server_test.log 2>&1 &
      SERVER_PID=$!

      # Wait for server to become ready (up to 5s)
      COUNT=0
      while ! curl -s "http://localhost:$PORT/api/v1/health" >/dev/null 2>&1; do
        sleep 0.2
        COUNT=$((COUNT + 1))
        if [ "$COUNT" -gt 25 ]; then
          echo "Error: Server failed to start on port $PORT."
          cat /tmp/server_test.log
          kill "$SERVER_PID" 2>/dev/null || true
          exit 1
        fi
      done
      echo "Server ready on port $PORT (PID: $SERVER_PID)."
    fi

    echo "Testing connection to port $PORT with JSON..."
    echo "Sending payload: $PAYLOAD"
    curl -i -X POST "http://localhost:$PORT/api/v1/test" \
      -H "Content-Type: application/json" \
      -d "$PAYLOAD"
    echo ""

    if [ "$WAS_RUNNING" -eq 0 ]; then
      echo "Stopping server (PID: $SERVER_PID)..."
      kill "$SERVER_PID" 2>/dev/null || true
      wait "$SERVER_PID" 2>/dev/null || true
      echo "Stopped."
    fi
    ;;

  clean)
    rm -rf "$SERVER_DIR/bin"
    echo "Cleaned server/bin"
    ;;

  *)
    echo "Usage: $0 {build|run|dev|test [json]|clean}"
    exit 1
    ;;
esac
