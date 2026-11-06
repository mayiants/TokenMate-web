import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Button, Card, Empty, Spin } from '@douyinfe/semi-ui';
import type { EChartsOption } from 'echarts';
import {
  IconBriefcaseStroked,
  IconCreditCardStroked,
  IconHistogram,
  IconKeyStroked,
  IconMailStroked,
  IconRefresh,
  IconServerStroked,
  IconTicketCode,
  IconUserGroup,
} from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import EChart from '../../components/EChart';
import { useI18n } from '../../i18n';
import { formatNumber, formatUSD } from '../../api/console';
import {
  getAdminAnalytics,
  getAdminStats,
  getAdminTicketStats,
  getAdminUsageChart,
  listAdminChangeRequests,
  listAdminEnterprises,
  listAdminTickets,
  listEnterpriseApplications,
  type AdminAnalytics,
  type AdminChangeRequest,
  type AdminStats,
  type AdminTicket,
  type AdminUsageChart,
  type EnterpriseApplication,
} from '../../api/admin';

interface OverviewData {
  analytics: AdminAnalytics;
  stats: AdminStats;
  chart: AdminUsageChart;
  tickets: Record<string, number>;
  openTickets: AdminTicket[];
  changes: AdminChangeRequest[];
  applications: EnterpriseApplication[];
  enterpriseCount: number;
}

const EMPTY: OverviewData = {
  analytics: {},
  stats: {},
  chart: {},
  tickets: {},
  openTickets: [],
  changes: [],
  applications: [],
  enterpriseCount: 0,
};

function safe<T>(p: Promise<T>, fallback: T): Promise<T> {
  return p.catch(() => fallback);
}

const TONES = ['wg-tint-blue', 'wg-tint-green', 'wg-tint-amber', 'wg-tint-violet'];

export default function AdminOverview() {
  const { t } = useI18n();
  const [data, setData] = useState<OverviewData | null>(null);

  const load = useCallback(async () => {
    setData(null);
    const [analytics, stats, chart, tickets, openTickets, changes, applications, enterprises] =
      await Promise.all([
        safe(getAdminAnalytics('1d'), {}),
        safe(getAdminStats(), {}),
        safe(getAdminUsageChart('30d'), {}),
        safe(getAdminTicketStats(), {}),
        safe(listAdminTickets('open'), []),
        safe(listAdminChangeRequests('pending'), []),
        safe(listEnterpriseApplications('pending'), []),
        safe(listAdminEnterprises(), []),
      ]);
    setData({
      analytics,
      stats,
      chart,
      tickets,
      openTickets,
      changes,
      applications,
      enterpriseCount: enterprises.length,
    });
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const labels = data?.chart.labels ?? [];
  const spendSeries = useMemo(() => (data?.chart.cost ?? []).map((v) => v / 1_000_000), [data]);
  const requestSeries = data?.chart.requests ?? [];

  const chartBase: EChartsOption = {
    grid: { left: 56, right: 20, top: 24, bottom: 28 },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: labels,
      axisLabel: { fontSize: 10, hideOverlap: true },
    },
    yAxis: { type: 'value' },
  };

  const spendOption: EChartsOption = {
    ...chartBase,
    yAxis: { type: 'value', axisLabel: { formatter: (v: number) => `$${v}` } },
    series: [
      {
        type: 'line',
        smooth: true,
        data: spendSeries,
        itemStyle: { color: '#0086ff' },
        areaStyle: { color: 'rgba(0,134,255,0.12)' },
      },
    ],
  };

  const requestOption: EChartsOption = {
    ...chartBase,
    series: [
      {
        type: 'bar',
        data: requestSeries,
        itemStyle: { color: '#22c55e', borderRadius: [4, 4, 0, 0] },
      },
    ],
  };

  const pendingTickets = (data?.tickets.open ?? 0) + (data?.tickets.pending ?? 0);

  const kpis = [
    {
      icon: <IconUserGroup />,
      label: t('admin.overview.kpi.users'),
      value: formatNumber(data?.analytics.stats?.totalUsers),
      cap: t('admin.overview.kpi.usersCap'),
    },
    {
      icon: <IconBriefcaseStroked />,
      label: t('admin.overview.kpi.enterprises'),
      value: formatNumber(data?.enterpriseCount),
      cap: t('admin.overview.kpi.enterprisesCap'),
    },
    {
      icon: <IconHistogram />,
      label: t('admin.overview.kpi.todayRequests'),
      value: formatNumber(data?.analytics.today?.requests),
      cap: t('admin.overview.kpi.todayRequestsCap'),
    },
    {
      icon: <IconCreditCardStroked />,
      label: t('admin.overview.kpi.todaySpend'),
      value: formatUSD((data?.analytics.today?.revenue ?? 0) / 1_000_000),
      cap: t('admin.overview.kpi.todaySpendCap'),
    },
    {
      icon: <IconKeyStroked />,
      label: t('admin.overview.kpi.activeKeys'),
      value: formatNumber(data?.analytics.stats?.activeKeys),
      cap: t('admin.overview.kpi.activeKeysCap'),
    },
    {
      icon: <IconServerStroked />,
      label: t('admin.overview.kpi.upstream'),
      value: formatNumber(data?.stats.upstreamKeys),
      cap: t('admin.overview.kpi.upstreamCap'),
    },
    {
      icon: <IconTicketCode />,
      label: t('admin.overview.kpi.pendingTickets'),
      value: formatNumber(pendingTickets),
      cap: t('admin.overview.kpi.pendingTicketsCap'),
    },
    {
      icon: <IconMailStroked />,
      label: t('admin.overview.kpi.pendingApplications'),
      value: formatNumber(data?.applications.length),
      cap: t('admin.overview.kpi.pendingApplicationsCap'),
    },
  ];

  return (
    <div className="wg-page">
      <PageHead
        title={t('admin.overview.title')}
        extra={
          <Button icon={<IconRefresh />} theme="light" onClick={load}>
            {t('common.refresh')}
          </Button>
        }
      />

      {data === null ? (
        <div className="wg-page-loading">
          <Spin size="large" />
        </div>
      ) : (
        <>
          <div className="wg-stat-grid">
            {kpis.map((k, i) => (
              <div key={k.label} className="wg-pro-card wg-stat">
                <div className={`wg-stat-icon ${TONES[i % TONES.length]}`}>{k.icon}</div>
                <div className="wg-stat-body">
                  <div className="wg-stat-label">{k.label}</div>
                  <div className="wg-stat-value">{k.value}</div>
                  <div className="wg-stat-cap">{k.cap}</div>
                </div>
              </div>
            ))}
          </div>

          <div className="wg-chart-grid">
            <Card className="wg-chart-card" bordered title={t('admin.overview.chart.spend')}>
              {spendSeries.length === 0 ? (
                <Empty description={t('common.emptyData')} />
              ) : (
                <EChart height={260} option={spendOption} />
              )}
            </Card>
            <Card className="wg-chart-card" bordered title={t('admin.overview.chart.requests')}>
              {requestSeries.length === 0 ? (
                <Empty description={t('common.emptyData')} />
              ) : (
                <EChart height={260} option={requestOption} />
              )}
            </Card>
          </div>

          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))',
              gap: 16,
            }}
          >
            <TodoCard
              title={t('admin.overview.todo.changeRequests')}
              extra={
                <Link to="/console/admin/enterprises?tab=change" className="wg-cell-dim">
                  {t('admin.overview.todo.viewAll')}
                </Link>
              }
              empty={t('admin.overview.todo.empty')}
              items={data.changes.slice(0, 5).map((c) => (
                <div key={c.id} className="wg-todo-item">
                  <span className="wg-cell-strong">{c.enterprise_name || '—'}</span>
                  <span className="wg-cell-dim">
                    {c.field}: {c.old_value || '—'} → {c.new_value || '—'}
                  </span>
                </div>
              ))}
            />
            <TodoCard
              title={t('admin.overview.todo.tickets')}
              empty={t('admin.overview.todo.empty')}
              items={data.openTickets.slice(0, 5).map((tk) => (
                <div key={tk.id} className="wg-todo-item">
                  <span className="wg-cell-strong">
                    #{tk.ticket_no ?? '—'} {tk.subject ?? ''}
                  </span>
                  <span className="wg-cell-dim">{tk.requester_email ?? ''}</span>
                </div>
              ))}
            />
            <TodoCard
              title={t('admin.overview.todo.applications')}
              extra={
                <Link to="/console/admin/enterprises" className="wg-cell-dim">
                  {t('admin.overview.todo.viewAll')}
                </Link>
              }
              empty={t('admin.overview.todo.empty')}
              items={data.applications.slice(0, 5).map((a) => (
                <div key={a.id} className="wg-todo-item">
                  <span className="wg-cell-strong">{a.enterprise_name || '—'}</span>
                  <span className="wg-cell-dim">{a.applicant_email || a.contact_email || ''}</span>
                </div>
              ))}
            />
          </div>
        </>
      )}
    </div>
  );
}

function TodoCard({
  title,
  extra,
  items,
  empty,
}: {
  title: string;
  extra?: ReactNode;
  items: ReactNode[];
  empty: string;
}) {
  return (
    <section className="wg-pro-card">
      <div className="wg-pro-card-head">
        <span className="wg-pro-card-title">{title}</span>
        {extra && <span className="wg-pro-card-extra">{extra}</span>}
      </div>
      <div className="wg-pro-card-body" style={{ paddingTop: 0 }}>
        {items.length === 0 ? (
          <Empty description={empty} />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>{items}</div>
        )}
      </div>
    </section>
  );
}
