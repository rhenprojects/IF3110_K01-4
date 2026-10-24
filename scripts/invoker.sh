#!/bin/sh
set -e

PORT="${PORT:-9000}"
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SERVER_DIR="$ROOT_DIR/server"
BIN_PATH="$SERVER_DIR/bin/server"

# ---------------------------------------------------------------------------
# Test database configuration (Docker)
# ---------------------------------------------------------------------------
PG_CONTAINER="${PG_CONTAINER:-if3110-test-pg}"
PG_IMAGE="${PG_IMAGE:-postgres:16-alpine}"
PG_PORT="${PG_PORT:-55432}"
PG_USER="${PG_USER:-postgres}"
PG_PASSWORD="${PG_PASSWORD:-if3110-test-password}"
PG_DB="${PG_DB:-web_backend_test}"
PG_DSN="${TEST_PG_DSN:-postgres://${PG_USER}:${PG_PASSWORD}@127.0.0.1:${PG_PORT}/${PG_DB}?sslmode=disable}"

# Target database used by the connection CRUD API smoke test. Defaults to the
# same PostgreSQL instance the backend uses.
TARGET_HOST="${TARGET_HOST:-127.0.0.1}"
TARGET_PORT="${TARGET_PORT:-$PG_PORT}"
TARGET_DB="${TARGET_DB:-$PG_DB}"
TARGET_USER="${TARGET_USER:-$PG_USER}"
TARGET_PASSWORD="${TARGET_PASSWORD:-$PG_PASSWORD}"

# API smoke-test configuration
API_TEST_PORT="${API_TEST_PORT:-9100}"
API_BASE="http://127.0.0.1:${API_TEST_PORT}/api/v1"
ADMIN_USER="${ADMIN_USERNAME:-admin}"
ADMIN_PASS="${ADMIN_PASSWORD:-admin-password}"
TEST_SESSION_HASH_KEY="${SESSION_HASH_KEY:-test-hash-key-0123456789abcdef0123456789abcdef0123456789abcdef0123456789}"
TEST_SESSION_BLOCK_KEY="${SESSION_BLOCK_KEY:-0123456789abcdef0123456789abcdef}"
TEST_DB_ENC_KEY="${DB_ENC_KEY:-MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=}"

API_SERVER_PID=""
API_TMP_DIR=""

build_server() {
  echo "Building server..."
  mkdir -p "$SERVER_DIR/bin"
  (cd "$SERVER_DIR" && go build -o bin/server app/main.go)
  echo "Built: server/bin/server"
}

db_running() {
  [ "$(docker inspect -f '{{.State.Running}}' "$PG_CONTAINER" 2>/dev/null)" = "true" ]
}

ensure_db() {
  if [ -n "${TEST_PG_DSN:-}" ]; then
    PG_DSN="$TEST_PG_DSN"
    echo "Using external PostgreSQL from TEST_PG_DSN"
    return
  fi

  if ! command -v docker >/dev/null 2>&1; then
    echo "Error: docker is required to start the test database." >&2
    echo "Set TEST_PG_DSN to use an existing PostgreSQL instead." >&2
    exit 1
  fi

  if docker inspect "$PG_CONTAINER" >/dev/null 2>&1; then
    if db_running; then
      echo "PostgreSQL container $PG_CONTAINER already running."
    else
      echo "Starting PostgreSQL container $PG_CONTAINER..."
      docker start "$PG_CONTAINER" >/dev/null
    fi
  else
    echo "Creating PostgreSQL container $PG_CONTAINER on port $PG_PORT..."
    docker run -d --name "$PG_CONTAINER" \
      -e POSTGRES_USER="$PG_USER" \
      -e POSTGRES_PASSWORD="$PG_PASSWORD" \
      -e POSTGRES_DB="$PG_DB" \
      -p "127.0.0.1:${PG_PORT}:5432" \
      "$PG_IMAGE" >/dev/null
  fi

  printf "Waiting for PostgreSQL..."
  COUNT=0
  while ! docker exec "$PG_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; do
    sleep 0.5
    COUNT=$((COUNT + 1))
    if [ "$COUNT" -gt 60 ]; then
      printf "\n"
      echo "Error: PostgreSQL did not become ready in time." >&2
      docker logs "$PG_CONTAINER" 2>&1 | tail -20 >&2
      exit 1
    fi
  done
  printf " ready.\n"

  if ! docker exec -e PGPASSWORD="$PG_PASSWORD" "$PG_CONTAINER" \
    psql -h 127.0.0.1 -U "$PG_USER" -d "$PG_DB" -c 'SELECT 1' >/dev/null 2>&1; then
    echo "Error: container $PG_CONTAINER exists but its credentials/database do not match." >&2
    echo "Run '$0 db-down' and retry to recreate it." >&2
    exit 1
  fi
}

db_down() {
  if docker inspect "$PG_CONTAINER" >/dev/null 2>&1; then
    echo "Removing PostgreSQL container $PG_CONTAINER..."
    docker rm -f "$PG_CONTAINER" >/dev/null
  else
    echo "PostgreSQL container $PG_CONTAINER does not exist."
  fi
}

test_unit() {
  echo "Running unit tests (integration tests are skipped)..."
  (cd "$SERVER_DIR" && TEST_PG_DSN= go test -p 1 ./... -count=1)
}

test_integration() {
  ensure_db
  echo "Running backend <-> database integration tests..."
  echo "TEST_PG_DSN=$PG_DSN"
  (cd "$SERVER_DIR" && TEST_PG_DSN="$PG_DSN" go test -p 1 ./... -count=1)
}

cleanup_api() {
  if [ -n "$API_SERVER_PID" ]; then
    kill "$API_SERVER_PID" 2>/dev/null || true
    wait "$API_SERVER_PID" 2>/dev/null || true
    API_SERVER_PID=""
  fi
  if [ -n "$API_TMP_DIR" ] && [ -d "$API_TMP_DIR" ]; then
    rm -rf "$API_TMP_DIR"
  fi
}

choose_api_port() {
  CANDIDATE="$API_TEST_PORT"
  LIMIT=$((API_TEST_PORT + 100))
  while [ "$CANDIDATE" -lt "$LIMIT" ]; do
    if ! curl -s --max-time 0.3 "http://127.0.0.1:${CANDIDATE}/api/v1/health" >/dev/null 2>&1; then
      API_TEST_PORT="$CANDIDATE"
      API_BASE="http://127.0.0.1:${API_TEST_PORT}/api/v1"
      return
    fi
    CANDIDATE=$((CANDIDATE + 1))
  done
  echo "Error: no free port found for the API test server (starting at $API_TEST_PORT)." >&2
  exit 1
}

start_api_server() {
  echo "Starting backend on port $API_TEST_PORT..."
  PORT="$API_TEST_PORT" \
    DB_DSN="$PG_DSN" \
    ADMIN_USERNAME="$ADMIN_USER" \
    ADMIN_PASSWORD="$ADMIN_PASS" \
    SESSION_HASH_KEY="$TEST_SESSION_HASH_KEY" \
    SESSION_BLOCK_KEY="$TEST_SESSION_BLOCK_KEY" \
    DB_ENC_KEY="$TEST_DB_ENC_KEY" \
    "$BIN_PATH" >"$API_TMP_DIR/server.log" 2>&1 &
  API_SERVER_PID=$!

  COUNT=0
  while ! curl -s --max-time 1 "$API_BASE/health" >/dev/null 2>&1; do
    if ! kill -0 "$API_SERVER_PID" 2>/dev/null; then
      echo "Error: backend exited during startup." >&2
      cat "$API_TMP_DIR/server.log" >&2
      exit 1
    fi
    sleep 0.2
    COUNT=$((COUNT + 1))
    if [ "$COUNT" -gt 75 ]; then
      echo "Error: backend did not become ready in time." >&2
      cat "$API_TMP_DIR/server.log" >&2
      exit 1
    fi
  done
  echo "Backend ready on $API_BASE"
}

API_STATUS=""
API_BODY=""

# api_request METHOD PATH [BODY] [COOKIE_JAR] [CSRF_TOKEN]
api_request() {
  METHOD="$1"
  PATH_SUFFIX="$2"
  BODY="${3:-}"
  JAR="${4:-}"
  CSRF="${5:-}"
  OUT="$API_TMP_DIR/body"

  set -- -sS -o "$OUT" -w '%{http_code}' -X "$METHOD" -H 'Content-Type: application/json'
  if [ -n "$JAR" ]; then
    set -- "$@" -b "$JAR" -c "$JAR"
  fi
  if [ -n "$CSRF" ]; then
    set -- "$@" -H "X-CSRF-Token: $CSRF"
  fi
  if [ -n "$BODY" ]; then
    set -- "$@" -d "$BODY"
  fi

  if API_STATUS=$(curl "$@" "$API_BASE$PATH_SUFFIX"); then
    :
  else
    API_STATUS="000"
  fi
  API_BODY=$(cat "$OUT" 2>/dev/null || true)
}

json_string() {
  printf '%s' "$1" | sed -n "s/.*\"$2\":\"\([^\"]*\)\".*/\1/p" | head -n 1
}

jar_session() {
  [ -f "$1" ] || return 0
  awk '$6 == "session" { value = $7 } END { if (value != "") print value }' "$1"
}

API_PASS=0
API_FAIL=0

pass() {
  API_PASS=$((API_PASS + 1))
  printf "  PASS  %s\n" "$1"
}

fail() {
  API_FAIL=$((API_FAIL + 1))
  printf "  FAIL  %s (expected %s, got %s)\n" "$1" "$2" "$3"
  printf "        body: %s\n" "$4"
}

check_status() {
  DESCRIPTION="$1"
  EXPECTED="$2"
  ACTUAL="$3"
  if [ "$EXPECTED" = "$ACTUAL" ]; then
    pass "$DESCRIPTION"
  else
    fail "$DESCRIPTION" "$EXPECTED" "$ACTUAL" "$API_BODY"
  fi
}

check_contains() {
  DESCRIPTION="$1"
  HAYSTACK="$2"
  NEEDLE="$3"
  if printf '%s' "$HAYSTACK" | grep -qF "$NEEDLE"; then
    pass "$DESCRIPTION"
  else
    fail "$DESCRIPTION" "contains $NEEDLE" "missing" "$HAYSTACK"
  fi
}

check_not_contains() {
  DESCRIPTION="$1"
  HAYSTACK="$2"
  NEEDLE="$3"
  if printf '%s' "$HAYSTACK" | grep -qF "$NEEDLE"; then
    fail "$DESCRIPTION" "does not contain $NEEDLE" "found" "$HAYSTACK"
  else
    pass "$DESCRIPTION"
  fi
}

check_differs() {
  DESCRIPTION="$1"
  BEFORE="$2"
  AFTER="$3"
  if [ -n "$BEFORE" ] && [ -n "$AFTER" ] && [ "$BEFORE" != "$AFTER" ]; then
    pass "$DESCRIPTION"
  else
    fail "$DESCRIPTION" "different values" "before=$BEFORE after=$AFTER" ""
  fi
}

test_api() {
  ensure_db
  build_server
  choose_api_port

  API_TMP_DIR=$(mktemp -d)
  trap cleanup_api EXIT
  trap 'cleanup_api; exit 130' INT TERM

  start_api_server

  # Keep going after a failed assertion so the report is complete.
  set +e

  ADMIN_JAR="$API_TMP_DIR/admin.jar"
  USER_JAR="$API_TMP_DIR/user.jar"
  echo ""
  echo "Running API smoke tests with curl..."

  api_request GET /health
  check_status "GET /health" 200 "$API_STATUS"
  check_contains "health reports database connected" "$API_BODY" '"database":"connected"'

  api_request POST /test '{"message":"ping","port":9000,"status":"connected"}'
  check_status "POST /test" 200 "$API_STATUS"
  check_contains "POST /test echoes payload" "$API_BODY" '"message":"ping"'

  api_request GET /users/profile
  check_status "GET /users/profile without session" 401 "$API_STATUS"

  ADMIN_LOGIN=$(printf '{"username":"%s","password":"%s"}' "$ADMIN_USER" "$ADMIN_PASS")
  api_request POST /users/login "$ADMIN_LOGIN" "$ADMIN_JAR"
  check_status "POST /users/login (admin)" 200 "$API_STATUS"
  check_contains "login response contains admin user" "$API_BODY" "\"username\":\"$ADMIN_USER\""
  check_not_contains "login response hides password" "$API_BODY" "$ADMIN_PASS"
  ADMIN_CSRF=$(json_string "$API_BODY" csrf_token)
  if [ -n "$ADMIN_CSRF" ]; then
    pass "login returns a csrf token"
  else
    fail "login returns a csrf token" "non-empty" "empty" "$API_BODY"
  fi

  ROTATE_BEFORE=$(jar_session "$ADMIN_JAR")
  api_request GET /users/profile "" "$ADMIN_JAR"
  ROTATE_AFTER=$(jar_session "$ADMIN_JAR")
  check_status "GET /users/profile with session" 200 "$API_STATUS"
  check_contains "profile contains admin user" "$API_BODY" "\"username\":\"$ADMIN_USER\""
  check_differs "session token rotates on authenticated request" "$ROTATE_BEFORE" "$ROTATE_AFTER"

  api_request POST /admin/users '{"username":"no-csrf","password":"no-csrf-password"}' "$ADMIN_JAR"
  check_status "POST /admin/users without csrf" 403 "$API_STATUS"

  USER_NAME="apiuser$$"
  USER_PASS="apiuser-password"
  CREATE_USER=$(printf '{"username":"%s","name":"API User","password":"%s"}' "$USER_NAME" "$USER_PASS")

  api_request POST /admin/users "$CREATE_USER" "$ADMIN_JAR" "$ADMIN_CSRF"
  check_status "POST /admin/users as admin" 201 "$API_STATUS"
  check_contains "created user has requested username" "$API_BODY" "\"username\":\"$USER_NAME\""
  check_not_contains "created user response hides password" "$API_BODY" "$USER_PASS"
  USER_ID=$(json_string "$API_BODY" id)

  api_request POST /admin/users "$CREATE_USER" "$ADMIN_JAR" "$ADMIN_CSRF"
  check_status "POST /admin/users duplicate" 409 "$API_STATUS"

  USER_LOGIN=$(printf '{"username":"%s","password":"%s"}' "$USER_NAME" "$USER_PASS")
  api_request POST /users/login "$USER_LOGIN" "$USER_JAR"
  check_status "POST /users/login (regular user)" 200 "$API_STATUS"
  USER_CSRF=$(json_string "$API_BODY" csrf_token)
  USER_ID=$(json_string "$API_BODY" id)

  api_request POST /admin/users "$CREATE_USER" "$USER_JAR" "$USER_CSRF"
  check_status "POST /admin/users as regular user" 403 "$API_STATUS"

  CONN_NAME="api_conn_$$"
  CONN_BODY=$(printf '{"name":"%s","host":"%s","port":%s,"db_name":"%s","username":"%s","password":"%s","ssl_mode":"disable"}' \
    "$CONN_NAME" "$TARGET_HOST" "$TARGET_PORT" "$TARGET_DB" "$TARGET_USER" "$TARGET_PASSWORD")

  api_request GET /admin/connections "" "$USER_JAR"
  check_status "GET /admin/connections as regular user" 403 "$API_STATUS"

  api_request POST /admin/connections "$CONN_BODY" "$ADMIN_JAR" "$ADMIN_CSRF"
  check_status "POST /admin/connections as admin" 201 "$API_STATUS"
  check_contains "created connection has requested name" "$API_BODY" "\"name\":\"$CONN_NAME\""
  check_not_contains "connection response hides target password" "$API_BODY" "$TARGET_PASSWORD"
  CONN_ID=$(json_string "$API_BODY" id)

  if [ -n "$CONN_ID" ]; then
    pass "created connection returns an id"
  else
    fail "created connection returns an id" "uuid" "missing" "$API_BODY"
  fi

  api_request GET /connections "" "$USER_JAR"
  check_status "GET /connections as user without group access" 200 "$API_STATUS"
  check_not_contains "unassigned connection is not visible" "$API_BODY" "$CONN_NAME"

  GROUP_NAME="api_group_$$"
  GROUP_BODY=$(printf '{"name":"%s","description":"API group"}' "$GROUP_NAME")
  api_request POST /admin/groups "$GROUP_BODY" "$ADMIN_JAR" "$ADMIN_CSRF"
  check_status "POST /admin/groups as admin" 201 "$API_STATUS"
  GROUP_ID=$(json_string "$API_BODY" id)
  if [ -n "$GROUP_ID" ]; then
    pass "created group returns an id"
  else
    fail "created group returns an id" "uuid" "missing" "$API_BODY"
  fi

  if [ -n "$GROUP_ID" ]; then
    api_request GET "/admin/groups/$GROUP_ID" "" "$ADMIN_JAR"
    check_status "GET /admin/groups/:id" 200 "$API_STATUS"

    MEMBER_BODY=$(printf '{"user_id":"%s"}' "$USER_ID")
    api_request POST "/admin/groups/$GROUP_ID/members" "$MEMBER_BODY" "$ADMIN_JAR" "$ADMIN_CSRF"
    check_status "POST /admin/groups/:id/members" 201 "$API_STATUS"

    api_request GET "/admin/groups/$GROUP_ID/members" "" "$ADMIN_JAR"
    check_status "GET /admin/groups/:id/members" 200 "$API_STATUS"
    check_contains "member list contains user" "$API_BODY" "\"username\":\"$USER_NAME\""

    if [ -n "$CONN_ID" ]; then
      api_request PUT "/admin/groups/$GROUP_ID/connections/$CONN_ID" '{"level":"view"}' "$ADMIN_JAR" "$ADMIN_CSRF"
      check_status "PUT group access level view" 200 "$API_STATUS"

      api_request GET /connections "" "$USER_JAR"
      check_status "GET /connections with view access" 200 "$API_STATUS"
      check_contains "granted connection is visible" "$API_BODY" "$CONN_NAME"

      api_request GET "/connections/$CONN_ID" "" "$USER_JAR"
      check_status "GET /connections/:id with view access" 200 "$API_STATUS"

      api_request PUT "/connections/$CONN_ID" "$CONN_BODY" "$USER_JAR" "$USER_CSRF"
      check_status "PUT /connections/:id with view only" 404 "$API_STATUS"

      api_request PUT "/admin/groups/$GROUP_ID/connections/$CONN_ID" '{"level":"edit"}' "$ADMIN_JAR" "$ADMIN_CSRF"
      check_status "PUT group access level edit" 200 "$API_STATUS"

      USER_UPDATE_CONN=$(printf '{"name":"%s-user","host":"%s","port":%s,"db_name":"%s","username":"%s","ssl_mode":"disable"}' \
        "$CONN_NAME" "$TARGET_HOST" "$TARGET_PORT" "$TARGET_DB" "$TARGET_USER")
      api_request PUT "/connections/$CONN_ID" "$USER_UPDATE_CONN" "$USER_JAR" "$USER_CSRF"
      check_status "PUT /connections/:id with edit access" 200 "$API_STATUS"

      api_request POST "/connections/$CONN_ID/test" "" "$USER_JAR" "$USER_CSRF"
      check_status "POST /connections/:id/test with view access" 200 "$API_STATUS"
      check_contains "connection test returns server version" "$API_BODY" '"server_version"'
    fi

    api_request DELETE "/admin/groups/$GROUP_ID/members/$USER_ID" "" "$ADMIN_JAR" "$ADMIN_CSRF"
    check_status "DELETE group member" 204 "$API_STATUS"

    if [ -n "$CONN_ID" ]; then
      api_request GET "/connections/$CONN_ID" "" "$USER_JAR"
      check_status "GET connection after membership removal" 404 "$API_STATUS"

      api_request DELETE "/admin/groups/$GROUP_ID/connections/$CONN_ID" "" "$ADMIN_JAR" "$ADMIN_CSRF"
      check_status "DELETE group access" 204 "$API_STATUS"
    fi

    api_request DELETE "/admin/groups/$GROUP_ID" "" "$ADMIN_JAR" "$ADMIN_CSRF"
    check_status "DELETE group" 204 "$API_STATUS"
  fi

  if [ -n "$CONN_ID" ]; then
    api_request GET "/admin/connections/$CONN_ID" "" "$ADMIN_JAR"
    check_status "GET /admin/connections/:id" 200 "$API_STATUS"

    api_request POST "/admin/connections/$CONN_ID/test" "" "$ADMIN_JAR" "$ADMIN_CSRF"
    check_status "POST /admin/connections/:id/test" 200 "$API_STATUS"

    api_request DELETE "/admin/connections/$CONN_ID" "" "$ADMIN_JAR" "$ADMIN_CSRF"
    check_status "DELETE /admin/connections/:id" 204 "$API_STATUS"

    api_request GET "/admin/connections/$CONN_ID" "" "$ADMIN_JAR"
    check_status "GET deleted connection" 404 "$API_STATUS"
  fi

  api_request GET /admin/users "" "$ADMIN_JAR"
  check_status "GET /admin/users" 200 "$API_STATUS"
  check_contains "user list contains created user" "$API_BODY" "\"username\":\"$USER_NAME\""

  if [ -n "$USER_ID" ]; then
    api_request GET "/admin/users/$USER_ID" "" "$ADMIN_JAR"
    check_status "GET /admin/users/:id" 200 "$API_STATUS"

    api_request PUT "/admin/users/$USER_ID" '{"name":"API User Updated"}' "$ADMIN_JAR" "$ADMIN_CSRF"
    check_status "PUT /admin/users/:id" 200 "$API_STATUS"
    check_contains "updated user has new name" "$API_BODY" '"name":"API User Updated"'
  fi

  api_request POST /users/logout "" "$USER_JAR" "$USER_CSRF"
  check_status "POST /users/logout (regular user)" 204 "$API_STATUS"
  api_request GET /users/profile "" "$USER_JAR"
  check_status "profile after logout" 401 "$API_STATUS"

  if [ -n "$USER_ID" ]; then
    api_request DELETE "/admin/users/$USER_ID" "" "$ADMIN_JAR" "$ADMIN_CSRF"
    check_status "DELETE /admin/users/:id" 204 "$API_STATUS"
    api_request GET "/admin/users/$USER_ID" "" "$ADMIN_JAR"
    check_status "GET deleted user" 404 "$API_STATUS"
  fi

  api_request POST /users/logout "" "$ADMIN_JAR" "$ADMIN_CSRF"
  check_status "POST /users/logout (admin)" 204 "$API_STATUS"
  api_request GET /users/profile "" "$ADMIN_JAR"
  check_status "profile after admin logout" 401 "$API_STATUS"

  printf "\nAPI smoke tests: %d passed, %d failed\n" "$API_PASS" "$API_FAIL"

  set -e
  if [ "$API_FAIL" -gt 0 ]; then
    echo "Server log: $API_TMP_DIR/server.log" >&2
    exit 1
  fi
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

  test)
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

  test-unit)
    test_unit
    ;;

  test-integration)
    test_integration
    ;;

  test-api)
    test_api
    ;;

  test-all)
    test_unit
    test_integration
    test_api
    ;;

  db-up)
    ensure_db
    ;;

  db-down)
    db_down
    ;;

  clean)
    rm -rf "$SERVER_DIR/bin"
    echo "Cleaned server/bin"
    ;;

  *)
    echo "Usage: $0 {build|run|dev|test [json]|test-unit|test-integration|test-api|test-all|db-up|db-down|clean}"
    echo ""
    echo "  test               Smoke-test the /api/v1/test echo endpoint (legacy)."
    echo "  test-unit          Run unit tests only."
    echo "  test-integration   Start PostgreSQL (Docker), then run backend <-> DB integration tests."
    echo "  test-api           Start PostgreSQL + backend, then exercise the API with curl."
    echo "  test-all           Run test-unit, test-integration, and test-api."
    echo "  db-up / db-down    Start / remove the disposable PostgreSQL test container."
    echo ""
    echo "Environment overrides:"
    echo "  TEST_PG_DSN        Use an existing PostgreSQL instead of Docker."
    echo "  PG_PORT, PG_USER, PG_PASSWORD, PG_DB, PG_CONTAINER, PG_IMAGE"
    echo "  API_TEST_PORT, ADMIN_USERNAME, ADMIN_PASSWORD"
    echo "  TARGET_HOST, TARGET_PORT, TARGET_DB, TARGET_USER, TARGET_PASSWORD"
    exit 1
    ;;
esac
