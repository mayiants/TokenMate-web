import { useCallback, useEffect, useState } from 'react';
import { Button, DatePicker, Empty, Input, Select, Table, Tag, Toast } from '@douyinfe/semi-ui';
import { IconExport, IconHistogram, IconRefresh, IconSearch, IconSendStroked, IconTickCircle } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { download } from '../../api/client';
import { listPublicModels } from '../../api/models';
import { listKeys } from '../../api/keys';
import {
  formatDateTime,
  formatNumber,
  formatUSD,
  getUsageRecords,
  type Paged,
  type UsageRecord,
} from '../../api/console';

function pad(n: number): string {
  return String(n).padStart(2, '0');
}

function toDateStr(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

const PAGE_SIZE_OPTIONS = [10, 20, 50];

export default function UsageLogs() {
  const { t } = useI18n();

  // applied filters (trigger search) vs. draft values
  const [dateRange, setDateRange] = useState<[Date, Date] | null>(null);
  const [keyId, setKeyId] = useState<string>();
  const [model, setModel] = useState<string>();
  const [status, setStatus] = useState<string>();
  const [requestId, setRequestId] = useState('');
  const [applied, setApplied] = useState<{ start?: string; end?: string; keyId?: string; model?: string; status?: string; traceId?: string }>({});
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [data, setData] = useState<Paged<UsageRecord> | null>(null);
  const [successTotal, setSuccessTotal] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [exporting, setExporting] = useState(false);
  const [keys, setKeys] = useState<{ label: string; value: string }[]>([]);
  const [modelOptions, setModelOptions] = useState<{ label: string; value: string }[]>([]);

  useEffect(() => {
    listKeys()
      .then((list) => setKeys(list.map((k) => ({ label: k.name, value: k.id }))))
      .catch(() => setKeys([]));
    listPublicModels()
      .then((models) =>
        setModelOptions(models.map((m) => ({ label: m.api_model_name, value: m.api_model_name })).filter((m) => m.value))
      )
      .catch(() => setModelOptions([]));
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await getUsageRecords({
        page,
        page_size: pageSize,
        start_date: applied.start,
        end_date: applied.end,
        api_key_id: applied.keyId,
        model: applied.model,
        status: applied.status,
        trace_id: applied.traceId,
      });
      setData(res);
      // success-rate stat: reuse the same filters with status=success
      if (applied.status === 'success') {
        setSuccessTotal(res.total);
      } else if (applied.status === 'error') {
        setSuccessTotal(0);
      } else {
        const ok = await getUsageRecords({
          page: 1,
          page_size: 1,
          start_date: applied.start,
          end_date: applied.end,
          api_key_id: applied.keyId,
          model: applied.model,
          status: 'success',
          trace_id: applied.traceId,
        });
        setSuccessTotal(ok.total);
      }
    } catch {
      // auth failures redirect globally
    } finally {
      setLoading(false);
    }
  }, [page, pageSize, applied]);

  useEffect(() => {
    load();
  }, [load]);

  const onSearch = () => {
    setApplied({
      start: dateRange ? toDateStr(dateRange[0]) : undefined,
      end: dateRange ? toDateStr(dateRange[1]) : undefined,
      keyId,
      model,
      status,
      traceId: requestId.trim() || undefined,
    });
    setPage(1);
  };

  const onReset = () => {
    setDateRange(null);
    setKeyId(undefined);
    setModel(undefined);
    setStatus(undefined);
    setRequestId('');
    setApplied({});
    setPage(1);
  };

  const onExport = async () => {
    setExporting(true);
    try {
      const params = new URLSearchParams();
      if (applied.start) params.set('start', applied.start);
      if (applied.end) params.set('end', applied.end);
      const qs = params.toString();
      await download(`/api/usage/export${qs ? `?${qs}` : ''}`);
      Toast.success(t('logs.exported'));
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setExporting(false);
    }
  };

  const statusPill = (s: string) => {
    if (s === 'success') {
      return (
        <Tag color="green" size="small" shape="circle">
          {t('logs.status.success')}
        </Tag>
      );
    }
    if (s === 'error') {
      return (
        <Tag color="red" size="small" shape="circle">
          {t('logs.status.error')}
        </Tag>
      );
    }
    return <Tag size="small">{s || '—'}</Tag>;
  };

  const columns = [
    {
      title: t('logs.col.time'),
      dataIndex: 'created_at',
      width: 165,
      render: (_: unknown, r: UsageRecord) => <span className="wg-num wg-cell-dim">{formatDateTime(r.created_at)}</span>,
    },
    {
      title: t('logs.col.key'),
      dataIndex: 'api_key_name',
      width: 130,
      render: (_: unknown, r: UsageRecord) => (
        <span className="wg-cell-ellipsis" title={r.api_key_name || r.key_prefix}>
          {r.api_key_name || r.key_prefix || '—'}
        </span>
      ),
    },
    {
      title: t('logs.col.model'),
      dataIndex: 'model',
      width: 190,
      render: (_: unknown, r: UsageRecord) => (
        <span className="wg-cell-ellipsis wg-mono-sm" title={r.model}>
          {r.model}
        </span>
      ),
    },
    {
      title: t('logs.col.input'),
      dataIndex: 'input_tokens',
      width: 90,
      render: (_: unknown, r: UsageRecord) => <span className="wg-num">{formatNumber(r.input_tokens)}</span>,
    },
    {
      title: t('logs.col.output'),
      dataIndex: 'output_tokens',
      width: 90,
      render: (_: unknown, r: UsageRecord) => <span className="wg-num">{formatNumber(r.output_tokens)}</span>,
    },
    {
      title: t('logs.col.cost'),
      dataIndex: 'total_cost',
      width: 100,
      render: (_: unknown, r: UsageRecord) => <span className="wg-num">{formatUSD(r.total_cost)}</span>,
    },
    { title: t('logs.col.status'), dataIndex: 'status', width: 96, render: (_: unknown, r: UsageRecord) => statusPill(r.status) },
    {
      title: t('logs.col.ip'),
      dataIndex: 'client_ip',
      width: 120,
      render: (_: unknown, r: UsageRecord) => <span className="wg-cell-dim wg-mono-sm">{r.client_ip || '—'}</span>,
    },
  ];

  const expandDetail = (r: UsageRecord) => {
    const items: { k: string; v: string }[] = [
      { k: t('logs.detail.traceId'), v: r.trace_id || '—' },
      { k: t('logs.detail.provider'), v: r.provider || '—' },
      { k: t('logs.detail.endpoint'), v: r.endpoint || '—' },
      { k: t('logs.detail.latency'), v: typeof r.latency_ms === 'number' ? `${formatNumber(r.latency_ms)} ms` : '—' },
      { k: t('logs.detail.cached'), v: typeof r.cached_tokens === 'number' ? formatNumber(r.cached_tokens) : '—' },
      { k: t('logs.detail.group'), v: r.group_name || '—' },
      { k: t('logs.detail.ua'), v: r.user_agent || '—' },
    ];
    return (
      <div className="wg-expand-detail">
      <PageHead title={t('console.nav.logs')} />
        {items.map((it) => (
          <div key={it.k} className="wg-expand-item">
            <span className="wg-expand-k">{it.k}</span>
            <span className="wg-expand-v wg-mono-sm" title={it.v}>
              {it.v}
            </span>
          </div>
        ))}
      </div>
    );
  };

  const total = data?.total ?? 0;
  const from = total === 0 ? 0 : (page - 1) * pageSize + 1;
  const to = Math.min(page * pageSize, total);
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const successRate = total > 0 && successTotal !== null ? `${((successTotal / total) * 100).toFixed(1)}%` : '—';

  const stats = [
    { icon: <IconSendStroked />, tint: 'wg-tint-blue', label: t('logs.stat.requests'), value: formatNumber(total) },
    { icon: <IconTickCircle />, tint: 'wg-tint-green', label: t('logs.stat.success'), value: successTotal === null ? '—' : formatNumber(successTotal) },
    { icon: <IconHistogram />, tint: 'wg-tint-amber', label: t('logs.stat.rate'), value: successRate },
  ];

  return (
    <div className="wg-page">
      <div className="wg-page-head">
        <div>
          <h2 className="wg-page-title">{t('console.nav.logs')}</h2>
        </div>
      </div>

      <div className="wg-stat-grid">
        {stats.map((s) => (
          <div key={s.label} className="wg-pro-card wg-stat">
            <span className={`wg-stat-icon ${s.tint}`}>{s.icon}</span>
            <div className="wg-stat-body">
              <div className="wg-stat-label">{s.label}</div>
              <div className="wg-stat-value">{s.value}</div>
              <div className="wg-stat-cap">{t('logs.stat.follows')}</div>
            </div>
          </div>
        ))}
      </div>

      <div className="wg-pro-card wg-filter-card">
        <DatePicker
          type="dateRange"
          style={{ width: 240 }}
          placeholder={[t('dash.range.start'), t('dash.range.end')]}
          value={dateRange ?? undefined}
          onChange={(v) => setDateRange((v as [Date, Date] | null) ?? null)}
        />
        <Select
          style={{ width: 160 }}
          placeholder={t('logs.filter.key')}
          optionList={keys}
          value={keyId}
          onChange={(v) => setKeyId(v as string | undefined)}
          filter
          showClear
        />
        <Select
          style={{ width: 190 }}
          placeholder={t('logs.filter.model')}
          optionList={modelOptions}
          value={model}
          onChange={(v) => setModel(v as string | undefined)}
          filter
          showClear
        />
        <Select
          style={{ width: 120 }}
          placeholder={t('logs.filter.status')}
          optionList={[
            { label: t('logs.status.success'), value: 'success' },
            { label: t('logs.status.error'), value: 'error' },
          ]}
          value={status}
          onChange={(v) => setStatus(v as string | undefined)}
          showClear
        />
        <Input
          style={{ width: 180 }}
          placeholder={t('logs.filter.requestId')}
          value={requestId}
          onChange={setRequestId}
          showClear
        />
        <div className="wg-filter-actions">
          <Button type="primary" theme="solid" icon={<IconSearch />} onClick={onSearch}>
            {t('logs.search')}
          </Button>
          <Button theme="light" icon={<IconRefresh />} onClick={onReset}>
            {t('logs.reset')}
          </Button>
        </div>
      </div>

      <div className="wg-pro-card wg-table-card">
        <div className="wg-table-toolbar">
          <div className="wg-table-toolbar-left">
            <span className="wg-pro-card-title">{t('logs.table.title')}</span>
          </div>
          <Button theme="light" icon={<IconExport />} loading={exporting} onClick={onExport}>
            {t('logs.export')}
          </Button>
        </div>
        <Table
          loading={loading}
          columns={columns}
          dataSource={data?.records ?? []}
          pagination={false}
          rowKey="trace_id"
          tableLayout="fixed"
          expandRowByClick
          expandedRowRender={(r) => expandDetail(r as UsageRecord)}
          empty={<Empty description={t('logs.empty')} />}
        />
        <div className="wg-table-footer">
          <span className="wg-cell-dim">
            {t('keys.showing').replace('{from}', String(from)).replace('{to}', String(to)).replace('{total}', String(total))}
          </span>
          <div className="wg-table-footer-right">
            <Select<number>
              size="small"
              value={pageSize}
              onChange={(v) => {
                setPageSize((v as number | undefined) ?? 10);
                setPage(1);
              }}
              optionList={PAGE_SIZE_OPTIONS.map((n) => ({ label: `${t('keys.show')} ${n}`, value: n }))}
              style={{ width: 110 }}
            />
            <div className="wg-table-pager">
              <Button size="small" theme="light" disabled={page <= 1} onClick={() => setPage(page - 1)}>
                ‹
              </Button>
              <span className="wg-num">
                {Math.min(page, totalPages)} / {totalPages}
              </span>
              <Button size="small" theme="light" disabled={page >= totalPages} onClick={() => setPage(page + 1)}>
                ›
              </Button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
