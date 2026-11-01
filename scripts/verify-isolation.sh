#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
binary="$repo_root/bin/movies-api"
tmpdir=
server_pid=
cleanup() {
	if [ -n "$server_pid" ]; then
		kill "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	if [ -n "$tmpdir" ]; then rm -rf "$tmpdir"; fi
}
trap cleanup EXIT HUP INT TERM
fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

[ -x "$binary" ] || fail "missing executable $binary; run make build"
set -a
# shellcheck disable=SC1091
. "$repo_root/.env"
set +a
DB_HOST=127.0.0.1
export DB_HOST
APP_PORT=${VERIFY_APP_PORT:-18080}
export APP_PORT

printf 'Binary size: '
du -h "$binary" | awk '{print $1}'
case "$(uname -s)" in
	Linux)
		info=$(file "$binary")
		printf '%s\n' "$info"
		case "$info" in *static*) ;; *) fail "binary is not statically linked" ;; esac
		if command -v ldd >/dev/null 2>&1; then
			info=$(ldd "$binary" 2>&1 || true)
			printf '%s\n' "$info"
			case "$info" in *"not a dynamic executable"*|*"statically linked"*) ;; *) fail "ldd did not confirm static linking" ;; esac
		fi
		;;
	Darwin)
		file "$binary"
		deps=$(otool -L "$binary")
		printf '%s\n' "$deps"
		unexpected=$(printf '%s\n' "$deps" | sed '1d' | grep -v '^[[:space:]]*/usr/lib/\|^[[:space:]]*/System/Library/Frameworks/' || true)
		[ -z "$unexpected" ] || fail "unexpected non-system dependencies: $unexpected"
		printf 'PASS: CGO-disabled binary has only macOS system runtime dependencies\n'
		;;
	*) fail "no static-link inspection method configured for $(uname -s)" ;;
esac

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/movies-api-isolation.XXXXXX")
cp "$binary" "$tmpdir/movies-api"
[ "$(find "$tmpdir" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ')" = 1 ] || fail "run directory contains files besides the binary"
cd "$tmpdir"

run_mode() {
	mode=$1
	base="http://127.0.0.1:$APP_PORT"
	log="$tmpdir/server-$mode.log"
	env DB_HOST="$DB_HOST" DB_PORT="$DB_PORT" DB_USER="$DB_USER" DB_PASSWORD="$DB_PASSWORD" \
		DB_NAME="$DB_NAME" DB_SSLMODE="${DB_SSLMODE:-disable}" JWT_SECRET="$JWT_SECRET" \
		APP_PORT="$APP_PORT" WEB_UI_ENABLED="$mode" "$tmpdir/movies-api" >"$log" 2>&1 &
	server_pid=$!
	ready=false
	i=0
	while [ "$i" -lt 60 ]; do
		if curl -fsS "$base/health" >/dev/null 2>&1; then ready=true; break; fi
		if ! kill -0 "$server_pid" 2>/dev/null; then break; fi
		i=$((i + 1)); sleep 1
	done
	[ "$ready" = true ] || { cat "$log" >&2; fail "server did not become healthy in WEB_UI_ENABLED=$mode mode"; }

	if [ "$mode" = true ]; then
		curl -fsS -D "$tmpdir/catalog.headers" "$base/catalog" -o "$tmpdir/catalog.body" || fail "GET /catalog failed"
		grep -qi '^HTTP/.* 200' "$tmpdir/catalog.headers" || fail "catalog status was not 200"
		grep -qi '^Content-Type: text/html' "$tmpdir/catalog.headers" || fail "catalog content type was not text/html"
		grep -q '<!DOCTYPE html>' "$tmpdir/catalog.body" || fail "catalog lacks layout markup"
		for asset in css/style.css js/htmx.min.js js/app.js; do
			curl -fsS -D "$tmpdir/asset.headers" "$base/static/$asset" -o "$tmpdir/asset.body" || fail "GET /static/$asset failed"
			grep -qi '^HTTP/.* 200' "$tmpdir/asset.headers" || fail "/static/$asset status was not 200"
			case "$asset" in
				css/*) grep -qi '^Content-Type: text/css' "$tmpdir/asset.headers" || fail "CSS content type was incorrect" ;;
				js/htmx.min.js) grep -Eqi '^Content-Type: (application|text)/(javascript|x-javascript)' "$tmpdir/asset.headers" || fail "HTMX content type was not JavaScript" ;;
			esac
		done
	else
		code=$(curl -sS -o "$tmpdir/catalog.body" -w '%{http_code}' "$base/catalog") || fail "GET /catalog failed"
		[ "$code" = 404 ] || fail "API-only /catalog returned $code, expected 404"
		grep -q '"error"' "$tmpdir/catalog.body" || fail "API-only 404 was not JSON"
	fi
	curl -fsS -D "$tmpdir/api.headers" "$base/api/v1/movies" -o "$tmpdir/api.body" || fail "GET /api/v1/movies failed"
	grep -qi '^HTTP/.* 200' "$tmpdir/api.headers" || fail "movies API status was not 200"
	grep -qi '^Content-Type: application/json' "$tmpdir/api.headers" || fail "movies API did not return JSON"
	tr -d '[:space:]' <"$tmpdir/api.body" | grep -Eq '^\[.*\]$' || fail "movies API response was not a JSON array"
	kill "$server_pid" 2>/dev/null || true
	wait "$server_pid" 2>/dev/null || true
	server_pid=
	i=0
	while [ "$i" -lt 20 ]; do
		if ! curl -fsS "$base/health" >/dev/null 2>&1; then break; fi
		i=$((i + 1)); sleep 0.25
	done
	[ "$i" -lt 20 ] || fail "previous server did not release port $APP_PORT"
	printf 'PASS: isolation checks with WEB_UI_ENABLED=%s\n' "$mode"
}

run_mode true
run_mode false
printf 'PASS: single-binary isolation verification complete\n'
