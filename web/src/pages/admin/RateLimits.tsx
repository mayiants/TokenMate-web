import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { Button, Empty, Progress, Select, Spin, Table, Tabs, Tag, Toast } from '@douyinfe/semi-ui';
import {
  IconCloudStroked,
  IconExport,
  IconKeyStroked,
  IconRefresh,
  IconSendStroked,
  IconServerStroked,
} from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { formatNumber, formatUSD } from '../../api/console';
import {
  exportRateLimits,
  getRateLimitApiKeys,
  getRateLimitCapacity,
  getRateLimitGroups,
  getRateLimitUpstreamKeys,
  getRateLimitUsage,
  type RateLimitApiKey,
  type RateLimitApiKeysResponse,
  type RateLimitCapacity,
  type RateLimitDimension,
  type RateLimitGroup,
  type RateLimitGroupsResponse,
  type RateLimitUpstreamKey,
  type RateLimitUpstreamResponse,
  type RateLimitUsageResponse,
  type RateLimitUser,
  type RateLimitWindow,
} from '../../api/adminRateLimits';

type TabKey = 'users' | 'groups' | 'apikeys' | 'upstream' | 'capacity';

const WINDOWS: RateLimitWindow[] = ['1h', '24h', '7d'];

const TAB_DIMENSION: Partial<Record<TabKey, RateLimitDimension>> = {
  users: 'users',
  groups: 'groups',
  apikeys: 'apikeys',
  upstream: 'upstream',
};

function StatCard({
  icon,
  tint,
  label,
  value,
  caption,
}: {
  icon: ReactNode;
  tint: string;
  label: string;
  value: string;
  caption?: string;
}) {
  return (
    <div className="wg-pro-card wg-stat">
      <span className={`wg-stat-icon ${tint}`}>{icon}</span>
      <div className="wg-stat-body">
        <div className="wg-stat-label">{label}</div>
        <div className="wg-stat-value">{value}</div>
        {caption && <div className="wg-stat-cap">{caption}</div>}
      </div>
    </div>
  );
}

function StatusTag({ status, t }: { status?: string; t: (k: string) => string }) {
  const key = `admin.ratelimit.status.${status ?? 'unknown'}`;
  const label = t(key);
  const color = status === 'limited' ? 'red' : status === 'near' ? 'orange' : 'green';
  return (
    <Tag color={color} size="small">
      {label === key ? status ?? '—' : label}
    </Tag>
  );
}

/** Highest ratio across the windows the backend actually filled in. */
function maxUtil(util: Record<string, number> | undefined): number {
  const values = Object.values(util ?? {}).filter((v) => typeof v === 'number' && Number.isFinite(v));
  return values.length ? Math.min(1, Math.max(...values)) : 0;
}

function UtilCell({ util, label }: { util: Record<string, number> | undefined; label: string }) {
  const ratio = maxUtil(util);
  const percent = Math.round(ratio * 1000) / 10;
  const stroke = ratio >= 0.9 ? 'var(--semi-color-danger)' : ratio >= 0.7 ? 'var(--semi-color-warning)' : 'var(--semi-color-primary)';
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      <Progress
        percent={percent}
        showInfo={false}
        stroke={stroke}
        size="small"
        style={{ width: 72 }}
        aria-label={label}
      />
      <span className="wg-num wg-cell-dim" style={{ fontSize: 12 }}>{percent.toFixed(0)}%</span>
    </div>
  );
}

function limitCell(limits: Record<string, number | null> | undefined) {
  return (
    <span className="wg-num">
      {limits?.rpm ?? '—'} / {limits?.tpm ?? '—'}
    </span>
  );
}

/** Admin rate-limit console (parity with the legacy /admin/rate-limits page). */
export default function RateLimits() {
  const { t } = useI18n();
  const [tab, setTab] = useState<TabKey>('users');
  const [win, setWin] = useState<RateLimitWindow>('24h');
  const [loading, setLoading] = useState(true);
  const [exporting, setExporting] = useState(false);

  const [usage, setUsage] = useState<RateLimitUsageResponse | null>(null);
  const [groups, setGroups] = useState<RateLimitGroupsResponse | null>(null);
  const [apiKeys, setApiKeys] = useState<RateLimitApiKeysResponse | null>(null);
  const [upstream, setUpstream] = useState<RateLimitUpstreamResponse | null>(null);
  const [capacity, setCapacity] = useState<RateLimitCapacity | null>(null);

  const load = useCallback(
    async (key: TabKey, window: RateLimitWindow) => {
      setLoading(true);
      try {
        if (key === 'users') setUsage(await getRateLimitUsage(window));
        else if (key === 'groups') setGroups(await getRateLimitGroups(window));
        else if (key === 'apikeys') setApiKeys(await getRateLimitApiKeys(window));
        else if (key === 'upstream') setUpstream(await getRateLimitUpstreamKeys(window));
        else setCapacity(await getRateLimitCapacity(window));
      } catch (e) {
        Toast.error(e instanceof Error ? e.message : t('common.error'));
      } finally {
        setLoading(false);
      }
    },
    [t]
  );

  useEffect(() => {
    load(tab, win);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onExport = async () => {
    const dimension = TAB_DIMENSION[tab];
    if (!dimension) return;
    setExporting(true);
    try {
      await exportRateLimits(dimension, win);
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setExporting(false);
    }
  };

  const userColumns: any[] = [
    {
      title: t('admin.ratelimit.col.user'),
      dataIndex: 'name',
      render: (v: string, r: RateLimitUser) => (
        <div style={{ display: 'flex', flexDirection: 'column' }}>
          <span>{v || r.email}</span>
          <span className="wg-cell-dim" style={{ fontSize: 12 }}>
            {r.email}
          </span>
        </div>
      ),
    },
    {
      title: t('admin.ratelimit.col.requests'),
      dataIndex: 'request_count',
      width: 110,
      render: (v: number) => <span className="wg-num">{formatNumber(v ?? 0)}</span>,
    },
    {
      title: t('admin.ratelimit.col.tokens'),
      dataIndex: 'total_tokens',
      width: 120,
      render: (v: number) => <span className="wg-num">{formatNumber(v ?? 0)}</span>,
    },
    {
      title: t('admin.ratelimit.col.cost'),
      dataIndex: 'total_cost',
      width: 110,
      render: (v: number) => <span className="wg-num">{formatUSD(v ?? 0)}</span>,
    },
    {
      title: t('admin.ratelimit.col.limits'),
      width: 150,
      render: (_: unknown, r: RateLimitUser) => limitCell(r.limits),
    },
    {
      title: t('admin.ratelimit.col.util'),
      width: 150,
      render: (_: unknown, r: RateLimitUser) => <UtilCell util={r.utilization} label={t('admin.ratelimit.col.util')} />,
    },
    {
      title: t('admin.ratelimit.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string) => <StatusTag status={v} t={t} />,
    },
  ];

  const groupColumns: any[] = [
    { title: t('admin.ratelimit.col.group'), dataIndex: 'name' },
    {
      title: t('admin.ratelimit.col.requests'),
      width: 110,
      render: (_: unknown, r: RateLimitGroup) => (
        <span className="wg-num">{formatNumber(r.usage?.requests ?? 0)}</span>
      ),
    },
    {
      title: t('admin.ratelimit.col.tokens'),
      width: 120,
      render: (_: unknown, r: RateLimitGroup) => (
        <span className="wg-num">{formatNumber(r.usage?.tokens ?? 0)}</span>
      ),
    },
    {
      title: t('admin.ratelimit.col.limits'),
      width: 150,
      render: (_: unknown, r: RateLimitGroup) => limitCell(r.limits),
    },
    {
      title: t('admin.ratelimit.col.util'),
      width: 150,
      render: (_: unknown, r: RateLimitGroup) => <UtilCell util={r.utilization} label={t('admin.ratelimit.col.util')} />,
    },
    {
      title: t('admin.ratelimit.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string) => <StatusTag status={v} t={t} />,
    },
  ];

  const apiKeyColumns: any[] = [
    { title: t('admin.ratelimit.col.key'), dataIndex: 'name' },
    {
      title: t('admin.ratelimit.col.keyType'),
      dataIndex: 'key_type',
      width: 110,
      render: (v: string) => <span className="wg-cell-dim">{v || '—'}</span>,
    },
    {
      title: t('admin.ratelimit.col.requests'),
      width: 110,
      render: (_: unknown, r: RateLimitApiKey) => (
        <span className="wg-num">{formatNumber(r.usage?.requests ?? 0)}</span>
      ),
    },
    {
      title: t('admin.ratelimit.col.tokens'),
      width: 120,
      render: (_: unknown, r: RateLimitApiKey) => (
        <span className="wg-num">{formatNumber(r.usage?.tokens ?? 0)}</span>
      ),
    },
    {
      title: t('admin.ratelimit.col.effective'),
      width: 170,
      render: (_: unknown, r: RateLimitApiKey) => limitCell(r.effective_limits),
    },
    {
      title: t('admin.ratelimit.col.util'),
      width: 150,
      render: (_: unknown, r: RateLimitApiKey) => <UtilCell util={r.utilization} label={t('admin.ratelimit.col.util')} />,
    },
    {
      title: t('admin.ratelimit.col.status'),
      dataIndex: 'item_status',
      width: 100,
      render: (v: string) => <StatusTag status={v} t={t} />,
    },
  ];

  const upstreamColumns: any[] = [
    { title: t('admin.ratelimit.col.provider'), dataIndex: 'provider', width: 150 },
    { title: t('admin.ratelimit.col.key'), dataIndex: 'name' },
    {
      title: t('admin.ratelimit.col.requests'),
      width: 110,
      render: (_: unknown, r: RateLimitUpstreamKey) => (
        <span className="wg-num">{formatNumber(r.usage?.requests ?? 0)}</span>
      ),
    },
    {
      title: t('admin.ratelimit.col.tokens'),
      width: 120,
      render: (_: unknown, r: RateLimitUpstreamKey) => (
        <span className="wg-num">{formatNumber(r.usage?.tokens ?? 0)}</span>
      ),
    },
    {
      title: t('admin.ratelimit.col.limits'),
      width: 150,
      render: (_: unknown, r: RateLimitUpstreamKey) => limitCell(r.limits),
    },
    {
      title: t('admin.ratelimit.col.util'),
      width: 150,
      render: (_: unknown, r: RateLimitUpstreamKey) => <UtilCell util={r.utilization} label={t('admin.ratelimit.col.util')} />,
    },
    {
      title: t('admin.ratelimit.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string) => <StatusTag status={v} t={t} />,
    },
  ];

  const cap = capacity;
  const pct = (v: number | undefined) => Math.round((v ?? 0) * 1000) / 10;
  const statusLabel = (s: string | undefined) => {
    const key = `admin.ratelimit.status.${s ?? 'unknown'}`;
    const v = t(key);
    return v === key ? s ?? '—' : v;
  };
  const windowLabel = t(`admin.ratelimit.window.${win}`);

  return (
    <div className="wg-page">
      <PageHead title={t('admin.ratelimit.title')} sub={windowLabel} />

      <div className="wg-pro-card wg-filter-card">
        <Select
          style={{ width: 150 }}
          value={win}
          aria-label={t('admin.ratelimit.windowLabel')}
          onChange={(v) => {
            const next = (v as RateLimitWindow) ?? '24h';
            setWin(next);
            load(tab, next);
          }}
          optionList={WINDOWS.map((w) => ({ value: w, label: t(`admin.ratelimit.window.${w}`) }))}
        />
        <div className="wg-filter-actions">
          <Button icon={<IconRefresh />} theme="light" onClick={() => load(tab, win)}>
            {t('common.refresh')}
          </Button>
          {TAB_DIMENSION[tab] && (
            <Button icon={<IconExport />} theme="light" loading={exporting} onClick={onExport}>
              {t('common.export')}
            </Button>
          )}
        </div>
      </div>

      <Tabs
        type="line"
        activeKey={tab}
        onChange={(k) => {
          const key = k as TabKey;
          setTab(key);
          load(key, win);
        }}
      >
        <Tabs.TabPane itemKey="users" tab={t('admin.ratelimit.tab.users')}>
          <div className="wg-stat-grid">
            <StatCard
              icon={<IconSendStroked />}
              tint="wg-tint-blue"
              label={t('admin.ratelimit.kpi.requests')}
              value={formatNumber(usage?.summary.total_requests ?? 0)}
              caption={usage?.summary.period}
            />
            <StatCard
              icon={<IconKeyStroked />}
              tint="wg-tint-blue"
              label={t('admin.ratelimit.kpi.tokens')}
              value={formatNumber(usage?.summary.total_tokens ?? 0)}
              caption={t('admin.ratelimit.kpi.tokensCap')}
            />
            <StatCard
              icon={<IconServerStroked />}
              tint="wg-tint-amber"
              label={t('admin.ratelimit.kpi.cost')}
              value={formatUSD(usage?.summary.total_cost ?? 0)}
              caption={t('admin.ratelimit.kpi.costCap')}
            />
            <StatCard
              icon={<IconCloudStroked />}
              tint="wg-tint-red"
              label={t('admin.ratelimit.kpi.limited')}
              value={formatNumber(usage?.summary.limited_count ?? 0)}
              caption={t('admin.ratelimit.usersCount', { n: usage?.summary.total_users ?? 0 })}
            />
          </div>
          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <span className="wg-pro-card-title">
                {t('admin.ratelimit.tab.users')}
                <span className="wg-cell-dim"> · {formatNumber(usage?.users.length ?? 0)}</span>
              </span>
            </div>
            <Table
              loading={loading}
              rowKey="id"
              columns={userColumns}
              dataSource={usage?.users ?? []}
              pagination={{ pageSize: 20 }}
              tableLayout="fixed"
              empty={<Empty description={t('common.emptyData')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="groups" tab={t('admin.ratelimit.tab.groups')}>
          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <span className="wg-pro-card-title">
                {t('admin.ratelimit.tab.groups')}
                <span className="wg-cell-dim"> · {formatNumber(groups?.summary.total_groups ?? 0)}</span>
              </span>
            </div>
            <Table
              loading={loading}
              rowKey="id"
              columns={groupColumns}
              dataSource={groups?.groups ?? []}
              pagination={{ pageSize: 20 }}
              tableLayout="fixed"
              empty={<Empty description={t('common.emptyData')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="apikeys" tab={t('admin.ratelimit.tab.apikeys')}>
          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <span className="wg-pro-card-title">
                {t('admin.ratelimit.tab.apikeys')}
                <span className="wg-cell-dim"> · {formatNumber(apiKeys?.summary.total_keys ?? 0)}</span>
              </span>
            </div>
            <Table
              loading={loading}
              rowKey="id"
              columns={apiKeyColumns}
              dataSource={apiKeys?.api_keys ?? []}
              pagination={{ pageSize: 20 }}
              tableLayout="fixed"
              empty={<Empty description={t('common.emptyData')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="upstream" tab={t('admin.ratelimit.tab.upstream')}>
          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <span className="wg-pro-card-title">
                {t('admin.ratelimit.tab.upstream')}
                <span className="wg-cell-dim"> · {formatNumber(upstream?.summary.total_keys ?? 0)}</span>
              </span>
            </div>
            <Table
              loading={loading}
              rowKey="id"
              columns={upstreamColumns}
              dataSource={upstream?.upstream_keys ?? []}
              pagination={{ pageSize: 20 }}
              tableLayout="fixed"
              empty={<Empty description={t('common.emptyData')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="capacity" tab={t('admin.ratelimit.tab.capacity')}>
          {loading && !cap ? (
            <div style={{ display: 'flex', justifyContent: 'center', padding: 64 }}>
              <Spin />
            </div>
          ) : cap ? (
            <>
              <div className="wg-stat-grid">
                <StatCard
                  icon={<IconServerStroked />}
                  tint="wg-tint-blue"
                  label={t('admin.ratelimit.kpi.totalRpm')}
                  value={formatNumber(cap.upstream?.total_rpm ?? 0)}
                  caption={t('admin.ratelimit.kpi.keysHint', {
                    enabled: formatNumber(cap.upstream?.enabled_keys ?? 0),
                    total: formatNumber(cap.upstream?.total_keys ?? 0),
                  })}
                />
                <StatCard
                  icon={<IconServerStroked />}
                  tint="wg-tint-blue"
                  label={t('admin.ratelimit.kpi.totalTpm')}
                  value={formatNumber(cap.upstream?.total_tpm ?? 0)}
                  caption={t('admin.ratelimit.kpi.utilization', { pct: `${pct(cap.utilization?.tpm).toFixed(1)}%` })}
                />
                <StatCard
                  icon={<IconCloudStroked />}
                  tint="wg-tint-amber"
                  label={t('admin.ratelimit.kpi.quota')}
                  value={`${formatNumber(cap.upstream?.quota_used ?? 0)} / ${formatNumber(cap.upstream?.total_quota ?? 0)}`}
                  caption={t('admin.ratelimit.kpi.utilization', { pct: `${pct(cap.utilization?.quota).toFixed(1)}%` })}
                />
                <StatCard
                  icon={<IconSendStroked />}
                  tint="wg-tint-green"
                  label={t('admin.ratelimit.kpi.status')}
                  value={statusLabel(cap.summary?.status)}
                  caption={cap.summary?.period}
                />
              </div>

              <section className="wg-pro-card">
                <div className="wg-pro-card-head">
                  <div>
                    <span className="wg-pro-card-title">{t('admin.ratelimit.capacity.title')}</span>
                    <div className="wg-chart-sub">{t('admin.ratelimit.capacity.sub')}</div>
                  </div>
                </div>
                <div className="wg-pro-card-body" style={{ display: 'grid', gap: 20 }}>
                  {[
                    { key: 'rpm', label: t('admin.ratelimit.kpi.avgRpm'), value: formatNumber(Math.round(cap.downstream?.avg_rpm ?? 0)), ratio: cap.utilization?.rpm ?? 0 },
                    { key: 'tpm', label: t('admin.ratelimit.kpi.avgTpm'), value: formatNumber(Math.round(cap.downstream?.avg_tpm ?? 0)), ratio: cap.utilization?.tpm ?? 0 },
                    { key: 'quota', label: t('admin.ratelimit.kpi.quota'), value: `${formatNumber(cap.upstream?.quota_used ?? 0)} / ${formatNumber(cap.upstream?.total_quota ?? 0)}`, ratio: cap.utilization?.quota ?? 0 },
                  ].map((row) => (
                    <div key={row.key} style={{ display: 'grid', gridTemplateColumns: '160px 120px 1fr', alignItems: 'center', gap: 16 }}>
                      <span className="wg-cell-dim">{row.label}</span>
                      <span className="wg-num">{row.value}</span>
                      <Progress
                        percent={pct(row.ratio)}
                        showInfo
                        format={(v) => `${(v ?? 0).toFixed(1)}%`}
                        stroke={row.ratio >= 0.9 ? 'var(--semi-color-danger)' : row.ratio >= 0.7 ? 'var(--semi-color-warning)' : 'var(--semi-color-primary)'}
                        aria-label={row.label}
                      />
                    </div>
                  ))}
                  <div style={{ display: 'flex', gap: 32, flexWrap: 'wrap' }}>
                    <span className="wg-cell-dim">
                      {t('admin.ratelimit.kpi.peakRpm')}: <b className="wg-num">{formatNumber(cap.downstream?.peak_rpm ?? 0)}</b>
                    </span>
                    <span className="wg-cell-dim">
                      {t('admin.ratelimit.kpi.peakTpm')}: <b className="wg-num">{formatNumber(cap.downstream?.peak_tpm ?? 0)}</b>
                    </span>
                  </div>
                </div>
              </section>
            </>
          ) : (
            <Empty description={t('common.emptyData')} />
          )}
        </Tabs.TabPane>
      </Tabs>
    </div>
  );
}
