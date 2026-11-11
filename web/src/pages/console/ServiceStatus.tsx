import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Empty, Select, Table, Tag } from '@douyinfe/semi-ui';
import { IconAlertCircle, IconRefresh, IconTickCircle } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { formatDateTime } from '../../api/console';
import { getUpstreamStatus, type UpstreamPeriod, type UpstreamStatusRow } from '../../api/status';

const PERIODS: UpstreamPeriod[] = ['90m', '24h', '7d'];

export default function ServiceStatus() {
  const { t } = useI18n();
  const [period, setPeriod] = useState<UpstreamPeriod>('24h');
  const [rows, setRows] = useState<UpstreamStatusRow[] | null>(null);
  const [updatedAt, setUpdatedAt] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const tl = (key: string, fallback: string) => {
    const v = t(key);
    return v === key ? fallback : v;
  };

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await getUpstreamStatus(period);
      setRows(res.data ?? []);
      setUpdatedAt(res.meta?.updated_at ?? null);
    } catch {
      setRows([]);
    } finally {
      setLoading(false);
    }
  }, [period]);

  useEffect(() => {
    load();
  }, [load]);

  const overall = useMemo(() => {
    const list = rows ?? [];
    if (list.length === 0) return 'empty' as const;
    if (list.some((r) => r.status === 'unavailable')) return 'down' as const;
    if (list.some((r) => r.status === 'degraded')) return 'degraded' as const;
    return 'ok' as const;
  }, [rows]);

  const statusMeta = (status: string) => {
    switch (status) {
      case 'available':
        return { color: 'green' as const, label: t('status.state.available') };
      case 'degraded':
        return { color: 'orange' as const, label: t('status.state.degraded') };
      case 'unavailable':
        return { color: 'red' as const, label: t('status.state.unavailable') };
      case 'unknown':
        return { color: 'grey' as const, label: t('status.state.unknown') };
      default:
        return { color: 'grey' as const, label: t('status.state.missing') };
    }
  };

  const columns = [
    {
      title: t('status.col.model'),
      dataIndex: 'model',
      render: (_: unknown, r: UpstreamStatusRow) => <span className="wg-cell-strong">{r.model}</span>,
    },
    {
      title: t('status.col.group'),
      dataIndex: 'group_name',
      width: 160,
      render: (_: unknown, r: UpstreamStatusRow) => (
        <span className="wg-cell-dim wg-cell-ellipsis" style={{ display: 'inline-block', maxWidth: 150 }}>
          {r.group_name || r.group_id || '—'}
        </span>
      ),
    },
    {
      title: t('status.col.state'),
      dataIndex: 'status',
      width: 120,
      render: (_: unknown, r: UpstreamStatusRow) => {
        const m = statusMeta(r.status);
        return (
          <Tag color={m.color} size="small" shape="circle">
            {m.label}
          </Tag>
        );
      },
    },
    {
      title: t('status.col.availability'),
      dataIndex: 'availability',
      width: 120,
      render: (_: unknown, r: UpstreamStatusRow) => (
        <span className="wg-num">
          {r.availability < 0 ? '—' : `${r.availability.toFixed(2)}%`}
        </span>
      ),
    },
    {
      title: t('status.col.latency'),
      dataIndex: 'avg_latency_ms',
      width: 130,
      render: (_: unknown, r: UpstreamStatusRow) => (
        <span className="wg-num">{r.avg_latency_ms > 0 ? `${Math.round(r.avg_latency_ms)} ms` : '—'}</span>
      ),
    },
    {
      title: t('status.col.requests'),
      width: 160,
      render: (_: unknown, r: UpstreamStatusRow) => (
        <span className="wg-num">
          {r.request_count}
          {r.failure_count > 0 && <span className="wg-amount-neg"> / {r.failure_count}</span>}
        </span>
      ),
    },
    {
      title: t('status.col.updated'),
      dataIndex: 'last_bucket',
      width: 170,
      render: (_: unknown, r: UpstreamStatusRow) => (
        <span className="wg-num wg-cell-dim">
          {r.last_bucket && r.last_bucket !== 'epoch' ? formatDateTime(String(r.last_bucket)) : '—'}
        </span>
      ),
    },
  ];

  const banner =
    overall === 'ok'
      ? { bg: 'rgba(34,197,94,0.10)', border: 'rgba(34,197,94,0.35)', icon: <IconTickCircle />, text: t('status.banner.ok') }
      : overall === 'degraded'
        ? { bg: 'rgba(245,158,11,0.10)', border: 'rgba(245,158,11,0.35)', icon: <IconAlertCircle />, text: t('status.banner.degraded') }
        : overall === 'down'
          ? { bg: 'rgba(239,68,68,0.10)', border: 'rgba(239,68,68,0.35)', icon: <IconAlertCircle />, text: t('status.banner.down') }
          : { bg: 'var(--wg-surface-2)', border: 'var(--wg-border)', icon: <IconAlertCircle />, text: t('status.banner.empty') };

  return (
    <div className="wg-page">
      <PageHead title={t('acc.nav.status')} />

      <section className="wg-pro-card wg-filter-card">
        <Select
          value={period}
          onChange={(v) => setPeriod(String(v) as UpstreamPeriod)}
          style={{ width: 160 }}
          aria-label={t('status.period')}
          optionList={PERIODS.map((p) => ({ value: p, label: tl(`status.period.${p}`, p) }))}
        />
        <Button style={{ marginLeft: 'auto' }} theme="borderless" icon={<IconRefresh />} loading={loading} onClick={load}>
          {t('common.refresh')}
        </Button>
      </section>

      <div
        role="status"
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          padding: '14px 20px',
          borderRadius: 12,
          border: `1px solid ${banner.border}`,
          background: banner.bg,
          color: 'var(--wg-text)',
        }}
      >
        <span style={{ fontSize: 18, display: 'inline-flex' }}>{banner.icon}</span>
        <span className="wg-cell-strong">{banner.text}</span>
        {updatedAt && (
          <span className="wg-cell-dim" style={{ marginLeft: 'auto' }}>
            {t('status.updatedAt')} {formatDateTime(updatedAt)}
          </span>
        )}
      </div>

      <section className="wg-pro-card wg-table-card">
        <div className="wg-table-toolbar">
          <span className="wg-cell-dim">{t('status.scopeNote')}</span>
          <span className="wg-cell-dim">{t('status.count').replace('{n}', String((rows ?? []).length))}</span>
        </div>
        <Table
          loading={loading}
          columns={columns}
          dataSource={rows ?? []}
          pagination={false}
          rowKey={(r?: UpstreamStatusRow) => `${r?.model}-${r?.group_id}`}
          tableLayout="fixed"
          empty={<Empty description={t('status.empty')} />}
        />
      </section>
    </div>
  );
}
