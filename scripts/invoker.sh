#!/bin/sh
# Backend/frontend invoker: build, run, unit/integration tests, contract smoke
# tests, and an end-to-end test through the Vite proxy.
set -e

PORT="${PORT:-9000}"
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SERVER_DIR="$ROOT_DIR/server"
FRONTEND_DIR="$ROOT_DIR/frontend"
BIN_PATH="$SERVER_DIR/bin/server"

PG_CONTAINER="${PG_CONTAINER:-if3110-test-pg}"
PG_IMAGE="${PG_IMAGE:-postgres:16-alpine}"
PG_PORT="${PG_PORT:-55432}"
PG_USER="${PG_USER:-postgres}"
PG_PASSWORD="${PG_PASSWORD:-if3110-test-password}"
PG_DB="${PG_DB:-web_backend_test}"
PG_DSN="${TEST_PG_DSN:-postgres://${PG_USER}:${PG_PASSWORD}@127.0.0.1:${PG_PORT}/${PG_DB}?sslmode=disable}"

TARGET_HOST="${TARGET_HOST:-127.0.0.1}"
TARGET_PORT="${TARGET_PORT:-$PG_PORT}"
TARGET_DB="${TARGET_DB:-$PG_DB}"
TARGET_USER="${TARGET_USER:-$PG_USER}"
TARGET_PASSWORD="${TARGET_PASSWORD:-$PG_PASSWORD}"

API_TEST_PORT="${API_TEST_PORT:-9100}"
FRONTEND_TEST_PORT="${FRONTEND_TEST_PORT:-5173}"
API_BASE="http://127.0.0.1:${API_TEST_PORT}/api/v1"
ADMIN_USER="${ADMIN_USERNAME:-admin}"
ADMIN_PASS="${ADMIN_PASSWORD:-admin-password}"

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

SERVER_PID=""
FRONTEND_PID=""
TMP_DIR=""

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
    echo "Using external PostgreSQL from TEST_PG_DSN."
    return
  fi

  command -v docker >/dev/null 2>&1 || {
    echo "Error: docker is required, or set TEST_PG_DSN." >&2
    exit 1
  }

  if docker inspect "$PG_CONTAINER" >/dev/null 2>&1; then
    db_running || docker start "$PG_CONTAINER" >/dev/null
  else
    docker run -d --name "$PG_CONTAINER" \
      -e POSTGRES_USER="$PG_USER" \
      -e POSTGRES_PASSWORD="$PG_PASSWORD" \
      -e POSTGRES_DB="$PG_DB" \
      -p "127.0.0.1:${PG_PORT}:5432" \
      "$PG_IMAGE" >/dev/null
  fi

  printf "Waiting for PostgreSQL..."
  i=0
  while ! docker exec "$PG_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; do
    sleep 0.5
    i=$((i + 1))
    [ "$i" -le 60 ] || {
      printf "\n"
      echo "Error: PostgreSQL did not become ready." >&2
      exit 1
    }
  done
  printf " ready.\n"
}

db_down() {
  if docker inspect "$PG_CONTAINER" >/dev/null 2>&1; then
    docker rm -f "$PG_CONTAINER" >/dev/null
    echo "Removed $PG_CONTAINER."
  else
    echo "Container $PG_CONTAINER does not exist."
  fi
}

test_unit() {
  echo "Running unit tests..."
  (cd "$SERVER_DIR" && TEST_PG_DSN= go test -p 1 ./... -count=1)
}

test_integration() {
  ensure_db
  echo "Running integration tests..."
  (cd "$SERVER_DIR" && TEST_PG_DSN="$PG_DSN" go test -p 1 ./... -count=1)
}

test_auth() {
  ensure_db
  echo "Running auth and security tests..."
  (cd "$SERVER_DIR" && TEST_PG_DSN="$PG_DSN" go test -p 1 \
    ./internal/authctx ./internal/authz ./internal/user_handler ./internal/group_handler -count=1)
}

test_db() {
  ensure_db
  echo "Running target database tests..."
  (cd "$SERVER_DIR" && TEST_PG_DSN="$PG_DSN" go test -p 1 ./internal/targetdb ./internal/database_handler -count=1)
}

ensure_frontend_deps() {
  if [ ! -d "$FRONTEND_DIR/node_modules" ]; then
    echo "Installing frontend dependencies..."
    (cd "$FRONTEND_DIR" && npm ci)
  fi
}

test_frontend() {
  ensure_frontend_deps
  echo "Checking frontend types and build..."
  (cd "$FRONTEND_DIR" && npm run typecheck && npm run build)
  echo "Frontend typecheck and build passed."
}

cleanup() {
  if [ -n "$FRONTEND_PID" ]; then
    kill "$FRONTEND_PID" 2>/dev/null || true
    wait "$FRONTEND_PID" 2>/dev/null || true
    FRONTEND_PID=""
  fi
  if [ -n "$SERVER_PID" ]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    SERVER_PID=""
  fi
  if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}

start_api_server() {
  if curl -s --max-time 0.5 "$API_BASE/health" >/dev/null 2>&1; then
    echo "Error: port $API_TEST_PORT is already in use; set API_TEST_PORT." >&2
    exit 1
  fi

  PORT="$API_TEST_PORT" \
    DB_DSN="$PG_DSN" \
    ADMIN_USERNAME="$ADMIN_USER" \
    ADMIN_PASSWORD="$ADMIN_PASS" \
    SESSION_HASH_KEY="test-hash-key-0123456789abcdef0123456789abcdef0123456789abcdef0123456789" \
    SESSION_BLOCK_KEY="0123456789abcdef0123456789abcdef" \
    DB_ENC_KEY="MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" \
    "$BIN_PATH" >"$TMP_DIR/server.log" 2>&1 &
  SERVER_PID=$!

  i=0
  while ! curl -s --max-time 1 "$API_BASE/health" >/dev/null 2>&1; do
    kill -0 "$SERVER_PID" 2>/dev/null || {
      echo "Error: backend exited during startup." >&2
      cat "$TMP_DIR/server.log" >&2
      exit 1
    }
    sleep 0.2
    i=$((i + 1))
    [ "$i" -le 75 ] || {
      echo "Error: backend did not become ready." >&2
      cat "$TMP_DIR/server.log" >&2
      exit 1
    }
  done
  echo "Backend ready on $API_BASE"
}

start_frontend_server() {
  ensure_frontend_deps
  if curl -s --max-time 0.5 "http://127.0.0.1:${FRONTEND_TEST_PORT}/" >/dev/null 2>&1; then
    echo "Error: port $FRONTEND_TEST_PORT is already in use; set FRONTEND_TEST_PORT." >&2
    exit 1
  fi

  (cd "$FRONTEND_DIR" && \
    VITE_API_TARGET="http://127.0.0.1:${API_TEST_PORT}" \
    npm run dev -- --host 127.0.0.1 --port "$FRONTEND_TEST_PORT") \
    >"$TMP_DIR/frontend.log" 2>&1 &
  FRONTEND_PID=$!

  i=0
  while ! curl -s --max-time 1 "http://127.0.0.1:${FRONTEND_TEST_PORT}/" >/dev/null 2>&1; do
    kill -0 "$FRONTEND_PID" 2>/dev/null || {
      echo "Error: frontend dev server exited during startup." >&2
      cat "$TMP_DIR/frontend.log" >&2
      exit 1
    }
    sleep 0.3
    i=$((i + 1))
    [ "$i" -le 100 ] || {
      echo "Error: frontend dev server did not become ready." >&2
      cat "$TMP_DIR/frontend.log" >&2
      exit 1
    }
  done
  echo "Frontend ready on http://127.0.0.1:${FRONTEND_TEST_PORT} (API via Vite proxy)"
}

STATUS=""
BODY=""

request() {
  METHOD="$1"
  PATH_SUFFIX="$2"
  PAYLOAD="${3:-}"
  JAR="${4:-}"
  CSRF="${5:-}"
  OUT="$TMP_DIR/body"

  set -- -sS -o "$OUT" -w '%{http_code}' -X "$METHOD" -H 'Content-Type: application/json'
  [ -n "$JAR" ] && set -- "$@" -b "$JAR" -c "$JAR"
  [ -n "$CSRF" ] && set -- "$@" -H "X-CSRF-Token: $CSRF"
  [ -n "$PAYLOAD" ] && set -- "$@" -d "$PAYLOAD"

  if STATUS=$(curl "$@" "$API_BASE$PATH_SUFFIX"); then
    BODY=$(cat "$OUT" 2>/dev/null || true)
  else
    STATUS="000"
    BODY=""
  fi
}

ok() {
  printf "%bPASS%b %s\n" "$GREEN" "$NC" "$1"
}

fail() {
  printf "%bFAIL%b %s\n" "$RED" "$NC" "$1" >&2
  [ -n "$2" ] && printf "  Expected: %s\n" "$2" >&2
  [ -n "$BODY" ] && printf "  Body: %.600s\n" "$BODY" >&2
  exit 1
}

assert_status() {
  if [ "$STATUS" = "$1" ]; then ok "$2"; else fail "$2" "HTTP $1, got $STATUS"; fi
}

assert_contains() {
  case "$1" in
    *"$2"*) ok "$3" ;;
    *) fail "$3" "missing: $2" ;;
  esac
}

assert_not_contains() {
  case "$1" in
    *"$2"*) fail "$3" "unexpected: $2" ;;
    *) ok "$3" ;;
  esac
}

json_string() {
  printf '%s' "$1" | sed -n "s/.*\"$2\":\"\\([^\"]*\\)\".*/\\1/p" | head -n 1
}

cookie_value() {
  awk -v name="$2" '$6 == name { value = $7 } END { print value }' "$1"
}

section() {
  printf "\n%b[%s]%b\n" "$YELLOW" "$1" "$NC"
}

run_contract_tests() {
  ADMIN_JAR="$TMP_DIR/admin.jar"
  USER_JAR="$TMP_DIR/user.jar"
  SUFFIX="$$"
  SMOKE_TABLE="api_smoke_${SUFFIX}"

  section "Health and echo"
  request GET /health
  assert_status 200 "GET /health"
  assert_contains "$BODY" '"status":"ok"' "health status"
  assert_contains "$BODY" '"database":"connected"' "database connected"

  request POST /test '{"message":"ping"}'
  assert_status 200 "POST /test"
  assert_contains "$BODY" '"message":"ping"' "echo payload"

  section "Authentication and CSRF"
  request GET /users/profile
  assert_status 401 "profile without session"

  request POST /users/login "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" "$ADMIN_JAR"
  assert_status 200 "admin login"
  assert_contains "$BODY" "\"username\":\"$ADMIN_USER\"" "admin identity"
  assert_not_contains "$BODY" "$ADMIN_PASS" "password hidden"
  assert_not_contains "$BODY" "csrf_token" "csrf token not exposed in login body"
  assert_not_contains "$BODY" "expires_at" "session expiry not exposed in login body"
  ADMIN_ID=$(json_string "$BODY" id)
  ADMIN_CSRF=$(cookie_value "$ADMIN_JAR" csrf_token)
  [ -n "$ADMIN_CSRF" ] || fail "csrf_token cookie set"
  ok "csrf_token cookie set"

  request GET /users/profile "" "$ADMIN_JAR"
  assert_status 200 "profile with session"
  assert_contains "$BODY" "\"username\":\"$ADMIN_USER\"" "profile identity"

  request GET /users/csrf "" "$ADMIN_JAR"
  assert_status 404 "csrf token endpoint not exposed"

  section "Users"
  USER_NAME="apiuser$$"
  USER_PASS="apiuser-password"
  CREATE_USER="{\"username\":\"$USER_NAME\",\"name\":\"API User\",\"password\":\"$USER_PASS\"}"

  request POST /admin/users "$CREATE_USER" "$ADMIN_JAR"
  assert_status 403 "create user without CSRF is rejected"

  request POST /admin/users "$CREATE_USER" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 201 "create user"
  assert_contains "$BODY" "\"username\":\"$USER_NAME\"" "created username"
  assert_not_contains "$BODY" "$USER_PASS" "created password hidden"
  USER_ID=$(json_string "$BODY" id)

  request POST /admin/users "$CREATE_USER" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 409 "duplicate username"

  request POST /users/login "{\"username\":\"$USER_NAME\",\"password\":\"$USER_PASS\"}" "$USER_JAR"
  assert_status 200 "regular user login"
  assert_not_contains "$BODY" "$USER_PASS" "login password hidden"
  USER_CSRF=$(cookie_value "$USER_JAR" csrf_token)
  [ -n "$USER_CSRF" ] || fail "user csrf_token cookie set"
  ok "user csrf_token cookie set"
  USER_ID=$(json_string "$BODY" id)

  request POST /admin/users "$CREATE_USER" "$USER_JAR" "$USER_CSRF"
  assert_status 403 "regular user cannot create users"

  request PUT "/admin/users/$ADMIN_ID" '{"is_admin":false}' "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 409 "admin cannot revoke own admin status"

  section "Connections (admin)"
  CONN_NAME="api_conn_$$"
  CONN_BODY="{\"name\":\"$CONN_NAME\",\"host\":\"$TARGET_HOST\",\"port\":$TARGET_PORT,\"db_name\":\"$TARGET_DB\",\"username\":\"$TARGET_USER\",\"password\":\"$TARGET_PASSWORD\",\"ssl_mode\":\"disable\"}"

  request POST /admin/connections "$CONN_BODY" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 201 "create connection"
  assert_not_contains "$BODY" "$TARGET_PASSWORD" "connection password hidden"
  CONN_ID=$(json_string "$BODY" id)

  request POST /admin/connections "$CONN_BODY" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 409 "duplicate connection name"

  request POST /admin/connections/test "$CONN_BODY" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 200 "test unsaved connection"
  assert_contains "$BODY" '"server_version"' "test returns server version"

  request POST "/admin/connections/$CONN_ID/test" "" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 200 "test saved connection"

  request GET /admin/connections "" "$ADMIN_JAR"
  assert_status 200 "admin lists connections"
  assert_contains "$BODY" "$CONN_NAME" "connection in admin list"

  section "Groups (contract)"
  GROUP_NAME="api_group_$$"
  request POST /admin/groups "{\"name\":\"$GROUP_NAME\",\"description\":\"API group\"}" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 201 "create group"
  assert_contains "$BODY" '"member_ids":[]' "new group has empty member_ids"
  GROUP_ID=$(json_string "$BODY" id)

  request PUT "/admin/groups/$GROUP_ID/members" "{\"user_ids\":[\"$USER_ID\"]}" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 200 "replace group members"
  assert_contains "$BODY" "$USER_ID" "group response includes member id"

  request PUT "/admin/groups/$GROUP_ID/members" '{"user_ids":["00000000-0000-0000-0000-000000000000"]}' "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 400 "unknown member id rejected"

  request GET "/admin/groups/$GROUP_ID/access" "" "$ADMIN_JAR"
  assert_status 200 "list group access"
  assert_contains "$BODY" '"access":[]' "group has no access initially"

  request PUT "/admin/groups/$GROUP_ID/access" "{\"access\":[{\"connection_id\":\"$CONN_ID\",\"level\":\"view\"}]}" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 200 "replace group access with view"
  assert_contains "$BODY" '"level":"view"' "view access returned"

  request PUT "/admin/groups/$GROUP_ID/access" "{\"access\":[{\"connection_id\":\"$CONN_ID\",\"level\":\"admin\"}]}" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 400 "invalid access level rejected"

  section "Database access and viewer (user)"
  request GET /databases "" "$USER_JAR"
  assert_status 200 "user lists databases"
  assert_contains "$BODY" "$CONN_NAME" "database visible with view access"
  assert_contains "$BODY" '"level":"view"' "effective level is view"

  request GET "/databases/$CONN_ID" "" "$USER_JAR"
  assert_status 200 "user gets database detail"
  assert_not_contains "$BODY" '"host"' "user response hides host"

  request GET "/databases/$CONN_ID/schema" "" "$USER_JAR"
  assert_status 200 "user reads schema"
  assert_contains "$BODY" '"schemas"' "schema response present"

  request POST "/databases/$CONN_ID/migrations" "{\"name\":\"blocked\",\"sql\":\"SELECT 1;\"}" "$USER_JAR" "$USER_CSRF"
  assert_status 403 "view cannot create migration"

  request PUT "/admin/groups/$GROUP_ID/access" "{\"access\":[{\"connection_id\":\"$CONN_ID\",\"level\":\"edit\"}]}" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 200 "upgrade group access to edit"

  request GET /databases "" "$USER_JAR"
  assert_status 200 "user lists databases after edit grant"
  assert_contains "$BODY" '"level":"edit"' "effective level is edit"

  section "Migrations"
  CREATE_SQL="CREATE TABLE $SMOKE_TABLE (id serial PRIMARY KEY, note text); CREATE INDEX ${SMOKE_TABLE}_note_idx ON $SMOKE_TABLE (note);"
  CREATE_MIGRATION="{\"name\":\"create $SMOKE_TABLE\",\"sql\":\"$CREATE_SQL\"}"
  request POST "/databases/$CONN_ID/migrations" "$CREATE_MIGRATION" "$USER_JAR" "$USER_CSRF"
  assert_status 201 "create migration draft"
  assert_contains "$BODY" '"status":"draft"' "draft status"
  assert_contains "$BODY" "\"created_by_name\":\"API User\"" "created_by_name included"
  MIG_ID=$(json_string "$BODY" id)

  request PUT "/databases/$CONN_ID/migrations/$MIG_ID" "{\"name\":\"create $SMOKE_TABLE\",\"sql\":\"$CREATE_SQL\"}" "$USER_JAR" "$USER_CSRF"
  assert_status 200 "update migration draft"

  request POST "/databases/$CONN_ID/migrations/$MIG_ID/execute" "" "$USER_JAR" "$USER_CSRF"
  assert_status 200 "execute migration"
  assert_contains "$BODY" '"status":"success"' "migration succeeded"
  assert_contains "$BODY" '"version":1' "first version is 1"
  assert_contains "$BODY" "\"executed_by_name\":\"API User\"" "executed_by_name included"

  request POST "/databases/$CONN_ID/migrations/$MIG_ID/execute" "" "$USER_JAR" "$USER_CSRF"
  assert_status 409 "executed migration cannot run again"

  request GET "/databases/$CONN_ID/schema" "" "$USER_JAR"
  assert_status 200 "schema after migration"
  assert_contains "$BODY" "\"name\":\"$SMOKE_TABLE\"" "new table appears in schema"

  request GET "/databases/$CONN_ID/tables/public/$SMOKE_TABLE/rows?limit=50&offset=0" "" "$USER_JAR"
  assert_status 200 "read rows of new table"
  assert_contains "$BODY" '"total":0' "new table is empty"
  assert_contains "$BODY" '"limit":50' "rows limit echoed"

  request GET "/databases/$CONN_ID/tables/public/table_that_does_not_exist/rows" "" "$USER_JAR"
  assert_status 404 "unknown table rejected"

  INSERT_SQL="INSERT INTO $SMOKE_TABLE (note) VALUES ('halo');"
  INSERT_MIGRATION="{\"name\":\"insert smoke row\",\"sql\":\"$INSERT_SQL\"}"
  request POST "/databases/$CONN_ID/migrations" "$INSERT_MIGRATION" "$USER_JAR" "$USER_CSRF"
  assert_status 201 "create insert migration"
  INSERT_ID=$(json_string "$BODY" id)

  request POST "/databases/$CONN_ID/migrations/$INSERT_ID/execute" "" "$USER_JAR" "$USER_CSRF"
  assert_status 200 "execute insert migration"
  assert_contains "$BODY" '"version":2' "second successful version is 2"

  request GET "/databases/$CONN_ID/tables/public/$SMOKE_TABLE/rows?limit=50&offset=0" "" "$USER_JAR"
  assert_status 200 "read inserted row"
  assert_contains "$BODY" '"total":1' "row count is 1"
  assert_contains "$BODY" '"halo"' "inserted value returned"

  FAIL_MIGRATION='{"name":"broken migration","sql":"THIS IS NOT SQL;"}'
  request POST "/databases/$CONN_ID/migrations" "$FAIL_MIGRATION" "$USER_JAR" "$USER_CSRF"
  assert_status 201 "create failing migration"
  FAIL_ID=$(json_string "$BODY" id)

  request POST "/databases/$CONN_ID/migrations/$FAIL_ID/execute" "" "$USER_JAR" "$USER_CSRF"
  assert_status 200 "failing SQL still returns 200"
  assert_contains "$BODY" '"status":"failed"' "migration marked failed"
  assert_contains "$BODY" '"version":null' "failed migration has no version"

  request POST "/databases/$CONN_ID/migrations/$FAIL_ID/execute" "" "$USER_JAR" "$USER_CSRF"
  assert_status 409 "failed migration cannot rerun"

  request GET "/databases/$CONN_ID/migrations" "" "$USER_JAR"
  assert_status 200 "list migration history"
  assert_contains "$BODY" '"status":"success"' "history contains success"
  assert_contains "$BODY" '"status":"failed"' "history contains failed"

  request GET "/databases/$CONN_ID/migrations/$MIG_ID" "" "$USER_JAR"
  assert_status 200 "get migration detail"
  assert_contains "$BODY" "$SMOKE_TABLE" "detail includes SQL"

  section "Admin has no implicit database access"
  request GET /databases "" "$ADMIN_JAR"
  assert_status 200 "admin lists databases"
  assert_not_contains "$BODY" "$CONN_NAME" "admin without group cannot see database"

  request GET "/databases/$CONN_ID" "" "$ADMIN_JAR"
  assert_status 403 "admin without group gets 403"

  section "Access revocation and cleanup"
  request PUT "/admin/groups/$GROUP_ID/access" '{"access":[]}' "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 200 "clear group access"

  request GET "/databases/$CONN_ID" "" "$USER_JAR"
  assert_status 403 "revoked user gets 403"

  request GET /databases "" "$USER_JAR"
  assert_status 200 "revoked user lists databases"
  assert_not_contains "$BODY" "$CONN_NAME" "revoked database no longer listed"

  request PUT "/admin/groups/$GROUP_ID/members" '{"user_ids":[]}' "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 200 "clear group members"

  request DELETE "/admin/groups/$GROUP_ID" "" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 204 "delete group"

  request DELETE "/admin/connections/$CONN_ID" "" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 204 "delete connection"

  request GET "/admin/connections/$CONN_ID" "" "$ADMIN_JAR"
  assert_status 404 "deleted connection not found"

  section "User updates and logout"
  request GET /admin/users "" "$ADMIN_JAR"
  assert_status 200 "list users"
  assert_contains "$BODY" "\"username\":\"$USER_NAME\"" "user in list"

  request PUT "/admin/users/$USER_ID" '{"name":"API User Updated"}' "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 200 "update user"
  assert_contains "$BODY" '"name":"API User Updated"' "updated name"

  UPDATED_USER_PASS="apiuser-new-password"
  request PUT "/admin/users/$USER_ID" "{\"password\":\"$UPDATED_USER_PASS\"}" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 200 "update user password"

  request GET /users/profile "" "$USER_JAR"
  assert_status 401 "password change revokes user session"

  request POST /users/login "{\"username\":\"$USER_NAME\",\"password\":\"$UPDATED_USER_PASS\"}" "$USER_JAR"
  assert_status 200 "login with updated password"
  USER_CSRF=$(cookie_value "$USER_JAR" csrf_token)
  [ -n "$USER_CSRF" ] || fail "csrf_token cookie set after relogin"
  ok "csrf_token cookie set after relogin"

  request PUT "/admin/users/$USER_ID" '{"is_admin":true}' "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 200 "change admin status"
  request GET /users/profile "" "$USER_JAR"
  assert_status 401 "admin status change revokes user session"

  request POST /users/login "{\"username\":\"$USER_NAME\",\"password\":\"$UPDATED_USER_PASS\"}" "$USER_JAR"
  assert_status 200 "login after admin status change"
  USER_CSRF=$(cookie_value "$USER_JAR" csrf_token)
  [ -n "$USER_CSRF" ] || fail "csrf_token cookie set after admin status change"
  ok "csrf_token cookie set after admin status change"

  request POST /users/logout "" "$USER_JAR" "$USER_CSRF"
  assert_status 204 "user logout"
  request GET /users/profile "" "$USER_JAR"
  assert_status 401 "profile after user logout"

  request DELETE "/admin/users/$USER_ID" "" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 204 "delete user"

  request POST /users/logout "" "$ADMIN_JAR" "$ADMIN_CSRF"
  assert_status 204 "admin logout"
  request GET /users/profile "" "$ADMIN_JAR"
  assert_status 401 "profile after admin logout"

  printf "\n%bAll API contract tests passed.%b\n" "$GREEN" "$NC"
}

test_api() {
  ensure_db
  build_server

  TMP_DIR=$(mktemp -d)
  trap cleanup EXIT
  trap 'exit 130' INT TERM

  start_api_server
  run_contract_tests
}

test_e2e() {
  ensure_db
  build_server

  TMP_DIR=$(mktemp -d)
  trap cleanup EXIT
  trap 'exit 130' INT TERM

  start_api_server
  start_frontend_server
  API_BASE="http://127.0.0.1:${FRONTEND_TEST_PORT}/api/v1"
  run_contract_tests
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
    PAYLOAD="${2:-}"
    [ -n "$PAYLOAD" ] || PAYLOAD='{"message":"ping","port":9000,"status":"connected"}'
    [ -f "$BIN_PATH" ] || build_server

    TMP_DIR=$(mktemp -d)
    trap cleanup EXIT
    PORT="$PORT" "$BIN_PATH" >"$TMP_DIR/server.log" 2>&1 &
    SERVER_PID=$!
    i=0
    while ! curl -s "http://127.0.0.1:$PORT/api/v1/health" >/dev/null 2>&1; do
      sleep 0.2
      i=$((i + 1))
      [ "$i" -le 25 ] || {
        echo "Error: server did not start." >&2
        cat "$TMP_DIR/server.log" >&2
        exit 1
      }
    done
    curl -i -X POST "http://127.0.0.1:$PORT/api/v1/test" \
      -H "Content-Type: application/json" \
      -d "$PAYLOAD"
    ;;

  test-unit)
    test_unit
    ;;

  test-integration)
    test_integration
    ;;

  test-auth)
    test_auth
    ;;

  test-db)
    test_db
    ;;

  test-frontend)
    test_frontend
    ;;

  test-api)
    test_api
    ;;

  test-e2e)
    test_e2e
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
    echo "Usage: $0 {build|run|dev|test [json]|test-unit|test-integration|test-auth|test-db|test-frontend|test-api|test-e2e|test-all|db-up|db-down|clean}"
    echo ""
    echo "  test               Smoke-test /api/v1/test (legacy)."
    echo "  test-unit          Run unit tests only."
    echo "  test-integration   Start PostgreSQL, then run integration tests."
    echo "  test-auth          Run auth, session, authorization, user, and group tests."
    echo "  test-db            Run target database and database viewer tests."
    echo "  test-frontend      Typecheck and build the React frontend."
    echo "  test-api           Start PostgreSQL + backend, then run API contract tests."
    echo "  test-e2e           Run the same contract tests through the Vite proxy (frontend <-> backend <-> database)."
    echo "  test-all           Run unit, integration, and API tests."
    echo "  db-up / db-down    Start / remove the test PostgreSQL container."
    echo ""
    echo "Environment: TEST_PG_DSN, PG_*, API_TEST_PORT, FRONTEND_TEST_PORT, ADMIN_USERNAME, ADMIN_PASSWORD, TARGET_*"
    exit 1
    ;;
esac
