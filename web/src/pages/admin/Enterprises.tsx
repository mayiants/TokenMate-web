import { useCallback, useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Button,
  Dropdown,
  Empty,
  Input,
  Select,
  SideSheet,
  Spin,
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
  activateAdminEnterprise,
  cancelAdminInvitation,
  createAdminEnterprise,
  deleteAdminEnterprise,
  getAdminEnterprise,
  listAdminChangeRequests,
  listAdminEnterprises,
  listAdminInvitations,
  reviewAdminChangeRequest,
  suspendAdminEnterprise,
  topupAdminEnterprise,
  updateAdminEnterprise,
  verifyAdminEnterprise,
  type AdminChangeRequest,
  type AdminEnterprise,
  type AdminInvitation,
} from '../../api/admin';

type TabKey = 'list' | 'change' | 'invitations';

interface Filters {
  name: string;
  status: string;
  verified: string;
}

const EMPTY_FILTERS: Filters = { name: '', status: 'all', verified: 'all' };

function pad(n: number): string {
  return String(n).padStart(2, '0');
}

function fmtTime(iso?: string | null): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return String(iso);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export default function AdminEnterprises() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const tab = (params.get('tab') as TabKey) || 'list';

  const [enterprises, setEnterprises] = useState<AdminEnterprise[] | null>(null);
  const [verified, setVerified] = useState<Record<string, boolean>>({});
  const [loading, setLoading] = useState(true);
  const [draft, setDraft] = useState<Filters>(EMPTY_FILTERS);
  const [applied, setApplied] = useState<Filters>(EMPTY_FILTERS);

  const [changes, setChanges] = useState<AdminChangeRequest[] | null>(null);
  const [changeStatus, setChangeStatus] = useState('pending');
  const [invitations, setInvitations] = useState<AdminInvitation[] | null>(null);

  const [editTarget, setEditTarget] = useState<AdminEnterprise | null>(null);
  const [topupTarget, setTopupTarget] = useState<AdminEnterprise | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const setTab = (key: string) => {
    const next = new URLSearchParams(params);
    if (key === 'list') next.delete('tab');
    else next.set('tab', key);
    setParams(next, { replace: true });
  };

  const loadEnterprises = useCallback(async () => {
    setLoading(true);
    try {
      const list = await listAdminEnterprises();
      // The list endpoint omits verified_at, so details are fetched in parallel
      // to drive the verification tag (N is small for enterprise customer lists).
      const details = await Promise.all(
        list.map((e) => getAdminEnterprise(e.id).catch(() => null))
      );
      const map: Record<string, boolean> = {};
      for (const d of details) {
        if (d?.enterprise?.id) map[d.enterprise.id] = Boolean(d.enterprise.verified_at);
      }
      setVerified(map);
      setEnterprises(list);
    } catch (e) {
      setEnterprises([]);
      Toast.error((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  const loadChanges = useCallback(async (status: string) => {
    setChanges(null);
    try {
      setChanges(await listAdminChangeRequests(status));
    } catch (e) {
      setChanges([]);
      Toast.error((e as Error).message);
    }
  }, []);

  const loadInvitations = useCallback(async () => {
    setInvitations(null);
    try {
      setInvitations(await listAdminInvitations());
    } catch (e) {
      setInvitations([]);
      Toast.error((e as Error).message);
    }
  }, []);

  useEffect(() => {
    loadEnterprises();
  }, [loadEnterprises]);

  useEffect(() => {
    if (tab === 'change') loadChanges(changeStatus);
    if (tab === 'invitations') loadInvitations();
  }, [tab, changeStatus, loadChanges, loadInvitations]);

  const filtered = useMemo(() => {
    const list = enterprises ?? [];
    const name = applied.name.trim().toLowerCase();
    return list.filter((e) => {
      if (name && !e.name.toLowerCase().includes(name)) return false;
      if (applied.status !== 'all' && (e.status ?? 'active') !== applied.status) return false;
      if (applied.verified === 'yes' && !verified[e.id]) return false;
      if (applied.verified === 'no' && verified[e.id]) return false;
      return true;
    });
  }, [enterprises, applied, verified]);

  const run = async (fn: () => Promise<unknown>, okMsg: string) => {
    try {
      await fn();
      Toast.success(okMsg);
      await loadEnterprises();
    } catch (e) {
      Toast.error((e as Error).message);
    }
  };

  const openEdit = async (e: AdminEnterprise) => {
    try {
      const detail = await getAdminEnterprise(e.id);
      setEditTarget({ ...e, ...detail.enterprise });
    } catch {
      setEditTarget(e);
    }
  };

  const statusTag = (status?: string) => {
    const value = status ?? 'active';
    return value === 'active' ? (
      <Tag color="green">{t('admin.ent.status.active')}</Tag>
    ) : value === 'suspended' ? (
      <Tag color="red">{t('admin.ent.status.suspended')}</Tag>
    ) : (
      <Tag color="grey">{value}</Tag>
    );
  };

  const changeStatusLabel = (status?: string) => {
    const key = `admin.ent.change.status.${status ?? 'pending'}`;
    const label = t(key);
    return label === key ? status ?? '—' : label;
  };

  const invitationStatusLabel = (status?: string) => {
    const key = `admin.ent.inviteStatus.${status ?? 'pending'}`;
    const label = t(key);
    return label === key ? status ?? '—' : label;
  };

  const enterpriseColumns = [
    {
      title: t('admin.ent.col.name'),
      dataIndex: 'name',
      render: (v: string) => <span className="wg-cell-strong">{v}</span>,
    },
    {
      title: t('admin.ent.col.contact'),
      dataIndex: 'contact_email',
      render: (v: string) => v || <span className="wg-cell-dim">—</span>,
    },
    {
      title: t('admin.ent.col.members'),
      dataIndex: 'member_count',
      width: 90,
      render: (v: number) => <span className="wg-num">{formatNumber(v)}</span>,
    },
    {
      title: t('admin.ent.col.pool'),
      width: 150,
      render: (_: unknown, e: AdminEnterprise) => (
        <span className="wg-num">
          {formatNumber(e.quota_used)} / {formatNumber(e.quota_total)}
        </span>
      ),
    },
    {
      title: t('admin.ent.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string) => statusTag(v),
    },
    {
      title: t('admin.ent.col.verified'),
      width: 100,
      render: (_: unknown, e: AdminEnterprise) =>
        verified[e.id] ? (
          <Tag color="green">{t('admin.ent.verified')}</Tag>
        ) : (
          <Tag color="grey">{t('admin.ent.unverified')}</Tag>
        ),
    },
    {
      title: t('admin.ent.col.createdAt'),
      dataIndex: 'created_at',
      width: 150,
      render: (v: string) => <span className="wg-cell-dim">{fmtTime(v)}</span>,
    },
    {
      title: t('common.actions'),
      width: 230,
      render: (_: unknown, e: AdminEnterprise) => {
        const isActive = (e.status ?? 'active') === 'active';
        return (
          <div className="wg-cell-actions">
            <Button theme="borderless" size="small" onClick={() => openEdit(e)}>
              {t('admin.ent.edit')}
            </Button>
            <Button theme="borderless" size="small" onClick={() => setTopupTarget(e)}>
              {t('admin.ent.topup')}
            </Button>
            <Dropdown
              trigger="click"
              position="bottomRight"
              render={
                <Dropdown.Menu>
                  <Dropdown.Item
                    onClick={() =>
                      isActive
                        ? dangerConfirm({
                            title: t('admin.ent.suspendTitle'),
                            content: t('admin.ent.suspendDesc'),
                            okText: t('admin.ent.suspend'),
                            cancelText: t('common.cancelAction'),
                            onOk: () => run(() => suspendAdminEnterprise(e.id), t('admin.common.saved')),
                          })
                        : run(() => activateAdminEnterprise(e.id), t('admin.common.saved'))
                    }
                  >
                    {isActive ? t('admin.ent.suspend') : t('admin.ent.activate')}
                  </Dropdown.Item>
                  {!verified[e.id] && (
                    <Dropdown.Item
                      onClick={() => run(() => verifyAdminEnterprise(e.id), t('admin.ent.verified'))}
                    >
                      {t('admin.ent.verify')}
                    </Dropdown.Item>
                  )}
                  <Dropdown.Divider />
                  <Dropdown.Item
                    type="danger"
                    onClick={() =>
                      dangerConfirm({
                        title: t('admin.ent.deleteTitle'),
                        content: t('admin.ent.deleteDesc'),
                        okText: t('common.delete'),
                        cancelText: t('common.cancelAction'),
                        onOk: () => run(() => deleteAdminEnterprise(e.id), t('admin.common.deleted')),
                      })
                    }
                  >
                    {t('admin.ent.delete')}
                  </Dropdown.Item>
                </Dropdown.Menu>
              }
            >
              <Button theme="borderless" size="small">
                {t('admin.ent.more')}
              </Button>
            </Dropdown>
          </div>
        );
      },
    },
  ];

  const changeColumns = [
    {
      title: t('admin.ent.change.col.enterprise'),
      dataIndex: 'enterprise_name',
      render: (v: string) => <span className="wg-cell-strong">{v || '—'}</span>,
    },
    {
      title: t('admin.ent.change.col.requester'),
      dataIndex: 'requester_email',
      render: (v: string) => v || '—',
    },
    { title: t('admin.ent.change.col.field'), dataIndex: 'field', width: 140 },
    {
      title: t('admin.ent.change.col.change'),
      width: 260,
      render: (_: unknown, r: AdminChangeRequest) => (
        <span className="wg-cell-dim">
          {r.old_value || '—'} → {r.new_value || '—'}
        </span>
      ),
    },
    {
      title: t('admin.ent.change.col.reason'),
      dataIndex: 'reason',
      render: (v: string) => <span className="wg-cell-dim">{v || '—'}</span>,
    },
    {
      title: t('admin.ent.change.col.status'),
      dataIndex: 'status',
      width: 110,
      render: (v: string) => (
        <Tag color={v === 'pending' ? 'orange' : v === 'approved' ? 'green' : 'red'}>
          {changeStatusLabel(v)}
        </Tag>
      ),
    },
    {
      title: t('admin.ent.change.col.createdAt'),
      dataIndex: 'created_at',
      width: 150,
      render: (v: string) => <span className="wg-cell-dim">{fmtTime(v)}</span>,
    },
    {
      title: t('common.actions'),
      width: 160,
      render: (_: unknown, r: AdminChangeRequest) =>
        r.status === 'pending' ? (
          <div className="wg-cell-actions">
            <Button
              theme="borderless"
              size="small"
              onClick={() =>
                reviewAdminChangeRequest(r.id, 'approve')
                  .then(() => {
                    Toast.success(t('admin.ent.change.approved'));
                    loadChanges(changeStatus);
                  })
                  .catch((e: Error) => Toast.error(e.message))
              }
            >
              {t('admin.ent.change.approve')}
            </Button>
            <Button
              theme="borderless"
              size="small"
              type="danger"
              onClick={() =>
                reviewAdminChangeRequest(r.id, 'reject')
                  .then(() => {
                    Toast.success(t('admin.ent.change.rejected'));
                    loadChanges(changeStatus);
                  })
                  .catch((e: Error) => Toast.error(e.message))
              }
            >
              {t('admin.ent.change.reject')}
            </Button>
          </div>
        ) : null,
    },
  ];

  const invitationColumns = [
    { title: t('admin.ent.invitations.col.email'), dataIndex: 'email' },
    {
      title: t('admin.ent.invitations.col.enterprise'),
      dataIndex: 'enterprise_name',
      render: (v: string) => v || '—',
    },
    {
      title: t('admin.ent.invitations.col.role'),
      dataIndex: 'role',
      width: 140,
      render: (v: string) => <Tag color="grey">{v || '—'}</Tag>,
    },
    {
      title: t('admin.ent.invitations.col.status'),
      dataIndex: 'status',
      width: 140,
      render: (v: string) => (
        <Tag color={v === 'pending' ? 'orange' : v === 'accepted' ? 'green' : 'grey'}>
          {invitationStatusLabel(v)}
        </Tag>
      ),
    },
    {
      title: t('admin.ent.invitations.col.expires'),
      dataIndex: 'expires_at',
      width: 150,
      render: (v: string) => <span className="wg-cell-dim">{fmtTime(v)}</span>,
    },
    {
      title: t('admin.ent.invitations.col.createdAt'),
      dataIndex: 'created_at',
      width: 150,
      render: (v: string) => <span className="wg-cell-dim">{fmtTime(v)}</span>,
    },
    {
      title: t('common.actions'),
      width: 100,
      render: (_: unknown, r: AdminInvitation) =>
        r.status === 'pending' ? (
          <Button
            theme="borderless"
            size="small"
            type="danger"
            onClick={() =>
              dangerConfirm({
                title: t('admin.ent.invitations.revokeTitle'),
                content: t('admin.ent.invitations.revokeDesc'),
                okText: t('admin.ent.invitations.revoke'),
                cancelText: t('common.cancelAction'),
                onOk: () =>
                  cancelAdminInvitation(r.id)
                    .then(() => {
                      Toast.success(t('admin.ent.invitations.revoked'));
                      loadInvitations();
                    })
                    .catch((e: Error) => Toast.error(e.message)),
              })
            }
          >
            {t('admin.ent.invitations.revoke')}
          </Button>
        ) : null,
    },
  ];

  return (
    <div className="wg-page">
      <PageHead
        title={t('admin.ent.title')}
        extra={
          <Button icon={<IconPlus />} type="primary" theme="solid" onClick={() => setCreateOpen(true)}>
            {t('admin.ent.create')}
          </Button>
        }
      />

      <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as string)}>
        <Tabs.TabPane itemKey="list" tab={t('admin.ent.tab.list')}>
          <div className="wg-console-stack">
            <div className="wg-pro-card wg-filter-card">
              <Input
                style={{ width: 200 }}
                placeholder={t('admin.ent.filter.namePh')}
                value={draft.name}
                onChange={(v) => setDraft((d) => ({ ...d, name: v }))}
                showClear
              />
              <Select
                style={{ width: 140 }}
                value={draft.status}
                onChange={(v) => setDraft((d) => ({ ...d, status: (v as string) ?? 'all' }))}
                optionList={[
                  { value: 'all', label: t('admin.common.all') },
                  { value: 'active', label: t('admin.ent.status.active') },
                  { value: 'suspended', label: t('admin.ent.status.suspended') },
                ]}
              />
              <Select
                style={{ width: 140 }}
                value={draft.verified}
                onChange={(v) => setDraft((d) => ({ ...d, verified: (v as string) ?? 'all' }))}
                optionList={[
                  { value: 'all', label: t('admin.common.all') },
                  { value: 'yes', label: t('admin.ent.verified') },
                  { value: 'no', label: t('admin.ent.unverified') },
                ]}
              />
              <div className="wg-filter-actions">
                <Button
                  type="primary"
                  theme="solid"
                  icon={<IconSearch />}
                  onClick={() => setApplied({ ...draft })}
                >
                  {t('common.search')}
                </Button>
                <Button
                  theme="light"
                  icon={<IconRefresh />}
                  onClick={() => {
                    setDraft(EMPTY_FILTERS);
                    setApplied({ ...EMPTY_FILTERS });
                    loadEnterprises();
                  }}
                >
                  {t('common.reset')}
                </Button>
              </div>
            </div>

            <div className="wg-pro-card wg-table-card">
              <div className="wg-table-toolbar">
                <div className="wg-table-toolbar-left">
                  <span className="wg-pro-card-title">{t('admin.ent.tableTitle')}</span>
                </div>
                <Button icon={<IconRefresh />} theme="light" onClick={loadEnterprises}>
                  {t('common.refresh')}
                </Button>
              </div>
              <Table
                loading={loading}
                rowKey="id"
                columns={enterpriseColumns}
                dataSource={filtered}
                pagination={{ pageSize: 10, showTotal: true }}
                tableLayout="fixed"
                empty={<Empty description={t('admin.ent.empty')} />}
              />
            </div>
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="change" tab={t('admin.ent.tab.changes')}>
          <div className="wg-console-stack">
            <div className="wg-pro-card wg-table-card">
              <div className="wg-table-toolbar">
                <div className="wg-table-toolbar-left">
                  <span className="wg-pro-card-title">{t('admin.ent.tab.changes')}</span>
                  <Select
                    style={{ width: 140 }}
                    value={changeStatus}
                    onChange={(v) => setChangeStatus((v as string) ?? 'pending')}
                    optionList={[
                      { value: 'pending', label: t('admin.ent.change.status.pending') },
                      { value: 'approved', label: t('admin.ent.change.status.approved') },
                      { value: 'rejected', label: t('admin.ent.change.status.rejected') },
                    ]}
                  />
                </div>
                <Button icon={<IconRefresh />} theme="light" onClick={() => loadChanges(changeStatus)}>
                  {t('common.refresh')}
                </Button>
              </div>
              {changes === null ? (
                <div className="wg-page-loading">
                  <Spin />
                </div>
              ) : (
                <Table
                  rowKey="id"
                  columns={changeColumns}
                  dataSource={changes}
                  pagination={{ pageSize: 10, showTotal: true }}
                  tableLayout="fixed"
                  empty={<Empty description={t('admin.ent.emptyChanges')} />}
                />
              )}
            </div>
          </div>
        </Tabs.TabPane>

        <Tabs.TabPane itemKey="invitations" tab={t('admin.ent.tab.invitations')}>
          <div className="wg-console-stack">
            <div className="wg-pro-card wg-table-card">
              <div className="wg-table-toolbar">
                <div className="wg-table-toolbar-left">
                  <span className="wg-pro-card-title">{t('admin.ent.tab.invitations')}</span>
                </div>
                <Button icon={<IconRefresh />} theme="light" onClick={loadInvitations}>
                  {t('common.refresh')}
                </Button>
              </div>
              {invitations === null ? (
                <div className="wg-page-loading">
                  <Spin />
                </div>
              ) : (
                <Table
                  rowKey="id"
                  columns={invitationColumns}
                  dataSource={invitations}
                  pagination={{ pageSize: 10, showTotal: true }}
                  tableLayout="fixed"
                  empty={<Empty description={t('admin.ent.invitations.empty')} />}
                />
              )}
            </div>
          </div>
        </Tabs.TabPane>
      </Tabs>

      {editTarget && (
        <EditEnterpriseDrawer
          target={editTarget}
          onClose={() => setEditTarget(null)}
          onSaved={loadEnterprises}
        />
      )}

      {topupTarget && (
        <TopupDrawer
          target={topupTarget}
          onClose={() => setTopupTarget(null)}
          onSaved={loadEnterprises}
        />
      )}

      {createOpen && (
        <CreateEnterpriseDrawer onClose={() => setCreateOpen(false)} onCreated={loadEnterprises} />
      )}
    </div>
  );
}

function EditEnterpriseDrawer({
  target,
  onClose,
  onSaved,
}: {
  target: AdminEnterprise;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(target.name);
  const [contact, setContact] = useState(target.contact_email ?? '');
  const [maxMembers, setMaxMembers] = useState(String(target.max_members ?? 50));
  const [saving, setSaving] = useState(false);

  const save = async () => {
    setSaving(true);
    try {
      await updateAdminEnterprise(target.id, {
        name: name.trim() || undefined,
        contact_email: contact.trim() || undefined,
        max_members: Number(maxMembers) || undefined,
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
      title={t('admin.ent.editTitle')}
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
          <label className="wg-form-label">{t('admin.ent.field.name')}</label>
          <Input value={name} onChange={setName} />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ent.field.contact')}</label>
          <Input value={contact} onChange={setContact} placeholder="name@company.com" />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ent.field.maxMembers')}</label>
          <Input value={maxMembers} onChange={setMaxMembers} />
        </div>
      </div>
    </SideSheet>
  );
}

function CreateEnterpriseDrawer({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState('');
  const [contact, setContact] = useState('');
  const [maxMembers, setMaxMembers] = useState('50');
  const [quotaTotal, setQuotaTotal] = useState('');
  const [creditTotal, setCreditTotal] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    if (!name.trim()) {
      Toast.error(t('admin.ent.nameRequired'));
      return;
    }
    setBusy(true);
    try {
      await createAdminEnterprise({
        name: name.trim(),
        contact_email: contact.trim() || undefined,
        max_members: Number(maxMembers) || undefined,
        quota_total: Number(quotaTotal) || undefined,
        credit_total: Number(creditTotal) || undefined,
      });
      Toast.success(t('admin.common.created'));
      onCreated();
      onClose();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <SideSheet
      title={t('admin.ent.createTitle')}
      visible
      onCancel={onClose}
      width={600}
      closeOnEsc
      maskClosable={false}
      footer={
        <div className="wg-drawer-footer">
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          <Button type="primary" theme="solid" loading={busy} onClick={submit}>
            {t('admin.ent.create')}
          </Button>
        </div>
      }
    >
      <div className="wg-field-grid">
        <div className="wg-form-field">
          <label className="wg-form-label">
            {t('admin.ent.field.name')}
            <span className="wg-form-required">*</span>
          </label>
          <Input value={name} onChange={setName} />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ent.field.contact')}</label>
          <Input value={contact} onChange={setContact} placeholder="name@company.com" />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ent.field.maxMembers')}</label>
          <Input value={maxMembers} onChange={setMaxMembers} />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ent.field.quotaTotal')}</label>
          <Input value={quotaTotal} onChange={setQuotaTotal} placeholder="0" />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ent.field.creditTotal')}</label>
          <Input value={creditTotal} onChange={setCreditTotal} placeholder="0" />
        </div>
      </div>
    </SideSheet>
  );
}

function TopupDrawer({
  target,
  onClose,
  onSaved,
}: {
  target: AdminEnterprise;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const [quota, setQuota] = useState('');
  const [credit, setCredit] = useState('');
  const [prepaid, setPrepaid] = useState('');
  const [reason, setReason] = useState('');
  const [issueInvoice, setIssueInvoice] = useState(false);
  const [invoiceTitle, setInvoiceTitle] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    const quotaAmount = Number(quota) || 0;
    const creditAmount = Number(credit) || 0;
    const prepaidAmount = Number(prepaid) || 0;
    if (quotaAmount <= 0 && creditAmount <= 0 && prepaidAmount <= 0) {
      Toast.error(t('admin.ent.topupEmpty'));
      return;
    }
    if (issueInvoice && !invoiceTitle.trim()) {
      Toast.error(t('admin.ent.invoiceTitleRequired'));
      return;
    }
    setBusy(true);
    try {
      await topupAdminEnterprise(target.id, {
        quota_total: quotaAmount > 0 ? quotaAmount : undefined,
        credit_total: creditAmount > 0 ? creditAmount : undefined,
        prepaid_balance: prepaidAmount > 0 ? prepaidAmount : undefined,
        reason: reason.trim() || undefined,
        issue_invoice: issueInvoice || undefined,
        invoice_title: issueInvoice ? invoiceTitle.trim() : undefined,
      });
      Toast.success(t('admin.ent.topupDone'));
      onSaved();
      onClose();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <SideSheet
      title={`${t('admin.ent.topupTitle')} — ${target.name}`}
      visible
      onCancel={onClose}
      width={600}
      closeOnEsc
      maskClosable={false}
      footer={
        <div className="wg-drawer-footer">
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          <Button type="primary" theme="solid" loading={busy} onClick={submit}>
            {t('admin.ent.topup')}
          </Button>
        </div>
      }
    >
      <div className="wg-field-grid">
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ent.topupQuota')}</label>
          <Input value={quota} onChange={setQuota} placeholder="0" />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ent.topupCredit')}</label>
          <Input value={credit} onChange={setCredit} placeholder="0" />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ent.topupPrepaid')}</label>
          <Input value={prepaid} onChange={setPrepaid} placeholder="0" />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ent.topupReason')}</label>
          <Input value={reason} onChange={setReason} placeholder={t('admin.ent.topupReasonPh')} />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.ent.issueInvoice')}</label>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <Switch checked={issueInvoice} onChange={setIssueInvoice} />
            <span className="wg-cell-dim">{t('admin.ent.issueInvoiceHint')}</span>
          </div>
        </div>
        {issueInvoice && (
          <div className="wg-form-field">
            <label className="wg-form-label">
              {t('admin.ent.invoiceTitle')}
              <span className="wg-form-required">*</span>
            </label>
            <Input value={invoiceTitle} onChange={setInvoiceTitle} />
          </div>
        )}
      </div>
    </SideSheet>
  );
}
