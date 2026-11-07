import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  DatePicker,
  Empty,
  Input,
  Select,
  SideSheet,
  Table,
  Tabs,
  Tag,
  Toast,
} from '@douyinfe/semi-ui';
import { IconExport, IconLock, IconRefresh, IconSearch } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import dangerConfirm from '../../components/dangerConfirm';
import { useI18n } from '../../i18n';
import { getAdminModeCached } from '../../api/admin';
import { formatDateTime, formatNumber } from '../../api/console';
import {
  anonymizePendingDeletion,
  exportAdminAuditLogs,
  getAdminAuditLog,
  getRetentionDefaults,
  listAdminAuditLogs,
  listGdprExportRequests,
  listPendingDeletions,
  processGdprExportRequest,
  type AdminAuditLog,
  type AdminAuditLogDetail,
  type AuditFilters,
  type GdprExportRequest,
  type PendingDeletion,
  type RetentionDefaults,
} from '../../api/adminSecurity';

type TabKey = 'audit' | 'compliance';

const CATEGORIES = ['auth', 'user', 'billing', 'enterprise', 'admin', 'settings', 'security', 'gdpr'];
const LEVELS = ['info', 'warning', 'error'];

function ymd(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

const CATEGORY_COLORS: Record<string, 'blue' | 'green' | 'orange' | 'violet' | 'red' | 'grey'> = {
  auth: 'blue',
  user: 'green',
  billing: 'violet',
  enterprise: 'orange',
  admin: 'red',
  settings: 'grey',
  security: 'red',
  gdpr: 'orange',
};

export default function AdminSecurity() {
  const { t } = useI18n();
  const [tab, setTab] = useState<TabKey>('audit');
  // The compliance tab reads super-admin-only endpoints (retention defaults,
  // GDPR export requests, pending deletions) — gate the whole tab so a
  // system_admin sees an explicit lock instead of silently empty cards.
  const [isSuper, setIsSuper] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getAdminModeCached()
      .then((m) => !cancelled && setIsSuper(m.userRole === 'super_admin'))
      .catch(() => !cancelled && setIsSuper(false));
    return () => {
      cancelled = true;
    };
  }, []);

  // audit
  const [logs, setLogs] = useState<AdminAuditLog[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [category, setCategory] = useState('all');
  const [level, setLevel] = useState('all');
  const [action, setAction] = useState('');
  const [range, setRange] = useState<[Date, Date] | null>(null);
  const [exporting, setExporting] = useState(false);
  const [detailId, setDetailId] = useState<number | null>(null);

  // compliance
  const [retention, setRetention] = useState<RetentionDefaults>({});
  const [exports, setExports] = useState<GdprExportRequest[] | null>(null);
  const [deletions, setDeletions] = useState<PendingDeletion[] | null>(null);
  const [gdprRetentionDays, setGdprRetentionDays] = useState<number | undefined>();
  const [compLoading, setCompLoading] = useState(false);

  const currentFilters = (p = page): AuditFilters => ({
    page: p,
    limit: 20,
    category: category === 'all' ? undefined : category,
    level: level === 'all' ? undefined : level,
    action: action.trim() || undefined,
    start_date: range ? ymd(range[0]) : undefined,
    end_date: range ? ymd(range[1]) : undefined,
  });

  const loadAudit = useCallback(
    async (p = 1, override?: AuditFilters) => {
      setLoading(true);
      try {
        const res = await listAdminAuditLogs(
          override ?? {
            page: p,
            limit: 20,
            category: category === 'all' ? undefined : category,
            level: level === 'all' ? undefined : level,
            action: action.trim() || undefined,
            start_date: range ? ymd(range[0]) : undefined,
            end_date: range ? ymd(range[1]) : undefined,
          }
        );
        setLogs(res.logs);
        setTotal(res.total);
        setPage(res.page);
      } catch (e) {
        setLogs([]);
        Toast.error((e as Error).message);
      } finally {
        setLoading(false);
      }
    },
    [category, level, action, range]
  );

  const loadCompliance = useCallback(async () => {
    if (!isSuper) return;
    setCompLoading(true);
    try {
      const [ret, exp, del] = await Promise.all([
        getRetentionDefaults().catch(() => ({})),
        listGdprExportRequests().catch(() => []),
        listPendingDeletions().catch(() => ({
          retention_days: undefined,
          pending_deletions: [] as PendingDeletion[],
        })),
      ]);
      setRetention(ret);
      setExports(exp);
      setDeletions(del.pending_deletions);
      setGdprRetentionDays(del.retention_days);
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setCompLoading(false);
    }
  }, [isSuper]);

  useEffect(() => {
    loadAudit(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (tab === 'compliance' && exports === null) loadCompliance();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);

  const onExport = async () => {
    setExporting(true);
    try {
      await exportAdminAuditLogs(currentFilters(1));
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setExporting(false);
    }
  };

  const auditColumns = [
    {
      title: t('admin.security.audit.col.time'),
      dataIndex: 'created_at',
      width: 160,
      render: (v: string) => <span className="wg-cell-dim">{formatDateTime(v)}</span>,
    },
    {
      title: t('admin.security.audit.col.operator'),
      dataIndex: 'user_email',
      width: 190,
      render: (v: string) => <span className="wg-cell-ellipsis">{v || '—'}</span>,
    },
    {
      title: t('admin.security.audit.col.category'),
      dataIndex: 'category',
      width: 100,
      render: (v: string) => (
        <Tag color={CATEGORY_COLORS[v] ?? 'grey'} size="small">
          {categoryLabel(v, t)}
        </Tag>
      ),
    },
    {
      title: t('admin.security.audit.col.action'),
      dataIndex: 'action',
      width: 180,
      render: (v: string, r: AdminAuditLog) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span className="wg-mono-sm wg-cell-ellipsis" title={v}>
            {v || '—'}
          </span>
          <span className="wg-cell-dim wg-cell-ellipsis" title={r.message}>
            {r.message || ''}
          </span>
        </div>
      ),
    },
    {
      title: t('admin.security.audit.col.target'),
      width: 170,
      render: (_: unknown, r: AdminAuditLog) => (
        <span className="wg-mono-sm wg-cell-ellipsis">
          {r.target_type ? `${r.target_type}${r.target_id ? `:${r.target_id}` : ''}` : '—'}
        </span>
      ),
    },
    {
      title: t('admin.security.audit.col.result'),
      dataIndex: 'level',
      width: 100,
      render: (v: string) => (
        <Tag color={v === 'error' ? 'red' : v === 'warning' ? 'orange' : 'green'}>
          {t(`admin.security.audit.level.${v ?? 'info'}`)}
        </Tag>
      ),
    },
    {
      title: t('common.actions'),
      width: 90,
      render: (_: unknown, r: AdminAuditLog) => (
        <Button theme="borderless" size="small" onClick={() => setDetailId(r.id)}>
          {t('common.view')}
        </Button>
      ),
    },
  ];

  const exportColumns = [
    {
      title: t('admin.security.comp.export.col.user'),
      dataIndex: 'user_email',
      render: (v: string, r: GdprExportRequest) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span className="wg-cell-strong">{v || r.email || '—'}</span>
          <span className="wg-cell-dim">{r.user_id || ''}</span>
        </div>
      ),
    },
    {
      title: t('admin.security.comp.export.col.scope'),
      dataIndex: 'scope',
      width: 110,
      render: (v: string) => <Tag color="blue">{v || '—'}</Tag>,
    },
    {
      title: t('admin.security.comp.export.col.status'),
      dataIndex: 'status',
      width: 110,
      render: (v: string) => (
        <Tag color={v === 'ready' ? 'green' : v === 'pending' ? 'orange' : 'grey'}>
          {t(`admin.security.comp.export.status.${v ?? 'pending'}`)}
        </Tag>
      ),
    },
    {
      title: t('admin.security.comp.export.col.requested'),
      dataIndex: 'requested_at',
      width: 160,
      render: (v: string) => <span className="wg-cell-dim">{formatDateTime(v)}</span>,
    },
    {
      title: t('admin.security.comp.export.col.processed'),
      dataIndex: 'processed_at',
      width: 160,
      render: (v: string | null) => <span className="wg-cell-dim">{formatDateTime(v)}</span>,
    },
    {
      title: t('common.actions'),
      width: 120,
      render: (_: unknown, r: GdprExportRequest) =>
        r.status === 'pending' ? (
          <Button
            theme="borderless"
            size="small"
            onClick={async () => {
              try {
                await processGdprExportRequest(r.id);
                Toast.success(t('admin.security.comp.export.processedDone'));
                await loadCompliance();
              } catch (e) {
                Toast.error((e as Error).message);
              }
            }}
          >
            {t('admin.security.comp.export.process')}
          </Button>
        ) : (
          <span className="wg-cell-dim">—</span>
        ),
    },
  ];

  const deletionColumns = [
    {
      title: t('admin.security.comp.deletion.col.user'),
      dataIndex: 'email',
      render: (v: string, r: PendingDeletion) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span className="wg-cell-strong">{r.name || v || '—'}</span>
          <span className="wg-cell-dim">{v || r.user_id}</span>
        </div>
      ),
    },
    {
      title: t('admin.security.comp.deletion.col.requested'),
      dataIndex: 'requested_at',
      width: 170,
      render: (v: string) => <span className="wg-cell-dim">{formatDateTime(v)}</span>,
    },
    {
      title: t('admin.security.comp.deletion.col.remaining'),
      dataIndex: 'days_remaining',
      width: 140,
      render: (v: number) => (
        <span className="wg-num">
          {formatNumber(v ?? 0)} {t('common.days')}
        </span>
      ),
    },
    {
      title: t('common.actions'),
      width: 130,
      render: (_: unknown, r: PendingDeletion) => (
        <Button
          theme="borderless"
          size="small"
          type="danger"
          onClick={() =>
            dangerConfirm({
              title: t('admin.security.comp.deletion.anonymizeTitle'),
              content: t('admin.security.comp.deletion.anonymizeDesc', {
                email: r.email || r.user_id,
              }),
              okText: t('admin.security.comp.deletion.anonymize'),
              cancelText: t('common.cancelAction'),
              onOk: async () => {
                try {
                  await anonymizePendingDeletion(r.user_id);
                  Toast.success(t('admin.security.comp.deletion.anonymizedDone'));
                  await loadCompliance();
                } catch (e) {
                  Toast.error((e as Error).message);
                }
              },
            })
          }
        >
          {t('admin.security.comp.deletion.anonymize')}
        </Button>
      ),
    },
  ];

  const pendingExports = (exports ?? []).filter((r) => r.status === 'pending').length;

  return (
    <div className="wg-page">
      <PageHead title={t('admin.security.title')} />

      <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as TabKey)}>
        <Tabs.TabPane itemKey="audit" tab={t('admin.security.tab.audit')}>
          <div className="wg-pro-card wg-filter-card">
            <Select
              style={{ width: 150 }}
              value={category}
              onChange={(v) => setCategory((v as string) ?? 'all')}
              optionList={[
                { value: 'all', label: t('admin.security.audit.category.all') },
                ...CATEGORIES.map((c) => ({ value: c, label: categoryLabel(c, t) })),
              ]}
            />
            <Select
              style={{ width: 130 }}
              value={level}
              onChange={(v) => setLevel((v as string) ?? 'all')}
              optionList={[
                { value: 'all', label: t('admin.security.audit.level.all') },
                ...LEVELS.map((l) => ({
                  value: l,
                  label: t(`admin.security.audit.level.${l}`),
                })),
              ]}
            />
            <Input
              style={{ width: 190 }}
              placeholder={t('admin.security.audit.actionPh')}
              value={action}
              onChange={setAction}
              showClear
            />
            <DatePicker
              type="dateRange"
              style={{ width: 240 }}
              placeholder={[t('dash.range.start'), t('dash.range.end')]}
              value={range ?? undefined}
              onChange={(v) => setRange((v as [Date, Date] | null) ?? null)}
            />
            <div className="wg-filter-actions">
              <Button
                type="primary"
                theme="solid"
                icon={<IconSearch />}
                onClick={() => loadAudit(1)}
              >
                {t('common.search')}
              </Button>
              <Button
                icon={<IconRefresh />}
                theme="light"
                onClick={() => {
                  setCategory('all');
                  setLevel('all');
                  setAction('');
                  setRange(null);
                  loadAudit(1, {});
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
                  {t('admin.security.audit.tableTitle')}
                  <span className="wg-cell-dim"> · {formatNumber(total)}</span>
                </span>
              </div>
              <Button
                theme="light"
                icon={<IconExport />}
                loading={exporting}
                onClick={onExport}
              >
                {t('admin.security.audit.export')}
              </Button>
            </div>
            <Table
              loading={loading}
              rowKey="id"
              columns={auditColumns}
              dataSource={logs ?? []}
              pagination={{
                currentPage: page,
                pageSize: 20,
                total,
                onPageChange: (p) => loadAudit(p),
              }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.security.audit.empty')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="compliance" tab={t('admin.security.tab.compliance')}>
          {!isSuper ? (
            <div className="wg-pro-card">
              <div className="wg-pro-card-body">
                <div
                  style={{
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    gap: 8,
                    padding: '48px 16px',
                    textAlign: 'center',
                  }}
                >
                  <IconLock size="large" style={{ color: 'var(--wg-text-2)' }} />
                  <div style={{ fontWeight: 600 }}>{t('admin.security.superOnly.title')}</div>
                  <div className="wg-cell-dim" style={{ maxWidth: 420 }}>
                    {t('admin.security.superOnly.hint')}
                  </div>
                  <Tag color="red" size="small">
                    {t('profile.role.super_admin')}
                  </Tag>
                </div>
              </div>
            </div>
          ) : (
          <>
          <div className="wg-stat-grid">
            <StatCard
              label={t('admin.security.comp.kpi.retention')}
              value={`${retention.days ?? 0} ${t('common.days')}`}
            />
            <StatCard label={t('admin.security.comp.kpi.gdpr')} value={`${gdprRetentionDays ?? 0} ${t('common.days')}`} />
            <StatCard label={t('admin.security.comp.kpi.exports')} value={formatNumber(pendingExports)} />
            <StatCard label={t('admin.security.comp.kpi.deletions')} value={formatNumber(deletions?.length ?? 0)} />
          </div>

          <div className="wg-pro-card">
            <div className="wg-pro-card-head">
              <span className="wg-pro-card-title">{t('admin.security.comp.retention.title')}</span>
              <Button icon={<IconRefresh />} theme="light" size="small" onClick={loadCompliance}>
                {t('common.refresh')}
              </Button>
            </div>
            <div className="wg-pro-card-body">
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 16 }}>
                <Meta
                  label={t('admin.security.comp.retention.mode')}
                  value={t(`admin.security.comp.retention.modeValue.${retention.mode ?? 'off'}`)}
                />
                <Meta
                  label={t('admin.security.comp.retention.days')}
                  value={`${retention.days ?? 0} ${t('common.days')}`}
                />
                <Meta
                  label={t('admin.security.comp.retention.range')}
                  value={`${retention.min_days ?? 0} – ${retention.max_days ?? 0} ${t('common.days')}`}
                />
                <Meta
                  label={t('admin.security.comp.retention.gdpr')}
                  value={`${gdprRetentionDays ?? 0} ${t('common.days')}`}
                />
              </div>
              <p className="wg-cell-dim" style={{ marginBottom: 0 }}>
                {t('admin.security.comp.retention.note')}
              </p>
            </div>
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.security.comp.export.title')}</span>
              </div>
            </div>
            <Table
              loading={compLoading}
              rowKey="id"
              columns={exportColumns}
              dataSource={exports ?? []}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.security.comp.export.empty')} />}
            />
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.security.comp.deletion.title')}</span>
              </div>
            </div>
            <Table
              loading={compLoading}
              rowKey="user_id"
              columns={deletionColumns}
              dataSource={deletions ?? []}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.security.comp.deletion.empty')} />}
            />
          </div>
          </>
          )}
        </Tabs.TabPane>
      </Tabs>

      {detailId !== null && (
        <AuditDetailDrawer id={detailId} onClose={() => setDetailId(null)} />
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

function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <span className="wg-cell-dim">{label}</span>
      <span className="wg-cell-strong">{value}</span>
    </div>
  );
}

function categoryLabel(v: string, t: (k: string) => string) {
  const key = `admin.security.audit.category.${v}`;
  const label = t(key);
  return label === key ? v || '—' : label;
}

function AuditDetailDrawer({ id, onClose }: { id: number; onClose: () => void }) {
  const { t } = useI18n();
  const [detail, setDetail] = useState<AdminAuditLogDetail | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    getAdminAuditLog(id)
      .then((d) => !cancelled && setDetail(d))
      .catch((e) => !cancelled && Toast.error((e as Error).message))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [id]);

  return (
    <SideSheet
      title={t('admin.security.audit.detailTitle')}
      visible
      onCancel={onClose}
      width={640}
      closeOnEsc
      maskClosable={false}
      footer={
        <div className="wg-drawer-footer">
          <Button onClick={onClose}>{t('common.close')}</Button>
        </div>
      }
    >
      {loading ? (
        <div className="wg-page-loading">
          <span className="wg-text-2">{t('common.loading')}</span>
        </div>
      ) : !detail ? (
        <Empty description={t('admin.security.audit.empty')} />
      ) : (
        <div className="wg-field-grid">
          <Meta label={t('admin.security.audit.col.time')} value={formatDateTime(detail.created_at)} />
          <Meta label={t('admin.security.audit.col.operator')} value={detail.user_email || '—'} />
          <Meta
            label={t('admin.security.audit.col.category')}
            value={categoryLabel(detail.category ?? '', t)}
          />
          <Meta label={t('admin.security.audit.col.action')} value={detail.action || '—'} />
          <Meta
            label={t('admin.security.audit.col.result')}
            value={t(`admin.security.audit.level.${detail.level ?? 'info'}`)}
          />
          <Meta
            label={t('admin.security.audit.col.target')}
            value={
              detail.target_type
                ? `${detail.target_type}${detail.target_id ? `:${detail.target_id}` : ''}`
                : '—'
            }
          />
          <Meta label={t('admin.security.audit.detail.ip')} value={detail.ip_address || '—'} />
          <Meta label={t('admin.security.audit.detail.userAgent')} value={detail.user_agent || '—'} />

          <div className="wg-form-field">
            <label className="wg-form-label">{t('admin.security.audit.detail.message')}</label>
            <div className="wg-pro-card" style={{ padding: 12, whiteSpace: 'pre-wrap' }}>
              {detail.message || '—'}
            </div>
          </div>

          {detail.details && (
            <div className="wg-form-field">
              <label className="wg-form-label">{t('admin.security.audit.detail.details')}</label>
              <pre className="wg-pro-card" style={{ padding: 12, margin: 0, overflow: 'auto', maxHeight: 240 }}>
                {detail.details}
              </pre>
            </div>
          )}

          {(detail.old_value || detail.new_value) && (
            <div className="wg-field-grid" style={{ gridTemplateColumns: '1fr 1fr' }}>
              <div className="wg-form-field">
                <label className="wg-form-label">{t('admin.security.audit.detail.old')}</label>
                <pre className="wg-pro-card" style={{ padding: 12, margin: 0, overflow: 'auto' }}>
                  {detail.old_value || '—'}
                </pre>
              </div>
              <div className="wg-form-field">
                <label className="wg-form-label">{t('admin.security.audit.detail.new')}</label>
                <pre className="wg-pro-card" style={{ padding: 12, margin: 0, overflow: 'auto' }}>
                  {detail.new_value || '—'}
                </pre>
              </div>
            </div>
          )}
        </div>
      )}
    </SideSheet>
  );
}
