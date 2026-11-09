#!/usr/bin/env bash
# Security smoke test for the batch migration API.
# Usage: BASE_URL=http://localhost:9000/api/v1 ADMIN_PASSWORD=... ./scripts/security-batch.sh
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:9000/api/v1}"
ADMIN_USERNAME="${ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin-password}"

PASS=0
FAIL=0
SKIP=0

JAR=$(mktemp)
JAR2=$(mktemp)
JAR3=$(mktemp)
BODY=$(mktemp)
trap 'rm -f "$JAR" "$JAR2" "$JAR3" "$BODY"' EXIT

jget() {
	python3 -c '
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for key in sys.argv[1].split("."):
    if data is None:
        break
    data = data[int(key)] if key.isdigit() else data.get(key)
print("" if data is None else data)
' "$1" 2>/dev/null
}

csrf() {
	awk '$6 == "csrf_token" { print $7 }' "$1" | tail -n 1
}

call() {
	local jar=$1 method=$2 path=$3 data=${4:-}
	local token args
	token=$(csrf "$jar")
	args=(-sS -o "$BODY" -w '%{http_code}' -b "$jar" -c "$jar" -X "$method" -H 'Content-Type: application/json')
	if [ -n "$token" ] && [ "$method" != "GET" ]; then
		args+=(-H "X-CSRF-Token: $token")
	fi
	if [ -n "$data" ]; then
		args+=(-d "$data")
	fi
	curl "${args[@]}" "$BASE_URL$path"
}

login() {
	local jar=$1 user=$2 pass=$3
	curl -sS -o "$BODY" -c "$jar" -H 'Content-Type: application/json' \
		-d "{\"username\":\"$user\",\"password\":\"$pass\"}" "$BASE_URL/users/login" >/dev/null
	[ -n "$(csrf "$jar")" ]
}

check() {
	local name=$1 expected=$2 actual=$3
	case " $expected " in
	*" $actual "*)
		echo "PASS  $name ($actual)"
		PASS=$((PASS + 1))
		;;
	*)
		echo "FAIL  $name (expected $expected, got $actual)"
		head -c 300 "$BODY" 2>/dev/null
		echo
		FAIL=$((FAIL + 1))
		;;
	esac
}

skip() {
	echo "SKIP  $1"
	SKIP=$((SKIP + 1))
}

echo "== Login =="
if ! login "$JAR" "$ADMIN_USERNAME" "$ADMIN_PASSWORD"; then
	echo "Gagal login sebagai $ADMIN_USERNAME. Set ADMIN_PASSWORD bila berbeda." >&2
	exit 2
fi

DB_ID=$(call "$JAR" GET /databases >/dev/null; jget databases.0.id)
LEVEL=$(call "$JAR" GET /databases >/dev/null; jget databases.0.level)
if [ -z "$DB_ID" ]; then
	echo "Tidak ada database yang bisa diakses. Seed koneksi dan akses terlebih dahulu." >&2
	exit 2
fi
echo "Database uji: $DB_ID (level $LEVEL)"

MIGRATIONS=$(call "$JAR" GET "/databases/$DB_ID/migrations" >/dev/null; python3 -c '
import json, sys
data = json.load(sys.stdin)
items = data.get("migrations", [])
drafts = [m for m in items if m.get("status") == "draft"]
done = [m for m in items if m.get("status") == "success"]
print(drafts[0]["id"] if drafts else "")
print(done[0]["id"] if done else "")
' 2>/dev/null)
DRAFT_ID=$(printf '%s\n' "$MIGRATIONS" | sed -n '1p')
DONE_ID=$(printf '%s\n' "$MIGRATIONS" | sed -n '2p')

if [ -z "$DRAFT_ID" ]; then
	STATUS=$(call "$JAR" POST "/databases/$DB_ID/migrations" '{"name":"security seed","sql":"SELECT 1"}')
	if [ "$STATUS" = "201" ]; then
		DRAFT_ID=$(jget id)
		echo "Dibuat draft seed: $DRAFT_ID"
	fi
fi

echo
echo "== Matriks keamanan =="

STATUS=$(curl -sS -o "$BODY" -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
	-d "{\"migration_ids\":[\"$DRAFT_ID\"]}" "$BASE_URL/databases/$DB_ID/batches")
check "tanpa login ditolak" "401" "$STATUS"

STATUS=$(curl -sS -o "$BODY" -w '%{http_code}' -b "$JAR" -X POST -H 'Content-Type: application/json' \
	-d "{\"migration_ids\":[\"$DRAFT_ID\"]}" "$BASE_URL/databases/$DB_ID/batches")
check "tanpa CSRF ditolak" "403" "$STATUS"

STATUS=$(call "$JAR" GET "/databases/$DB_ID/batches/1%20OR%201=1")
check "batch id non-UUID tidak 5xx" "400 404" "$STATUS"

STATUS=$(call "$JAR" GET "/databases/$DB_ID/batches/00000000-0000-0000-0000-000000000000")
check "batch asing -> 404" "404" "$STATUS"

STATUS=$(call "$JAR" POST "/databases/$DB_ID/batches" '{"migration_ids":[]}')
check "daftar kosong -> 400" "400" "$STATUS"

STATUS=$(call "$JAR" POST "/databases/$DB_ID/batches" '{"migration_ids":["00000000-0000-0000-0000-000000000000"]}')
check "migration asing -> 409" "409" "$STATUS"

if [ -n "$DONE_ID" ]; then
	STATUS=$(call "$JAR" POST "/databases/$DB_ID/batches" "{\"migration_ids\":[\"$DONE_ID\"]}")
	check "migration non-draft -> 409" "409" "$STATUS"
else
	skip "migration non-draft (belum ada migration success)"
fi

STATUS=$(call "$JAR" PUT "/databases/$DB_ID/executions" '{}')
check "audit PUT ditolak" "405" "$STATUS"
STATUS=$(call "$JAR" DELETE "/databases/$DB_ID/executions")
check "audit DELETE ditolak" "405" "$STATUS"

if [ -n "$DRAFT_ID" ]; then
	SPOOF="11111111-1111-1111-1111-111111111111"
	STATUS=$(call "$JAR" POST "/databases/$DB_ID/batches" "{\"migration_ids\":[\"$DRAFT_ID\"],\"execution_id\":\"$SPOOF\"}")
	check "create batch -> 202" "202" "$STATUS"
	BATCH_ID=$(jget id)
	if [ -n "$BATCH_ID" ]; then
		sleep 1
		call "$JAR" GET "/databases/$DB_ID/batches/$BATCH_ID/items" >/dev/null
		ITEM_EXEC=$(jget items.0.execution_id)
		if [ -n "$ITEM_EXEC" ] && [ "$ITEM_EXEC" != "$SPOOF" ]; then
			echo "PASS  execution_id dari klien diabaikan"
			PASS=$((PASS + 1))
		else
			echo "FAIL  execution_id dari klien dipakai ($ITEM_EXEC)"
			FAIL=$((FAIL + 1))
		fi
		call "$JAR" POST "/databases/$DB_ID/batches/$BATCH_ID/cancel" '{}' >/dev/null
	fi
fi

echo
echo "== Hak akses view-only =="
SUFFIX=$(date +%s)
VUSER="batchview$SUFFIX"
STATUS=$(call "$JAR" POST /admin/users "{\"username\":\"$VUSER\",\"name\":\"Batch View\",\"password\":\"viewpass123\"}")
if [ "$STATUS" = "201" ]; then
	VUSER_ID=$(jget id)
	STATUS=$(call "$JAR" POST /admin/groups "{\"name\":\"batchview$SUFFIX\"}")
	if [ "$STATUS" = "201" ]; then
		GROUP_ID=$(jget id)
		call "$JAR" PUT "/admin/groups/$GROUP_ID/members" "{\"user_ids\":[\"$VUSER_ID\"]}" >/dev/null
		call "$JAR" PUT "/admin/groups/$GROUP_ID/access" "{\"access\":[{\"connection_id\":\"$DB_ID\",\"level\":\"view\"}]}" >/dev/null
		if login "$JAR2" "$VUSER" "viewpass123"; then
			STATUS=$(call "$JAR2" GET "/databases/$DB_ID/batches")
			check "view boleh melihat batch" "200" "$STATUS"
			STATUS=$(call "$JAR2" POST "/databases/$DB_ID/batches" "{\"migration_ids\":[\"$DRAFT_ID\"]}")
			check "view tidak boleh membuat batch" "403" "$STATUS"
		else
			skip "login user view-only gagal"
		fi
	else
		skip "gagal membuat group uji"
	fi
else
	skip "gagal membuat user uji (mungkin sudah ada)"
fi

echo
echo "Hasil: $PASS lulus, $FAIL gagal, $SKIP dilewati"
[ "$FAIL" -eq 0 ]
