import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Empty,
  Input,
  InputNumber,
  Select,
  SideSheet,
  Table,
  Tabs,
  Tag,
  Toast,
} from '@douyinfe/semi-ui';
import {
  IconExport,
  IconPlus,
  IconRefresh,
  IconSearch,
} from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import CopyButton from '../../components/CopyButton';
import dangerConfirm from '../../components/dangerConfirm';
import { useI18n } from '../../i18n';
import { formatDateTime, formatNumber, formatUSD } from '../../api/console';
import {
  createAdminVouchers,
  exportReconciliation,
  getAdminSubscriptionStats,
  getAdminVoucherStats,
  getReconciliationDetail,
  getReconciliationSummary,
  listAdminUserSubscriptions,
  listAdminVouchers,
  toggleAdminVoucher,
  updateAdminUserSubscription,
  type AdminSubscriptionStats,
  type AdminUserSubscription,
  type AdminVoucher,
  type AdminVoucherStats,
  type CreateVouchersPayload,
  type ReconPeriod,
  type ReconciliationDetail,
  type ReconciliationRow,
  type ReconciliationSummary,
} from '../../api/adminFinance';

type TabKey = 'subscriptions' | 'vouchers' | 'reconciliation';

const VOUCHER_TYPES = ['gift', 'welcome', 'referral'] as const;
const VOUCHER_STATUSES = ['active', 'disabled', 'exhausted', 'expired'] as const;
const PERIODS: ReconPeriod[] = ['24h', '7d', '30d', '90d'];

function isExpired(v: AdminVoucher): boolean {
  return Boolean(v.expires_at) && new Date(v.expires_at as string).getTime() < Date.now();
}

export default function AdminFinance() {
  const { t } = useI18n();
  const [tab, setTab] = useState<TabKey>('subscriptions');

  // subscriptions
  const [subs, setSubs] = useState<AdminUserSubscription[] | null>(null);
  const [subsLoading, setSubsLoading] = useState(true);
  const [subStats, setSubStats] = useState<AdminSubscriptionStats>({});
  const [adjustTarget, setAdjustTarget] = useState<AdminUserSubscription | null>(null);

  // vouchers
  const [vouchers, setVouchers] = useState<AdminVoucher[] | null>(null);
  const [voucherTotal, setVoucherTotal] = useState(0);
  const [vouchersLoading, setVouchersLoading] = useState(false);
  const [voucherStats, setVoucherStats] = useState<AdminVoucherStats>({});
  const [voucherType, setVoucherType] = useState('all');
  const [voucherStatus, setVoucherStatus] = useState('all');
  const [createOpen, setCreateOpen] = useState(false);

  // reconciliation
  const [period, setPeriod] = useState<ReconPeriod>('7d');
  const [summary, setSummary] = useState<ReconciliationSummary>({});
  const [detail, setDetail] = useState<ReconciliationDetail | null>(null);
  const [reconLoading, setReconLoading] = useState(false);
  const [reconSearch, setReconSearch] = useState('');
  const [diffThreshold, setDiffThreshold] = useState<number | undefined>();
  const [exporting, setExporting] = useState(false);

  const loadSubs = useCallback(async () => {
    setSubsLoading(true);
    try {
      const [list, stats] = await Promise.all([
        listAdminUserSubscriptions(),
        getAdminSubscriptionStats().catch(() => ({})),
      ]);
      setSubs(list);
      setSubStats(stats);
    } catch (e) {
      setSubs([]);
      Toast.error((e as Error).message);
    } finally {
      setSubsLoading(false);
    }
  }, []);

  const loadVouchers = useCallback(
    async (type = voucherType, status = voucherStatus) => {
      setVouchersLoading(true);
      try {
        const [page, stats] = await Promise.all([
          listAdminVouchers({ type, status, limit: 50 }),
          getAdminVoucherStats().catch(() => ({})),
        ]);
        setVouchers(page.vouchers);
        setVoucherTotal(page.total);
        setVoucherStats(stats);
      } catch (e) {
        setVouchers([]);
        Toast.error((e as Error).message);
      } finally {
        setVouchersLoading(false);
      }
    },
    [voucherType, voucherStatus]
  );

  const loadRecon = useCallback(
    async (p = period, search = reconSearch, threshold = diffThreshold) => {
      setReconLoading(true);
      try {
        const [sum, page] = await Promise.all([
          getReconciliationSummary(p),
          getReconciliationDetail({
            period: p,
            search: search.trim() || undefined,
            diff_threshold: threshold,
            limit: 50,
          }),
        ]);
        setSummary(sum);
        setDetail(page);
      } catch (e) {
        Toast.error((e as Error).message);
        setSummary({});
        setDetail({ rows: [], total: 0, page: 1, limit: 50, total_pages: 1 });
      } finally {
        setReconLoading(false);
      }
    },
    [period, reconSearch, diffThreshold]
  );

  useEffect(() => {
    loadSubs();
  }, [loadSubs]);

  useEffect(() => {
    if (tab === 'vouchers' && vouchers === null) loadVouchers();
    if (tab === 'reconciliation' && detail === null) loadRecon();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);

  const onExport = async () => {
    setExporting(true);
    try {
      await exportReconciliation({
        period,
        search: reconSearch.trim() || undefined,
        diff_threshold: diffThreshold,
      });
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setExporting(false);
    }
  };

  const subColumns = [
    {
      title: t('admin.finance.sub.col.user'),
      dataIndex: 'user_email',
      render: (v: string, r: AdminUserSubscription) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span className="wg-cell-strong">{r.user_name || v || '—'}</span>
          {r.user_name && v ? <span className="wg-cell-dim">{v}</span> : null}
        </div>
      ),
    },
    {
      title: t('admin.finance.sub.col.plan'),
      dataIndex: 'plan_name',
      width: 160,
      render: (v: string) => v || '—',
    },
    {
      title: t('admin.finance.sub.col.status'),
      dataIndex: 'status',
      width: 110,
      render: (v: string) => statusTag(v, t),
    },
    {
      title: t('admin.finance.sub.col.quota'),
      width: 170,
      render: (_: unknown, r: AdminUserSubscription) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span className="wg-num">
            {formatUSD(r.quota_remaining ?? 0)} / {formatUSD(r.quota_used ?? 0)}
          </span>
          <span className="wg-cell-dim">{t('admin.finance.sub.quotaHint')}</span>
        </div>
      ),
    },
    {
      title: t('admin.finance.sub.col.daily'),
      width: 130,
      render: (_: unknown, r: AdminUserSubscription) => (
        <span className="wg-num">
          {formatNumber(r.daily_quota_used ?? 0)} / {formatNumber(r.daily_limit ?? 0)}
        </span>
      ),
    },
    {
      title: t('admin.finance.sub.col.created'),
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => <span className="wg-cell-dim">{formatDateTime(v)}</span>,
    },
    {
      title: t('common.actions'),
      width: 110,
      render: (_: unknown, r: AdminUserSubscription) => (
        <div className="wg-cell-actions">
          <Button theme="borderless" size="small" onClick={() => setAdjustTarget(r)}>
            {t('admin.finance.sub.adjust')}
          </Button>
        </div>
      ),
    },
  ];

  const voucherColumns = [
    {
      title: t('admin.finance.voucher.col.code'),
      dataIndex: 'code',
      width: 200,
      render: (v: string) => (
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <span className="wg-mono-sm">{v}</span>
          <CopyButton text={v} ariaLabel={t('admin.finance.voucher.copyCode')} />
        </div>
      ),
    },
    {
      title: t('admin.finance.voucher.col.name'),
      dataIndex: 'name',
      render: (v: string) => v || '—',
    },
    {
      title: t('admin.finance.voucher.col.type'),
      dataIndex: 'type',
      width: 100,
      render: (v: string) => <Tag color="blue">{voucherTypeLabel(v, t)}</Tag>,
    },
    {
      title: t('admin.finance.voucher.col.amount'),
      dataIndex: 'amount',
      width: 110,
      render: (v: number) => <span className="wg-num">{formatUSD(v ?? 0)}</span>,
    },
    {
      title: t('admin.finance.voucher.col.usage'),
      width: 100,
      render: (_: unknown, r: AdminVoucher) => (
        <span className="wg-num">
          {formatNumber(r.used_count ?? 0)} / {formatNumber(r.max_uses ?? 0)}
        </span>
      ),
    },
    {
      title: t('admin.finance.voucher.col.status'),
      width: 110,
      render: (_: unknown, r: AdminVoucher) =>
        isExpired(r) ? (
          <Tag color="grey">{t('admin.finance.voucher.status.expired')}</Tag>
        ) : (
          <Tag color={r.status === 'active' ? 'green' : r.status === 'exhausted' ? 'orange' : 'red'}>
            {t(`admin.finance.voucher.status.${r.status ?? 'disabled'}`)}
          </Tag>
        ),
    },
    {
      title: t('admin.finance.voucher.col.expires'),
      dataIndex: 'expires_at',
      width: 130,
      render: (v: string | null) => <span className="wg-cell-dim">{formatDateTime(v)}</span>,
    },
    {
      title: t('common.actions'),
      width: 110,
      render: (_: unknown, r: AdminVoucher) => {
        const active = r.status === 'active' && !isExpired(r);
        return (
          <div className="wg-cell-actions">
            {active ? (
              <Button
                theme="borderless"
                size="small"
                type="danger"
                onClick={() =>
                  dangerConfirm({
                    title: t('admin.finance.voucher.disableTitle'),
                    content: t('admin.finance.voucher.disableDesc', { code: r.code }),
                    okText: t('admin.finance.voucher.disable'),
                    cancelText: t('common.cancelAction'),
                    onOk: async () => {
                      try {
                        await toggleAdminVoucher(r.id);
                        Toast.success(t('admin.finance.voucher.disableDone'));
                        await loadVouchers();
                      } catch (e) {
                        Toast.error((e as Error).message);
                      }
                    },
                  })
                }
              >
                {t('admin.finance.voucher.disable')}
              </Button>
            ) : r.status === 'disabled' ? (
              <Button
                theme="borderless"
                size="small"
                onClick={async () => {
                  try {
                    await toggleAdminVoucher(r.id);
                    Toast.success(t('admin.finance.voucher.enableDone'));
                    await loadVouchers();
                  } catch (e) {
                    Toast.error((e as Error).message);
                  }
                }}
              >
                {t('admin.finance.voucher.enable')}
              </Button>
            ) : (
              <span className="wg-cell-dim">—</span>
            )}
          </div>
        );
      },
    },
  ];

  const reconColumns = [
    { title: t('admin.finance.recon.col.provider'), dataIndex: 'provider', width: 110 },
    {
      title: t('admin.finance.recon.col.model'),
      dataIndex: 'model',
      width: 180,
      render: (v: string) => <span className="wg-mono-sm wg-cell-ellipsis">{v || '—'}</span>,
    },
    {
      title: t('admin.finance.recon.col.key'),
      dataIndex: 'key_preview',
      width: 140,
      render: (v: string) => <span className="wg-mono-sm wg-cell-dim">{v || '—'}</span>,
    },
    {
      title: t('admin.finance.recon.col.requests'),
      width: 150,
      render: (_: unknown, r: { upstream_requests?: number; billed_requests?: number }) => (
        <span className="wg-num">
          {formatNumber(r.upstream_requests ?? 0)} / {formatNumber(r.billed_requests ?? 0)}
        </span>
      ),
    },
    {
      title: t('admin.finance.recon.col.cost'),
      width: 160,
      render: (_: unknown, r: { upstream_cost_usd?: number; billed_cost_usd?: number }) => (
        <span className="wg-num">
          {formatUSD(r.upstream_cost_usd ?? 0)} / {formatUSD(r.billed_cost_usd ?? 0)}
        </span>
      ),
    },
    {
      title: t('admin.finance.recon.col.diff'),
      dataIndex: 'cost_diff_usd',
      width: 120,
      render: (v: number) => (
        <span
          className="wg-num"
          style={Math.abs(v ?? 0) > 0.01 ? { color: 'var(--semi-color-danger)' } : undefined}
        >
          {formatUSD(v ?? 0)}
        </span>
      ),
    },
    {
      title: t('admin.finance.recon.col.margin'),
      dataIndex: 'gross_margin_pct',
      width: 100,
      render: (v: number) => <span className="wg-num">{(v ?? 0).toFixed(1)}%</span>,
    },
    {
      title: t('admin.finance.recon.col.errors'),
      width: 120,
      render: (_: unknown, r: { error_count?: number; error_rate?: number }) => (
        <span className="wg-num">
          {formatNumber(r.error_count ?? 0)} · {(r.error_rate ?? 0).toFixed(2)}%
        </span>
      ),
    },
  ];

  const ov = subStats.overview ?? {};
  const vStats = voucherStats.vouchers ?? {};

  return (
    <div className="wg-page">
      <PageHead title={t('admin.finance.title')} />

      <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as TabKey)}>
        <Tabs.TabPane itemKey="subscriptions" tab={t('admin.finance.tab.subscriptions')}>
          <div className="wg-stat-grid">
            <StatCard label={t('admin.finance.sub.kpi.active')} value={formatNumber(ov.active_count ?? 0)} />
            <StatCard label={t('admin.finance.sub.kpi.expired')} value={formatNumber(ov.expired_count ?? 0)} />
            <StatCard label={t('admin.finance.sub.kpi.cancelled')} value={formatNumber(ov.cancelled_count ?? 0)} />
            <StatCard label={t('admin.finance.sub.kpi.quota')} value={formatUSD(ov.total_revenue ?? 0)} />
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.finance.sub.tableTitle')}</span>
              </div>
              <Button icon={<IconRefresh />} theme="light" onClick={loadSubs}>
                {t('common.refresh')}
              </Button>
            </div>
            <p className="wg-cell-dim" style={{ margin: '0 0 12px' }}>
              {t('admin.finance.sub.refundNote')}
            </p>
            <Table
              loading={subsLoading}
              rowKey="id"
              columns={subColumns}
              dataSource={subs ?? []}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.finance.sub.empty')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="vouchers" tab={t('admin.finance.tab.vouchers')}>
          <div className="wg-stat-grid">
            <StatCard label={t('admin.finance.voucher.kpi.total')} value={formatNumber(vStats.total ?? 0)} />
            <StatCard label={t('admin.finance.voucher.kpi.active')} value={formatNumber(vStats.active ?? 0)} />
            <StatCard label={t('admin.finance.voucher.kpi.disabled')} value={formatNumber(vStats.disabled ?? 0)} />
            <StatCard
              label={t('admin.finance.voucher.kpi.redeemed')}
              value={formatUSD(voucherStats.uses?.total_amount ?? 0)}
            />
          </div>

          <div className="wg-pro-card wg-filter-card">
            <Select
              style={{ width: 160 }}
              value={voucherType}
              onChange={(v) => setVoucherType((v as string) ?? 'all')}
              optionList={[
                { value: 'all', label: t('admin.common.all') },
                ...VOUCHER_TYPES.map((v) => ({ value: v, label: t(`admin.finance.voucher.type.${v}`) })),
              ]}
            />
            <Select
              style={{ width: 160 }}
              value={voucherStatus}
              onChange={(v) => setVoucherStatus((v as string) ?? 'all')}
              optionList={[
                { value: 'all', label: t('admin.common.all') },
                ...VOUCHER_STATUSES.map((v) => ({
                  value: v,
                  label: t(`admin.finance.voucher.status.${v}`),
                })),
              ]}
            />
            <div className="wg-filter-actions">
              <Button
                type="primary"
                theme="solid"
                icon={<IconSearch />}
                onClick={() => loadVouchers()}
              >
                {t('common.search')}
              </Button>
              <Button
                icon={<IconRefresh />}
                theme="light"
                onClick={() => {
                  setVoucherType('all');
                  setVoucherStatus('all');
                  loadVouchers('all', 'all');
                }}
              >
                {t('common.reset')}
              </Button>
            </div>
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">
                  {t('admin.finance.voucher.tableTitle')}
                  <span className="wg-cell-dim"> · {formatNumber(voucherTotal)}</span>
                </span>
              </div>
              <Button icon={<IconPlus />} type="primary" theme="solid" onClick={() => setCreateOpen(true)}>
                {t('admin.finance.voucher.generate')}
              </Button>
            </div>
            <Table
              loading={vouchersLoading}
              rowKey="id"
              columns={voucherColumns}
              dataSource={vouchers ?? []}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.finance.voucher.empty')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="reconciliation" tab={t('admin.finance.tab.reconciliation')}>
          <div className="wg-pro-card wg-filter-card">
            <Select
              style={{ width: 140 }}
              value={period}
              onChange={(v) => {
                const p = (v as ReconPeriod) ?? '7d';
                setPeriod(p);
                loadRecon(p);
              }}
              optionList={PERIODS.map((p) => ({
                value: p,
                label: t(`admin.finance.recon.period.${p}`),
              }))}
            />
            <Input
              style={{ width: 220 }}
              placeholder={t('admin.finance.recon.searchPh')}
              value={reconSearch}
              onChange={setReconSearch}
              showClear
            />
            <InputNumber
              style={{ width: 180 }}
              min={0}
              precision={2}
              placeholder={t('admin.finance.recon.thresholdPh')}
              value={diffThreshold}
              onChange={(v) => setDiffThreshold(typeof v === 'number' ? v : undefined)}
            />
            <div className="wg-filter-actions">
              <Button
                type="primary"
                theme="solid"
                icon={<IconSearch />}
                onClick={() => loadRecon()}
              >
                {t('common.search')}
              </Button>
              <Button icon={<IconExport />} theme="light" loading={exporting} onClick={onExport}>
                {t('admin.finance.recon.export')}
              </Button>
            </div>
          </div>

          <div className="wg-stat-grid">
            <StatCard
              label={t('admin.finance.recon.kpi.upstream')}
              value={formatUSD(summary.estimated_upstream_cost_usd ?? 0)}
            />
            <StatCard
              label={t('admin.finance.recon.kpi.billed')}
              value={formatUSD(summary.billed_cost_usd ?? 0)}
            />
            <StatCard
              label={t('admin.finance.recon.kpi.diff')}
              value={formatUSD(summary.cost_diff_usd ?? 0)}
            />
            <StatCard
              label={t('admin.finance.recon.kpi.margin')}
              value={`${(summary.gross_margin_pct ?? 0).toFixed(1)}%`}
            />
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">
                  {t('admin.finance.recon.tableTitle')}
                  <span className="wg-cell-dim"> · {formatNumber(detail?.total ?? 0)}</span>
                </span>
              </div>
              <span className="wg-cell-dim">
                {t('admin.finance.recon.requestsSummary', {
                  upstream: formatNumber(summary.upstream_requests ?? 0),
                  billed: formatNumber(summary.billed_requests ?? 0),
                  errors: formatNumber(summary.upstream_errors ?? 0),
                })}
              </span>
            </div>
            <Table
              loading={reconLoading}
              rowKey={(r?: ReconciliationRow) =>
                r ? `${r.provider}-${r.model}-${r.upstream_key_id ?? ''}` : ''
              }
              columns={reconColumns}
              dataSource={detail?.rows ?? []}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.finance.recon.empty')} />}
            />
          </div>
        </Tabs.TabPane>
      </Tabs>

      {adjustTarget && (
        <AdjustSubscriptionDrawer
          target={adjustTarget}
          onClose={() => setAdjustTarget(null)}
          onSaved={loadSubs}
        />
      )}

      {createOpen && (
        <GenerateVoucherDrawer
          onClose={() => setCreateOpen(false)}
          onSaved={() => loadVouchers()}
        />
      )}
    </div>
  );
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="wg-pro-card wg-stat">
      <div className="wg-stat-body">
        <div className="wg-stat-label">{label}</div>
        <div className="wg-stat-value">{value}</div>
      </div>
    </div>
  );
}

function statusTag(v: string, t: (k: string) => string) {
  const color =
    v === 'active' ? 'green' : v === 'expired' ? 'grey' : v === 'cancelled' ? 'red' : 'blue';
  return <Tag color={color}>{t(`admin.finance.sub.status.${v || 'expired'}`)}</Tag>;
}

function voucherTypeLabel(v: string, t: (k: string) => string) {
  const key = `admin.finance.voucher.type.${v}`;
  const label = t(key);
  return label === key ? v || '—' : label;
}

function AdjustSubscriptionDrawer({
  target,
  onClose,
  onSaved,
}: {
  target: AdminUserSubscription;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const [quota, setQuota] = useState<number>((target.quota_remaining ?? 0) / 1_000_000);
  const [daily, setDaily] = useState<number>(target.daily_limit ?? 0);
  const [rpm, setRpm] = useState<number>(target.rpm_limit ?? 0);
  const [tpm, setTpm] = useState<number>(target.tpm_limit ?? 0);
  const [saving, setSaving] = useState(false);

  const save = async () => {
    setSaving(true);
    try {
      await updateAdminUserSubscription(target.id, {
        quota_remaining: Math.round(quota * 1_000_000),
        daily_limit: daily,
        rpm_limit: rpm,
        tpm_limit: tpm,
      });
      Toast.success(t('admin.finance.sub.adjustDone'));
      onSaved();
      onClose();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <SideSheet
      title={t('admin.finance.sub.adjustTitle')}
      visible
      onCancel={onClose}
      width={560}
      closeOnEsc
      maskClosable={false}
      footer={
        <div className="wg-drawer-footer">
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          <Button type="primary" theme="solid" loading={saving} onClick={save}>
            {t('common.save')}
          </Button>
        </div>
      }
    >
      <div className="wg-field-grid">
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.finance.sub.field.user')}</label>
          <span className="wg-cell-strong">
            {target.user_name || target.user_email || target.user_id}
          </span>
          <span className="wg-cell-dim">{target.plan_name || '—'}</span>
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.finance.sub.field.quotaRemaining')}</label>
          <InputNumber
            min={0}
            precision={2}
            value={quota}
            onChange={(v) => setQuota(typeof v === 'number' ? v : 0)}
            suffix="USD"
            style={{ width: 240 }}
          />
          <span className="wg-cell-dim">{t('admin.finance.sub.field.quotaHint')}</span>
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.finance.sub.field.dailyLimit')}</label>
          <InputNumber
            min={0}
            value={daily}
            onChange={(v) => setDaily(typeof v === 'number' ? v : 0)}
            style={{ width: 240 }}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.finance.sub.field.limits')}</label>
          <div style={{ display: 'flex', gap: 12 }}>
            <InputNumber
              min={0}
              value={rpm}
              onChange={(v) => setRpm(typeof v === 'number' ? v : 0)}
              prefix="RPM"
              style={{ width: 130 }}
            />
            <InputNumber
              min={0}
              value={tpm}
              onChange={(v) => setTpm(typeof v === 'number' ? v : 0)}
              prefix="TPM"
              style={{ width: 160 }}
            />
          </div>
        </div>
      </div>
    </SideSheet>
  );
}

function GenerateVoucherDrawer({
  onClose,
  onSaved,
}: {
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState('');
  const [type, setType] = useState<string>('gift');
  const [amount, setAmount] = useState<number>(5);
  const [count, setCount] = useState<number>(1);
  const [maxUses, setMaxUses] = useState<number>(1);
  const [expires, setExpires] = useState('');
  const [description, setDescription] = useState('');
  const [saving, setSaving] = useState(false);
  const [codes, setCodes] = useState<string[]>([]);

  const save = async () => {
    if (!name.trim()) {
      Toast.error(t('admin.finance.voucher.nameRequired'));
      return;
    }
    if (!amount || amount <= 0) {
      Toast.error(t('admin.finance.voucher.amountRequired'));
      return;
    }
    const payload: CreateVouchersPayload = {
      name: name.trim(),
      type,
      amount,
      count,
      max_uses: maxUses,
      description: description.trim(),
      expires_at: expires || undefined,
    };
    setSaving(true);
    try {
      const res = await createAdminVouchers(payload);
      setCodes(res.codes);
      Toast.success(t('admin.finance.voucher.generated', { count: res.count }));
      onSaved();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <SideSheet
      title={t('admin.finance.voucher.generateTitle')}
      visible
      onCancel={onClose}
      width={620}
      closeOnEsc
      maskClosable={false}
      footer={
        <div className="wg-drawer-footer">
          <Button onClick={onClose}>{codes.length > 0 ? t('common.close') : t('common.cancel')}</Button>
          <Button type="primary" theme="solid" loading={saving} onClick={save}>
            {t('admin.finance.voucher.generate')}
          </Button>
        </div>
      }
    >
      <div className="wg-field-grid">
        <div className="wg-form-field">
          <label className="wg-form-label">
            {t('admin.finance.voucher.field.name')}
            <span className="wg-form-required">*</span>
          </label>
          <Input value={name} onChange={setName} placeholder={t('admin.finance.voucher.namePh')} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.finance.voucher.field.type')}</label>
          <Select
            value={type}
            onChange={(v) => setType((v as string) ?? 'gift')}
            optionList={VOUCHER_TYPES.map((v) => ({
              value: v,
              label: t(`admin.finance.voucher.type.${v}`),
            }))}
            style={{ width: 220 }}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">
            {t('admin.finance.voucher.field.amount')}
            <span className="wg-form-required">*</span>
          </label>
          <InputNumber
            min={0.01}
            precision={2}
            value={amount}
            onChange={(v) => setAmount(typeof v === 'number' ? v : 0)}
            suffix="USD"
            style={{ width: 220 }}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.finance.voucher.field.count')}</label>
          <InputNumber
            min={1}
            value={count}
            onChange={(v) => setCount(typeof v === 'number' ? v : 1)}
            style={{ width: 220 }}
          />
          <span className="wg-cell-dim">{t('admin.finance.voucher.field.countHint')}</span>
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.finance.voucher.field.maxUses')}</label>
          <InputNumber
            min={1}
            value={maxUses}
            onChange={(v) => setMaxUses(typeof v === 'number' ? v : 1)}
            style={{ width: 220 }}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.finance.voucher.field.expires')}</label>
          <Input type="date" value={expires} onChange={setExpires} style={{ width: 220 }} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.finance.voucher.field.desc')}</label>
          <Input value={description} onChange={setDescription} />
        </div>

        {codes.length > 0 && (
          <div className="wg-form-field">
            <label className="wg-form-label">{t('admin.finance.voucher.generatedTitle')}</label>
            <div
              className="wg-pro-card"
              style={{ padding: 12, maxHeight: 220, overflow: 'auto', display: 'grid', gap: 6 }}
            >
              {codes.map((c) => (
                <div key={c} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span className="wg-mono-sm">{c}</span>
                  <CopyButton text={c} ariaLabel={t('admin.finance.voucher.copyCode')} />
                </div>
              ))}
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <CopyButton text={codes.join('\n')} toast ariaLabel={t('admin.finance.voucher.copyAll')} />
              <span className="wg-cell-dim">{t('admin.finance.voucher.copyAllHint')}</span>
            </div>
          </div>
        )}
      </div>
    </SideSheet>
  );
}
