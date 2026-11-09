#!/bin/sh
# Run CodeQL locally for the Go backend and the React frontend, then write SARIF
# results to .codeql/results. Requires the CodeQL CLI (codeql) on PATH or
# CODEQL_HOME pointing at an extracted CLI bundle.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUT="$ROOT/.codeql/results"

if [ -n "${CODEQL_HOME:-}" ]; then
	PATH="$CODEQL_HOME:$PATH"
	export PATH
fi

if ! command -v codeql >/dev/null 2>&1; then
	echo "CodeQL CLI tidak ditemukan." >&2
	echo "Unduh dari https://github.com/github/codeql-action/releases lalu set CODEQL_HOME," >&2
	echo "atau install lewat package manager, kemudian jalankan ulang script ini." >&2
	exit 1
fi

mkdir -p "$OUT"

run() {
	lang=$1
	src=$2
	db="$ROOT/.codeql/db-$lang"

	echo "==> CodeQL $lang ($src)"
	codeql database create "$db" --language="$lang" --source-root="$ROOT/$src" --overwrite
	codeql database analyze "$db" --format=sarif-latest --output="$OUT/$lang.sarif"
}

run go server
run javascript-typescript frontend

echo "Hasil SARIF: $OUT"
