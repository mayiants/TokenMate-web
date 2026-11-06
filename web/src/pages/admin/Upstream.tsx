import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Button,
  Empty,
  Input,
  InputNumber,
  Select,
  SideSheet,
  Switch,
  Table,
  Tabs,
  Tag,
  Toast,
} from '@douyinfe/semi-ui';
import { IconPlus, IconRefresh, IconSearch } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import dangerConfirm from '../../components/dangerConfirm';
import { useI18n } from '../../i18n';
import { formatNumber } from '../../api/console';
import {
  createAdminKey,
  deleteAdminKey,
  getHealthSummary,
  listAdminKeys,
  listAdminProviders,
  listKeyMetrics,
  listProviderHealth,
  listUpstreamErrors,
  probeAdminProvider,
  toggleAdminKey,
  toggleAdminProvider,
  updateAdminKey,
  updateAdminProvider,
  validateAdminKey,
  type AdminKey,
  type AdminProvider,
  type ErrorRow,
  type HealthSummary,
  type KeyMetric,
  type MonitorPeriod,
  type ProviderHealthRow,
} from '../../api/adminUpstream';

type TabKey = 'providers' | 'keys' | 'monitor';

const PERIODS: MonitorPeriod[] = ['24h', '7d', '15d', '30d', '90d'];

function healthTag(status: string | undefined, t: (k: string) => string) {
  switch (status) {
    case 'available':
    case 'valid':
      return <Tag color="green">{t('admin.upstream.health.available')}</Tag>;
    case 'degraded':
      return <Tag color="orange">{t('admin.upstream.health.degraded')}</Tag>;
    case 'unavailable':
    case 'invalid':
      return <Tag color="red">{t('admin.upstream.health.unavailable')}</Tag>;
    default:
      return <Tag color="grey">{t('admin.upstream.health.unknown')}</Tag>;
  }
}

function enabledPill(enabled: boolean, t: (k: string) => string) {
  return enabled ? (
    <Tag color="green">{t('admin.upstream.enabled')}</Tag>
  ) : (
    <Tag color="red">{t('admin.upstream.disabled')}</Tag>
  );
}

interface ProviderHealthAgg {
  provider: string;
  requests: number;
  failures: number;
  avgLatency: number;
  availability: number;
  status: string;
}

export default function AdminUpstream() {
  const { t } = useI18n();
  const [tab, setTab] = useState<TabKey>('providers');

  const [providers, setProviders] = useState<AdminProvider[] | null>(null);
  const [providersLoading, setProvidersLoading] = useState(true);
  const [search, setSearch] = useState('');
  const [probing, setProbing] = useState<string[]>([]);
  const [editProvider, setEditProvider] = useState<AdminProvider | null>(null);

  const [keys, setKeys] = useState<AdminKey[] | null>(null);
  const [metrics, setMetrics] = useState<Record<string, KeyMetric>>({});
  const [keysLoading, setKeysLoading] = useState(false);
  const [keySearch, setKeySearch] = useState('');
  const [keyTarget, setKeyTarget] = useState<AdminKey | null>(null);
  const [keyCreate, setKeyCreate] = useState(false);

  const [period, setPeriod] = useState<MonitorPeriod>('24h');
  const [summary, setSummary] = useState<HealthSummary>({});
  const [healthRows, setHealthRows] = useState<ProviderHealthRow[]>([]);
  const [errors, setErrors] = useState<ErrorRow[]>([]);
  const [monitorLoading, setMonitorLoading] = useState(false);

  const loadProviders = useCallback(async () => {
    setProvidersLoading(true);
    try {
      setProviders(await listAdminProviders());
    } catch (e) {
      setProviders([]);
      Toast.error((e as Error).message);
    } finally {
      setProvidersLoading(false);
    }
  }, []);

  const loadKeys = useCallback(async () => {
    setKeysLoading(true);
    try {
      const [list, metricList] = await Promise.all([
        listAdminKeys(),
        listKeyMetrics('24h').catch(() => [] as KeyMetric[]),
      ]);
      setKeys(list);
      const map: Record<string, KeyMetric> = {};
      for (const m of metricList) map[m.id] = m;
      setMetrics(map);
    } catch (e) {
      setKeys([]);
      Toast.error((e as Error).message);
    } finally {
      setKeysLoading(false);
    }
  }, []);

  const loadMonitor = useCallback(async (p: MonitorPeriod) => {
    setMonitorLoading(true);
    try {
      const [sum, health, errs] = await Promise.all([
        getHealthSummary(p).catch(() => ({})),
        listProviderHealth(p).catch(() => [] as ProviderHealthRow[]),
        listUpstreamErrors(p).catch(() => [] as ErrorRow[]),
      ]);
      setSummary(sum);
      setHealthRows(health);
      setErrors(errs);
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setMonitorLoading(false);
    }
  }, []);

  useEffect(() => {
    loadProviders();
  }, [loadProviders]);

  useEffect(() => {
    if (tab === 'keys' && keys === null) loadKeys();
    if (tab === 'monitor') loadMonitor(period);
  }, [tab, period, keys, loadKeys, loadMonitor]);

  const filteredProviders = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return providers ?? [];
    return (providers ?? []).filter(
      (p) => p.id.toLowerCase().includes(q) || p.name.toLowerCase().includes(q)
    );
  }, [providers, search]);

  const filteredKeys = useMemo(() => {
    const q = keySearch.trim().toLowerCase();
    if (!q) return keys ?? [];
    return (keys ?? []).filter(
      (k) =>
        k.provider.toLowerCase().includes(q) ||
        (k.key_preview ?? '').toLowerCase().includes(q) ||
        (k.tier ?? '').toLowerCase().includes(q)
    );
  }, [keys, keySearch]);

  const healthByProvider = useMemo(() => {
    const map = new Map<string, ProviderHealthAgg>();
    for (const row of healthRows) {
      const agg = map.get(row.provider) ?? {
        provider: row.provider,
        requests: 0,
        failures: 0,
        avgLatency: 0,
        availability: 0,
        status: 'unknown',
      };
      agg.requests += row.request_count;
      agg.failures += row.failure_count;
      agg.avgLatency = Math.max(agg.avgLatency, row.avg_latency_ms ?? 0);
      agg.availability = Math.max(agg.availability, row.availability ?? 0);
      if (row.status === 'unavailable') agg.status = 'unavailable';
      else if (row.status === 'degraded' && agg.status !== 'unavailable') agg.status = 'degraded';
      else if (row.status === 'available' && agg.status === 'unknown') agg.status = 'available';
      map.set(row.provider, agg);
    }
    return Array.from(map.values()).sort((a, b) => b.requests - a.requests);
  }, [healthRows]);

  const probe = async (p: AdminProvider) => {
    setProbing((prev) => [...prev, p.id]);
    try {
      const res = await probeAdminProvider(p.id, 5);
      Toast.success(
        t('admin.upstream.probeDone', {
          ok: formatNumber(res.validated ?? 0),
          bad: formatNumber(res.invalid ?? 0),
        })
      );
      await loadProviders();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setProbing((prev) => prev.filter((id) => id !== p.id));
    }
  };

  const providerColumns = [
    { title: t('admin.upstream.col.provider'), dataIndex: 'name' },
    {
      title: t('admin.upstream.col.protocol'),
      dataIndex: 'type',
      width: 130,
      render: (v: string) => <Tag color="blue">{v || '—'}</Tag>,
    },
    {
      title: t('admin.upstream.col.baseUrl'),
      dataIndex: 'base_url',
      render: (v: string) => <span className="wg-mono-sm wg-cell-dim">{v || '—'}</span>,
    },
    {
      title: t('admin.upstream.col.status'),
      dataIndex: 'enabled',
      width: 100,
      render: (v: boolean) => enabledPill(v, t),
    },
    {
      title: t('admin.upstream.col.priority'),
      dataIndex: 'sort_order',
      width: 90,
      render: (v: number) => <span className="wg-num">{v ?? 0}</span>,
    },
    {
      title: t('admin.upstream.col.keyCount'),
      dataIndex: 'key_count',
      width: 90,
      render: (v: number) => <span className="wg-num">{formatNumber(v ?? 0)}</span>,
    },
    {
      title: t('common.actions'),
      width: 180,
      render: (_: unknown, r: AdminProvider) => (
        <div className="wg-cell-actions">
          <Button theme="borderless" size="small" onClick={() => setEditProvider(r)}>
            {t('common.edit')}
          </Button>
          <Button
            theme="borderless"
            size="small"
            loading={probing.includes(r.id)}
            onClick={() => probe(r)}
          >
            {t('admin.upstream.probe')}
          </Button>
        </div>
      ),
    },
  ];

  const keyColumns = [
    { title: t('admin.upstream.col.provider'), dataIndex: 'provider', width: 150 },
    {
      title: t('admin.upstream.col.key'),
      dataIndex: 'key_preview',
      render: (v: string) => <span className="wg-mono-sm">{v || '—'}</span>,
    },
    {
      title: t('admin.upstream.col.weight'),
      dataIndex: 'tier',
      width: 100,
      render: (v: string) => v || <span className="wg-cell-dim">—</span>,
    },
    {
      title: t('admin.upstream.col.health'),
      width: 110,
      render: (_: unknown, r: AdminKey) =>
        healthTag(metrics[r.id]?.health_status || r.validation_status || undefined, t),
    },
    {
      title: t('admin.upstream.col.todayUsage'),
      width: 120,
      render: (_: unknown, r: AdminKey) => (
        <span className="wg-num">{formatNumber(metrics[r.id]?.request_count ?? 0)}</span>
      ),
    },
    {
      title: t('admin.upstream.col.enabled'),
      dataIndex: 'enabled',
      width: 100,
      render: (v: boolean, r: AdminKey) => (
        <Switch
          size="small"
          checked={v}
          onChange={() =>
            (async () => {
              try {
                await toggleAdminKey(r.id, !v);
                Toast.success(t('admin.common.saved'));
                await loadKeys();
              } catch (e) {
                Toast.error((e as Error).message);
              }
            })()
          }
        />
      ),
    },
    {
      title: t('common.actions'),
      width: 230,
      render: (_: unknown, r: AdminKey) => (
        <div className="wg-cell-actions">
          <Button theme="borderless" size="small" onClick={() => setKeyTarget(r)}>
            {t('admin.upstream.rotate')}
          </Button>
          <Button
            theme="borderless"
            size="small"
            onClick={() =>
              (async () => {
                try {
                  const res = await validateAdminKey(r.id);
                  Toast.success(t('admin.upstream.validateDone', { status: res.status ?? '—' }));
                  await loadKeys();
                } catch (e) {
                  Toast.error((e as Error).message);
                }
              })()
            }
          >
            {t('admin.upstream.validate')}
          </Button>
          <Button
            theme="borderless"
            size="small"
            type="danger"
            onClick={() =>
              dangerConfirm({
                title: t('admin.upstream.deleteKeyTitle'),
                content: t('admin.upstream.deleteKeyDesc'),
                okText: t('common.delete'),
                cancelText: t('common.cancelAction'),
                onOk: async () => {
                  try {
                    await deleteAdminKey(r.id);
                    Toast.success(t('admin.common.deleted'));
                    await loadKeys();
                  } catch (e) {
                    Toast.error((e as Error).message);
                  }
                },
              })
            }
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
  ];

  const monitorColumns = [
    { title: t('admin.upstream.col.provider'), dataIndex: 'provider' },
    {
      title: t('admin.upstream.col.requests'),
      dataIndex: 'requests',
      width: 120,
      render: (v: number) => <span className="wg-num">{formatNumber(v)}</span>,
    },
    {
      title: t('admin.upstream.col.failures'),
      dataIndex: 'failures',
      width: 100,
      render: (v: number) => (
        <span className="wg-num">{formatNumber(v)}</span>
      ),
    },
    {
      title: t('admin.upstream.col.latency'),
      dataIndex: 'avgLatency',
      width: 110,
      render: (v: number) => <span className="wg-num">{formatNumber(v)} ms</span>,
    },
    {
      title: t('admin.upstream.col.availability'),
      dataIndex: 'availability',
      width: 120,
      render: (v: number) => <span className="wg-num">{(v ?? 0).toFixed(1)}%</span>,
    },
    {
      title: t('admin.upstream.col.health'),
      dataIndex: 'status',
      width: 120,
      render: (v: string) => healthTag(v, t),
    },
  ];

  const errorColumns = [
    { title: t('admin.upstream.col.model'), dataIndex: 'model' },
    {
      title: t('admin.upstream.col.group'),
      dataIndex: 'group_name',
      render: (v: string, r: ErrorRow) => v || r.group_id || <span className="wg-cell-dim">—</span>,
    },
    {
      title: t('admin.upstream.col.failures'),
      dataIndex: 'failures',
      width: 100,
      render: (v: number) => <span className="wg-num">{formatNumber(v)}</span>,
    },
    {
      title: t('admin.upstream.col.timeout'),
      dataIndex: 'timeout',
      width: 100,
      render: (v: number) => <span className="wg-num">{formatNumber(v)}</span>,
    },
    {
      title: t('admin.upstream.col.rateLimit'),
      dataIndex: 'rate_limit',
      width: 100,
      render: (v: number) => <span className="wg-num">{formatNumber(v)}</span>,
    },
    {
      title: t('admin.upstream.col.error5xx'),
      dataIndex: 'error_5xx',
      width: 100,
      render: (v: number) => <span className="wg-num">{formatNumber(v)}</span>,
    },
    {
      title: t('admin.upstream.col.impacted'),
      dataIndex: 'impacted_users',
      width: 120,
      render: (v: number) => <span className="wg-num">{formatNumber(v)}</span>,
    },
  ];

  const summaryCards = [
    {
      key: 'available',
      label: t('admin.upstream.health.available'),
      value: summary.available ?? 0,
      tone: 'wg-tint-green',
    },
    {
      key: 'degraded',
      label: t('admin.upstream.health.degraded'),
      value: summary.degraded ?? 0,
      tone: 'wg-tint-amber',
    },
    {
      key: 'unavailable',
      label: t('admin.upstream.health.unavailable'),
      value: summary.unavailable ?? 0,
      tone: 'wg-tint-red',
    },
    {
      key: 'unknown',
      label: t('admin.upstream.health.unknown'),
      value: summary.unknown ?? 0,
      tone: 'wg-tint-blue',
    },
  ];

  return (
    <div className="wg-page">
      <PageHead
        title={t('admin.upstream.title')}
        extra={
          <Button
            icon={<IconRefresh />}
            theme="light"
            onClick={() => {
              if (tab === 'providers') loadProviders();
              else if (tab === 'keys') loadKeys();
              else loadMonitor(period);
            }}
          >
            {t('common.refresh')}
          </Button>
        }
      />

      <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as TabKey)}>
        <Tabs.TabPane itemKey="providers" tab={t('admin.upstream.tab.providers')}>
          <div className="wg-pro-card wg-filter-card">
            <Input
              style={{ width: 260 }}
              placeholder={t('admin.upstream.searchProviderPh')}
              value={search}
              onChange={setSearch}
              prefix={<IconSearch />}
              showClear
            />
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.upstream.providerTitle')}</span>
              </div>
            </div>
            <Table
              loading={providersLoading}
              rowKey="id"
              columns={providerColumns}
              dataSource={filteredProviders}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.upstream.providerEmpty')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="keys" tab={t('admin.upstream.tab.keys')}>
          <div className="wg-pro-card wg-filter-card">
            <Input
              style={{ width: 260 }}
              placeholder={t('admin.upstream.searchKeyPh')}
              value={keySearch}
              onChange={setKeySearch}
              prefix={<IconSearch />}
              showClear
            />
            <div className="wg-filter-actions">
              <Button icon={<IconPlus />} type="primary" theme="solid" onClick={() => setKeyCreate(true)}>
                {t('admin.upstream.addKey')}
              </Button>
            </div>
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.upstream.keyTitle')}</span>
              </div>
            </div>
            <Table
              loading={keysLoading}
              rowKey="id"
              columns={keyColumns}
              dataSource={filteredKeys}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.upstream.keyEmpty')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="monitor" tab={t('admin.upstream.tab.monitor')}>
          <div className="wg-pro-card wg-filter-card">
            <Select
              style={{ width: 150 }}
              value={period}
              onChange={(v) => setPeriod((v as MonitorPeriod) ?? '24h')}
              optionList={PERIODS.map((p) => ({ value: p, label: t(`admin.upstream.period.${p}`) }))}
            />
          </div>

          <div className="wg-stat-grid">
            {summaryCards.map((c) => (
              <div key={c.key} className="wg-pro-card wg-stat">
                <div className={`wg-stat-icon ${c.tone}`}>
                  <span className="wg-stat-dot" />
                </div>
                <div className="wg-stat-body">
                  <div className="wg-stat-label">{c.label}</div>
                  <div className="wg-stat-value">{formatNumber(c.value)}</div>
                </div>
              </div>
            ))}
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.upstream.healthTitle')}</span>
              </div>
            </div>
            <Table
              loading={monitorLoading}
              rowKey="provider"
              columns={monitorColumns}
              dataSource={healthByProvider}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.upstream.healthEmpty')} />}
            />
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.upstream.errorTitle')}</span>
              </div>
            </div>
            <Table
              loading={monitorLoading}
              rowKey={(r?: ErrorRow) => `${r?.model ?? ''}|${r?.group_id ?? ''}`}
              columns={errorColumns}
              dataSource={errors}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.upstream.errorEmpty')} />}
            />
          </div>
        </Tabs.TabPane>
      </Tabs>

      {editProvider && (
        <ProviderDrawer
          target={editProvider}
          onClose={() => setEditProvider(null)}
          onSaved={loadProviders}
        />
      )}

      {(keyTarget || keyCreate) && (
        <KeyDrawer
          target={keyTarget ?? undefined}
          providers={providers ?? []}
          onClose={() => {
            setKeyTarget(null);
            setKeyCreate(false);
          }}
          onSaved={loadKeys}
        />
      )}
    </div>
  );
}

function ProviderDrawer({
  target,
  onClose,
  onSaved,
}: {
  target: AdminProvider;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(target.name);
  const [type, setType] = useState(target.type ?? 'international');
  const [baseUrl, setBaseUrl] = useState(target.base_url ?? '');
  const [apiKey, setApiKey] = useState('');
  const [description, setDescription] = useState(target.description ?? '');
  const [enabled, setEnabled] = useState(target.enabled);
  const [saving, setSaving] = useState(false);

  const save = async () => {
    setSaving(true);
    try {
      await updateAdminProvider(target.id, {
        name: name.trim() || undefined,
        type,
        base_url: baseUrl,
        api_key: apiKey.trim() || undefined,
        description,
        enabled,
      });
      Toast.success(t('admin.common.saved'));
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
      title={t('admin.upstream.editProvider')}
      visible
      onCancel={onClose}
      width={600}
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
          <label className="wg-form-label">{t('admin.upstream.field.name')}</label>
          <Input value={name} onChange={setName} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.upstream.field.protocol')}</label>
          <Input value={type} onChange={setType} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.upstream.field.baseUrl')}</label>
          <Input value={baseUrl} onChange={setBaseUrl} placeholder="https://api.example.com/v1" />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.upstream.field.apiKey')}</label>
          <Input
            mode="password"
            value={apiKey}
            onChange={setApiKey}
            placeholder={target.api_key ?? t('admin.upstream.apiKeyKeep')}
          />
          <span className="wg-cell-dim">{t('admin.upstream.apiKeyHint')}</span>
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.upstream.field.desc')}</label>
          <Input value={description} onChange={setDescription} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.upstream.field.status')}</label>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <Switch checked={enabled} onChange={(v) => setEnabled(v)} />
            <span className="wg-cell-dim">
              {enabled ? t('admin.upstream.enabled') : t('admin.upstream.disabled')}
            </span>
          </div>
        </div>
      </div>
    </SideSheet>
  );
}

function KeyDrawer({
  target,
  providers,
  onClose,
  onSaved,
}: {
  target?: AdminKey;
  providers: AdminProvider[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const editing = Boolean(target);

  const [provider, setProvider] = useState(target?.provider ?? providers[0]?.id ?? '');
  const [key, setKey] = useState('');
  const [tier, setTier] = useState(target?.tier ?? 'default');
  const [rpm, setRpm] = useState<number>(target?.rpm_limit ?? 0);
  const [tpm, setTpm] = useState<number>(target?.tpm_limit ?? 0);
  const [quota, setQuota] = useState<number>(target?.quota_limit ?? 0);
  const [saving, setSaving] = useState(false);

  const save = async () => {
    if (!provider) {
      Toast.error(t('admin.upstream.providerRequired'));
      return;
    }
    if (!editing && !key.trim()) {
      Toast.error(t('admin.upstream.keyRequired'));
      return;
    }
    setSaving(true);
    try {
      const payload = {
        provider,
        key: key.trim() || undefined,
        tier: tier.trim() || undefined,
        rpm_limit: rpm || undefined,
        tpm_limit: tpm || undefined,
        quota_limit: quota || undefined,
      };
      if (editing && target) await updateAdminKey(target.id, payload);
      else await createAdminKey(payload);
      Toast.success(t(editing ? 'admin.common.saved' : 'admin.common.created'));
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
      title={t(editing ? 'admin.upstream.rotateKey' : 'admin.upstream.addKey')}
      visible
      onCancel={onClose}
      width={600}
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
          <label className="wg-form-label">
            {t('admin.upstream.field.provider')}
            <span className="wg-form-required">*</span>
          </label>
          <Select
            filter
            disabled={editing}
            value={provider || undefined}
            onChange={(v) => setProvider((v as string) ?? '')}
            optionList={providers.map((p) => ({ value: p.id, label: `${p.name} (${p.id})` }))}
            placeholder={t('admin.upstream.field.provider')}
            emptyContent={t('common.emptyData')}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">
            {t('admin.upstream.field.key')}
            {!editing && <span className="wg-form-required">*</span>}
          </label>
          <Input
            mode="password"
            value={key}
            onChange={setKey}
            placeholder={editing ? t('admin.upstream.rotateKeep') : 'sk-...'}
          />
          {editing && <span className="wg-cell-dim">{t('admin.upstream.rotateHint')}</span>}
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.upstream.field.tier')}</label>
          <Input value={tier} onChange={setTier} placeholder="default" />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.upstream.field.limits')}</label>
          <div style={{ display: 'flex', gap: 12 }}>
            <div className="wg-form-field" style={{ flex: 1 }}>
              <span className="wg-cell-dim">RPM</span>
              <InputNumber min={0} value={rpm} onChange={(v) => setRpm(typeof v === 'number' ? v : 0)} />
            </div>
            <div className="wg-form-field" style={{ flex: 1 }}>
              <span className="wg-cell-dim">TPM</span>
              <InputNumber min={0} value={tpm} onChange={(v) => setTpm(typeof v === 'number' ? v : 0)} />
            </div>
            <div className="wg-form-field" style={{ flex: 1 }}>
              <span className="wg-cell-dim">{t('admin.upstream.field.quota')}</span>
              <InputNumber min={0} value={quota} onChange={(v) => setQuota(typeof v === 'number' ? v : 0)} />
            </div>
          </div>
        </div>
      </div>
    </SideSheet>
  );
}
