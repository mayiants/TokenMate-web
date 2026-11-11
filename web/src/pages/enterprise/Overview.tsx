import { useEffect, useMemo, useState } from 'react';
import { Card, Empty, Spin, Tag } from '@douyinfe/semi-ui';
import { IconBriefcaseStroked, IconCreditCardStroked, IconKeyStroked, IconUserGroup } from '@douyinfe/semi-icons';
import { useI18n } from '../../i18n';
import { useEnterprise } from './EntLayout';
import EChart from '../../components/EChart';
import {
  getEnterpriseBillingTrend,
  getEnterpriseUsageTrend,
  listEnterpriseKeys,
  type UsageTrendPoint,
} from '../../api/enterprise';
import { formatUSD } from '../../api/console';

function trendToSeries(points: UsageTrendPoint[], field: 'cost' | 'tokens' | 'requests') {
  return {
    x: points.map((p) => p.date?.slice(5) ?? ''),
    y: points.map((p) => Number(p[field] ?? 0)),
  };
}

export default function EntOverview() {
  const { t } = useI18n();
  const { info } = useEnterprise();
  const [usageTrend, setUsageTrend] = useState<UsageTrendPoint[] | null>(null);
  const [billingTrend, setBillingTrend] = useState<UsageTrendPoint[] | null>(null);
  const [keyCount, setKeyCount] = useState<number | null>(null);

  useEffect(() => {
    getEnterpriseUsageTrend().then(setUsageTrend).catch(() => setUsageTrend([]));
    getEnterpriseBillingTrend().then(setBillingTrend).catch(() => setBillingTrend([]));
    listEnterpriseKeys()
      .then((keys) => setKeyCount(keys.filter((k) => k.status === 'active').length))
      .catch(() => setKeyCount(null));
  }, []);

  const costSeries = useMemo(() => trendToSeries(billingTrend ?? [], 'cost'), [billingTrend]);
  const tokenSeries = useMemo(() => trendToSeries(usageTrend ?? [], 'tokens'), [usageTrend]);

  const credit = info.credit_total ?? 0;
  const creditUsed = info.credit_used ?? 0;
  const remaining = Math.max(0, credit - creditUsed);
  const memberCount = info.member_count ?? 0;

  const stats = [
    {
      icon: <IconCreditCardStroked />,
      tone: 'wg-tint-primary',
      label: t('ent.overview.creditRemaining'),
      value: formatUSD(remaining),
      hint: t('ent.overview.creditHint', { total: formatUSD(credit) }),
    },
    {
      icon: <IconUserGroup />,
      tone: 'wg-tint-green',
      label: t('ent.overview.members'),
      value: String(memberCount),
      hint: t('ent.overview.membersHint'),
    },
    {
      icon: <IconKeyStroked />,
      tone: 'wg-tint-amber',
      label: t('ent.overview.activeKeys'),
      value: keyCount === null ? '—' : String(keyCount),
      hint: t('ent.overview.activeKeysHint'),
    },
    {
      icon: <IconBriefcaseStroked />,
      tone: 'wg-tint-violet',
      label: t('ent.overview.status'),
      value: <Tag color={info.status === 'active' ? 'green' : 'grey'}>{info.status ?? '—'}</Tag>,
      hint: info.verified_at ? t('ent.overview.verified') : t('ent.overview.unverified'),
    },
  ];

  const chartCommon = {
    grid: { left: 48, right: 16, top: 24, bottom: 28 },
    tooltip: { trigger: 'axis' as const },
  };

  return (
    <div className="wg-console-stack">
      <div className="wg-page-head">
        <div>
          <h2 className="wg-page-title">{info.name}</h2>
        </div>
      </div>

      <div className="wg-stat-grid">
        {stats.map((s) => (
          <Card key={s.label} className="wg-stat-card" bordered>
            <div className={`wg-stat-icon wg-stat-icon ${s.tone}`}>{s.icon}</div>
            <div className="wg-stat-body">
              <div className="wg-stat-label">{s.label}</div>
              <div className="wg-stat-value">{s.value}</div>
              <div className="wg-stat-hint">{s.hint}</div>
            </div>
          </Card>
        ))}
      </div>

      <Card className="wg-chart-card" bordered title={t('ent.overview.enterpriseInfo')}>
        {/* Plain label/value grid instead of Semi Descriptions: the table-style
            Descriptions keeps 2–3 fixed columns and overflows a 390px viewport. */}
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))',
            gap: 16,
          }}
        >
          {[
            { key: t('ent.settings.name'), value: info.name },
            { key: t('ent.settings.contactEmail'), value: info.contact_email || '—' },
            { key: t('ent.overview.currency'), value: info.display_currency ?? 'USD' },
            {
              key: t('ent.overview.nextReset'),
              value: (info as { next_reset_at?: string }).next_reset_at ?? '—',
            },
            {
              key: t('ent.overview.quota'),
              value: `${info.quota_used ?? 0} / ${info.quota_total ?? 0}`,
            },
          ].map((row) => (
            <div key={row.key} style={{ minWidth: 0 }}>
              <div className="wg-stat-label">{row.key}</div>
              <div className="wg-cell-ellipsis" style={{ fontWeight: 600 }}>
                {row.value}
              </div>
            </div>
          ))}
        </div>
      </Card>

      <div className="wg-chart-grid">
        <Card className="wg-chart-card" bordered title={t('ent.overview.billingTrend')}>
          {billingTrend === null ? (
            <Spin />
          ) : billingTrend.length === 0 ? (
            <Empty description={t('common.emptyData')} />
          ) : (
            <EChart
              height={280}
              option={{
                ...chartCommon,
                xAxis: { type: 'category', data: costSeries.x },
                yAxis: { type: 'value' },
                series: [
                  {
                    type: 'bar',
                    data: costSeries.y,
                    itemStyle: { color: '#0086ff', borderRadius: [4, 4, 0, 0] },
                  },
                ],
              }}
            />
          )}
        </Card>
        <Card className="wg-chart-card" bordered title={t('ent.overview.usageTrend')}>
          {usageTrend === null ? (
            <Spin />
          ) : usageTrend.length === 0 ? (
            <Empty description={t('common.emptyData')} />
          ) : (
            <EChart
              height={280}
              option={{
                ...chartCommon,
                xAxis: { type: 'category', data: tokenSeries.x },
                yAxis: { type: 'value' },
                series: [
                  {
                    type: 'line',
                    smooth: true,
                    data: tokenSeries.y,
                    areaStyle: { opacity: 0.12 },
                    itemStyle: { color: '#22c55e' },
                  },
                ],
              }}
            />
          )}
        </Card>
      </div>
    </div>
  );
}
