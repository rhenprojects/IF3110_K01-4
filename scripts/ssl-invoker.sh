#!/usr/bin/env bash
# End-to-end TLS test for target database connections.
#
# Starts a disposable PostgreSQL container with a self-signed CA, creates a
# platform connection using ssl_mode=verify-full + ssl_root_cert, and verifies
# that the backend accepts the CA. Also checks that verify-full without the CA
# fails (proving the CA is actually used).
#
# Usage:
#   bash scripts/ssl-invoker.sh
#   BASE_URL=http://localhost:9000/api/v1 ADMIN_PASSWORD=... bash scripts/ssl-invoker.sh
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:9000/api/v1}"
ADMIN_USERNAME="${ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin-password}"
IMAGE="${IMAGE:-if3110-ssl-pg-test}"
CONTAINER="${CONTAINER:-if3110-ssl-pg-test}"
PORT="${SSL_TEST_PORT:-55433}"
DB_NAME="${SSL_TEST_DB:-ssl_test}"
DB_USER="${SSL_TEST_USER:-postgres}"
DB_PASSWORD="${SSL_TEST_PASSWORD:-postgres}"

PASS=0
FAIL=0
CONN_ID=""
TMP=$(mktemp -d /tmp/opencode/ssl-invoker.XXXXXX)
JAR="$TMP/cookies.txt"
BODY="$TMP/body.json"

die() {
	echo "ERROR: $*" >&2
	exit 1
}

cleanup() {
	if [ -n "$CONN_ID" ]; then
		curl -sS -o /dev/null -b "$JAR" -c "$JAR" -X DELETE \
			-H "X-CSRF-Token: $(csrf)" "$BASE_URL/admin/connections/$CONN_ID" || true
	fi
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
	docker rmi -f "$IMAGE" >/dev/null 2>&1 || true
	rm -rf "$TMP"
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || die "docker tidak ditemukan"
command -v openssl >/dev/null 2>&1 || die "openssl tidak ditemukan"
command -v curl >/dev/null 2>&1 || die "curl tidak ditemukan"
command -v python3 >/dev/null 2>&1 || die "python3 tidak ditemukan"

NETWORK="${NETWORK:-$(docker inspect if3110-backend --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}{{end}}' 2>/dev/null || true)}"
[ -n "$NETWORK" ] || die "backend container tidak berjalan; jalankan docker compose dulu"

jget() {
	python3 - "$1" "$BODY" <<'PY' 2>/dev/null || true
import json
import sys
key_path, body_path = sys.argv[1], sys.argv[2]
try:
    value = json.load(open(body_path))
except Exception:
    sys.exit(1)
for key in key_path.split("."):
    if value is None:
        break
    value = value[int(key)] if key.isdigit() else value.get(key)
print("" if value is None else value)
PY
}

csrf() {
	awk '$6 == "csrf_token" { print $7 }' "$JAR" | tail -n 1
}

call() {
	local method=$1 path=$2 data=${3:-}
	local token
	local args=()
	token=$(csrf)
	args=(-sS -o "$BODY" -w '%{http_code}' -b "$JAR" -c "$JAR" -X "$method" -H 'Content-Type: application/json')
	if [ -n "$token" ] && [ "$method" != "GET" ]; then
		args+=(-H "X-CSRF-Token: $token")
	fi
	if [ -n "$data" ]; then
		args+=(-d "$data")
	fi
	curl "${args[@]}" "$BASE_URL$path"
}

check() {
	local name=$1 expected=$2 actual=$3
	if [ "$expected" = "$actual" ]; then
		echo "PASS  $name ($actual)"
		PASS=$((PASS + 1))
	else
		echo "FAIL  $name (expected $expected, got $actual)"
		head -c 400 "$BODY" 2>/dev/null
		echo
		FAIL=$((FAIL + 1))
	fi
}

echo "== Siapkan CA dan server certificate =="
mkdir -p "$TMP/certs"
openssl req -x509 -newkey rsa:2048 -nodes \
	-keyout "$TMP/certs/ca.key" -out "$TMP/certs/ca.crt" \
	-days 1 -subj "/CN=if3110-test-ca" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes \
	-keyout "$TMP/certs/server.key" -out "$TMP/certs/server.csr" \
	-subj "/CN=$CONTAINER" >/dev/null 2>&1
cat >"$TMP/certs/ext.cnf" <<EOF
subjectAltName=DNS:$CONTAINER,DNS:localhost,IP:127.0.0.1
extendedKeyUsage=serverAuth
EOF
openssl x509 -req -in "$TMP/certs/server.csr" \
	-CA "$TMP/certs/ca.crt" -CAkey "$TMP/certs/ca.key" -CAcreateserial \
	-out "$TMP/certs/server.crt" -days 1 -extfile "$TMP/certs/ext.cnf" >/dev/null 2>&1

cat >"$TMP/Dockerfile" <<'EOF'
FROM postgres:16-alpine
COPY certs /certs
RUN chown -R postgres:postgres /certs && chmod 600 /certs/server.key
CMD ["postgres", "-c", "ssl=on", "-c", "ssl_cert_file=/certs/server.crt", "-c", "ssl_key_file=/certs/server.key"]
EOF

echo "== Build dan jalankan PostgreSQL TLS =="
docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
docker build -q -t "$IMAGE" "$TMP" >/dev/null
docker run -d --name "$CONTAINER" --network "$NETWORK" \
	-e POSTGRES_PASSWORD="$DB_PASSWORD" -e POSTGRES_DB="$DB_NAME" \
	-p "127.0.0.1:$PORT:5432" "$IMAGE" >/dev/null

for _ in $(seq 1 60); do
	if docker exec "$CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -tAc 'select 1' >/dev/null 2>&1; then
		break
	fi
	sleep 1
done
docker exec "$CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -tAc 'select 1' >/dev/null 2>&1 ||
	die "PostgreSQL TLS tidak siap"
[ "$(docker exec "$CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -tAc 'show ssl' | tr -d ' ')" = "on" ] ||
	die "SSL PostgreSQL tidak aktif"

echo "== Login API =="
LOGIN=$(python3 -c 'import json,sys; print(json.dumps({"username": sys.argv[1], "password": sys.argv[2]}))' "$ADMIN_USERNAME" "$ADMIN_PASSWORD")
STATUS=$(curl -sS -o "$BODY" -w '%{http_code}' -c "$JAR" \
	-H 'Content-Type: application/json' -d "$LOGIN" "$BASE_URL/users/login")
[ "$STATUS" = "200" ] || die "login gagal (HTTP $STATUS)"

NAME="ssl-test-$(date +%s)"
PAYLOAD=$(python3 - "$NAME" "$TMP/certs/ca.crt" <<'PY'
import json
import sys
print(json.dumps({
    "name": sys.argv[1],
    "host": "if3110-ssl-pg-test",
    "port": 5432,
    "db_name": "ssl_test",
    "username": "postgres",
    "password": "postgres",
    "ssl_mode": "verify-full",
    "ssl_root_cert": open(sys.argv[2]).read(),
}))
PY
)

echo "== Test koneksi dengan CA =="
STATUS=$(call POST /admin/connections/test "$PAYLOAD")
check "verify-full + CA diterima" "200" "$STATUS"

VERIFY_CA=$(python3 -c 'import json,sys; d=json.loads(sys.argv[1]); d["ssl_mode"]="verify-ca"; print(json.dumps(d))' "$PAYLOAD")
STATUS=$(call POST /admin/connections/test "$VERIFY_CA")
check "verify-ca + CA diterima" "200" "$STATUS"

echo "== Test koneksi tanpa CA =="
NO_CA=$(python3 -c 'import json,sys; d=json.loads(sys.argv[1]); d["ssl_root_cert"]=""; print(json.dumps(d))' "$PAYLOAD")
STATUS=$(call POST /admin/connections/test "$NO_CA")
check "verify-full tanpa CA ditolak" "502" "$STATUS"

BAD_MODE=$(python3 -c 'import json,sys; d=json.loads(sys.argv[1]); d["ssl_mode"]="prefer"; print(json.dumps(d))' "$PAYLOAD")
STATUS=$(call POST /admin/connections/test "$BAD_MODE")
check "CA dengan mode prefer ditolak" "400" "$STATUS"

echo "== Simpan dan test koneksi tersimpan =="
STATUS=$(call POST /admin/connections "$PAYLOAD")
check "create connection" "201" "$STATUS"
CONN_ID=$(jget id)
if [ -n "$CONN_ID" ]; then
	STATUS=$(call POST "/admin/connections/$CONN_ID/test")
	check "test saved connection" "200" "$STATUS"
else
	echo "FAIL  create connection tidak mengembalikan id"
	FAIL=$((FAIL + 1))
fi

echo
echo "Hasil: $PASS lulus, $FAIL gagal"
[ "$FAIL" -eq 0 ]
