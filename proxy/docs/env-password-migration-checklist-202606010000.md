# 🔐 Environment Password & Key Migration Checklist

> **⚠️ HISTORICAL** — Migration completed; kept for reference

> **⚠️ CRITICAL**: This document contains sensitive credentials. Store securely. Do NOT commit to git.

**Generated**: 2026-06-01  
**Source**: Production containers (`ai-portal-go`, `ai-proxy-go`)

---

## Legend

| Icon | Meaning |
|------|---------|
| 🔒 **FROZEN** | Must NEVER change. Data loss / user impact if changed. |
| ⚠️ **CAUTION** | Can change, but causes user impact (re-login, etc). |
| ✅ **SAFE** | Can change freely. Just sync both sides. |

---

## 1. AI-Portal-Go (`.env`)

| # | Key | Current Value (masked) | Status | Impact if Changed |
|---|-----|----------------------|--------|-------------------|
| 1 | `DB_PASSWORD` | `66ededab2a...cf69` (64 chars) | ✅ SAFE | Only affects DB connection. Change via `ALTER USER` inside Postgres first. |
| 2 | `REDIS_PASSWORD` | `52e024f824...fa09` (64 chars) | ✅ SAFE | Sync to both Portal and Proxy `.env`. |
| 3 | `JWT_SECRET` | `7a2224f4d3...28d8` (64 chars) | ⚠️ CAUTION | All logged-in users kicked out. Must re-login. Must be **identical** across Portal + all Proxy nodes. |
| 4 | `ENCRYPTION_KEY` | `4dc937a920...ea19` (64 chars) | 🔒 **FROZEN** | **ALL user API keys become permanently unreadable.** Cannot recover. |
| 5 | `PROXY_SYNC_SECRET` | `9e969d2d61...3e8c` (64 chars) | ✅ SAFE | Portal ↔ Proxy sync breaks if not identical. Just sync both sides. |
| 6 | `ADMIN_SECRET` | `9e969d2d61...3e8c` (64 chars) | ✅ SAFE | Admin endpoints (`/admin/*`, `/metrics`) auth changes. |
| 7 | `ADMIN_PASSWORD` | `<ADMIN_PASSWORD>` | ✅ SAFE | Only affects admin web login. |
| 8 | `SMTP_PASSWORD` | `hR6pUfHUfMkb4ng8` | ✅ SAFE | Only affects email sending. |

---

## 2. AI-Proxy-Go (`.env`)

| # | Key | Current Value (masked) | Status | Impact if Changed |
|---|-----|----------------------|--------|-------------------|
| 1 | `JWT_SECRET` | `7a2224f4d3...28d8` (64 chars) | ⚠️ CAUTION | Must be **exactly identical** to Portal's `JWT_SECRET`. |
| 2 | `ENCRYPTION_KEY` | `4dc937a920...ea19` (64 chars) | 🔒 **FROZEN** | Must be **exactly identical** to Portal's `ENCRYPTION_KEY`. |
| 3 | `PROXY_SYNC_SECRET` | `9e969d2d61...3e8c` (64 chars) | ✅ SAFE | Must be **exactly identical** to Portal's `PROXY_SYNC_SECRET`. |
| 4 | `ADMIN_SECRET` | `9e969d2d61...3e8c` (64 chars) | ✅ SAFE | Can reuse `PROXY_SYNC_SECRET` or set independently. |
| 5 | `REDIS_PASSWORD` | `52e024f824...fa09` (64 chars) | ✅ SAFE | Optional. Empty = memory-only mode. |

---

## 3. Cross-Node Consistency Matrix

These 4 keys **MUST be bit-for-bit identical** across Portal and every Proxy node:

```
┌─────────────────────┬────────────────────────────────────────────┐
│ Key                 │ Verification Command                         │
├─────────────────────┼────────────────────────────────────────────┤
│ JWT_SECRET          │ docker exec ai-portal-go env | grep JWT_     │
│                     │ docker exec ai-proxy-go env | grep JWT_      │
├─────────────────────┼────────────────────────────────────────────┤
│ ENCRYPTION_KEY      │ docker exec ai-portal-go env | grep ENCRYPT  │
│                     │ docker exec ai-proxy-go env | grep ENCRYPT   │
├─────────────────────┼────────────────────────────────────────────┤
│ PROXY_SYNC_SECRET   │ docker exec ai-portal-go env | grep PROXY_S  │
│                     │ docker exec ai-proxy-go env | grep PROXY_S   │
├─────────────────────┼────────────────────────────────────────────┤
│ ADMIN_SECRET        │ docker exec ai-portal-go env | grep ADMIN_S  │
│                     │ docker exec ai-proxy-go env | grep ADMIN_S   │
└─────────────────────┴────────────────────────────────────────────┘
```

---

## 4. Migration Copy-Paste Checklist

### Step 1 — Backup (MUST DO)
```bash
/home/ubuntu/workspace/ai-portal-go/scripts/backup-db.sh
```

### Step 2 — Copy These Files to New Server
```bash
# Portal
/home/ubuntu/workspace/ai-portal-go/.env
/home/ubuntu/workspace/ai-portal-go/docker-compose.portal.yml

# Proxy
/home/ubuntu/workspace/ai-proxy-go/.env
/home/ubuntu/workspace/ai-proxy-go/docker-compose.proxy.yml

# Nginx + SSL
/etc/nginx/sites-available/api.example.com
/etc/nginx/ssl/example.com.pem
/etc/nginx/ssl/example.com.key

# Latest DB backup
/home/ubuntu/backups/portal_$(ls /home/ubuntu/backups/ | tail -1)
```

### Step 3 — Verify 4 Critical Keys Match
```bash
# On new server, after starting containers:
docker exec ai-portal-go env | grep -E "JWT_SECRET|ENCRYPTION_KEY|PROXY_SYNC_SECRET|ADMIN_SECRET" | sort
docker exec ai-proxy-go env | grep -E "JWT_SECRET|ENCRYPTION_KEY|PROXY_SYNC_SECRET|ADMIN_SECRET" | sort
# Compare outputs — must be identical line-by-line
```

### Step 4 — Smoke Test
```bash
# Test API key validation
curl -s -H "Authorization: Bearer YOUR_TEST_API_KEY" \
  http://127.0.0.1:3001/api/apikeys/validate | jq .

# Expected: {"valid":true,...}
```

---

## 5. If You MUST Change Passwords

| Password | How to Change Safely |
|----------|---------------------|
| `DB_PASSWORD` | 1. `docker exec ai-portal-postgres psql -U postgres -c "ALTER USER postgres WITH PASSWORD '新密码';"` <br>2. Update `.env` <br>3. `docker compose up -d` |
| `REDIS_PASSWORD` | 1. Update `.env` in both Portal and Proxy <br>2. `docker compose up -d` (will restart Redis) |
| `JWT_SECRET` | 1. Choose a maintenance window <br>2. Update `.env` on Portal + ALL Proxy nodes <br>3. Restart all services <br>4. All users must re-login |
| `ENCRYPTION_KEY` | ❌ **DO NOT CHANGE.** If leaked, rotate by: <br>1. Decrypt all keys with old key <br>2. Re-encrypt with new key <br>3. Update `.env` <br>This requires custom script — ask for help. |
| `PROXY_SYNC_SECRET` | 1. Update `.env` on Portal + ALL Proxy nodes <br>2. Restart all services |
| `ADMIN_SECRET` | 1. Update `.env` on Portal + ALL Proxy nodes <br>2. Restart all services |

---

## 6. Disaster Recovery Contacts

| Scenario | Command |
|----------|---------|
| Database corrupted | `gunzip < /home/ubuntu/backups/portal_xxx.sql.gz \| docker exec -i ai-portal-postgres psql -U postgres -d ai_proxy_portal` |
| All API keys broken | Check `ENCRYPTION_KEY` mismatch. If changed by accident, **restore from backup** immediately. |
| 401 on all requests | Check `JWT_SECRET` / `PROXY_SYNC_SECRET` mismatch between Portal and Proxy. |

---

**Keep this file offline. Delete after migration is complete.** 🔥
