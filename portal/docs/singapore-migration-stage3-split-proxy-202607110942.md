# Singapore Migration — Stage 3 (Portal / Proxy Split EC2)

> **Date:** 2026-07-11 09:42 UTC | **Status:** COMPLETE (deployed and operational)
> **Scope:** Run Portal and Proxy on two separate EC2 instances in `ap-southeast-1`; RDS is deferred 1–2 days (local Docker PG on the Portal host is acceptable for light load).
> **Topology:**
> - **Portal EC2:** `172.31.45.213` / public `47.130.132.254` (t3.medium, 4 GB) — runs `ai-portal-go` + local PostgreSQL/Redis; acts as the TLS gateway for `api.woodgit.com`.
> - **Proxy EC2:** `172.31.43.214` / public `54.169.55.55` (t3.small, 2 GB) — runs `ai-proxy-go` + local proxy Redis only.
> - **Both EC2s share the same security group** (simplifies internal 3001/8080 traffic).
> - **Public domain:** `api.woodgit.com` (Cloudflare orange-cloud) currently points to the **Portal EC2**, which acts as the TLS gateway: `/v1/*` is forwarded to the Proxy EC2 over the VPC, and everything else is served locally by the Portal app.
> - **Recommended cleaner topology (deferred):** configure Cloudflare Origin Rules so the default `/*` route hits `portal-origin.woodgit.com` and only `/v1/*` hits `proxy-origin.woodgit.com` (see §8).

This document replaces the single-EC2 assumption in `singapore-migration-stage2-rds-202607110542.md` and documents the **actual split deployment** plus corrections discovered while building it.

---

## 0. Corrections to Stage-2 doc

These items in `singapore-migration-stage2-rds-202607110542.md` are wrong or incomplete for the split deployment:

### 0.1 Host Go is required

`ai-portal-go/start.sh` calls `go build` on the host for `portal`, `proxy`, `mvp`, `simulate`, `rebuild` modes (see `build_portal()` / `build_proxy()`). The Dockerfile multi-stage build is only used if you bypass `start.sh` and run `docker compose` directly.

Therefore the EC2 bootstrap **must install Go** (not just Docker):

```bash
GO_VERSION=1.25.12
curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | sudo tar -C /usr/local -xzf -
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
source /etc/profile.d/go.sh
go version   # go1.25.12 linux/amd64
```

> The Portal and Proxy repos both currently target Go 1.25; use the latest 1.25 patch (`1.25.12` as of this writing).

### 0.2 `docker-compose.proxy.yml` uses an `external` Docker network

`ai-proxy-go/docker-compose.proxy.yml` declares:

```yaml
networks:
  ai-platform-net:
    external: true
    name: ai-platform-net
```

On a dedicated Proxy EC2 that network does not exist, so `docker compose up` fails with:

```
network ai-platform-net declared as external, but could not be found
```

Workaround on the Proxy host:

```bash
docker network create ai-platform-net
cd /opt/ai-platform/ai-proxy-go
docker compose -f docker-compose.proxy.yml up -d --build
```

Long-term fix: make `start.sh` (or a proxy wrapper) create the network automatically when run in split mode, or change `docker-compose.proxy.yml` to create the network locally when `ai-portal-go` is not present.

### 0.3 Split deployment needs internal security-group rules

The Stage-2 doc only listed public-facing ports (22/80/443). When Portal and Proxy live on different EC2s, they must talk to each other over the VPC on ports 3001 and 8080.

**Current setup:** both EC2s are in the **same security group**, so no extra rules are required — the default self-referencing rule already allows all traffic between members of the same SG.

**If you ever split them back into different SGs**, add these rules:

| Direction | Port | Needed by |
|---|---|---|
| Proxy → Portal | TCP 3001 | Proxy syncs key pool, model registry, validates keys |
| Portal → Proxy | TCP 8080 | Portal callbacks (rate-limit/usage pushes, heartbeats) |

### 0.4 Public 3001/8080 must stay closed; Portal now has an EIP

Do **not** expose 3001 or 8080 to `0.0.0.0/0`. They are internal-only.

For the current **Portal-as-gateway** setup, only the **Portal EC2 public IP / EIP** needs to be exposed on 443 (and optionally 80 for redirect). The Portal EC2 has an Elastic IP (`47.130.132.254`) assigned, and Cloudflare `api.woodgit.com` points to it. The Proxy EC2 (`54.169.55.55`) only needs inbound 22 from your admin IP; its 8080 is reached via the VPC private IP (`172.31.43.214`) from the Portal SG.

---

## 1. Current split-deployment state (as of 2026-07-11)

| Host | Role | Private IP | Public IP / EIP | Instance | Security group | Key config |
|---|---|---|---|---|---|---|
| Portal | `ai-portal-go` + PG + Redis | `172.31.45.213` | `47.130.132.254` | t3.medium, Ubuntu 24.04 | shared with Proxy | `SERVER_ID=portal-apse1-01`, `DB_HOST=postgres`, `PROXY_CALLBACK_URLS=http://172.31.43.214:8080` |
| Proxy | `ai-proxy-go` + proxy Redis | `172.31.43.214` | `54.169.55.55` | t3.small, Ubuntu 24.04 | shared with Portal | `SERVER_ID=proxy-apse1-01`, `PORTAL_URL=http://172.31.45.213:3001`, `PROXY_CALLBACK_URLS=http://172.31.43.214:8080` |

Both hosts have Docker 29.x, Docker Compose v2, AWS CLI v2, Go 1.25.12, and Nginx installed.
Both containers report `healthy` locally, and Proxy sync is now successful:

---

## 2. How to reach the hosts

Both EC2s now have public EIPs, so you can SSH directly:

```bash
KEY=/home/ubuntu/ssl/portal-sg.pem

# Portal (Cloudflare gateway; direct admin access)
ssh -i "$KEY" ubuntu@47.130.132.254

# Proxy
ssh -i "$KEY" ubuntu@54.169.55.55

# Proxy via Portal private IP (if you prefer to keep Proxy off the public internet later)
ssh -i "$KEY" -o ProxyCommand="ssh -i $KEY -W %h:%p ubuntu@47.130.132.254" ubuntu@172.31.43.214
```

> Tip: once you finish debugging, you can remove the Proxy EIP or restrict its security group so only the Portal VPC IP can reach it, reducing the attack surface.

---

## 3. VPC security-group rules

### 3.1 Identify the security group

```bash
# Run from either host via instance metadata (no IAM needed)
curl -s http://169.254.169.254/latest/meta-data/security-groups
```

Current result: **both EC2s use the same security group**.

Because both instances are in the same SG, the AWS default self-referencing rule allows them to talk to each other on any port. No explicit 3001/8080 rules are required.

### 3.2 If you split them back into different SGs later

| Direction | Port | Source SG | Note |
|---|---|---|---|
| Proxy → Portal | TCP 3001 | Proxy SG | Proxy sync |
| Portal → Proxy | TCP 8080 | Portal SG | Portal callback |

AWS CLI example (placeholder IDs):

```bash
REGION=ap-southeast-1
PORTAL_SG=sg-xxxxxxxxxxxxxxxxx
PROXY_SG=sg-yyyyyyyyyyyyyyyyy

aws ec2 authorize-security-group-ingress \
  --region "$REGION" --group-id "$PORTAL_SG" \
  --ip-permissions "IpProtocol=tcp,FromPort=3001,ToPort=3001,UserIdGroupPairs=[{GroupId=$PROXY_SG}]"

aws ec2 authorize-security-group-ingress \
  --region "$REGION" --group-id "$PROXY_SG" \
  --ip-permissions "IpProtocol=tcp,FromPort=8080,ToPort=8080,UserIdGroupPairs=[{GroupId=$PORTAL_SG}]"
```

### 3.3 Public rules (keep as-is)

| Port | Source | Purpose |
|---|---|---|
| 22 | your IP / `0.0.0.0/0` | SSH |
| 80, 443 | Cloudflare IPs or `0.0.0.0/0` | Public web/API |

> Ideally lock 443 to [Cloudflare IP ranges](https://www.cloudflare.com/ips/) so attackers cannot bypass Cloudflare.

---

## 4. Verify internal communication

On the **Proxy** host:

```bash
# Should return Portal health JSON
curl -s --max-time 5 http://172.31.45.213:3001/api/health
```

On the **Portal** host:

```bash
# Should return Proxy health JSON
curl -s --max-time 5 http://172.31.43.214:8080/health
```

Then watch Proxy logs:

```bash
docker logs -f ai-proxy-go
```

Expected outcome:
- `key_pool` and `model_registry` health checks show fresh `last_sync` timestamps and `"status":true`.
- Portal admin → **Proxy Instances** shows `proxy-apse1-01` with a recent heartbeat.

---

## 5. EC2 bootstrap scripts (placeholder)

Use these as User Data or run them once after the instance boots. They are intentionally generic; secrets are applied in §6.

### 5.1 Portal EC2 bootstrap

```bash
#!/bin/bash
# Bootstrap for Portal EC2 (ap-southeast-1)
set -euxo pipefail
exec > >(tee -a /var/log/ai-portal-bootstrap.log) 2>&1

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y ca-certificates curl wget git unzip tar gnupg lsb-release jq nginx

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

# Go (required because start.sh builds on the host)
GO_VERSION=1.25.12
curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xzf -
echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh

# AWS CLI v2
curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip
unzip -q -o /tmp/awscliv2.zip -d /tmp
/tmp/aws/install --update

mkdir -p /opt/ai-platform && chown -R ubuntu:ubuntu /opt/ai-platform
echo "Portal bootstrap done"
```

### 5.2 Proxy EC2 bootstrap

Same as Portal plus the Docker network workaround:

```bash
#!/bin/bash
# Bootstrap for Proxy EC2 (ap-southeast-1)
set -euxo pipefail
exec > >(tee -a /var/log/ai-proxy-bootstrap.log) 2>&1

# ... same apt-get install ... nginx ... Docker / Go / AWS CLI as Portal ...

mkdir -p /opt/ai-platform && chown -R ubuntu:ubuntu /opt/ai-platform

# ai-proxy-go/docker-compose.proxy.yml declares ai-platform-net as external;
# create it before the first docker compose up.
docker network create ai-platform-net || true

echo "Proxy bootstrap done"
```

After bootstrap, copy the TLS cert/key onto the Proxy host (from the secure bundle or from the Sydney source host):

```bash
sudo install -m 600 ssl/woodgit.com.key.txt /etc/ssl/private/woodgit.com.key
sudo install -m 644 ssl/woodgit.com.pem.txt /etc/ssl/certs/woodgit.com.pem
```

---

## 6. Post-boot configuration

### 6.1 Clone repos

```bash
cd /opt/ai-platform
git clone https://github.com/mayiants/ai-portal-go.git
git clone https://github.com/mayiants/ai-proxy-go.git
```

### 6.2 Restore secrets from secure bundle

Decrypt the bundle produced on the source host (`local-config.secure.enc`) and copy the real files into place:

```bash
cd /opt/ai-platform
openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 \
  -in incoming/local-config.secure.enc -out - \
  -pass pass:<SECURE_BUNDLE_PASSPHRASE> | tar -xzf -

# Copy live config files
cp workspace/ai-portal-go/.env.go-platform /opt/ai-platform/ai-portal-go/.env.go-platform
cp workspace/ai-proxy-go/.env            /opt/ai-platform/ai-proxy-go/.env
cp workspace/ai-proxy-go/config.yaml     /opt/ai-platform/ai-proxy-go/config.yaml

# TLS cert/key
sudo install -m 600 ssl/woodgit.com.key.txt /etc/ssl/private/woodgit.com.key
sudo install -m 644 ssl/woodgit.com.pem.txt /etc/ssl/certs/woodgit.com.pem
```

> Never commit real secrets. The checked-in template `env-go-platform.rds.template` is commit-safe.

### 6.3 Portal `.env.go-platform`

Start from the bundle's real file, then change **only** these keys:

```bash
SERVER_ID=portal-apse1-01
SERVER_REGION=ap-southeast-1
APP_BASE_URL=https://api.woodgit.com
ALLOWED_ORIGINS=https://api.woodgit.com

# Local DB for now (RDS deferred)
DB_HOST=postgres
DB_PORT=5432
DB_NAME=ai_proxy_portal
DB_USER=postgres
DB_SSLMODE=disable

# Point Portal callbacks back to the Proxy EC2
PROXY_CALLBACK_URLS=http://172.31.43.214:8080
```

When RDS is created later, flip to:

```bash
DB_HOST=<RDS_WRITER_ENDPOINT>
DB_PASSWORD=<RDS_MASTER_PASSWORD>
DB_SSLMODE=verify-full
DB_SSL_ROOT_CERT=/opt/ai-platform/certs/rds-ca.pem
```

See `env-go-platform.rds.template` for the full template.

### 6.4 Proxy `.env.go-platform`

```bash
SERVER_ID=proxy-apse1-01
SERVER_REGION=ap-southeast-1

# Point Proxy to Portal over VPC internal IP
PORTAL_URL=http://172.31.45.213:3001

# 4 global secrets must match Portal byte-for-byte
JWT_SECRET=<FROM_SECURE_BUNDLE>
ENCRYPTION_KEY=<FROM_SECURE_BUNDLE>
PROXY_SYNC_SECRET=<FROM_SECURE_BUNDLE>
ADMIN_SECRET=<FROM_SECURE_BUNDLE>
```

### 6.5 Start services

Portal host:

```bash
cd /opt/ai-platform/ai-portal-go
./start.sh portal --app-base-url=https://api.woodgit.com
```

Proxy host:

```bash
cd /opt/ai-platform/ai-proxy-go
docker compose -f docker-compose.proxy.yml up -d --build
```

### 6.6 Configure Nginx

The cert in the secure bundle is a **Cloudflare Origin CA** certificate (valid for `*.woodgit.com` and `woodgit.com`). Install it on both hosts and configure Nginx.

**Option A: Portal as gateway** (current, Free-tier friendly)

Cloudflare `api.woodgit.com` A record points to Portal EIP `47.130.132.254`.

Portal Nginx (`/etc/nginx/sites-available/api.woodgit.com`):

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

    location /v1/ {
        proxy_pass http://172.31.43.214:8080/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
        proxy_buffering off;
    }

    location / {
        proxy_pass http://127.0.0.1:3001;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Proxy Nginx (`/etc/nginx/sites-available/api.woodgit.com`):

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

    location /v1/ {
        proxy_pass http://127.0.0.1:8080/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
        proxy_buffering off;
    }
}
```

**Option B: Proxy as gateway** (alternative)

Cloudflare `api.woodgit.com` A record points to Proxy EIP `54.169.55.55` instead.

Portal Nginx (`/etc/nginx/sites-available/api.woodgit.com`):

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

    location / {
        proxy_pass http://127.0.0.1:3001;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Proxy Nginx (`/etc/nginx/sites-available/api.woodgit.com`):

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

    location /v1/ {
        proxy_pass http://127.0.0.1:8080/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
        proxy_buffering off;
    }

    location / {
        proxy_pass http://172.31.45.213:3001;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Enable on both hosts:

```bash
sudo rm -f /etc/nginx/sites-enabled/default
sudo ln -sf /etc/nginx/sites-available/api.woodgit.com /etc/nginx/sites-enabled/api.woodgit.com
sudo nginx -t && sudo systemctl reload nginx
```

---

## 7. RDS placeholder commands (deferred)

When you are ready to move PostgreSQL off the Portal EC2, run these (replace placeholders):

```bash
REGION=ap-southeast-1
VPC_ID=<YOUR_VPC_ID>
PORTAL_SG=sg-xxxxxxxxxxxxxxxxx       # Portal EC2 security group
RDS_SG_NAME=ai-portal-rds-sg
SUBNET_GROUP=ai-portal-db-subnet     # must span >= 2 AZs
DB_ID=ai-portal-db
DB_NAME=ai_proxy_portal
DB_USER=postgres
DB_PASSWORD='<STRONG_RDS_PASSWORD>'

# 1. RDS security group
aws ec2 create-security-group --region "$REGION" --group-name "$RDS_SG_NAME" \
  --description "Portal EC2 -> RDS 5432" --vpc-id "$VPC_ID"
RDS_SG=$(aws ec2 describe-security-groups --region "$REGION" \
  --filters "Name=group-name,Values=$RDS_SG_NAME" --query 'SecurityGroups[0].GroupId' --output text)

aws ec2 authorize-security-group-ingress --region "$REGION" --group-id "$RDS_SG" \
  --ip-permissions "IpProtocol=tcp,FromPort=5432,ToPort=5432,UserIdGroupPairs=[{GroupId=$PORTAL_SG}]"

# 2. Create RDS PostgreSQL 15
aws rds create-db-instance --region "$REGION" \
  --db-instance-identifier "$DB_ID" \
  --db-instance-class db.t3.medium \
  --engine postgres --engine-version 15 \
  --allocated-storage 20 --storage-type gp3 \
  --master-username "$DB_USER" --master-user-password "$DB_PASSWORD" \
  --db-name "$DB_NAME" \
  --db-subnet-group-name "$SUBNET_GROUP" \
  --vpc-security-group-ids "$RDS_SG" \
  --no-publicly-accessible \
  --backup-retention-period 7 \
  --deletion-protection \
  --storage-encrypted

aws rds wait db-instance-available --region "$REGION" --db-instance-identifier "$DB_ID"
RDS_ENDPOINT=$(aws rds describe-db-instances --region "$REGION" --db-instance-identifier "$DB_ID" \
  --query 'DBInstances[0].Endpoint.Address' --output text)
echo "$RDS_ENDPOINT"

# 3. Download RDS CA
sudo mkdir -p /opt/ai-platform/certs
sudo curl -fsSL -o /opt/ai-platform/certs/rds-ca.pem \
  https://truststore.pki.rds.amazonaws.com/$REGION/$REGION-bundle.pem
```

Then update Portal `.env.go-platform` with the RDS endpoint and `DB_SSLMODE=verify-full`, and restart.

---

## 8. Cloudflare routing

### 8.1 当前运行方案：Portal EC2 做 TLS 网关

`api.woodgit.com` 橙云，A 记录指向 **Portal EIP** `47.130.132.254`。Cloudflare 上**不需要** Origin Rules 或 path 分流，因为 Nginx 在 Portal 上做分流：

| Path | Destination |
|---|---|
| `/v1/*` | Proxy EC2 internal (`172.31.43.214:8080`) |
| everything else | 本地 Portal app (`127.0.0.1:3001`) |

这种方案**完全兼容 Cloudflare Free tier**，也是目前实际跑通的配置。Portal 配置更高（t3.medium vs t3.small），作为公网入口更合适。

Verified public endpoints:

```bash
curl -sf https://api.woodgit.com/api/health
curl -sf https://api.woodgit.com/v1/health
curl -sf https://api.woodgit.com/login
```

### 8.2 替代方案：Proxy EC2 做 TLS 网关

如果未来想把网关放回 Proxy：

1. Cloudflare `api.woodgit.com` A 记录改为 **Proxy EIP** `54.169.55.55`。
2. Proxy Nginx 增加 `location / { proxy_pass http://172.31.45.213:3001; ... }` 兜底转发，并保留 `location /v1/` 本地处理。
3. Portal Nginx 只保留 `location /` 块（处理 web 请求）。

这种方式同样只需要 Free tier 的一条 A 记录，无需 Origin Rules。

### 8.3 Cloudflare path 分流（需要 Enterprise 或 Workers）

如果想让 Cloudflare 边缘按 path 分流（web → Portal，API → Proxy），需要：

- **Enterprise**：使用 Origin Rules 覆盖源站 hostname。
- **Free/Pro/Business**：Origin Rules 只能覆盖端口，不能改 hostname；只能用 Cloudflare Workers（Free 每日 10 万请求限制）做按 path 选源站。

因此，对于大多数场景，**Nginx 网关方案更简单、无限制、无需升级 Cloudflare**。

如果使用 path 分流，才需要创建以下灰云子域：

| DNS record | Type | Target | Cloudflare proxy |
|---|---|---|---|
| `api.woodgit.com` | A/CNAME | Cloudflare orange-cloud | Orange |
| `portal-origin.woodgit.com` | A | `47.130.132.254` | Grey (DNS only) |
| `proxy-origin.woodgit.com` | A | `54.169.55.55` | Grey (DNS only) |

### 8.4 SSL/TLS mode

Set Cloudflare SSL/TLS encryption mode to **Full (strict)**. The origin certificate is a Cloudflare Origin CA cert (`*.woodgit.com`, `woodgit.com`), which is only trusted by Cloudflare.

---

## 9. Operational commands

```bash
# Portal host
./start.sh status
./start.sh logs portal
./start.sh stop-portal
./start.sh portal --app-base-url=https://api.woodgit.com

# Proxy host
docker compose -f docker-compose.proxy.yml ps
docker logs -f ai-proxy-go
docker compose -f docker-compose.proxy.yml down
docker compose -f docker-compose.proxy.yml up -d --build

# Nginx on either host
sudo nginx -t
sudo systemctl reload nginx
sudo systemctl status nginx
```

---

## 10. References

- `singapore-migration-stage2-rds-202607110542.md` — single-EC2 Stage-2 plan (superseded by this doc for the split topology).
- `cloudflare-portal-proxy-split-202607100703.md` — Cloudflare path-routing design.
- `aurora-postgresql-migration-guide.md` — external DB migration, data-only dump/restore, CA certs.
- `production-readiness-handover-202607101500.md` — pre-deploy checklist.
- `env-go-platform.rds.template` — commit-safe RDS template for `.env.go-platform`.
