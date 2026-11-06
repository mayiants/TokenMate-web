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
import { formatNumber, formatUSD } from '../../api/console';
import { listPublicModels, type PublicModel } from '../../api/models';
import {
  createAdminGroup,
  deleteAdminGroup,
  deleteAdminPlan,
  getAdminPlanSubscribers,
  listAdminGroups,
  listAdminPlans,
  normalizeModelList,
  toggleAdminPlan,
  updateAdminGroup,
  updateAdminPlan,
  type AdminGroup,
  type AdminPlan,
} from '../../api/adminGroups';

const GROUP_TYPES = ['general', 'economy', 'subscription', 'enterprise'];

type TabKey = 'groups' | 'plans';

export default function AdminGroups() {
  const { t } = useI18n();
  const [tab, setTab] = useState<TabKey>('groups');

  const [groups, setGroups] = useState<AdminGroup[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState('');

  const [plans, setPlans] = useState<AdminPlan[] | null>(null);
  const [subscribers, setSubscribers] = useState<Record<string, number>>({});
  const [plansLoading, setPlansLoading] = useState(false);

  const [models, setModels] = useState<PublicModel[]>([]);
  const [editGroup, setEditGroup] = useState<AdminGroup | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [editPlan, setEditPlan] = useState<AdminPlan | null>(null);

  const loadGroups = useCallback(async () => {
    setLoading(true);
    try {
      setGroups(await listAdminGroups());
    } catch (e) {
      setGroups([]);
      Toast.error((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  const loadPlans = useCallback(async () => {
    setPlansLoading(true);
    try {
      const [list, subs] = await Promise.all([
        listAdminPlans(),
        getAdminPlanSubscribers().catch(() => ({})),
      ]);
      setPlans(list);
      setSubscribers(subs);
    } catch (e) {
      setPlans([]);
      Toast.error((e as Error).message);
    } finally {
      setPlansLoading(false);
    }
  }, []);

  useEffect(() => {
    loadGroups();
    listPublicModels()
      .then(setModels)
      .catch(() => setModels([]));
  }, [loadGroups]);

  useEffect(() => {
    if (tab === 'plans' && plans === null) loadPlans();
  }, [tab, plans, loadPlans]);

  const filteredGroups = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return groups ?? [];
    return (groups ?? []).filter(
      (g) =>
        g.name.toLowerCase().includes(q) ||
        (g.enterprise_name ?? '').toLowerCase().includes(q) ||
        (g.description ?? '').toLowerCase().includes(q)
    );
  }, [groups, search]);

  const modelOptions = useMemo(() => {
    const seen = new Set<string>();
    const opts: { value: string; label: string }[] = [];
    for (const m of models) {
      const name = m.api_model_name;
      if (!name || seen.has(name)) continue;
      seen.add(name);
      opts.push({ value: name, label: name });
    }
    return opts.sort((a, b) => a.label.localeCompare(b.label));
  }, [models]);

  const groupTypeLabel = (type?: string) => {
    const key = `admin.group.type.${type ?? 'general'}`;
    const label = t(key);
    return label === key ? type ?? '—' : label;
  };

  const groupTypeTag = (type?: string) => {
    const colors: Record<string, 'blue' | 'green' | 'violet' | 'orange'> = {
      general: 'blue',
      economy: 'green',
      subscription: 'violet',
      enterprise: 'orange',
    };
    return <Tag color={colors[type ?? 'general'] ?? 'grey'}>{groupTypeLabel(type)}</Tag>;
  };

  const statusTag = (status?: string) =>
    status === 'inactive' ? (
      <Tag color="red">{t('admin.group.status.inactive')}</Tag>
    ) : (
      <Tag color="green">{t('admin.group.status.active')}</Tag>
    );

  const run = async (fn: () => Promise<unknown>, okMsg: string) => {
    try {
      await fn();
      Toast.success(okMsg);
      await loadGroups();
    } catch (e) {
      Toast.error((e as Error).message);
    }
  };

  const groupColumns = [
    {
      title: t('admin.group.col.name'),
      dataIndex: 'name',
      render: (v: string, r: AdminGroup) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span className="wg-cell-strong">{v}</span>
          {r.enterprise_name ? <span className="wg-cell-dim">{r.enterprise_name}</span> : null}
        </div>
      ),
    },
    {
      title: t('admin.group.col.type'),
      dataIndex: 'group_type',
      width: 120,
      render: (v: string) => groupTypeTag(v),
    },
    {
      title: t('admin.group.col.limits'),
      width: 190,
      render: (_: unknown, r: AdminGroup) => (
        <span className="wg-num">
          {formatNumber(r.rate_limit_rpm ?? 0)} / {formatNumber(r.rate_limit_tpm ?? 0)}
        </span>
      ),
    },
    {
      title: t('admin.group.col.concurrent'),
      dataIndex: 'concurrent_limit',
      width: 90,
      render: (v: number) => <span className="wg-num">{formatNumber(v ?? 0)}</span>,
    },
    {
      title: t('admin.group.col.models'),
      width: 90,
      render: (_: unknown, r: AdminGroup) => (
        <span className="wg-num">{normalizeModelList(r.models).length}</span>
      ),
    },
    {
      title: t('admin.group.col.members'),
      dataIndex: 'member_count',
      width: 90,
      render: (v: number) => <span className="wg-num">{formatNumber(v ?? 0)}</span>,
    },
    {
      title: t('admin.group.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string) => statusTag(v),
    },
    {
      title: t('common.actions'),
      width: 150,
      render: (_: unknown, r: AdminGroup) => (
        <div className="wg-cell-actions">
          <Button theme="borderless" size="small" onClick={() => setEditGroup(r)}>
            {t('common.edit')}
          </Button>
          <Button
            theme="borderless"
            size="small"
            type="danger"
            onClick={() =>
              dangerConfirm({
                title: t('admin.group.deleteTitle'),
                content: t('admin.group.deleteDesc'),
                okText: t('common.delete'),
                cancelText: t('common.cancelAction'),
                onOk: () => run(() => deleteAdminGroup(r.id), t('admin.common.deleted')),
              })
            }
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
  ];

  const planColumns = [
    {
      title: t('admin.plan.col.name'),
      dataIndex: 'name',
      render: (v: string, r: AdminPlan) => (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span className="wg-cell-strong">{v}</span>
          {r.description ? <span className="wg-cell-dim">{r.description}</span> : null}
        </div>
      ),
    },
    {
      title: t('admin.plan.col.price'),
      dataIndex: 'price',
      width: 120,
      render: (v: number) => <span className="wg-num">{formatUSD((v ?? 0) / 1_000_000)}</span>,
    },
    {
      title: t('admin.plan.col.period'),
      dataIndex: 'duration_days',
      width: 100,
      render: (v: number) => `${formatNumber(v ?? 0)} ${t('common.days')}`,
    },
    {
      title: t('admin.plan.col.quota'),
      dataIndex: 'quota',
      width: 140,
      render: (v: number) => <span className="wg-num">{formatUSD((v ?? 0) / 1_000_000)}</span>,
    },
    {
      title: t('admin.plan.col.models'),
      width: 100,
      render: (_: unknown, r: AdminPlan) => (
        <span className="wg-num">{normalizeModelList(r.model_permissions).length}</span>
      ),
    },
    {
      title: t('admin.plan.col.subscribers'),
      width: 100,
      render: (_: unknown, r: AdminPlan) => (
        <span className="wg-num">{formatNumber(subscribers[r.name] ?? 0)}</span>
      ),
    },
    {
      title: t('admin.plan.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string, r: AdminPlan) => (
        <Switch
          size="small"
          checked={v === 'active'}
          onChange={() =>
            (async () => {
              try {
                await toggleAdminPlan(r.id);
                Toast.success(t('admin.common.saved'));
                await loadPlans();
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
      width: 150,
      render: (_: unknown, r: AdminPlan) => (
        <div className="wg-cell-actions">
          <Button theme="borderless" size="small" onClick={() => setEditPlan(r)}>
            {t('common.edit')}
          </Button>
          <Button
            theme="borderless"
            size="small"
            type="danger"
            onClick={() =>
              dangerConfirm({
                title: t('admin.plan.deleteTitle'),
                content: t('admin.plan.deleteDesc'),
                okText: t('common.delete'),
                cancelText: t('common.cancelAction'),
                onOk: async () => {
                  try {
                    await deleteAdminPlan(r.id);
                    Toast.success(t('admin.common.deleted'));
                    await loadPlans();
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
      <PageHead title={t('admin.group.title')} />

      <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as TabKey)}>
        <Tabs.TabPane itemKey="groups" tab={t('admin.group.tab.groups')}>
          <div className="wg-pro-card wg-filter-card">
            <Input
              style={{ width: 240 }}
              placeholder={t('admin.group.searchPh')}
              value={search}
              onChange={setSearch}
              prefix={<IconSearch />}
              showClear
            />
            <div className="wg-filter-actions">
              <Button icon={<IconRefresh />} theme="light" onClick={loadGroups}>
                {t('common.refresh')}
              </Button>
            </div>
          </div>

          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.group.tableTitle')}</span>
              </div>
              <Button icon={<IconPlus />} type="primary" theme="solid" onClick={() => setCreateOpen(true)}>
                {t('admin.group.create')}
              </Button>
            </div>
            <Table
              loading={loading}
              rowKey="id"
              columns={groupColumns}
              dataSource={filteredGroups}
              pagination={{ pageSize: 10, showTotal: true }}
              tableLayout="fixed"
              empty={<Empty description={t('admin.group.empty')} />}
            />
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="plans" tab={t('admin.group.tab.plans')}>
          <div className="wg-pro-card wg-table-card">
            <div className="wg-table-toolbar">
              <div className="wg-table-toolbar-left">
                <span className="wg-pro-card-title">{t('admin.plan.tableTitle')}</span>
              </div>
              <Button icon={<IconRefresh />} theme="light" onClick={loadPlans}>
                {t('common.refresh')}
              </Button>
            </div>
            <Table
              loading={plansLoading}
              rowKey="id"
              columns={planColumns}
              dataSource={plans ?? []}
              pagination={false}
              tableLayout="fixed"
              empty={<Empty description={t('admin.plan.empty')} />}
            />
          </div>
        </Tabs.TabPane>
      </Tabs>

      {createOpen && (
        <GroupDrawer
          modelOptions={modelOptions}
          onClose={() => setCreateOpen(false)}
          onSaved={loadGroups}
        />
      )}

      {editGroup && (
        <GroupDrawer
          target={editGroup}
          modelOptions={modelOptions}
          onClose={() => setEditGroup(null)}
          onSaved={loadGroups}
        />
      )}

      {editPlan && (
        <PlanDrawer
          target={editPlan}
          modelOptions={modelOptions}
          onClose={() => setEditPlan(null)}
          onSaved={loadPlans}
        />
      )}
    </div>
  );
}

interface ModelOption {
  value: string;
  label: string;
}

function GroupDrawer({
  target,
  modelOptions,
  onClose,
  onSaved,
}: {
  target?: AdminGroup;
  modelOptions: ModelOption[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const editing = Boolean(target);

  const [name, setName] = useState(target?.name ?? '');
  const [description, setDescription] = useState(target?.description ?? '');
  const [groupType, setGroupType] = useState(target?.group_type ?? 'general');
  const [rpm, setRpm] = useState<number>(target?.rate_limit_rpm ?? 0);
  const [tpm, setTpm] = useState<number>(target?.rate_limit_tpm ?? 0);
  const [concurrent, setConcurrent] = useState<number>(target?.concurrent_limit ?? 10);
  const [modelList, setModelList] = useState<string[]>(normalizeModelList(target?.models));
  const [active, setActive] = useState((target?.status ?? 'active') !== 'inactive');
  const [saving, setSaving] = useState(false);

  const save = async () => {
    if (!name.trim()) {
      Toast.error(t('admin.group.nameRequired'));
      return;
    }
    setSaving(true);
    try {
      if (editing && target) {
        await updateAdminGroup(target.id, {
          name: name.trim(),
          description,
          group_type: groupType,
          rpm,
          tpm,
          concurrent_limit: concurrent,
          models: modelList,
          status: active ? 'active' : 'inactive',
        });
      } else {
        await createAdminGroup({
          name: name.trim(),
          description,
          group_type: groupType,
          rate_limit_rpm: rpm,
          rate_limit_tpm: tpm,
          concurrent_limit: concurrent,
          models: modelList,
        });
      }
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
      title={t(editing ? 'admin.group.editTitle' : 'admin.group.createTitle')}
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
            {t('admin.group.field.name')}
            <span className="wg-form-required">*</span>
          </label>
          <Input value={name} onChange={setName} placeholder={t('admin.group.namePh')} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.group.field.desc')}</label>
          <Input value={description} onChange={setDescription} placeholder={t('admin.group.descPh')} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.group.field.type')}</label>
          <Select
            value={groupType}
            onChange={(v) => setGroupType((v as string) ?? 'general')}
            optionList={GROUP_TYPES.map((type) => ({
              value: type,
              label: t(`admin.group.type.${type}`),
            }))}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.group.field.limits')}</label>
          <div style={{ display: 'flex', gap: 12 }}>
            <div className="wg-form-field" style={{ flex: 1 }}>
              <span className="wg-cell-dim">{t('admin.group.field.rpm')}</span>
              <InputNumber
                min={0}
                value={rpm}
                onChange={(v) => setRpm(typeof v === 'number' ? v : 0)}
              />
            </div>
            <div className="wg-form-field" style={{ flex: 1 }}>
              <span className="wg-cell-dim">{t('admin.group.field.tpm')}</span>
              <InputNumber
                min={0}
                value={tpm}
                onChange={(v) => setTpm(typeof v === 'number' ? v : 0)}
              />
            </div>
          </div>
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.group.field.concurrent')}</label>
          <InputNumber
            style={{ width: 200 }}
            min={0}
            value={concurrent}
            onChange={(v) => setConcurrent(typeof v === 'number' ? v : 0)}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.group.field.models')}</label>
          <Select
            multiple
            filter
            value={modelList}
            onChange={(v) => setModelList((v as string[]) ?? [])}
            optionList={modelOptions}
            placeholder={t('admin.group.modelsPh')}
            maxTagCount={8}
            style={{ width: '100%' }}
            emptyContent={t('common.emptyData')}
          />
          <span className="wg-cell-dim">{t('admin.group.modelsHint')}</span>
        </div>

        {editing && (
          <div className="wg-form-field">
            <label className="wg-form-label">{t('admin.group.field.status')}</label>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <Switch checked={active} onChange={setActive} />
              <span className="wg-cell-dim">
                {active ? t('admin.group.status.active') : t('admin.group.status.inactive')}
              </span>
            </div>
            <span className="wg-cell-dim">{t('admin.group.statusHint')}</span>
          </div>
        )}
      </div>
    </SideSheet>
  );
}

function PlanDrawer({
  target,
  modelOptions,
  onClose,
  onSaved,
}: {
  target: AdminPlan;
  modelOptions: ModelOption[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(target.name);
  const [description, setDescription] = useState(target.description ?? '');
  const [price, setPrice] = useState<number>((target.price ?? 0) / 1_000_000);
  const [quota, setQuota] = useState<number>((target.quota ?? 0) / 1_000_000);
  const [days, setDays] = useState<number>(target.duration_days ?? 30);
  const [modelList, setModelList] = useState<string[]>(
    normalizeModelList(target.model_permissions)
  );
  const [saving, setSaving] = useState(false);

  const save = async () => {
    if (!name.trim()) {
      Toast.error(t('admin.plan.nameRequired'));
      return;
    }
    setSaving(true);
    try {
      await updateAdminPlan(target.id, {
        name: name.trim(),
        description,
        price: Math.round(price * 1_000_000),
        quota: Math.round(quota * 1_000_000),
        duration_days: days,
        model_permissions: modelList,
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
      title={t('admin.plan.editTitle')}
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
            {t('admin.plan.field.name')}
            <span className="wg-form-required">*</span>
          </label>
          <Input value={name} onChange={setName} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.plan.field.desc')}</label>
          <Input value={description} onChange={setDescription} />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.plan.field.price')}</label>
          <InputNumber
            min={0}
            precision={2}
            value={price}
            onChange={(v) => setPrice(typeof v === 'number' ? v : 0)}
            suffix="USD"
            style={{ width: 220 }}
          />
          <span className="wg-cell-dim">{t('admin.plan.priceHint')}</span>
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.plan.field.quota')}</label>
          <InputNumber
            min={0}
            precision={2}
            value={quota}
            onChange={(v) => setQuota(typeof v === 'number' ? v : 0)}
            suffix="USD"
            style={{ width: 220 }}
          />
          <span className="wg-cell-dim">{t('admin.plan.quotaHint')}</span>
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.plan.field.period')}</label>
          <InputNumber
            min={1}
            value={days}
            onChange={(v) => setDays(typeof v === 'number' ? v : 30)}
            suffix={t('common.days')}
            style={{ width: 220 }}
          />
        </div>

        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.plan.field.models')}</label>
          <Select
            multiple
            filter
            value={modelList}
            onChange={(v) => setModelList((v as string[]) ?? [])}
            optionList={modelOptions}
            placeholder={t('admin.group.modelsPh')}
            maxTagCount={8}
            style={{ width: '100%' }}
            emptyContent={t('common.emptyData')}
          />
          <span className="wg-cell-dim">{t('admin.plan.modelsHint')}</span>
        </div>
      </div>
    </SideSheet>
  );
}
