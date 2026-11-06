# AWS Deployment Guide

> **Date:** 2026-05-25 09:30 | **Status:** ACTIVE

## Architecture Overview

```
                            ┌─────────────────────────┐
                            │   Nginx (SSL, 80/443)   │
                            └───────────┬─────────────┘
                                        │
                    ┌───────────────────┼───────────────────┐
                    │                   │                   │
              ┌─────▼─────┐      ┌─────▼─────┐      ┌─────▼─────┐
              │ Portal    │      │ Proxy     │      │ Proxy     │
              │ :3001     │◄────►│ :8080     │      │ :8080     │
              │           │ sync │ (per-proxy│      │ (per-proxy│
              │ DB+Redis  │ 30s  │ key pool) │      │ key pool) │
              └───────────┘      └───────────┘      └───────────┘
```

**Key relationships:**
- Portal owns the single source of truth (PostgreSQL + Redis)
- Each proxy independently syncs key pool from Portal every 30s
- No proxy-to-proxy communication needed
- Proxy caches key validation results for 5 minutes
- All instances share same `ENCRYPTION_KEY`, `JWT_SECRET`, `PORTAL_SYNC_SECRET`

---

## Image Distribution (China-Friendly)

Docker Hub is slow/blocked in China. Use `docker save/load` for direct transfer:

```bash
# === On BUILD machine (current server) ===
cd ai-portal-go
docker build -t ai-portal-go:latest .
cd ../ai-proxy-go
docker build -t ai-proxy-go:latest .

docker save ai-portal-go:latest | gzip > /tmp/portal.tar.gz
docker save ai-proxy-go:latest | gzip > /tmp/proxy.tar.gz

# Transfer to deploy server (~100MB total)
scp /tmp/portal.tar.gz /tmp/proxy.tar.gz ec2-user@<deploy-ip>:/opt/

# === On DEPLOY machine (EC2) ===
cd /opt
gunzip -c portal.tar.gz | docker load
gunzip -c proxy.tar.gz | docker load
docker compose up -d
```

No Docker Hub, no GitHub clone, no Go compile on EC2. ~10x faster in China.

---

## 4-Stage Progressive Deployment

### Stage 1: Single EC2 (Current Phase — 10-100 Users)

**Everything on one machine.** Portal + Proxy + PostgreSQL + Redis + Nginx.

```
EC2 (t3.large, 2 vCPU, 8GB)
├── Nginx    (:80/443 → :3001, :8080)
├── Portal   (:3001)
├── Proxy    (:8080)
├── PostgreSQL (container)
└── Redis    (container)
```

**docker-compose.yml:**

```yaml
version: "3.8"
services:
  postgres:
    image: postgres:15-alpine
    environment:
      POSTGRES_DB: ai_proxy_portal
      POSTGRES_USER: portal_admin
      POSTGRES_PASSWORD: ${DB_PASSWORD}
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U portal_admin"]
      interval: 10s
      retries: 5

  redis:
    image: redis:7-alpine
    command: redis-server --appendonly yes
    volumes:
      - redisdata:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      retries: 5

  portal:
    image: ai-portal-go:latest
    ports:
      - "3001:3001"
    environment:
      - PORT=3001
      - DB_HOST=postgres
      - DB_PORT=5432
      - DB_NAME=ai_proxy_portal
      - DB_USER=portal_admin
      - DB_PASSWORD=${DB_PASSWORD}
      - REDIS_HOST=redis
      - REDIS_PORT=6379
      - JWT_SECRET=${JWT_SECRET}
      - ENCRYPTION_KEY=${ENCRYPTION_KEY}
      - ADMIN_PASSWORD=${ADMIN_PASSWORD}
      - ADMIN_MODE=production
      - PORTAL_SYNC_SECRET=${PORTAL_SYNC_SECRET}
      - ALLOWED_ORIGINS="https://your-domain.com"
    depends_on:
      postgres: { condition: service_healthy }
      redis:    { condition: service_healthy }

  proxy:
    image: ai-proxy-go:latest
    ports:
      - "8080:8080"
    environment:
      - PORT=8080
      - PORTAL_URL=http://portal:3001
      - PORTAL_SYNC_SECRET=${PORTAL_SYNC_SECRET}
      - REDIS_HOST=redis
      - REDIS_PORT=6379
      - JWT_SECRET=${JWT_SECRET}
    depends_on:
      portal: { condition: service_started }

  nginx:
    image: nginx:alpine
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./nginx.conf:/etc/nginx/conf.d/default.conf
      - /etc/letsencrypt:/etc/letsencrypt:ro
    depends_on:
      - portal
      - proxy

volumes:
  pgdata:
  redisdata:
```

**nginx.conf:**

```nginx
server {
    listen 443 ssl http2;
    server_name your-domain.com;
    ssl_certificate /etc/letsencrypt/live/your-domain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/your-domain.com/privkey.pem;

    location / {
        proxy_pass http://portal:3001;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location /v1/ {
        proxy_pass http://proxy:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 120s;
        proxy_buffering off;
    }
}
```

**.env:**

```bash
DB_PASSWORD=<secure-password>
JWT_SECRET=<64-char-hex>
ENCRYPTION_KEY=<32-byte-hex>
ADMIN_PASSWORD=<ADMIN_PASSWORD>
PORTAL_SYNC_SECRET=<proxy-sync-secret>
```

**EC2 security group:**

| Port | Source | Purpose |
|------|--------|---------|
| 22 | Your IP | SSH |
| 80 | 0.0.0.0/0 | HTTP → redirect to HTTPS |
| 443 | 0.0.0.0/0 | HTTPS → Portal + Proxy |
| 5432 | sg-self | PostgreSQL |
| 6379 | sg-self | Redis |

**Deploy script (`deploy.sh`):**

```bash
#!/bin/bash
set -e
cd /opt/ai-platform

echo "=== Loading images ==="
gunzip -c /tmp/portal.tar.gz | docker load
gunzip -c /tmp/proxy.tar.gz | docker load

echo "=== Restarting (downtime ~30s) ==="
docker compose down portal proxy nginx
docker compose up -d

echo "=== Health check ==="
sleep 10
curl -sf http://localhost:3001/api/health && echo "Portal: OK"
curl -sf http://localhost:8080/health && echo "Proxy: OK"
```

**Cost:** ~$50-80/month (t3.large EC2)

---

### Stage 2: Separate Database (100-500 Users)

**Move PostgreSQL to RDS for reliability + backup automation.** Portal + Proxy + Redis still on EC2.

```
┌─ EC2 (t3.medium) ───────────────┐     ┌─ AWS RDS ──────────────────┐
│ Nginx, Portal(:3001),           │     │ PostgreSQL 15              │
│ Proxy(:8080), Redis             │────►│ db.t3.medium, 20GB GP3     │
└─────────────────────────────────┘     │ Multi-AZ (optional)        │
                                        │ 7-day automated backup      │
                                        └────────────────────────────┘
```

**Changes from Stage 1:**
- Remove `postgres` from docker-compose.yml
- Update Portal env: `DB_HOST=<rds-endpoint>`, `DB_USER=<rds-user>`, `DB_PASSWORD=<rds-password>`
- No code changes needed

**docker-compose.yml (diff):**

```yaml
# REMOVE this service:
#   postgres:
#     image: postgres:15-alpine
#     ...

# UPDATE portal environment:
portal:
  environment:
    - DB_HOST=ai-portal-db.xxxx.ap-southeast-2.rds.amazonaws.com
    - DB_PORT=5432
    - DB_NAME=ai_proxy_portal
    - DB_USER=portal_admin
    - DB_PASSWORD=${DB_PASSWORD}   # RDS password, different from Stage 1
```

**RDS setup:**

```bash
aws rds create-db-instance \
  --db-instance-identifier ai-portal-db \
  --db-instance-class db.t3.medium \
  --engine postgres --engine-version 15 \
  --allocated-storage 20 --storage-type gp3 \
  --master-username portal_admin \
  --master-user-password <rds-password> \
  --vpc-security-group-ids <sg-id> \
  --backup-retention-period 7
```

**Migrate data from Stage 1:**

```bash
# Dump from old container
docker exec ai-portal-postgres pg_dump -U portal_admin ai_proxy_portal > /tmp/dump.sql

# Restore to RDS
psql -h <rds-endpoint> -U portal_admin -d ai_proxy_portal -f /tmp/dump.sql
```

**Cost:** ~$100-150/month (t3.medium EC2 + db.t3.medium RDS)

---

### Stage 3: Add Regional Proxies (500-2000 Users)

**Deploy additional proxy-only EC2 instances in regions with latency-sensitive users (e.g., China).** Portal stays on primary EC2.

```
                    ┌──────────────────────┐
                    │ Portal (Singapore)    │
                    │ :3001 + PostgreSQL    │
                    │ Redis                 │
                    └──────┬───────────────┘
                           │ key-pool/sync (REST, 30s)
              ┌────────────┼────────────────────┐
              │            │                    │
        ┌─────▼─────┐ ┌───▼────────┐  ┌────────▼──────┐
        │Proxy SG   │ │Proxy CN    │  │Proxy US       │
        │(same EC2) │ │(Beijing)   │  │(Oregon)       │
        └───────────┘ └────────────┘  └───────────────┘
```

**Proxy-only EC2 setup** (repeated for each region):

```
EC2 (t3.small, 2 vCPU, 2GB)
├── Proxy   (:8080)
├── Nginx   (:443 → :8080)
└── Redis   (container, rate limiting + nonce cache)
```

**docker-compose.proxy.yml:**

```yaml
version: "3.8"
services:
  redis:
    image: redis:7-alpine
    command: redis-server --appendonly yes

  proxy:
    image: ai-proxy-go:latest
    ports:
      - "8080:8080"
    environment:
      - PORT=8080
      - PORTAL_URL=https://portal.your-domain.com
      - PORTAL_SYNC_SECRET=${PORTAL_SYNC_SECRET}
      - REDIS_HOST=redis
      - REDIS_PORT=6379
      - JWT_SECRET=${JWT_SECRET}
    depends_on:
      - redis

  nginx:
    image: nginx:alpine
    ports:
      - "443:443"
    volumes:
      - ./nginx-proxy.conf:/etc/nginx/conf.d/default.conf
      - /etc/letsencrypt:/etc/letsencrypt:ro
```

**Proxy-only nginx.conf:**

```nginx
server {
    listen 443 ssl http2;
    server_name proxy.your-domain.com;
    ssl_certificate /etc/letsencrypt/live/proxy.your-domain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/proxy.your-domain.com/privkey.pem;

    location /v1/ {
        proxy_pass http://proxy:8080;
        proxy_set_header Host $host;
        proxy_read_timeout 120s;
        proxy_buffering off;
    }
}
```

**Key considerations for China proxy:**
- Portal URL: proxy in Beijing calls portal in Singapore → ~100ms RTT per key validation. Cached 5 minutes, so ~100ms penalty only on first request per key.
- Image transfer: use `docker save/load` + SCP (bypasses Docker Hub completely)
- HTTPS certificate: Let's Encrypt may be blocked in China → use aliyun free SSL cert or Cloudflare

**Cost:** ~$80/region/month (t3.small EC2 per proxy) + existing Stage 2 costs

---

### Stage 4: Multi-Region Portal (2000+ Users)

**Dual portal with PostgreSQL streaming replication.** China portal = writer (admin writes go here). Overseas portal = reader (faster key validation for non-CN proxies).

```
┌─ Portal CN (Beijing) ───────┐    streaming    ┌─ Portal SG (Singapore) ────┐
│ :3001, Redis, PostgreSQL    │◄───────────────►│ :3001, Redis, PostgreSQL   │
│ (writer — admin UI here)    │   replication   │ (reader — faster for SG/US) │
└──────────┬──────────────────┘                 └──────────┬─────────────────┘
           │                                               │
    Proxy CN-1, CN-2                              Proxy SG, Proxy US
```

**PostgreSQL streaming replication setup:**

On writer (CN):
```sql
-- postgresql.conf
wal_level = replica
max_wal_senders = 5
wal_keep_size = 1024    -- 1GB WAL retention

-- pg_hba.conf
host replication replicator <reader-ip>/32 md5
```

On reader (SG):
```bash
# Initial base backup
pg_basebackup -h <writer-host> -U replicator -D /var/lib/postgresql/data -P -R

# standby.signal file tells PostgreSQL this is a replica
touch /var/lib/postgresql/data/standby.signal

# postgresql.conf
primary_conninfo = 'host=<writer-host> port=5432 user=replicator password=<rep-password>'
hot_standby = on
```

**Portal config on reader:**

```bash
# Reader portal: connects to local read-replica DB
DB_HOST=localhost
DB_NAME=ai_proxy_portal
DB_USER=portal_reader
# Admin writes disabled — redirect to CN portal
ADMIN_REDIRECT_URL=https://portal-cn.your-domain.com
```

**Cost:** ~$250-400/month (2x RDS Multi-AZ + 2x EC2 + proxies)

---

## Secrets Management

All instances must share the same secrets. Use `.env` files initially, migrate to AWS Secrets Manager later.

| Secret | Purpose | Same everywhere? |
|--------|---------|:---:|
| `ENCRYPTION_KEY` | Encrypt/decrypt upstream keys in DB | ✅ Yes |
| `JWT_SECRET` | Sign/verify auth tokens (login, API) | ✅ Yes |
| `PORTAL_SYNC_SECRET` | Proxy→Portal auth for key sync | ✅ Yes |
| `ADMIN_PASSWORD` | Default admin password (first boot only) | ✅ Yes |
| `DB_PASSWORD` | PostgreSQL connection | Per-instance |
| `ADMIN_SECRET` | Proxy admin API auth | Per-instance |

**Generating secrets:**

```bash
openssl rand -hex 32   # JWT_SECRET (64 chars)
openssl rand -hex 16   # ENCRYPTION_KEY (32 hex chars = 16 bytes)
openssl rand -hex 24   # PORTAL_SYNC_SECRET, ADMIN_SECRET
```

**Key transmission to proxy:** Portal's sync API decrypts upstream keys with `ENCRYPTION_KEY` and sends plaintext over HTTPS. The sync endpoint is protected by `x-proxy-secret` header. Only HTTPS is needed between portal and proxy — no additional encryption.

---

## Deployment Comparison

| Factor | Stage 1 | Stage 2 | Stage 3 | Stage 4 |
|--------|---------|---------|---------|---------|
| Portal instances | 1 | 1 | 1 | 2 (writer+reader) |
| Proxy instances | 1 (same EC2) | 1 (same EC2) | 3-5 (regional) | 5+ (regional) |
| DB | Container | RDS | RDS | RDS + replica |
| Redis | Container | Container | Per-proxy | Per-portal |
| Zero-downtime deploy | ❌ ~30s | ❌ ~30s | ✅ per-proxy | ✅ per-portal |
| China-friendly images | docker load | docker load | docker load | docker load |
| Max users | 10-100 | 100-500 | 500-2000 | 2000+ |
| Monthly cost | ~$50-80 | ~$100-150 | ~$200-350 | ~$250-400 |

---

## Checklist Before First Deploy

- [ ] Push all commits: `ai-portal-go` + `ai-proxy-go` to GitHub
- [ ] Generate all secrets: `JWT_SECRET`, `ENCRYPTION_KEY`, `PORTAL_SYNC_SECRET`
- [ ] Set `ADMIN_MODE=production`
- [ ] Set `ALLOWED_ORIGINS` to your actual domain (not `*`)
- [ ] Configure SSL (Let's Encrypt certbot)
- [ ] Test: `docker compose up -d` locally first
- [ ] Build images: `docker build` on build machine
- [ ] Transfer images: `docker save | gzip | scp` to EC2
- [ ] Verify health: `curl /api/health` and `curl /health`
- [ ] Create test API key + test proxy call
- [ ] Set up DB backup cron: `pg_dump` daily to S3
