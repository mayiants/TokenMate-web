# 上线运维风险清单

> **Date:** 2026-08-14  
> **Status:** ACTIVE  
> **适用阶段：** 初期上线（低流量期）

---

## 当前基础设施

| 组件 | 实例 | 规格 | 磁盘 | 冗余 |
|------|------|------|------|------|
| Portal EC2 | 172.31.45.213 | t3.medium (2C/4GB) | 40GB (78% used) | ❌ 单点 |
| Proxy EC2 | 172.31.43.214 | t3.small (2C/2GB) | 40GB (22% used) | ❌ 单点 |
| PostgreSQL | Docker on Portal EC2 | — | 25 MB 数据 | ❌ 单点，无备份 |
| Redis (Portal) | Docker on Portal EC2 | — | — | ❌ 单点 |
| Redis (Proxy) | Docker on Proxy EC2 | — | — | ❌ 单点 |

**已有保护措施：**
- ✅ 所有 Docker 容器 `restart: always`
- ✅ 健康检查已配置（Portal, Proxy, Postgres, Redis 各 10s 间隔）
- ✅ 容器失败后 Docker 自动重启

---

## 风险矩阵

### 🔴 高风险（数据丢失可能）

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **PostgreSQL 无备份** | 用户/密钥/计费/企业数据全部丢失，不可恢复 | 低（EBS 可靠性 99.999%） | **立即**：配置每日 pg_dump 到 S3 |
| **Portal EC2 EBS 损坏** | 同上 | 极低 | EBS Snapshot 定期备份 |
| **误操作 DROP TABLE** | 生产数据丢失 | 低 | pg_dump + 测试数据库隔离（已有） |

### 🟡 中风险（服务中断）

| 风险 | 影响 | 恢复时间 | 缓解措施 |
|------|------|---------|---------|
| **EC2 实例故障** | 服务不可用 | 10-30min（手动重启） | AWS Auto Recovery alarm |
| **Proxy OOM (2GB RAM)** | API 请求失败 | ~30s（Docker restart） | 监控内存使用；流量增长后升配 |
| **Portal 磁盘满（78%）** | 服务异常 | 需手动清理 | 定期清理 Docker build cache；考虑扩容 |
| **Redis 数据丢失** | 缓存冷启动，key validation 短暂变慢 | 自动恢复 <5min | 可接受（Redis 是缓存层） |
| **Proxy-Portal 网络中断** | API 请求验证失败（cache TTL 后） | 取决于网络恢复 | 同 VPC 内，概率极低 |

### 🟢 低风险（性能退化）

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| t3 CPU credit 耗尽 | 限速到基准性能 | 监控 CPU credit balance；流量大时升 t3.medium/large |
| Retention 数据增长 | 磁盘空间消耗 | 已记录在 HANDOVER.md；90 天后评估 S3 |
| TokenRouter 上游故障 | 模型不可用 | Proxy 有 fallback 路由 + 模型级 cooldown |

---

## 立即需要做的（上线前/上线当天）

### 1. PostgreSQL 备份（最重要）

```bash
# 在 Portal EC2 上设置每日备份到本地 + S3
cat > /opt/ai-platform/backup-db.sh << 'EOF'
#!/bin/bash
BACKUP_DIR="/opt/ai-platform/backups"
mkdir -p "$BACKUP_DIR"
FILENAME="ai_proxy_portal_$(date +%Y%m%d_%H%M%S).sql.gz"
docker exec ai-portal-postgres pg_dump -U postgres ai_proxy_portal | gzip > "$BACKUP_DIR/$FILENAME"
# 保留最近 7 天本地备份
find "$BACKUP_DIR" -name "*.sql.gz" -mtime +7 -delete
# 可选：上传到 S3
# aws s3 cp "$BACKUP_DIR/$FILENAME" s3://your-backup-bucket/postgres/
echo "Backup completed: $FILENAME ($(du -h "$BACKUP_DIR/$FILENAME" | cut -f1))"
EOF
chmod +x /opt/ai-platform/backup-db.sh

# 添加 cron (每天 3:00 UTC)
(crontab -l 2>/dev/null; echo "0 3 * * * /opt/ai-platform/backup-db.sh >> /opt/ai-platform/backups/backup.log 2>&1") | crontab -
```

### 2. Portal 磁盘空间清理

Portal EC2 磁盘 78% —— 主要是 Docker build cache：
```bash
docker builder prune -f  # 释放 ~5-10 GB
```

### 3. 简易监控脚本

```bash
# 健康检查 + 报警（可用 cron 每 5 分钟运行）
cat > /opt/ai-platform/health-check.sh << 'EOF'
#!/bin/bash
PORTAL=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:3001/api/health)
PROXY=$(curl -s -o /dev/null -w "%{http_code}" http://172.31.43.214:8080/health)
DISK_PCT=$(df / | tail -1 | awk '{print $5}' | tr -d '%')

if [ "$PORTAL" != "200" ] || [ "$PROXY" != "200" ]; then
    echo "$(date) ALERT: Portal=$PORTAL Proxy=$PROXY" >> /opt/ai-platform/alerts.log
    # 可选：发邮件/Webhook 通知
fi
if [ "$DISK_PCT" -gt 85 ]; then
    echo "$(date) ALERT: Disk usage ${DISK_PCT}%" >> /opt/ai-platform/alerts.log
fi
EOF
chmod +x /opt/ai-platform/health-check.sh
```

---

## 上线后第一周关注项

| 项 | 检查方式 | 频率 |
|----|---------|------|
| Portal/Proxy 健康 | `curl /api/health` | 每 5 分钟 (cron) |
| 磁盘使用率 | `df -h /` | 每天 |
| API 响应延迟 | Analytics 报告 | 每 6 小时（自动） |
| 计费准确性 | `usage_records` 抽样核对 | 每天 |
| Retention 数据增长 | `du -sh /var/lib/docker/volumes/ai-proxy-go_proxy-retention-data/` | 每周 |
| 错误率 | Docker logs + Analytics reliability | 每天 |

---

## 上线后 30 天内规划

| 优先级 | 项 | 说明 |
|--------|-----|------|
| P0 | DB 备份到 S3 | 当前 25MB 数据，日增极小，但一次丢失不可接受 |
| P1 | CloudWatch 告警 | EC2 StatusCheck、CPU credit、磁盘使用率 |
| P2 | Retention cleanup 实现 | 添加后台 goroutine 调用 Cleanup() |
| P2 | Portal 磁盘扩容或清理策略 | 当前 78% 需要关注 |
| P3 | 评估是否升级 Proxy 到 t3.medium | 取决于流量增长 |
| P3 | 考虑 RDS 替代 Docker Postgres | 提供自动备份、Multi-AZ |

---

## 故障恢复 Runbook

### 场景 A：Portal 容器挂了
```bash
# Docker restart: always 会自动重启
# 如果没有自动恢复：
cd /opt/ai-platform/ai-portal-go
docker compose --env-file .env.go-platform -f docker-compose.portal.yml up -d
```
**恢复时间：** ~30 秒（Docker 自动）/ ~2 分钟（手动）

### 场景 B：Proxy 容器挂了
```bash
ssh -i /opt/ai-platform/workspace/portal-sg.pem ubuntu@172.31.43.214
cd /opt/ai-platform/ai-proxy-go
docker compose --env-file .env.migrated -f docker-compose.proxy.yml up -d
```
**恢复时间：** ~30 秒（Docker 自动）/ ~2 分钟（手动）
**影响：** API 请求暂时失败，恢复后自动重新同步 key pool

### 场景 C：EC2 实例不可达
1. AWS Console → EC2 → 重启实例
2. 所有容器 `restart: always`，实例启动后 Docker 自动拉起全部服务
3. **恢复时间：** 5-10 分钟

### 场景 D：PostgreSQL 数据损坏
1. 停止 Portal 容器
2. 从最近的 pg_dump 恢复：
```bash
docker exec -i ai-portal-postgres psql -U postgres < /opt/ai-platform/backups/latest.sql
```
3. 重启 Portal
**恢复时间：** 10-30 分钟（取决于是否有备份！）

---

## 结论

**当前架构对初期低流量上线是可以接受的**，核心条件是：

1. ✅ Docker restart: always 已配置
2. ✅ 健康检查已配置
3. ⚠️ **必须在上线前配置 PostgreSQL 每日备份**（唯一不可恢复的风险）
4. Portal EC2 磁盘需要清理（一条命令即可）

单点故障在流量小的情况下可以接受（中断 = 分钟级，不是小时级）。等用户增长到需要 SLA 保证时，再考虑 ALB + 多副本 + RDS。
