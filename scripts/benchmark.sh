#!/bin/sh
set -eu
export LC_ALL=C

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SERVER_DIR="$ROOT_DIR/server"
BIN_PATH="$SERVER_DIR/bin/server"
HOST="${BENCH_HOST:-127.0.0.1}"
PORT="${PORT:-9000}"
BENCH_PATH="${BENCH_PATH:-/api/v1/bench-raw}"
DURATION="${BENCH_DURATION:-30}"
CONNECTIONS="${BENCH_CONNECTIONS:-100}"
THREADS="${BENCH_THREADS:-2}"
ROUNDS="${BENCH_ROUNDS:-3}"
WARMUP="${BENCH_WARMUP:-2}"
TOOL="${BENCH_TOOL:-auto}"
START=auto
BUILD=1
TAB=$(printf '\t')

usage() {
  cat <<'EOF'
Usage: ./scripts/benchmark.sh [OPTIONS] [LABEL=URL ...]

Benchmark native backend HTTP endpoints. This script does not use Docker.
With no targets, it builds and starts this backend natively on
http://127.0.0.1:9000 and benchmarks GET /api/v1/bench-raw.

Examples:
  ./scripts/benchmark.sh
  ./scripts/benchmark.sh http://127.0.0.1:9000/api/v1/bench-raw http://127.0.0.1:8080/api/v1/bench-raw
  ./scripts/benchmark.sh --start "other=http://127.0.0.1:8080/api/v1/bench-raw"

Options:
  -d, --duration SECONDS   Duration per round (default: 10)
  -c, --connections N      Concurrent connections (default: 100)
  -t, --threads N          wrk threads (default: 4)
  -r, --rounds N           Rounds per target (default: 3)
  -w, --warmup SECONDS     Warmup duration per target (default: 2, 0 disables)
  -p, --path PATH          Path for the auto-started backend (default: /api/v1/bench-raw)
      --start              Build and start this backend even when targets are given
      --no-start           Benchmark only already-running targets
      --no-build           Reuse server/bin/server when starting this backend
  -h, --help               Show this help

Environment:
  PORT, BENCH_HOST, BENCH_PATH, BENCH_DURATION, BENCH_CONNECTIONS, BENCH_THREADS,
  BENCH_ROUNDS, BENCH_WARMUP, BENCH_TOOL=auto|wrk|ab, DB_DSN

A target can be a plain URL or LABEL=URL. Labels are used in the summary.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    -d|--duration)
      [ "$#" -ge 2 ] || { echo "Error: $1 requires a value" >&2; exit 2; }
      DURATION="$2"
      shift 2
      ;;
    -c|--connections)
      [ "$#" -ge 2 ] || { echo "Error: $1 requires a value" >&2; exit 2; }
      CONNECTIONS="$2"
      shift 2
      ;;
    -t|--threads)
      [ "$#" -ge 2 ] || { echo "Error: $1 requires a value" >&2; exit 2; }
      THREADS="$2"
      shift 2
      ;;
    -r|--rounds)
      [ "$#" -ge 2 ] || { echo "Error: $1 requires a value" >&2; exit 2; }
      ROUNDS="$2"
      shift 2
      ;;
    -w|--warmup)
      [ "$#" -ge 2 ] || { echo "Error: $1 requires a value" >&2; exit 2; }
      WARMUP="$2"
      shift 2
      ;;
    -p|--path)
      [ "$#" -ge 2 ] || { echo "Error: $1 requires a value" >&2; exit 2; }
      BENCH_PATH="$2"
      shift 2
      ;;
    --start)
      START=1
      shift
      ;;
    --no-start)
      START=0
      shift
      ;;
    --no-build)
      BUILD=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      break
      ;;
    -*)
      echo "Error: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
    *)
      break
      ;;
  esac
done

validate_uint() {
  name="$1"
  value="$2"
  case "$value" in
    ''|*[!0-9]*)
      echo "Error: $name must be a non-negative integer" >&2
      exit 2
      ;;
  esac
}

validate_uint "duration" "$DURATION"
validate_uint "connections" "$CONNECTIONS"
validate_uint "threads" "$THREADS"
validate_uint "rounds" "$ROUNDS"
validate_uint "warmup" "$WARMUP"
[ "$DURATION" -gt 0 ] || { echo "Error: duration must be greater than 0" >&2; exit 2; }
[ "$CONNECTIONS" -gt 0 ] || { echo "Error: connections must be greater than 0" >&2; exit 2; }
[ "$THREADS" -gt 0 ] || { echo "Error: threads must be greater than 0" >&2; exit 2; }
[ "$ROUNDS" -gt 0 ] || { echo "Error: rounds must be greater than 0" >&2; exit 2; }

case "$TOOL" in
  auto)
    if command -v wrk >/dev/null 2>&1; then
      TOOL=wrk
    elif command -v ab >/dev/null 2>&1; then
      TOOL=ab
    else
      echo "Error: neither wrk nor ab is installed" >&2
      exit 1
    fi
    ;;
  wrk|ab)
    command -v "$TOOL" >/dev/null 2>&1 || {
      echo "Error: $TOOL is not installed" >&2
      exit 1
    }
    ;;
  *)
    echo "Error: BENCH_TOOL must be auto, wrk, or ab" >&2
    exit 2
    ;;
esac

TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/if3110-bench.XXXXXX")
TARGETS_FILE="$TMP_DIR/targets"
RESULTS_FILE="$TMP_DIR/results"
MEDIANS_FILE="$TMP_DIR/medians"
: >"$TARGETS_FILE"
: >"$RESULTS_FILE"
: >"$MEDIANS_FILE"
SERVER_PID=""

cleanup() {
  if [ -n "${SERVER_PID:-}" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [ -n "${TMP_DIR:-}" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

add_target() {
  label="$1"
  url="$2"
  if awk -F'\t' -v label="$label" -v url="$url" \
    '$1 == label && $2 == url { found = 1 } END { exit !found }' "$TARGETS_FILE"; then
    return 0
  fi
  printf '%s\t%s\n' "$label" "$url" >>"$TARGETS_FILE"
}

start_native() {
  own_url="$1"

  if curl -fsS --max-time 1 "$own_url" >/dev/null 2>&1; then
    echo "Error: $own_url is already responding; use --no-start or stop it first" >&2
    exit 1
  fi

  if [ "$BUILD" -eq 1 ]; then
    echo "Building native backend..."
    mkdir -p "$SERVER_DIR/bin"
    (cd "$SERVER_DIR" && go build -o bin/server app/main.go)
  fi
  if [ ! -x "$BIN_PATH" ]; then
    echo "Error: $BIN_PATH is missing; rerun without --no-build" >&2
    exit 1
  fi

  echo "Starting native backend on http://$HOST:$PORT (no Docker)"
  (
    cd "$SERVER_DIR"
    PORT="$PORT" \
      DB_DSN="${DB_DSN:-benchmark-no-db}" \
      ADMIN_USERNAME="${ADMIN_USERNAME:-admin}" \
      ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin-password}" \
      SESSION_HASH_KEY="${SESSION_HASH_KEY:-benchmark-hash-key-0123456789abcdef0123456789abcdef0123456789abcdef0123456789}" \
      SESSION_BLOCK_KEY="${SESSION_BLOCK_KEY:-0123456789abcdef0123456789abcdef}" \
      DB_ENC_KEY="${DB_ENC_KEY:-MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=}" \
      "$BIN_PATH"
  ) >"$TMP_DIR/server.log" 2>&1 &
  SERVER_PID=$!

  i=0
  while ! curl -fsS --max-time 0.5 "$own_url" >/dev/null 2>&1; do
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      echo "Error: native backend exited during startup" >&2
      cat "$TMP_DIR/server.log" >&2
      exit 1
    fi
    i=$((i + 1))
    if [ "$i" -gt 150 ]; then
      echo "Error: native backend did not become ready" >&2
      cat "$TMP_DIR/server.log" >&2
      exit 1
    fi
    sleep 0.1
  done
  echo "Backend ready"
}

run_bench() {
  url="$1"
  seconds="$2"
  raw="$3"
  case "$TOOL" in
    wrk) wrk -t"$THREADS" -c"$CONNECTIONS" -d"${seconds}s" --latency "$url" >"$raw" 2>&1 ;;
    ab) ab -k -q -t "$seconds" -c "$CONNECTIONS" "$url" >"$raw" 2>&1 ;;
  esac
}

parse_wrk() {
  raw="$1"
  REQ=$(awk '/Requests\/sec:/ { print $2; exit }' "$raw")
  AVG=$(awk '/^[[:space:]]+Latency/ { print $2; exit }' "$raw")
  P50=$(awk '$1 == "50%" { print $2; exit }' "$raw")
  P90=$(awk '$1 == "90%" { print $2; exit }' "$raw")
  P99=$(awk '$1 == "99%" { print $2; exit }' "$raw")
  ERRORS=$(awk '/Non-2xx or 3xx responses:/ { n = $NF } END { if (n == "") print 0; else print n }' "$raw")
  SOCKET_ERRORS=$(awk '/Socket errors:/ {
    for (i = 1; i <= NF; i++) {
      if ($i ~ /^[0-9]+,?$/) { gsub(",", "", $i); n += $i }
    }
  } END { print n + 0 }' "$raw")
  if [ "$SOCKET_ERRORS" -gt 0 ]; then
    ERRORS=$((ERRORS + SOCKET_ERRORS))
  fi
  [ -n "$REQ" ] || return 1
  [ -n "$AVG" ] || AVG="-"
  [ -n "$P50" ] || P50="-"
  [ -n "$P90" ] || P90="-"
  [ -n "$P99" ] || P99="-"
}

parse_ab() {
  raw="$1"
  REQ=$(awk '/Requests per second:/ { print $4; exit }' "$raw")
  AVG=$(awk '/Time per request:/ { print $4 " " $5; exit }' "$raw")
  P50="-"
  P90="-"
  P99="-"
  ERRORS=$(awk '/Failed requests:/ { print $3; exit }' "$raw")
  [ -n "$ERRORS" ] || ERRORS=0
  [ -n "$REQ" ] || return 1
  [ -n "$AVG" ] || AVG="-"
}

if [ "$START" = auto ]; then
  if [ "$#" -eq 0 ]; then
    START=1
  else
    START=0
  fi
fi

while [ "$#" -gt 0 ]; do
  arg="$1"
  shift
  case "$arg" in
    http://*|https://*)
      label="$arg"
      url="$arg"
      ;;
    *=*)
      label="${arg%%=*}"
      url="${arg#*=}"
      ;;
    *)
      label="$arg"
      url="$arg"
      ;;
  esac
  if [ -z "$url" ]; then
    echo "Error: target URL cannot be empty" >&2
    exit 2
  fi
  [ -n "$label" ] || label="$url"
  add_target "$label" "$url"
done

if [ "$START" -eq 1 ]; then
  start_native "http://$HOST:$PORT$BENCH_PATH"
  add_target "if3110-native:$PORT" "http://$HOST:$PORT$BENCH_PATH"
fi

if [ ! -s "$TARGETS_FILE" ]; then
  echo "Error: no benchmark targets; pass a URL or use --start" >&2
  exit 2
fi

while IFS="$TAB" read -r label url <&3; do
  if ! curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
    echo "Error: cannot reach $label ($url)" >&2
    exit 1
  fi
done 3<"$TARGETS_FILE"

case "$TOOL" in
  wrk) echo "Tool: wrk | duration: ${DURATION}s | connections: $CONNECTIONS | threads: $THREADS | rounds: $ROUNDS" ;;
  ab) echo "Tool: ab | duration: ${DURATION}s | connections: $CONNECTIONS | rounds: $ROUNDS" ;;
esac

if [ "$WARMUP" -gt 0 ]; then
  echo "Warming up for ${WARMUP}s per target"
  while IFS="$TAB" read -r label url <&3; do
    raw=$(mktemp "$TMP_DIR/warmup.XXXXXX")
    printf '  %-28s ' "$label"
    if run_bench "$url" "$WARMUP" "$raw"; then
      echo "ok"
    else
      echo "failed"
      cat "$raw" >&2
      exit 1
    fi
  done 3<"$TARGETS_FILE"
fi

round=1
while [ "$round" -le "$ROUNDS" ]; do
  echo "Round $round/$ROUNDS"
  while IFS="$TAB" read -r label url <&3; do
    raw=$(mktemp "$TMP_DIR/raw.XXXXXX")
    printf '  %-28s ' "$label"
    if ! run_bench "$url" "$DURATION" "$raw"; then
      echo "failed"
      cat "$raw" >&2
      exit 1
    fi
    case "$TOOL" in
      wrk)
        if ! parse_wrk "$raw"; then
          echo "failed to parse wrk output"
          cat "$raw" >&2
          exit 1
        fi
        ;;
      ab)
        if ! parse_ab "$raw"; then
          echo "failed to parse ab output"
          cat "$raw" >&2
          exit 1
        fi
        ;;
    esac
    printf '%10s req/s  avg %-9s p50 %-9s p90 %-9s p99 %-9s errors %s\n' \
      "$REQ" "$AVG" "$P50" "$P90" "$P99" "$ERRORS"
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
      "$label" "$round" "$REQ" "$AVG" "$P50" "$P90" "$P99" "$ERRORS" >>"$RESULTS_FILE"
  done 3<"$TARGETS_FILE"
  round=$((round + 1))
done

echo
echo "Summary (median of $ROUNDS round(s)):"
printf '  %-28s %14s %14s\n' "Target" "median req/s" "best req/s"
while IFS="$TAB" read -r label url <&3; do
  values=$(awk -F'\t' -v label="$label" '$1 == label { print $3 }' "$RESULTS_FILE" | sort -n)
  count=$(printf '%s\n' "$values" | awk 'NF { n++ } END { print n + 0 }')
  if [ "$count" -eq 0 ]; then
    median="-"
    best="-"
  else
    median=$(printf '%s\n' "$values" | awk '{ a[NR] = $1 } END {
      if (NR % 2) printf "%.2f", a[(NR + 1) / 2]
      else printf "%.2f", (a[NR / 2] + a[NR / 2 + 1]) / 2
    }')
    best=$(printf '%s\n' "$values" | awk '{ a[NR] = $1 } END { printf "%.2f", a[NR] }')
  fi
  printf '  %-28s %14s %14s\n' "$label" "$median" "$best"
  printf '%s\t%s\n' "$label" "$median" >>"$MEDIANS_FILE"
done 3<"$TARGETS_FILE"

target_count=$(awk 'END { print NR }' "$TARGETS_FILE")
if [ "$target_count" -eq 2 ]; then
  a_label=$(awk -F'\t' 'NR == 1 { print $1 }' "$MEDIANS_FILE")
  a_value=$(awk -F'\t' 'NR == 1 { print $2 }' "$MEDIANS_FILE")
  b_label=$(awk -F'\t' 'NR == 2 { print $1 }' "$MEDIANS_FILE")
  b_value=$(awk -F'\t' 'NR == 2 { print $2 }' "$MEDIANS_FILE")
  if [ "$a_value" != "-" ] && [ "$b_value" != "-" ]; then
    awk -v a="$a_value" -v b="$b_value" -v al="$a_label" -v bl="$b_label" \
      'BEGIN { if (a > 0) printf "\n%s throughput vs %s: %.2fx\n", bl, al, b / a }'
  fi
fi
