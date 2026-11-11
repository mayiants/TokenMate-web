import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  Button,
  DatePicker,
  Empty,
  Select,
  SideSheet,
  Spin,
  Table,
  Toast,
} from '@douyinfe/semi-ui';
import {
  IconClock,
  IconCoinMoneyStroked,
  IconExport,
  IconHistogram,
  IconRefresh,
  IconSearch,
  IconSendStroked,
} from '@douyinfe/semi-icons';
import type { EChartsOption } from 'echarts';
import PageHead from '../../components/PageHead';
import EChart from '../../components/EChart';
import { useI18n } from '../../i18n';
import { formatDateTime, formatNumber, formatUSD, quotaToUSD } from '../../api/console';
import {
  EXPLORER_DIMENSIONS,
  exportUsageExplorerAggregate,
  exportUsageExplorerLogs,
  getUsageExplorerAggregate,
  getUsageExplorerLogs,
  type ExplorerDimension,
  type ExplorerPeriod,
  type ExplorerRangeParams,
  type ExplorerUsageRecord,
  type UsageExplorerAggregate,
  type UsageExplorerRow,
} from '../../api/adminExplorer';

const PERIODS: ExplorerPeriod[] = ['24h', '7d', '15d', '30d', '90d'];

function ymd(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

function cssVar(name: string, fallback: string): string {
  const v = getComputedStyle(document.body).getPropertyValue(name).trim();
  return v || fallback;
}

function StatCard({
  icon,
  tint,
  label,
  value,
  caption,
  loading,
}: {
  icon: ReactNode;
  tint: string;
  label: string;
  value: string;
  caption?: string;
  loading?: boolean;
}) {
  return (
    <div className="wg-pro-card wg-stat">
      <span className={`wg-stat-icon ${tint}`}>{icon}</span>
      <div className="wg-stat-body">
        <div className="wg-stat-label">{label}</div>
        <div className="wg-stat-value">{loading ? <Spin size="small" /> : value}</div>
        {caption && <div className="wg-stat-cap">{caption}</div>}
      </div>
    </div>
  );
}

/** Admin usage explorer: aggregate by one dimension, drill into raw records. */
export default function UsageExplorer() {
  const { t } = useI18n();
  const [dimension, setDimension] = useState<ExplorerDimension>('user');
  const [period, setPeriod] = useState<ExplorerPeriod>('7d');
  const [range, setRange] = useState<[Date, Date] | null>(null);
  const [limit, setLimit] = useState(50);

  const [data, setData] = useState<UsageExplorerAggregate | null>(null);
  const [loading, setLoading] = useState(true);
  const [exporting, setExporting] = useState(false);

  // drill-down
  const [drill, setDrill] = useState<UsageExplorerRow | null>(null);
  const [records, setRecords] = useState<ExplorerUsageRecord[] | null>(null);
  const [recordsLoading, setRecordsLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);

  const rangeParams = useMemo<ExplorerRangeParams>(
    () => (range ? { date_from: ymd(range[0]), date_to: ymd(range[1]) } : { period }),
    [range, period]
  );

  const load = useCallback(
    async (dim: ExplorerDimension = dimension, params: ExplorerRangeParams = rangeParams, lim = limit) => {
      setLoading(true);
      try {
        setData(await getUsageExplorerAggregate(dim, params, lim));
      } catch (e) {
        setData(null);
        Toast.error(e instanceof Error ? e.message : t('common.error'));
      } finally {
        setLoading(false);
      }
    },
    [dimension, rangeParams, limit, t]
  );

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadRecords = useCallback(
    async (row: UsageExplorerRow, p = 1) => {
      setRecordsLoading(true);
      try {
        const res = await getUsageExplorerLogs(dimension, row.id, rangeParams, p, 20);
        setRecords(res.records ?? []);
        setTotal(res.total);
        setPage(res.page);
      } catch (e) {
        setRecords([]);
        Toast.error(e instanceof Error ? e.message : t('common.error'));
      } finally {
        setRecordsLoading(false);
      }
    },
    [dimension, rangeParams, t]
  );

  const openDrill = (row: UsageExplorerRow) => {
    setDrill(row);
    setRecords(null);
    setPage(1);
    loadRecords(row, 1);
  };

  const onExportAggregate = async () => {
    setExporting(true);
    try {
      await exportUsageExplorerAggregate(dimension, rangeParams);
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setExporting(false);
    }
  };

  const onExportLogs = async () => {
    if (!drill) return;
    try {
      await exportUsageExplorerLogs(dimension, drill.id, rangeParams);
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    }
  };

  const summary = data?.summary;
  const rows = data?.rows ?? [];
  const periodLabel = range ? `${ymd(range[0])} ~ ${ymd(range[1])}` : t(`admin.explorer.period.${period}`);

  /* ---------- chart: top rows by cost ---------- */
  const chartOption = useMemo<EChartsOption>(() => {
    const top = [...rows].sort((a, b) => b.total_cost - a.total_cost).slice(0, 10).reverse();
    const axisColor = cssVar('--wg-text-3', '#8f959e');
    const splitColor = cssVar('--wg-border', 'rgba(28,31,35,.08)');
    return {
      grid: { left: 8, right: 24, top: 8, bottom: 8, containLabel: true },
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        valueFormatter: (v) => formatUSD(Number(v ?? 0)),
      },
      xAxis: {
        type: 'value',
        axisLabel: {
          color: axisColor,
          fontSize: 11,
          formatter: (v: number) => (v >= 1000 ? `$${(v / 1000).toFixed(1)}k` : `$${v}`),
        },
        splitLine: { lineStyle: { color: splitColor } },
      },
      yAxis: {
        type: 'category',
        data: top.map((r) => r.name || r.id),
        axisTick: { show: false },
        axisLine: { lineStyle: { color: splitColor } },
        axisLabel: { color: axisColor, fontSize: 11, width: 120, overflow: 'truncate' },
      },
      series: [
        {
          type: 'bar',
          barMaxWidth: 18,
          itemStyle: { borderRadius: [0, 4, 4, 0], color: '#0086ff' },
          data: top.map((r) => Number(quotaToUSD(r.total_cost).toFixed(4))),
        },
      ],
    };
  }, [rows]);

  const columns: any[] = [
    {
      title: '#',
      width: 56,
      render: (_: unknown, __: UsageExplorerRow, i: number) => (
        <span className="wg-cell-dim wg-num">{i + 1}</span>
      ),
    },
    {
      title: t(`admin.explorer.col.${dimension}`),
      dataIndex: 'name',
      render: (v: string, r: UsageExplorerRow) => (
        <Button theme="borderless" size="small" type="primary" onClick={() => openDrill(r)}>
          {v || r.id}
        </Button>
      ),
    },
    ...(dimension === 'user'
      ? [
          {
            title: t('admin.explorer.col.email'),
            dataIndex: 'email',
            width: 200,
            render: (v: string) => <span className="wg-cell-dim">{v || '—'}</span>,
          },
        ]
      : []),
    ...(dimension === 'key'
      ? [
          {
            title: t('admin.explorer.col.keyPrefix'),
            dataIndex: 'key_prefix',
            width: 160,
            render: (v: string) => <span className="wg-num">{v || '—'}</span>,
          },
        ]
      : []),
    ...(dimension === 'subscription_plan'
      ? [
          {
            title: t('admin.explorer.col.subscribers'),
            dataIndex: 'subscribers',
            width: 110,
            render: (v: number) => <span className="wg-num">{formatNumber(v ?? 0)}</span>,
          },
        ]
      : []),
    {
      title: t('admin.explorer.col.requests'),
      dataIndex: 'requests',
      width: 110,
      render: (v: number) => <span className="wg-num">{formatNumber(v ?? 0)}</span>,
    },
    {
      title: t('admin.explorer.col.inputTokens'),
      dataIndex: 'input_tokens',
      width: 130,
      render: (v: number) => <span className="wg-num">{formatNumber(v ?? 0)}</span>,
    },
    {
      title: t('admin.explorer.col.outputTokens'),
      dataIndex: 'output_tokens',
      width: 130,
      render: (v: number) => <span className="wg-num">{formatNumber(v ?? 0)}</span>,
    },
    {
      title: t('admin.explorer.col.cachedTokens'),
      dataIndex: 'cached_tokens',
      width: 130,
      render: (v: number) => <span className="wg-num">{formatNumber(v ?? 0)}</span>,
    },
    {
      title: t('admin.explorer.col.cost'),
      dataIndex: 'total_cost',
      width: 130,
      render: (v: number) => (
        <span className="wg-num wg-cell-strong">{formatUSD(quotaToUSD(v ?? 0))}</span>
      ),
    },
    {
      title: t('admin.explorer.col.latency'),
      dataIndex: 'avg_latency_ms',
      width: 120,
      render: (v: number) => <span className="wg-num">{v ? `${formatNumber(v)} ms` : '—'}</span>,
    },
  ];

  const recordColumns: any[] = [
    {
      title: t('admin.explorer.log.time'),
      dataIndex: 'created_at',
      width: 160,
      render: (v: string) => <span className="wg-cell-dim">{formatDateTime(v)}</span>,
    },
    { title: t('admin.explorer.log.model'), dataIndex: 'model', width: 180 },
    {
      title: t('admin.explorer.log.tokens'),
      width: 160,
      render: (_: unknown, r: ExplorerUsageRecord) => (
        <span className="wg-num">
          {formatNumber(r.input_tokens ?? 0)} / {formatNumber(r.output_tokens ?? 0)}
        </span>
      ),
    },
    {
      title: t('admin.explorer.log.cost'),
      dataIndex: 'cost',
      width: 110,
      render: (v: number) => <span className="wg-num">{formatUSD(quotaToUSD(v ?? 0))}</span>,
    },
    {
      title: t('admin.explorer.log.latency'),
      dataIndex: 'latency_ms',
      width: 110,
      render: (v: number) => <span className="wg-num">{v ? `${formatNumber(v)} ms` : '—'}</span>,
    },
    {
      title: t('admin.explorer.log.status'),
      dataIndex: 'status',
      width: 110,
      render: (v: string) => <span className="wg-cell-dim">{v || '—'}</span>,
    },
    {
      title: t('admin.explorer.log.endpoint'),
      dataIndex: 'endpoint',
      width: 180,
      render: (v: string) => <span className="wg-cell-ellipsis wg-cell-dim">{v || '—'}</span>,
    },
  ];

  return (
    <div className="wg-page">
      <PageHead title={t('admin.explorer.title')} sub={periodLabel} />

      <div className="wg-pro-card wg-filter-card">
        <Select
          style={{ width: 170 }}
          value={dimension}
          aria-label={t('admin.explorer.dimLabel')}
          onChange={(v) => {
            const dim = (v as ExplorerDimension) ?? 'user';
            setDimension(dim);
            load(dim, rangeParams, limit);
          }}
          optionList={EXPLORER_DIMENSIONS.map((d) => ({
            value: d,
            label: t(`admin.explorer.dim.${d}`),
          }))}
        />
        <Select
          style={{ width: 140 }}
          value={period}
          disabled={!!range}
          aria-label={t('admin.explorer.periodLabel')}
          onChange={(v) => {
            const p = (v as ExplorerPeriod) ?? '7d';
            setPeriod(p);
            load(dimension, { period: p }, limit);
          }}
          optionList={PERIODS.map((p) => ({ value: p, label: t(`admin.explorer.period.${p}`) }))}
        />
        <DatePicker
          type="dateRange"
          style={{ width: 240 }}
          placeholder={[t('dash.range.start'), t('dash.range.end')]}
          value={range ?? undefined}
          onChange={(v) => {
            const next = (v as [Date, Date] | null) ?? null;
            setRange(next);
            load(
              dimension,
              next ? { date_from: ymd(next[0]), date_to: ymd(next[1]) } : { period },
              limit
            );
          }}
        />
        <div className="wg-filter-actions">
          <Button type="primary" theme="solid" icon={<IconSearch />} onClick={() => load()}>
            {t('common.search')}
          </Button>
          <Button
            icon={<IconRefresh />}
            theme="light"
            onClick={() => {
              setRange(null);
              setPeriod('7d');
              load(dimension, { period: '7d' }, limit);
            }}
          >
            {t('common.reset')}
          </Button>
        </div>
      </div>

      <div className="wg-stat-grid">
        <StatCard
          icon={<IconSendStroked />}
          tint="wg-tint-blue"
          label={t('admin.explorer.kpi.requests')}
          value={formatNumber(summary?.requests ?? 0)}
          caption={periodLabel}
          loading={loading && !summary}
        />
        <StatCard
          icon={<IconHistogram />}
          tint="wg-tint-blue"
          label={t('admin.explorer.kpi.input')}
          value={formatNumber(summary?.input_tokens ?? 0)}
          caption={t('admin.explorer.kpi.inputCap')}
          loading={loading && !summary}
        />
        <StatCard
          icon={<IconHistogram />}
          tint="wg-tint-green"
          label={t('admin.explorer.kpi.output')}
          value={formatNumber(summary?.output_tokens ?? 0)}
          caption={t('admin.explorer.kpi.outputCap')}
          loading={loading && !summary}
        />
        <StatCard
          icon={<IconCoinMoneyStroked />}
          tint="wg-tint-amber"
          label={t('admin.explorer.kpi.cost')}
          value={formatUSD(quotaToUSD(summary?.total_cost ?? 0))}
          caption={t('admin.explorer.kpi.costCap')}
          loading={loading && !summary}
        />
      </div>

      <section className="wg-pro-card">
        <div className="wg-pro-card-head">
          <div>
            <span className="wg-pro-card-title">{t('admin.explorer.chart.title')}</span>
            <div className="wg-chart-sub">
              {t('admin.explorer.chart.sub', { dimension: t(`admin.explorer.dim.${dimension}`) })}
            </div>
          </div>
          <span className="wg-chart-total">{formatUSD(quotaToUSD(summary?.total_cost ?? 0))}</span>
        </div>
        <div className="wg-pro-card-body">
          {loading && !rows.length ? (
            <div style={{ display: 'flex', justifyContent: 'center', padding: 64 }}>
              <Spin />
            </div>
          ) : rows.length ? (
            <EChart option={chartOption} height={Math.max(240, Math.min(rows.length, 10) * 32 + 40)} />
          ) : (
            <Empty description={t('common.emptyData')} style={{ padding: 48 }} />
          )}
        </div>
      </section>

      <div className="wg-pro-card wg-table-card">
        <div className="wg-table-toolbar">
          <div className="wg-table-toolbar-left">
            <span className="wg-pro-card-title">
              {t('admin.explorer.tableTitle', { dimension: t(`admin.explorer.dim.${dimension}`) })}
              <span className="wg-cell-dim"> · {formatNumber(rows.length)}</span>
            </span>
          </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <Select
              style={{ width: 130 }}
              value={limit}
              aria-label={t('admin.explorer.limitLabel')}
              onChange={(v) => {
                const n = Number(v) || 50;
                setLimit(n);
                load(dimension, rangeParams, n);
              }}
              optionList={[20, 50, 100, 200].map((n) => ({ value: n, label: `Top ${n}` }))}
            />
            <Button theme="light" icon={<IconExport />} loading={exporting} onClick={onExportAggregate}>
              {t('common.export')}
            </Button>
          </div>
        </div>
        <Table
          loading={loading}
          rowKey="id"
          columns={columns}
          dataSource={rows}
          pagination={false}
          tableLayout="fixed"
          empty={<Empty description={t('common.emptyData')} />}
        />
      </div>

      <SideSheet
        visible={!!drill}
        onCancel={() => {
          setDrill(null);
          setRecords(null);
        }}
        width={880}
        title={
          <span>
            {t('admin.explorer.logsTitle')}
            {drill ? ` · ${drill.name || drill.id}` : ''}
          </span>
        }
        footer={
          <Button icon={<IconExport />} onClick={onExportLogs}>
            {t('common.export')}
          </Button>
        }
      >
        {drill && (
          <div className="wg-stat-grid" style={{ marginBottom: 16 }}>
            <StatCard
              icon={<IconSendStroked />}
              tint="wg-tint-blue"
              label={t('admin.explorer.col.requests')}
              value={formatNumber(drill.requests ?? 0)}
            />
            <StatCard
              icon={<IconClock />}
              tint="wg-tint-green"
              label={t('admin.explorer.col.latency')}
              value={drill.avg_latency_ms ? `${formatNumber(drill.avg_latency_ms)} ms` : '—'}
            />
            <StatCard
              icon={<IconCoinMoneyStroked />}
              tint="wg-tint-amber"
              label={t('admin.explorer.col.cost')}
              value={formatUSD(quotaToUSD(drill.total_cost ?? 0))}
            />
          </div>
        )}
        <Table
          loading={recordsLoading}
          rowKey="trace_id"
          columns={recordColumns}
          dataSource={records ?? []}
          pagination={{
            currentPage: page,
            pageSize: 20,
            total,
            onPageChange: (p) => {
              if (drill) loadRecords(drill, p);
            },
          }}
          tableLayout="fixed"
          empty={<Empty description={t('common.emptyData')} />}
        />
      </SideSheet>
    </div>
  );
}
