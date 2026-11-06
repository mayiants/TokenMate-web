#!/bin/bash
set -e
PORTAL_URL="http://127.0.0.1:3001"
COOKIE_JAR="/tmp/e2e_apikeys_cookies.txt"
RAW=/tmp/e2e_apikeys_resp.json
BODY=/tmp/e2e_apikeys_body.html
PASS=0
FAIL=0

rm -f "$COOKIE_JAR" "$RAW" "$BODY"

red() { printf "\033[0;31m%s\033[0m\n" "$1"; }
green() { printf "\033[0;32m%s\033[0m\n" "$1"; }
ok() { green "PASS: $1"; PASS=$((PASS+1)); }
bad() { red "FAIL: $1"; FAIL=$((FAIL+1)); }

assert_eq() {
  local label="$1" actual="$2" expected="$3"
  if [ "$actual" = "$expected" ]; then ok "$label (got $actual)"; else bad "$label (expected $expected, got $actual)"; fi
}

http_code() {
  curl -s -o /dev/null -w "%{http_code}" "$@"
}


code=$(http_code "$PORTAL_URL/api/health")
assert_eq "GET /api/health (no auth) -> 200" "$code" "200"

code=$(http_code "$PORTAL_URL/login")
assert_eq "GET /login -> 200" "$code" "200"

code=$(http_code "$PORTAL_URL/register")
assert_eq "GET /register -> 200" "$code" "200"

code=$(http_code "$PORTAL_URL/dashboard")
assert_eq "GET /dashboard (no auth) -> 302" "$code" "302"

code=$(http_code "$PORTAL_URL/apikeys")
assert_eq "GET /apikeys (no auth) -> 302" "$code" "302"


LOGIN_RESP=$(curl -s -X POST "$PORTAL_URL/api/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"Admin@123456!"}')
TOKEN=$(echo "$LOGIN_RESP" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('token',''))" 2>/dev/null || echo "")
if [ -n "$TOKEN" ] && [ "${#TOKEN}" -gt 50 ]; then
  ok "POST /api/auth/login returns JWT (len=${#TOKEN})"
else
  bad "POST /api/auth/login did not return JWT: $LOGIN_RESP"
  echo
  echo "=== Test summary: $PASS passed, $FAIL failed ==="
  exit 1
fi

code=$(http_code -H "Authorization: Bearer $TOKEN" "$PORTAL_URL/api/apikeys")
assert_eq "GET /api/apikeys (Bearer) -> 200" "$code" "200"


rm -f "$COOKIE_JAR"
curl -s -c "$COOKIE_JAR" -o "$BODY" "$PORTAL_URL/login" >/dev/null
CSRF_TOKEN=$(grep -oE 'name="csrf_token" value="[^"]+"' "$BODY" | head -1 | sed 's/.*value="\([^"]*\)".*/\1/')
if [ -n "$CSRF_TOKEN" ]; then
  ok "GET /login returned csrf_token in form"
else
  bad "GET /login did not return csrf_token in form"
fi

curl -s -b "$COOKIE_JAR" -c "$COOKIE_JAR" -o /dev/null \
  -X POST "$PORTAL_URL/login" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "HX-Request: true" \
  --data-urlencode "csrf_token=$CSRF_TOKEN" \
  --data-urlencode "email=admin@example.com" \
  --data-urlencode "password=Admin@123456!" >/dev/null

if grep -qE "127\.0\.0\.1\s.*\sjwt\s" "$COOKIE_JAR"; then
  ok "POST /login (form) set jwt cookie"
else
  bad "POST /login (form) did not set jwt cookie"
  cat "$COOKIE_JAR" >&2
fi


code=$(http_code -b "$COOKIE_JAR" -c "$COOKIE_JAR" "$PORTAL_URL/dashboard")
assert_eq "GET /dashboard (cookie auth) -> 200" "$code" "200"

code=$(http_code -b "$COOKIE_JAR" -c "$COOKIE_JAR" "$PORTAL_URL/apikeys")
assert_eq "GET /apikeys (cookie auth) -> 200" "$code" "200"

curl -s -b "$COOKIE_JAR" -o "$BODY" "$PORTAL_URL/apikeys"
if grep -qE 'id="settings-drawer"|key-card-meta-usage' "$BODY"; then
  ok "/apikeys page contains new UI elements (settings drawer / key-card-meta-usage)"
else
  bad "/apikeys page missing new UI elements"
fi


KEY_NAME="e2e-test-$(date +%s)"

CREATE_RESP=$(curl -s -X POST "$PORTAL_URL/api/apikeys" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"$KEY_NAME\",\"key_type\":\"general\"}")
KEY_ID=$(echo "$CREATE_RESP" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('id',''))" 2>/dev/null || echo "")
RAW_KEY=$(echo "$CREATE_RESP" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('key',''))" 2>/dev/null || echo "")

if [ -n "$KEY_ID" ] && [ -n "$RAW_KEY" ]; then
  ok "POST /api/apikeys created key id=$KEY_ID (raw_key len=${#RAW_KEY})"
else
  bad "POST /api/apikeys did not return id+key: $CREATE_RESP"
  echo
  echo "=== Test summary: $PASS passed, $FAIL failed ==="
  exit 1
fi

code=$(http_code -H "Authorization: Bearer $TOKEN" "$PORTAL_URL/api/apikeys/$KEY_ID")
assert_eq "GET /api/apikeys/:id -> 200" "$code" "200"

code=$(http_code -X PATCH "$PORTAL_URL/api/apikeys/$KEY_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status":"disabled"}')
assert_eq "PATCH /api/apikeys/:id (status=disabled) -> 200" "$code" "200"

STATUS=$(curl -s -H "Authorization: Bearer $TOKEN" "$PORTAL_URL/api/apikeys/$KEY_ID" | \
  python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('key',{}).get('status',''))" 2>/dev/null)
assert_eq "GET /api/apikeys/:id key.status after patch" "$STATUS" "disabled"

code=$(http_code -X PATCH "$PORTAL_URL/api/apikeys/$KEY_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status":"active"}')
assert_eq "PATCH /api/apikeys/:id (status=active) -> 200" "$code" "200"

NEW_NAME="${KEY_NAME}-renamed"
code=$(http_code -X PATCH "$PORTAL_URL/api/apikeys/$KEY_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"$NEW_NAME\"}")
assert_eq "PATCH /api/apikeys/:id (rename) -> 200" "$code" "200"

code=$(http_code -X DELETE "$PORTAL_URL/api/apikeys/$KEY_ID" \
  -H "Authorization: Bearer $TOKEN")
assert_eq "DELETE /api/apikeys/:id -> 200" "$code" "200"

code=$(http_code -H "Authorization: Bearer $TOKEN" "$PORTAL_URL/api/apikeys/$KEY_ID")
assert_eq "GET /api/apikeys/:id after delete -> 404" "$code" "404"


code=$(http_code "$PORTAL_URL/api/apikeys")
assert_eq "GET /api/apikeys (no auth) -> 401" "$code" "401"

code=$(http_code -X POST "$PORTAL_URL/api/apikeys" \
  -H "Content-Type: application/json" \
  -d '{"name":"unauth"}')
assert_eq "POST /api/apikeys (no auth) -> 401" "$code" "401"


code=$(http_code -H "Authorization: Bearer not.a.real.jwt" "$PORTAL_URL/api/apikeys")
assert_eq "GET /api/apikeys (invalid JWT) -> 401" "$code" "401"


code=$(http_code -X POST "$PORTAL_URL/login" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "HX-Request: true" \
  --data-urlencode "email=admin@example.com" \
  --data-urlencode "password=Admin@123456!")
if [ "$code" -ge 400 ] && [ "$code" -lt 500 ]; then
  ok "POST /login without csrf_token rejected (4xx, got $code)"
else
  bad "POST /login without csrf_token returned $code (expected 4xx)"
fi

echo
echo "=== Test summary: $PASS passed, $FAIL failed ==="
rm -f "$COOKIE_JAR" "$RAW" "$BODY"
[ "$FAIL" -eq 0 ] || exit 1