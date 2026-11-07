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
import { IconExport, IconRefresh, IconSearch, IconUpload } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import dangerConfirm from '../../components/dangerConfirm';
import { useI18n } from '../../i18n';
import { formatNumber } from '../../api/console';
import { formatContext, formatPrice, fuzzyMatch } from '../../utils/format';
import {
  createPricingRule,
  deletePricingRule,
  exportPricingCsv,
  importServiceModels,
  listPricingRules,
  listServiceModels,
  resyncModelsToKeys,
  togglePricingRule,
  updatePricingRule,
  updateServiceModel,
  type PricingRule,
  type ServiceModel,
} from '../../api/adminModels';

type TabKey = 'general' | 'aws' | 'pricing';

const BILLING_MODES = ['flat', 'tiered', 'time_based', 'media'];

function modelName(m: ServiceModel): string {
  return m.api_model_name || m.product_id;
}

/** AWS providers encode the region in the id, e.g. amazon-us-east-1. */
function regionOf(provider: string): string {
  if (provider === 'amazon') return 'global';
  const idx = provider.indexOf('-');
  return idx >= 0 ? provider.slice(idx + 1) : provider;
}

function statusTag(status: string | undefined, t: (k: string) => string) {
  switch (status) {
    case 'active':
      return <Tag color="green">{t('admin.models.status.active')}</Tag>;
    case 'paused':
      return <Tag color="orange">{t('admin.models.status.paused')}</Tag>;
    case 'pending':
      return <Tag color="blue">{t('admin.models.status.pending')}</Tag>;
    case 'ignored':
      return <Tag color="grey">{t('admin.models.status.ignored')}</Tag>;
    default:
      return <Tag color="grey">{status || '—'}</Tag>;
  }
}

export default function AdminModels() {
  const { t } = useI18n();
  const [tab, setTab] = useState<TabKey>('general');

  const [models, setModels] = useState<ServiceModel[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState('');

  const [rules, setRules] = useState<PricingRule[] | null>(null);
  const [rulesLoading, setRulesLoading] = useState(false);
  const [ruleSearch, setRuleSearch] = useState('');
  const [editingRule, setEditingRule] = useState<PricingRule | null>(null);
  const [creatingRule, setCreatingRule] = useState(false);
  const [bulk, setBulk] = useState<'' | 'import' | 'sync'>('');

  const loadModels = useCallback(async () => {
    setLoading(true);
    try {
      setModels(await listServiceModels());
    } catch (e) {
      setModels([]);
      Toast.error((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  const loadRules = useCallback(async () => {
    setRulesLoading(true);
    try {
      setRules(await listPricingRules());
    } catch (e) {
      setRules([]);
      Toast.error((e as Error).message);
    } finally {
      setRulesLoading(false);
    }
  }, []);

  useEffect(() => {
    loadModels();
  }, [loadModels]);

  useEffect(() => {
    if (tab === 'pricing' && rules === null) loadRules();
  }, [tab, rules, loadRules]);

  const generalModels = useMemo(
    () => (models ?? []).filter((m) => !m.provider.toLowerCase().includes('amazon')),
    [models]
  );

  const awsModels = useMemo(
    () => (models ?? []).filter((m) => m.provider.toLowerCase().includes('amazon')),
    [models]
  );

  const filteredGeneral = useMemo(
    () => generalModels.filter((m) => fuzzyMatch(search, modelName(m), m.provider, m.vendor)),
    [generalModels, search]
  );

  const filteredAws = useMemo(
    () =>
      awsModels.filter((m) =>
        fuzzyMatch(search, modelName(m), m.provider, m.vendor, regionOf(m.provider))
      ),
    [awsModels, search]
  );

  const filteredRules = useMemo(
    () =>
      (rules ?? []).filter((r) =>
        fuzzyMatch(ruleSearch, r.api_model_name, r.product_id, r.provider, r.rule_name)
      ),
    [rules, ruleSearch]
  );

  const providers = useMemo(() => {
    const set = new Set((models ?? []).map((m) => m.provider));
    return Array.from(set).sort();
  }, [models]);

  const toggleModel = async (m: ServiceModel) => {
    const next = m.status === 'active' ? 'paused' : 'active';
    try {
      await updateServiceModel(m.id, { status: next });
      Toast.success(t('admin.common.saved'));
      await loadModels();
    } catch (e) {
      Toast.error((e as Error).message);
    }
  };

  const openPricing = (name: string) => {
    setRuleSearch(name);
    setTab('pricing');
  };

  const runBulk = async (kind: 'import' | 'sync') => {
    setBulk(kind);
    try {
      if (kind === 'import') {
        const res = await importServiceModels();
        Toast.success(
          t('admin.models.importDone', {
            added: formatNumber(res.added ?? 0),
            skipped: formatNumber(res.skipped ?? 0),
          })
        );
      } else {
        const res = await resyncModelsToKeys();
        Toast.success(t('admin.models.syncDone', { count: formatNumber(res.keys_updated ?? 0) }));
      }
      await loadModels();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setBulk('');
    }
  };

  const generalColumns = [
    {
      title: t('admin.models.col.provider'),
      dataIndex: 'provider',
      width: 140,
    },
    {
      title: t('admin.models.col.name'),
      render: (_: unknown, r: ServiceModel) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span className="wg-cell-strong">{modelName(r)}</span>
          <span className="wg-cell-dim wg-mono-sm">{r.product_id}</span>
        </div>
      ),
    },
    {
      title: t('admin.models.col.type'),
      dataIndex: 'model_type',
      width: 100,
      render: (v: string) => <Tag color="blue">{v || 'text'}</Tag>,
    },
    {
      title: t('admin.models.col.context'),
      dataIndex: 'context_window',
      width: 100,
      render: (v: number) => <span className="wg-num">{formatContext(v) ?? '—'}</span>,
    },
    {
      title: t('admin.models.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string, r: ServiceModel) =>
        v === 'active' || v === 'paused' ? (
          <Switch aria-label={t('admin.models.ariaToggleListing')} size="small" checked={v === 'active'} onChange={() => toggleModel(r)} />
        ) : (
          statusTag(v, t)
        ),
    },
    {
      title: t('common.actions'),
      width: 120,
      render: (_: unknown, r: ServiceModel) => (
        <Button theme="borderless" size="small" onClick={() => openPricing(modelName(r))}>
          {t('admin.models.action.pricing')}
        </Button>
      ),
    },
  ];

  const awsColumns = [
    {
      title: t('admin.models.col.region'),
      dataIndex: 'provider',
      width: 140,
      render: (v: string) => <span className="wg-mono-sm">{regionOf(v)}</span>,
    },
    {
      title: t('admin.models.col.name'),
      render: (_: unknown, r: ServiceModel) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span className="wg-cell-strong">{modelName(r)}</span>
          <span className="wg-cell-dim wg-mono-sm">{r.provider}</span>
        </div>
      ),
    },
    {
      title: t('admin.models.col.type'),
      dataIndex: 'model_type',
      width: 100,
      render: (v: string) => <Tag color="blue">{v || 'text'}</Tag>,
    },
    {
      title: t('admin.models.col.context'),
      dataIndex: 'context_window',
      width: 100,
      render: (v: number) => <span className="wg-num">{formatContext(v) ?? '—'}</span>,
    },
    {
      title: t('admin.models.col.inputPrice'),
      dataIndex: 'input_price',
      width: 110,
      render: (v: number) => <span className="wg-num">{formatPrice(v) ?? '—'}</span>,
    },
    {
      title: t('admin.models.col.outputPrice'),
      dataIndex: 'output_price',
      width: 110,
      render: (v: number) => <span className="wg-num">{formatPrice(v) ?? '—'}</span>,
    },
    {
      title: t('admin.models.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string) => statusTag(v, t),
    },
    {
      title: t('common.actions'),
      width: 120,
      render: (_: unknown, r: ServiceModel) => (
        <Button theme="borderless" size="small" onClick={() => openPricing(modelName(r))}>
          {t('admin.models.action.pricing')}
        </Button>
      ),
    },
  ];

  const ruleColumns = [
    {
      title: t('admin.models.col.model'),
      render: (_: unknown, r: PricingRule) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span className="wg-cell-strong">{r.api_model_name || r.product_id || '—'}</span>
          <span className="wg-cell-dim">
            {r.provider}
            {r.region ? ` · ${r.region}` : ''}
          </span>
        </div>
      ),
    },
    {
      title: t('admin.models.col.billingMode'),
      dataIndex: 'billing_mode',
      width: 120,
      render: (v: string) => <Tag color="violet">{v || 'flat'}</Tag>,
    },
    {
      title: t('admin.models.col.inputPrice'),
      width: 110,
      render: (_: unknown, r: PricingRule) => (
        <span className="wg-num">{formatPrice(r.prices?.input) ?? '—'}</span>
      ),
    },
    {
      title: t('admin.models.col.outputPrice'),
      width: 110,
      render: (_: unknown, r: PricingRule) => (
        <span className="wg-num">{formatPrice(r.prices?.output) ?? '—'}</span>
      ),
    },
    {
      title: t('admin.models.col.cachePrice'),
      width: 110,
      render: (_: unknown, r: PricingRule) => (
        <span className="wg-num">{formatPrice(r.prices?.cache_hit) ?? '—'}</span>
      ),
    },
    {
      title: t('admin.models.col.priority'),
      dataIndex: 'priority',
      width: 90,
      render: (v: number) => <span className="wg-num">{v ?? 0}</span>,
    },
    {
      title: t('admin.models.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string, r: PricingRule) => (
        <Switch aria-label={t('admin.models.ariaToggleListing')}
          size="small"
          checked={v === 'active'}
          onChange={() =>
            (async () => {
              try {
                await togglePricingRule(r.id);
                Toast.success(t('admin.common.saved'));
                await loadRules();
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
      width: 160,
      render: (_: unknown, r: PricingRule) => (
        <div className="wg-cell-actions">
          <Button theme="borderless" size="small" onClick={() => setEditingRule(r)}>
            {t('admin.models.action.editPricing')}
          </Button>
          <Button
            theme="borderless"
            size="small"
            type="danger"
            onClick={() =>
              dangerConfirm({
                title: t('admin.models.ruleDeleteTitle'),
                content: t('admin.models.ruleDeleteDesc'),
                okText: t('common.delete'),
                cancelText: t('common.cancelAction'),
                onOk: async () => {
                  try {
                    await deletePricingRule(r.id);
                    Toast.success(t('admin.common.deleted'));
                    await loadRules();
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

  return (
    <div className="wg-page">
      <PageHead
        title={t('admin.models.title')}
        extra={
          <Button icon={<IconRefresh />} theme="light" onClick={loadModels}>
            {t('common.refresh')}
          </Button>
        }
      />

      <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as TabKey)}>
        <Tabs.TabPane itemKey="general" tab={t('admin.models.tab.general')}>
          <div className="wg-pro-card wg-filter-card">
            <Input
              style={{ width: 260 }}
              placeholder={t('admin.models.searchPh')}
              value={search}
              onChange={setSearch}
              prefix={<IconSearch />}
              showClear
            />
            <div className="wg-filter-actions">
              <Button
                icon={<IconUpload />}
                theme="light"
                loading={bulk === 'import'}
                onClick={() => runBulk('import')}
              >
                {t('admin.models.import')}
              </Button>
              <Button
                theme="light"
                loading={bulk === 'sync'}
                onClick={() =>
                  dangerConfirm({
                    title: t('admin.models.syncTitle'),
                    content: t('admin.models.syncDesc'),
                    okText: t('admin.models.sync'),
                    cancelText: t('common.cancelAction'),
                    onOk: () => runBulk('sync'),
                  })
                }
              >
                {t('admin.models.sync')}
              </Button>
            </div>
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.models.generalTitle')}</span>
              </div>
            </div>
            <Table
              loading={loading}
              rowKey="id"
              columns={generalColumns}
              dataSource={filteredGeneral}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.models.empty')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="aws" tab={t('admin.models.tab.aws')}>
          <div className="wg-pro-card wg-filter-card">
            <Input
              style={{ width: 260 }}
              placeholder={t('admin.models.searchPh')}
              value={search}
              onChange={setSearch}
              prefix={<IconSearch />}
              showClear
            />
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.models.awsTitle')}</span>
              </div>
            </div>
            <Table
              loading={loading}
              rowKey="id"
              columns={awsColumns}
              dataSource={filteredAws}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.models.awsEmpty')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="pricing" tab={t('admin.models.tab.pricing')}>
          <div className="wg-pro-card wg-filter-card">
            <Input
              style={{ width: 260 }}
              placeholder={t('admin.models.ruleSearchPh')}
              value={ruleSearch}
              onChange={setRuleSearch}
              prefix={<IconSearch />}
              showClear
            />
            <div className="wg-filter-actions">
              <Button icon={<IconExport />} theme="light" onClick={() => exportPricingCsv().catch((e) => Toast.error((e as Error).message))}>
                {t('common.export')}
              </Button>
              <Button
                icon={<IconUpload />}
                type="primary"
                theme="solid"
                onClick={() => setCreatingRule(true)}
              >
                {t('admin.models.ruleCreate')}
              </Button>
            </div>
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.models.ruleTitle')}</span>
              </div>
              <Button icon={<IconRefresh />} theme="light" onClick={loadRules}>
                {t('common.refresh')}
              </Button>
            </div>
            <Table
              loading={rulesLoading}
              rowKey="id"
              columns={ruleColumns}
              dataSource={filteredRules}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.models.ruleEmpty')} />}
            />
          </div>
        </Tabs.TabPane>
      </Tabs>

      {(editingRule || creatingRule) && (
        <PricingDrawer
          target={editingRule ?? undefined}
          providers={providers}
          onClose={() => {
            setEditingRule(null);
            setCreatingRule(false);
          }}
          onSaved={loadRules}
        />
      )}
    </div>
  );
}

function PricingDrawer({
  target,
  providers,
  onClose,
  onSaved,
}: {
  target?: PricingRule;
  providers: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const editing = Boolean(target);

  const [apiModelName, setApiModelName] = useState(target?.api_model_name ?? '');
  const [productId, setProductId] = useState(target?.product_id ?? '');
  const [provider, setProvider] = useState(target?.provider ?? providers[0] ?? '');
  const [region, setRegion] = useState(target?.region ?? '');
  const [billingMode, setBillingMode] = useState(target?.billing_mode ?? 'flat');
  const [input, setInput] = useState<number>(target?.prices?.input ?? 0);
  const [output, setOutput] = useState<number>(target?.prices?.output ?? 0);
  const [cacheHit, setCacheHit] = useState<number>(target?.prices?.cache_hit ?? 0);
  const [cacheWrite, setCacheWrite] = useState<number>(target?.prices?.cache_write ?? 0);
  const [reasoning, setReasoning] = useState<number>(target?.prices?.reasoning ?? 0);
  const [priority, setPriority] = useState<number>(target?.priority ?? 0);
  const [ruleName, setRuleName] = useState(target?.rule_name ?? '');
  const [note, setNote] = useState(target?.note ?? '');
  const [active, setActive] = useState((target?.status ?? 'active') === 'active');
  const [saving, setSaving] = useState(false);

  const save = async () => {
    if (!provider.trim()) {
      Toast.error(t('admin.models.providerRequired'));
      return;
    }
    if (!apiModelName.trim() && !productId.trim()) {
      Toast.error(t('admin.models.modelRequired'));
      return;
    }
    setSaving(true);
    const payload = {
      provider: provider.trim(),
      billing_mode: billingMode,
      region,
      product_id: productId.trim(),
      api_model_name: apiModelName.trim(),
      prices: {
        input,
        output,
        cache_hit: cacheHit,
        cache_write: cacheWrite,
        reasoning,
      },
      priority,
      status: active ? 'active' : 'inactive',
      rule_name: ruleName,
      note,
    };
    try {
      if (editing && target) await updatePricingRule(target.id, payload);
      else await createPricingRule(payload);
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
      title={t(editing ? 'admin.models.editPricing' : 'admin.models.createPricing')}
      visible
      onCancel={onClose}
      width={640}
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
            {t('admin.models.field.model')}
            <span className="wg-form-required">*</span>
          </label>
          <Input
            value={apiModelName}
            onChange={setApiModelName}
            placeholder={t('admin.models.modelPh')}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.models.field.productId')}</label>
          <Input value={productId} onChange={setProductId} placeholder="gpt-4o" />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">
            {t('admin.models.field.provider')}
            <span className="wg-form-required">*</span>
          </label>
          <Select
            filter
            value={provider || undefined}
            onChange={(v) => setProvider((v as string) ?? '')}
            optionList={providers.map((p) => ({ value: p, label: p }))}
            placeholder={t('admin.models.field.provider')}
            emptyContent={t('common.emptyData')}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.models.field.region')}</label>
          <Input value={region} onChange={setRegion} placeholder="us-east-1" />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.models.field.billingMode')}</label>
          <Select
            value={billingMode}
            onChange={(v) => setBillingMode((v as string) ?? 'flat')}
            optionList={BILLING_MODES.map((m) => ({
              value: m,
              label: t(`admin.models.mode.${m}`),
            }))}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.models.field.prices')}</label>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <div className="wg-form-field">
              <span className="wg-cell-dim">{t('admin.models.col.inputPrice')}</span>
              <InputNumber min={0} value={input} onChange={(v) => setInput(typeof v === 'number' ? v : 0)} />
            </div>
            <div className="wg-form-field">
              <span className="wg-cell-dim">{t('admin.models.col.outputPrice')}</span>
              <InputNumber min={0} value={output} onChange={(v) => setOutput(typeof v === 'number' ? v : 0)} />
            </div>
            <div className="wg-form-field">
              <span className="wg-cell-dim">{t('admin.models.field.cacheHit')}</span>
              <InputNumber min={0} value={cacheHit} onChange={(v) => setCacheHit(typeof v === 'number' ? v : 0)} />
            </div>
            <div className="wg-form-field">
              <span className="wg-cell-dim">{t('admin.models.field.cacheWrite')}</span>
              <InputNumber min={0} value={cacheWrite} onChange={(v) => setCacheWrite(typeof v === 'number' ? v : 0)} />
            </div>
            <div className="wg-form-field">
              <span className="wg-cell-dim">{t('admin.models.field.reasoning')}</span>
              <InputNumber min={0} value={reasoning} onChange={(v) => setReasoning(typeof v === 'number' ? v : 0)} />
            </div>
            <div className="wg-form-field">
              <span className="wg-cell-dim">{t('admin.models.col.priority')}</span>
              <InputNumber value={priority} onChange={(v) => setPriority(typeof v === 'number' ? v : 0)} />
            </div>
          </div>
          <span className="wg-cell-dim">{t('admin.models.pricesHint')}</span>
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.models.field.ruleName')}</label>
          <Input value={ruleName} onChange={setRuleName} placeholder={t('admin.models.ruleNamePh')} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.models.field.note')}</label>
          <Input value={note} onChange={setNote} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.models.field.status')}</label>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <Switch aria-label={t('admin.models.ariaToggleListing')} checked={active} onChange={setActive} />
            <span className="wg-cell-dim">
              {active ? t('admin.group.status.active') : t('admin.group.status.inactive')}
            </span>
          </div>
        </div>
      </div>
    </SideSheet>
  );
}
