import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, DatePicker, Empty, Spin, Tabs, Tooltip } from '@douyinfe/semi-ui';
import type { EChartsOption } from 'echarts';
import {
  IconBolt,
  IconClock,
  IconCreditCardStroked,
  IconExport,
  IconHistogram,
  IconInfoCircle,
  IconSendStroked,
  IconTickCircle,
  IconTicketCode,
} from '@douyinfe/semi-icons';
import EChart from '../../components/EChart';
import { useAuth } from '../../auth/AuthContext';
import { useI18n } from '../../i18n';
import {
  formatNumber,
  formatRate,
  formatUSD,
  getBalance,
  getSuccessfulRequestsCount,
  getUsageByModel,
  getUsageRecords,
  getUsageSummary,
  getUsageTrend,
  type Balance,
  type RangeKey,
  type UsageByModelRow,
  type UsageSummary,
  type UsageTrendPoint,
} from '../../api/console';

const PALETTE = [
  '#0086ff', '#22c55e', '#f59e0b', '#ef4444', '#8b5cf6', '#06b6d4',
  '#ec4899', '#84cc16', '#f97316', '#14b8a6', '#eab308', '#6366f1',
];
const OTHER_COLOR = '#9aa1a9';
const TOP_MODELS = 12;
const DIST_MAX_PAGES = 5;
const DIST_PAGE_SIZE = 200;

type RangeKeyUI = 'all' | '30d' | '7d' | '1d';

const RANGE_DAYS: Record<RangeKeyUI, number> = { all: 90, '30d': 30, '7d': 7, '1d': 1 };
const RANGE_ORDER: RangeKey[] = ['7d', '15d', '30d', 'quarter'];

function pad(n: number): string {
  return String(n).padStart(2, '0');
}

function toDateStr(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

function cssVar(name: string, fallback: string): string {
  const v = getComputedStyle(document.body).getPropertyValue(name).trim();
  return v || fallback;
}

function nearestApiRange(days: number): RangeKey {
  for (const r of RANGE_ORDER) {
    const d = r === 'quarter' ? 90 : parseInt(r, 10);
    if (days <= d) return r;
  }
  return 'quarter';
}

interface DistDatum {
  /** bucket label -> model -> cost USD */
  buckets: Map<string, Map<string, number>>;
  partial: boolean;
}

async function loadDistribution(start: string, end: string, hourly: boolean): Promise<DistDatum> {
  const buckets = new Map<string, Map<string, number>>();
  let fetched = 0;
  let total = 0;
  for (let page = 1; page <= DIST_MAX_PAGES; page++) {
    const res = await getUsageRecords({ page, page_size: DIST_PAGE_SIZE, start_date: start, end_date: end });
    total = res.total;
    fetched += res.records.length;
    for (const r of res.records) {
      const d = new Date(r.created_at);
      if (Number.isNaN(d.getTime())) continue;
      const label = hourly ? `${pad(Math.floor(d.getHours() / 4) * 4)}:00` : `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
      let byModelMap = buckets.get(label);
      if (!byModelMap) {
        byModelMap = new Map();
        buckets.set(label, byModelMap);
      }
      const cost = typeof r.total_cost === 'number' ? r.total_cost : 0;
      byModelMap.set(r.model, (byModelMap.get(r.model) ?? 0) + cost);
    }
    if (page >= res.total_pages || res.records.length === 0) break;
  }
  return { buckets, partial: total > fetched };
}

export default function Dashboard() {
  const { t } = useI18n();
  const { user } = useAuth();

  const [range, setRange] = useState<RangeKeyUI>('all');
  const [customRange, setCustomRange] = useState<[Date, Date] | null>(null);
  const [chartTab, setChartTab] = useState<'dist' | 'trend' | 'rank'>('dist');

  const [balance, setBalance] = useState<Balance | null>(null);
  const [summary, setSummary] = useState<UsageSummary | null>(null);
  const [successTotal, setSuccessTotal] = useState<number | null>(null);
  const [byModel, setByModel] = useState<UsageByModelRow[]>([]);
  const [trend, setTrend] = useState<UsageTrendPoint[]>([]);
  const [dist, setDist] = useState<DistDatum>({ buckets: new Map(), partial: false });
  const [loading, setLoading] = useState(true);

  const days = customRange
    ? Math.max(1, Math.round((customRange[1].getTime() - customRange[0].getTime()) / 86_400_000) + 1)
    : RANGE_DAYS[range];
  const apiRange: RangeKey = customRange ? nearestApiRange(days) : range === 'all' ? 'quarter' : (range as RangeKey);
  const isHourly = apiRange === '1d';
  const startDate = customRange
    ? toDateStr(customRange[0])
    : toDateStr(new Date(Date.now() - (days - 1) * 86_400_000));
  const endDate = customRange ? toDateStr(customRange[1]) : toDateStr(new Date());

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [bal, sum, okTotal, models, trendPoints, distData] = await Promise.all([
        getBalance(),
        getUsageSummary(days),
        getSuccessfulRequestsCount(startDate, endDate),
        getUsageByModel(apiRange),
        getUsageTrend(apiRange),
        loadDistribution(startDate, endDate, isHourly),
      ]);
      setBalance(bal);
      setSummary(sum);
      setSuccessTotal(okTotal);
      setByModel(models);
      setTrend(trendPoints);
      setDist(distData);
    } catch {
      // auth failures redirect globally; missing sections degrade to empty
    } finally {
      setLoading(false);
    }
  }, [days, apiRange, startDate, endDate, isHourly]);

  useEffect(() => {
    load();
  }, [load]);

  /* ---------- greeting ---------- */
  const hour = new Date().getHours();
  const greetKey =
    hour < 5 ? 'dash.greet.evening' : hour < 12 ? 'dash.greet.morning' : hour < 18 ? 'dash.greet.afternoon' : 'dash.greet.evening';
  const displayName = user?.name || user?.email || '';

  /* ---------- stat cards (unified primary/success/warning tints) ---------- */
  const minutes = days * 24 * 60;
  const usageStats = [
    { icon: <IconCreditCardStroked />, tint: 'wg-tint-blue', label: t('dash.kpi.spend'), value: formatUSD(summary?.total_cost_usd), caption: t('dash.kpi.spendCap') },
    { icon: <IconSendStroked />, tint: 'wg-tint-green', label: t('dash.kpi.requests'), value: formatNumber(summary?.total_requests), caption: t('dash.kpi.requestsCap') },
    { icon: <IconClock />, tint: 'wg-tint-amber', label: t('dash.kpi.rpm'), value: formatRate((summary?.total_requests ?? 0) / minutes), caption: t('dash.kpi.rpmCap') },
    { icon: <IconHistogram />, tint: 'wg-tint-blue', label: t('dash.kpi.tokens'), value: formatNumber(summary?.total_tokens), caption: t('dash.kpi.tokensCap') },
    { icon: <IconTickCircle />, tint: 'wg-tint-green', label: t('dash.kpi.success'), value: successTotal === null ? '—' : formatNumber(successTotal), caption: t('dash.kpi.successCap') },
    { icon: <IconBolt />, tint: 'wg-tint-amber', label: t('dash.kpi.tpm'), value: formatRate((summary?.total_tokens ?? 0) / minutes), caption: t('dash.kpi.tpmCap') },
  ];

  /* ---------- chart options ---------- */
  const axisColor = cssVar('--wg-text-3', '#8f959e');
  const splitColor = cssVar('--wg-border', 'rgba(28,31,35,.08)');
  const tooltipBg = cssVar('--wg-surface', '#ffffff');
  const tooltipText = cssVar('--wg-text', '#1c1f23');
  const tooltipSub = cssVar('--wg-text-2', '#646a73');

  const bucketLabels = useMemo(
    () => trend.map((p) => (isHourly ? p.time || p.date : p.date.slice(5))),
    [trend, isHourly]
  );

  const distOption = useMemo<EChartsOption>(() => {
    const modelTotals = new Map<string, number>();
    dist.buckets.forEach((models) =>
      models.forEach((cost, model) => modelTotals.set(model, (modelTotals.get(model) ?? 0) + cost))
    );
    const top = [...modelTotals.entries()].sort((a, b) => b[1] - a[1]).slice(0, TOP_MODELS).map(([m]) => m);
    const otherLabel = t('dash.chart.other');
    const hasOther = modelTotals.size > TOP_MODELS;
    const names = hasOther ? [...top, otherLabel] : top;
    const colorMap = new Map<string, string>();
    top.forEach((m, i) => colorMap.set(m, PALETTE[i % PALETTE.length]));
    if (hasOther) colorMap.set(otherLabel, OTHER_COLOR);

    const series = names.map((name) => ({
      name,
      type: 'bar' as const,
      stack: 'cost',
      barMaxWidth: 28,
      itemStyle: { color: colorMap.get(name) },
      data: bucketLabels.map((label) => {
        const models = dist.buckets.get(label);
        if (!models) return 0;
        if (name === otherLabel) {
          let rest = 0;
          models.forEach((cost, model) => {
            if (!top.includes(model)) rest += cost;
          });
          return rest;
        }
        return models.get(name) ?? 0;
      }),
    }));

    return {
      animation: false,
      grid: { left: 56, right: 16, top: 24, bottom: 60 },
      legend: { type: 'scroll', bottom: 0, textStyle: { color: axisColor, fontSize: 11 }, itemWidth: 10, itemHeight: 10, icon: 'roundRect', pageIconColor: axisColor },
      xAxis: {
        type: 'category',
        data: bucketLabels,
        axisLine: { lineStyle: { color: splitColor } },
        axisTick: { show: false },
        axisLabel: { color: axisColor, fontSize: 11 },
      },
      yAxis: {
        type: 'value',
        axisLabel: { color: axisColor, fontSize: 11, formatter: (v: number) => `$${v >= 1000 ? `${(v / 1000).toFixed(1)}k` : v}` },
        splitLine: { lineStyle: { color: splitColor } },
      },
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        backgroundColor: tooltipBg,
        borderWidth: 1,
        borderColor: splitColor,
        textStyle: { color: tooltipText },
        formatter: (params) => {
          const list = Array.isArray(params) ? params : [params];
          const label = String((list[0] as { axisValue?: string }).axisValue ?? '');
          let total = 0;
          const rows: string[] = [];
          list.forEach((p) => {
            const value = typeof p.value === 'number' ? p.value : 0;
            if (value <= 0) return;
            total += value;
            rows.push(
              `<div style="display:flex;justify-content:space-between;gap:24px;margin-top:3px"><span>${p.marker ?? ''}<span style="color:${tooltipSub};font-size:12px">${p.seriesName}</span></span><b style="font-variant-numeric:tabular-nums">${formatUSD(value)}</b></div>`
            );
          });
          if (!rows.length) {
            return `<b>${label}</b><div style="margin-top:4px;color:${tooltipSub};font-size:12px">${formatUSD(0)}</div>`;
          }
          return `<b>${label}</b><div style="display:flex;justify-content:space-between;gap:24px;margin-top:4px"><span style="color:${tooltipSub}">${t('dash.chart.total')}</span><b style="font-variant-numeric:tabular-nums">${formatUSD(total)}</b></div><div style="margin-top:6px;border-top:1px solid ${splitColor};padding-top:6px">${rows.join('')}</div>`;
        },
      },
      series,
    };
  }, [dist, bucketLabels, axisColor, splitColor, tooltipBg, tooltipText, tooltipSub, t]);

  const trendOption = useMemo<EChartsOption>(() => {
    return {
      animation: false,
      grid: { left: 56, right: 16, top: 24, bottom: 40 },
      xAxis: {
        type: 'category',
        data: bucketLabels,
        boundaryGap: false,
        axisLine: { lineStyle: { color: splitColor } },
        axisTick: { show: false },
        axisLabel: { color: axisColor, fontSize: 11 },
      },
      yAxis: {
        type: 'value',
        axisLabel: { color: axisColor, fontSize: 11, formatter: (v: number) => `$${v >= 1000 ? `${(v / 1000).toFixed(1)}k` : v}` },
        splitLine: { lineStyle: { color: splitColor } },
      },
      tooltip: {
        trigger: 'axis',
        backgroundColor: tooltipBg,
        borderWidth: 1,
        borderColor: splitColor,
        textStyle: { color: tooltipText },
        valueFormatter: (v) => formatUSD(Number(v)),
      },
      series: [
        {
          name: t('dash.tab.trend'),
          type: 'line',
          smooth: true,
          showSymbol: false,
          lineStyle: { width: 2, color: '#0086ff' },
          itemStyle: { color: '#0086ff' },
          areaStyle: { color: 'rgba(0,134,255,0.12)' },
          data: trend.map((p) => (typeof p.cost === 'number' ? p.cost / 1_000_000 : 0)),
        },
      ],
    };
  }, [trend, bucketLabels, axisColor, splitColor, tooltipBg, tooltipText, t]);

  const rankingOption = useMemo<EChartsOption>(() => {
    const top10 = [...byModel].sort((a, b) => b.requests - a.requests).slice(0, 10);
    return {
      animation: false,
      grid: { left: 8, right: 40, top: 8, bottom: 8, containLabel: true },
      xAxis: {
        type: 'value',
        axisLabel: { color: axisColor, fontSize: 11 },
        splitLine: { lineStyle: { color: splitColor } },
      },
      yAxis: {
        type: 'category',
        inverse: true,
        data: top10.map((m) => m.model),
        axisLine: { show: false },
        axisTick: { show: false },
        axisLabel: { color: tooltipText, fontSize: 11 },
      },
      tooltip: {
        backgroundColor: tooltipBg,
        borderWidth: 1,
        borderColor: splitColor,
        textStyle: { color: tooltipText },
        formatter: (p) => {
          const item = Array.isArray(p) ? p[0] : p;
          const model = top10[item?.dataIndex ?? 0];
          if (!model) return '';
          return `<b>${model.model}</b><div style="margin-top:4px;display:flex;justify-content:space-between;gap:24px"><span style="color:${tooltipSub};font-size:12px">${t('dash.kpi.requests')}</span><b>${formatNumber(model.requests)}</b></div><div style="display:flex;justify-content:space-between;gap:24px;margin-top:2px"><span style="color:${tooltipSub};font-size:12px">${t('dash.kpi.spend')}</span><b>${formatUSD(model.cost)}</b></div>`;
        },
      },
      series: [
        {
          type: 'bar',
          barMaxWidth: 14,
          itemStyle: { color: '#0086ff', borderRadius: [0, 7, 7, 0] },
          data: top10.map((m) => m.requests),
        },
      ],
    };
  }, [byModel, axisColor, splitColor, tooltipBg, tooltipText, tooltipSub, t]);

  const hasDistData = dist.buckets.size > 0;
  const giftQuota = balance?.gift_balance_quota ?? 0;

  return (
    <div className="wg-page">
      <div className="wg-page-head">
        <div>
          <h2 className="wg-page-title">{t('console.nav.dashboard')}</h2>
          <p className="wg-page-sub">
            {t(greetKey)}
            {t('dash.greet.sep')}
            {displayName}
          </p>
        </div>
        <div className="wg-page-actions">
          {(['all', '30d', '7d', '1d'] as RangeKeyUI[]).map((r) => (
            <button
              key={r}
              type="button"
              className={`wg-range-pill${!customRange && range === r ? ' active' : ''}`}
              onClick={() => {
                setRange(r);
                setCustomRange(null);
              }}
            >
              {t(`dash.range.${r}`)}
            </button>
          ))}
          <DatePicker
            type="dateRange"
            size="small"
            style={{ width: 220 }}
            placeholder={[t('dash.range.start'), t('dash.range.end')]}
            value={customRange ?? undefined}
            onChange={(v) => setCustomRange((v as [Date, Date] | null) ?? null)}
          />
        </div>
      </div>

      <div className="wg-sec-row">
        <span className="wg-sec-row-title">{t('dash.balance.title')}</span>
        <Link to="/console/balance" className="wg-card-link">
          {t('common.viewAll')}
        </Link>
      </div>
      <div className="wg-stat-grid">
        <div className="wg-pro-card wg-stat">
          <span className="wg-stat-icon wg-tint-blue">
            <IconCreditCardStroked />
          </span>
          <div className="wg-stat-body">
            <div className="wg-stat-label">{t('dash.balance.cash')}</div>
            <div className="wg-stat-value">
              {balance ? formatUSD(balance.balance_usd) : loading ? <Spin size="small" /> : '—'}
            </div>
            <div className="wg-stat-cap">{t('dash.balance.cashCap')}</div>
          </div>
        </div>
        {giftQuota > 0 && (
          <div className="wg-pro-card wg-stat">
            <span className="wg-stat-icon wg-tint-amber">
              <IconTicketCode />
            </span>
            <div className="wg-stat-body">
              <div className="wg-stat-label">{t('dash.balance.voucher')}</div>
              <div className="wg-stat-value">{formatUSD(balance?.gift_balance_usd)}</div>
              <div className="wg-stat-cap">{t('dash.balance.voucherCap')}</div>
            </div>
          </div>
        )}
      </div>

      <div className="wg-sec-row">
        <span className="wg-sec-row-title">
          {t('dash.summary.title')}
          <Tooltip content={t('dash.summary.hint')}>
            <span className="wg-hint">
              {t('dash.summary.follows')}
              <IconInfoCircle size="small" />
            </span>
          </Tooltip>
        </span>
      </div>
      <div className="wg-stat-grid">
        {usageStats.map((k) => (
          <div key={k.label} className="wg-pro-card wg-stat">
            <span className={`wg-stat-icon ${k.tint}`}>{k.icon}</span>
            <div className="wg-stat-body">
              <div className="wg-stat-label">{k.label}</div>
              <div className="wg-stat-value">{loading && !summary ? <Spin size="small" /> : k.value}</div>
              <div className="wg-stat-cap">{k.caption}</div>
            </div>
          </div>
        ))}
      </div>

      <section className="wg-pro-card">
        <div className="wg-pro-card-head">
          <div>
            <span className="wg-pro-card-title">{t('dash.chart.title')}</span>
            <div className="wg-chart-sub">
              {t('dash.chart.distTitle')}
              <span className="wg-chart-total">
                {t('dash.chart.totalSpend')}: {formatUSD(summary?.total_cost_usd)}
              </span>
              {dist.partial && <span className="wg-chart-partial">{t('dash.chart.partial')}</span>}
            </div>
          </div>
          <div className="wg-pro-card-extra">
            <Tabs type="line" className="wg-chart-tabs" activeKey={chartTab} onChange={(k) => setChartTab(k as typeof chartTab)}>
              <Tabs.TabPane itemKey="dist" tab={t('dash.tab.dist')} />
              <Tabs.TabPane itemKey="trend" tab={t('dash.tab.trend')} />
              <Tabs.TabPane itemKey="rank" tab={t('dash.tab.rank')} />
            </Tabs>
            <Tooltip content={t('dash.chart.exportSoon')}>
              <span>
                <Button icon={<IconExport />} disabled>
                  {t('dash.chart.export')}
                </Button>
              </span>
            </Tooltip>
          </div>
        </div>
        <div className="wg-pro-card-body" style={{ paddingTop: 8 }}>
          {chartTab === 'trend' && <EChart option={trendOption} height={340} />}
          {chartTab === 'rank' &&
            (byModel.length ? (
              <EChart option={rankingOption} height={Math.max(220, 10 * 34)} />
            ) : (
              <div className="wg-chart-empty">
                <Empty description={t('dash.chart.empty')} />
              </div>
            ))}
          {chartTab === 'dist' &&
            (hasDistData ? (
              <EChart option={distOption} height={380} />
            ) : (
              <div className="wg-chart-empty">
                <Empty description={t('dash.chart.empty')} />
              </div>
            ))}
        </div>
      </section>
    </div>
  );
}
