# Singapore Migration — Stage 2 (Single EC2 Portal+Proxy + RDS)

> **Date:** 2026-07-11 05:42 UTC | **Status:** ACTIVE / ready-to-execute
> **Scope:** Migrate the dev EC2 (`ap-southeast-2`) to Singapore (`ap-southeast-1`).
> **Topology (initial):** ONE EC2 running Portal + Proxy + Redis + Nginx, PLUS ONE RDS PostgreSQL. Portal/Proxy are intentionally **not** split yet.
> **Source of truth:** this doc + the encrypted local-config bundle produced on the source host (`local-config.secure.enc`).

This is exactly **Stage 2** of `aws-deployment-guide-202605250930.md`. The optional Cloudflare path-routing split of Portal/Proxy onto two EC2 (`cloudflare-portal-proxy-split-202607100703.md`) is explicitly **deferred** and orthogonal to the DB move.

---

## 0. Non-obvious facts (read first)

- **Real config entrypoint is `ai-portal-go/.env.go-platform`** — not `.env`, not `docker-compose.portal.yml`. `start.sh` regenerates the compose file from `.env.go-platform` on every start; hand-edits to the compose file are overwritten (see `aurora-postgresql-migration-guide.md` §2/§8).
- **Proxy does not connect to PostgreSQL** — it uses Redis + Portal HTTP only. So **RDS serves Portal only**; Proxy config needs no DB change.
- **These four secrets must be byte-identical on Portal and Proxy, and `ENCRYPTION_KEY` must never change** (rotating it makes every stored upstream API key undecryptable): `ENCRYPTION_KEY`, `JWT_SECRET`, `PROXY_SYNC_SECRET`, `ADMIN_SECRET`. Copy them verbatim from the secure bundle.
- **`start.sh` auto-detects DB mode from `DB_HOST`**: `postgres` / `localhost` / empty → local container PG; anything else (e.g. `*.rds.amazonaws.com`) → external, no local PG container, Portal connects straight to the endpoint (`is_external_db()`).
- **RDS requires TLS**: minimum `DB_SSLMODE=require`; production target is `DB_SSLMODE=verify-full` + `DB_SSL_ROOT_CERT=/path/rds-ca.pem`.
- **Public domain is `api.woodgit.com`**; `APP_BASE_URL` must be `https://api.woodgit.com` (used in password-reset / enterprise-invite emails; must not be an IP). TLS cert/key (`woodgit.com.pem.txt` / `woodgit.com.key.txt`) are in the secure bundle.
- **Enterprise uploads are currently `FILE_STORAGE_PROVIDER=local` (`./uploads` bind-mounted on the host).** Fine for the initial single-EC2 cutover, but EBS is not real durability — plan to move to S3 (`FILE_STORAGE_REGION` already defaults to `ap-southeast-1`, bucket `ai-portal-uploads`).

---

## 1. Pre-flight (on the source host)

1. **Rotate the leaked GitHub PAT.** The `origin` remote URL in both repos embeds a `ghp_…` token in `.git/config`. Revoke it at GitHub, then:
   ```bash
   cd ~/workspace/ai-portal-go && git remote set-url origin https://github.com/mayiants/ai-portal-go.git
   cd ~/workspace/ai-proxy-go  && git remote set-url origin https://github.com/mayiants/ai-proxy-go.git
   ```
   Both repos were verified `0 ahead / 0 behind / clean tree` against `origin/main`, so `.git` need not be transferred — re-clone on the new host.
2. **Keep the encrypted bundle** `local-config.secure.enc` (contains `ssl/*`, `.ssh/authorized_keys`, both projects' `.env` / `.env.go-platform` / `.env.template`, `ai-proxy-go/config.yaml`, `.npmrc`, `.bashrc`, and a manifest). Transfer it to the new EC2 out-of-band (scp / SSM / encrypted S3). **Never commit it.**
3. **Confirm GitHub is current** (both repos pushed) so the new host can `git clone`.

---

## 2. Create the RDS (ap-southeast-1)

Mechanism is identical to the Aurora path in `aurora-postgresql-migration-guide.md`; only the service differs (RDS PostgreSQL vs Aurora). Keep engine **15.x**, initial DB `ai_proxy_portal`, master user `postgres` to match local and minimize change.

```bash
# ---- placeholders ----
REGION=ap-southeast-1
VPC_ID=<SG_VPC_ID>
EC2_SG=<SG_FOR_EC2>            # security group id of the app EC2 (create in §4)
RDS_SG=<SG_FOR_RDS>            # security group id for RDS (create below)
SUBNET_GROUP=<DB_SUBNET_GROUP> # spans >= 2 AZs in the VPC
DB_ID=ai-portal-db
DB_NAME=ai_proxy_portal
DB_USER=postgres
DB_PASSWORD=<SET_A_STRONG_RDS_PASSWORD>   # NEW value; also written into .env.go-platform

# 2.1 RDS security group: inbound 5432 ONLY from the EC2 SG, no public access
aws ec2 create-security-group --region $REGION --group-name ai-portal-rds-sg \
  --description "Portal EC2 -> RDS 5432 only" --vpc-id $VPC_ID
RDS_SG=<returned-sg-id>
aws ec2 authorize-security-group-ingress --region $REGION --group-id $RDS_SG \
  --ip-permissions "IpProtocol=tcp,FromPort=5432,ToPort=5432,UserIdGroupPairs=[{GroupId=$EC2_SG}]"

# 2.2 DB subnet group (private subnets across >=2 AZs) — create via console or:
# aws rds create-db-subnet-group --region $REGION --db-subnet-group-name ai-portal-db-subnet \
#   --db-subnet-group-description "private" --subnet-ids <subnet-a> <subnet-b>

# 2.3 Create the instance
aws rds create-db-instance --region $REGION \
  --db-instance-identifier $DB_ID \
  --db-instance-class db.t3.medium \
  --engine postgres --engine-version 15 \
  --allocated-storage 20 --storage-type gp3 \
  --master-username $DB_USER --master-user-password "$DB_PASSWORD" \
  --db-name $DB_NAME \
  --db-subnet-group-name $SUBNET_GROUP \
  --vpc-security-group-ids $RDS_SG \
  --no-publicly-accessible \
  --backup-retention-period 7 \
  --deletion-protection \
  --storage-encrypted

# 2.4 Wait + capture the writer endpoint
aws rds wait db-instance-available --region $REGION --db-instance-identifier $DB_ID
RDS_ENDPOINT=$(aws rds describe-db-instances --region $REGION --db-instance-identifier $DB_ID \
  --query 'DBInstances[0].Endpoint.Address' --output text)
echo "$RDS_ENDPOINT"

# 2.5 Download the RDS CA bundle (for DB_SSLMODE=verify-full)
sudo mkdir -p /opt/ai-platform/certs
sudo curl -fsSL -o /opt/ai-platform/certs/rds-ca.pem \
  https://truststore.pki.rds.amazonaws.com/$REGION/$REGION-bundle.pem
```

Notes: ensure `max_connections >= DB_POOL_MAX` (default 20) via a parameter group if you raise the pool; enable Performance Insights optionally.

---

## 3. Migrate existing data (OPTIONAL — skip for a fresh start)

If the dev data should come along, use the data-only path (`aurora-postgresql-migration-guide.md` §5): Portal auto-creates the schema on the empty RDS DB, then only data is loaded. Redis is cache/rate-limit — **do not migrate it** (key pool rebuilds from DB sync).

```bash
# 3.1 SOURCE host: safety snapshot + data-only dump (custom format)
cd ~/workspace/ai-portal-go && ./start.sh snapshot
docker exec ai-portal-postgres pg_dump -U postgres -d ai_proxy_portal \
  --data-only --no-owner --no-acl -Fc > /tmp/portal_data.dump
# copy /tmp/portal_data.dump to the new EC2 (scp / SSM / S3)

# 3.2 NEW host: point .env.go-platform at RDS (see §5), let Portal InitSchema on empty DB
cd /opt/ai-platform/ai-portal-go
./start.sh rebuild            # creates tables on empty RDS; business tables empty
./start.sh stop-portal        # quiesce writes before load

# 3.3 NEW host: load data via a throwaway client container (no psql install needed)
docker run --rm -i --network host \
  -e PGPASSWORD="$DB_PASSWORD" -e PGSSLMODE=verify-full \
  -e PGSSLROOTCERT=/opt/ai-platform/certs/rds-ca.pem \
  postgres:15-alpine pg_restore \
  "host=$RDS_ENDPOINT port=5432 user=postgres dbname=ai_proxy_portal" \
  --no-owner --no-acl < /tmp/portal_data.dump

./start.sh portal
```

Rollback: source local volume `ai-portal-go_postgres-data` is untouched; set `DB_HOST=postgres` + `./start.sh rebuild` to revert (note writes made after the RDS cutover live only in RDS — dump back first if you must keep them).

---

## 4. Provision the Singapore EC2 (app host)

Sizing: `t3.large` (2 vCPU / 8 GB) matches Stage-1; `t3.medium` is the Stage-2 documented floor. Ubuntu 22.04/24.04 (matches the existing `setup*.sh`). EBS gp3 ≥ 40 GB.

EC2 security group (`$EC2_SG`):

| Port | Source | Purpose |
|------|--------|---------|
| 22 | `<YOUR_IP>/32` | SSH |
| 80 | `0.0.0.0/0` (or Cloudflare IP ranges) | HTTP → redirect |
| 443 | `0.0.0.0/0` (or Cloudflare IP ranges) | HTTPS → Nginx |
| 3001 / 8080 | **none public** | only loopback behind Nginx |

### 4.1 EC2 user-data (OS + Docker only — no secrets here)

Keep secret handling out of user-data; it runs after boot (§5). This script is idempotent enough to re-run.

```bash
#!/bin/bash
# Ubuntu 22.04/24.04 bootstrap for the Portal+Proxy app host (ap-southeast-1)
set -euxo pipefail
exec > >(tee -a /var/log/ai-platform-bootstrap.log) 2>&1

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y ca-certificates curl wget git unzip tar gnupg lsb-release jq awscli

# Docker (official)
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
  https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" \
  > /etc/apt/sources.list.d/docker.list
apt-get update -y
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
systemctl enable --now docker
usermod -aG docker ubuntu

mkdir -p /opt/ai-platform && chown -R ubuntu:ubuntu /opt/ai-platform

# Go is NOT required on the host: images build inside Docker (golang:1.25).
# Install only if you intend to build natively (needs >= 1.25.5 / toolchain 1.25.12):
#   GO_VERSION=1.25.12
#   curl -fsSL https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz | tar -C /usr/local -xzf -

echo "bootstrap done; switch to user 'ubuntu' and run §5"
```

### 4.2 Post-boot (as `ubuntu`)

```bash
# Clone source (no .git transfer needed)
cd /opt/ai-platform
git clone https://github.com/mayiants/ai-portal-go.git
git clone https://github.com/mayiants/ai-proxy-go.git

# Fetch the bundle + CA cert (paths are examples)
mkdir -p /opt/ai-platform/incoming
# scp / SSM / aws s3 cp local-config.secure.enc -> /opt/ai-platform/incoming/
# scp portal_data.dump (optional) -> /opt/ai-platform/incoming/
```

---

## 5. Place secrets + configure `.env.go-platform`

Decrypt the bundle and restore files to their original relative paths:

```bash
cd /opt/ai-platform
openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 \
  -in incoming/local-config.secure.enc -out - \
  -pass pass:<SECURE_BUNDLE_PASSPHRASE> | tar -xzf -

# The bundle restores paths like:
#   ssl/woodgit.com.{key,pem}.txt
#   workspace/ai-portal-go/.env.go-platform   <- authoritative config (has real secrets)
#   workspace/ai-proxy-go/.env, workspace/ai-proxy-go/config.yaml
#   .ssh/authorized_keys
# Move/copy them into place, e.g.:
cp workspace/ai-portal-go/.env.go-platform /opt/ai-platform/ai-portal-go/.env.go-platform
cp workspace/ai-proxy-go/.env            /opt/ai-platform/ai-proxy-go/.env
cp workspace/ai-proxy-go/config.yaml     /opt/ai-platform/ai-proxy-go/config.yaml
sudo install -m 600 ssl/woodgit.com.key.txt /etc/ssl/private/woodgit.com.key
sudo install -m 644 ssl/woodgit.com.pem.txt /etc/ssl/certs/woodgit.com.pem
```

Now **edit only** `/opt/ai-platform/ai-portal-go/.env.go-platform`. A checked-in, commit-safe template with the exact keys and placeholders lives at `ai-portal-go/env-go-platform.rds.template` — start from the bundle's real file and flip just these keys:

```bash
# --- SG / RDS specific (change) ---
DB_HOST=<RDS_WRITER_ENDPOINT>                 # from §2.4
DB_PORT=5432
DB_NAME=ai_proxy_portal
DB_USER=postgres
DB_PASSWORD=<RDS_MASTER_PASSWORD>             # from §2 (the NEW RDS password)
DB_SSLMODE=verify-full
DB_SSL_ROOT_CERT=/opt/ai-platform/certs/rds-ca.pem

APP_BASE_URL=https://api.woodgit.com
ALLOWED_ORIGINS=https://api.woodgit.com
SERVER_ID=portal-apse1-01
SERVER_REGION=ap-southeast-1

# --- production flags (set) ---
ADMIN_MODE=production
SEED_E2E_ACCOUNTS=false
SEED_TEST_ENTERPRISE=false
REDIS_TLS=false                  # container redis on the same host; set true only if moving to ElastiCache
REDIS_TLS_SKIP_VERIFY=false

# --- secrets (copy VERBATIM from the bundle's .env.go-platform; never regenerate) ---
ENCRYPTION_KEY=<FROM_SECURE_BUNDLE>
JWT_SECRET=<FROM_SECURE_BUNDLE>
PROXY_SYNC_SECRET=<FROM_SECURE_BUNDLE>
ADMIN_SECRET=<FROM_SECURE_BUNDLE>
ADMIN_PASSWORD=<FROM_SECURE_BUNDLE>      # existing super-admin hash already in DB; bootstrap skips if present
ADMIN_EMAIL=<FROM_SECURE_BUNDLE>
ADMIN_NAME=<FROM_SECURE_BUNDLE>
REDIS_PASSWORD=<FROM_SECURE_BUNDLE>
PROXY_REDIS_PASSWORD=<FROM_SECURE_BUNDLE>
SMTP_HOST=<FROM_SECURE_BUNDLE>  SMTP_USER=<FROM_SECURE_BUNDLE>
SMTP_PASSWORD=<FROM_SECURE_BUNDLE>  SMTP_FROM=<FROM_SECURE_BUNDLE>
# All other keys (ports, GOPROXY/APK_MIRROR, PROXY_CALLBACK_URLS, PORTAL_URL, redis hosts): KEEP from bundle.
```

Bring the stack up:

```bash
cd /opt/ai-platform/ai-portal-go
./start.sh mvp --app-base-url=https://api.woodgit.com
./start.sh status
```

`./start.sh mvp` builds/starts Portal + Proxy (+ the two Redis containers) using the Dockerfiles (`golang:1.25-alpine` builder) — no host Go required.

---

## 6. Nginx + TLS (single-host)

`/etc/nginx/sites-available/api.woodgit.com`:

```nginx
server {
    listen 80;
    server_name api.woodgit.com;
    return 301 https://$host$request_uri;
}
server {
    listen 443 ssl http2;
    server_name api.woodgit.com;
    ssl_certificate     /etc/ssl/certs/woodgit.com.pem;
    ssl_certificate_key /etc/ssl/private/woodgit.com.key;

    # Web (Portal)
    location / {
        proxy_pass http://127.0.0.1:3001;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    # API (Proxy) — streaming friendly
    location /v1/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
        proxy_buffering off;
    }
}
```

```bash
sudo ln -s /etc/nginx/sites-available/api.woodgit.com /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

---

## 7. DNS cutover + validation

1. Lower TTL on `api.woodgit.com` to 60–120s ahead of time.
2. Point `api.woodgit.com` to the SG EC2 (Elastic IP recommended). Keep the Sydney host running until validated.
3. Validate (reuses `cloudflare-portal-proxy-split-202607100703.md` §8 / `production-readiness-handover-202607101500.md` §5):
   ```bash
   curl -sf https://api.woodgit.com/api/health && echo " portal OK"
   curl -sf https://api.woodgit.com/v1/health  && echo " proxy OK"
   ./start.sh status            # portal/proxy/redis healthy; DB = external, reachable
   ./start.sh logs portal | tail -50
   ```
   - Portal admin → proxy list shows `portal-apse1-01` with a fresh heartbeat.
   - Create a test API key; run a real `POST /v1/chat/completions` end-to-end.
   - Admin usage page shows new rows in RDS.

---

## 8. Rollback

- DNS: point `api.woodgit.com` back to Sydney (respect the lowered TTL).
- App: on the SG host set `DB_HOST=postgres` + `./start.sh rebuild` to use a local PG again (only valid if you kept a local volume; data written to RDS is not in it — dump back first if needed).

---

## 9. Cost (order of magnitude, ap-southeast-1)

| Piece | Choice | ~Monthly |
|---|---|---|
| EC2 app host | t3.medium → t3.large | $30–60 |
| RDS PostgreSQL | db.t3.medium, 20 GB gp3 | $60–90 |
| Redis | container on EC2 | $0 |
| **Total (Stage 2)** | | **~$100–150** |

(See `aws-deployment-guide-202605250930.md` and `aurora-postgresql-migration-guide.md` §9 for the source figures; Singapore pricing is comparable.)

---

## 10. Deferred (do later, on measured need)

- Cloudflare path-routing split of Portal/Proxy onto two EC2 (`cloudflare-portal-proxy-split-202607100703.md`).
- Cross-region proxy-only nodes (`deployment-guide-202605300640.md` Stage 3).
- Redis → ElastiCache (then set `REDIS_TLS=true`).
- Secrets → AWS Secrets Manager / SSM Parameter Store.
- Enterprise uploads → S3 (`FILE_STORAGE_PROVIDER=s3`, bucket `ai-portal-uploads`, region `ap-southeast-1`).

---

## 11. References

- `aws-deployment-guide-202605250930.md` — 4-stage plan; this doc = Stage 2.
- `aurora-postgresql-migration-guide.md` — external-DB switch, data-only migration, backup/restore, rollback.
- `production-readiness-handover-202607101500.md` — pre-deploy env checklist, operational commands.
- `cloudflare-portal-proxy-split-202607100703.md` — deferred Portal/Proxy split.
- `data-backup-and-migration-strategy-202607091843.md` — `portal data export/import` (selective core-entity moves, alternative to full pg_dump).
- `migrations/README.md` — `InitSchema` baseline + `Migrate()` startup path.
