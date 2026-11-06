import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Button,
  Empty,
  Input,
  Select,
  SideSheet,
  Spin,
  Switch,
  Table,
  Tag,
  Toast,
} from '@douyinfe/semi-ui';
import { IconPlus, IconRefresh, IconSearch } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import dangerConfirm from '../../components/dangerConfirm';
import { useI18n } from '../../i18n';
import { formatUSD } from '../../api/console';
import {
  createAdminUser,
  deleteAdminUser,
  listAdminUsers,
  sendAdminPasswordReset,
  toggleAdminUserStatus,
  updateAdminUser,
  type AdminUser,
} from '../../api/admin';

const PAGE_SIZE = 10;
const ADMIN_ROLES = ['system_admin', 'super_admin'];

interface Filters {
  email: string;
  name: string;
  role: string;
  status: string;
  enterprise: string;
}

const EMPTY_FILTERS: Filters = { email: '', name: '', role: 'all', status: 'all', enterprise: '' };

function pad(n: number): string {
  return String(n).padStart(2, '0');
}

function fmtTime(iso?: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export default function AdminUsers() {
  const { t } = useI18n();

  const [users, setUsers] = useState<AdminUser[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [draft, setDraft] = useState<Filters>(EMPTY_FILTERS);
  const [applied, setApplied] = useState<Filters>(EMPTY_FILTERS);
  const [editTarget, setEditTarget] = useState<AdminUser | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const load = useCallback(async (f: Filters) => {
    setLoading(true);
    try {
      // The backend search matches email OR name; role/status/enterprise are
      // filtered client-side on the returned page.
      const search = f.email.trim() || f.name.trim();
      setUsers(await listAdminUsers(search || undefined));
    } catch (e) {
      setUsers([]);
      Toast.error((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load(applied);
  }, [applied, load]);

  const filtered = useMemo(() => {
    const list = users ?? [];
    const email = applied.email.trim().toLowerCase();
    const name = applied.name.trim().toLowerCase();
    const ent = applied.enterprise.trim().toLowerCase();
    return list.filter((u) => {
      if (email && !u.email.toLowerCase().includes(email)) return false;
      if (name && !(u.name ?? '').toLowerCase().includes(name)) return false;
      if (applied.role !== 'all' && u.role !== applied.role) return false;
      if (applied.status !== 'all' && (u.status ?? 'active') !== applied.status) return false;
      if (ent && !(u.enterprise_name ?? '').toLowerCase().includes(ent)) return false;
      return true;
    });
  }, [users, applied]);

  const run = async (fn: () => Promise<unknown>, okMsg: string) => {
    try {
      await fn();
      Toast.success(okMsg);
      await load(applied);
    } catch (e) {
      Toast.error((e as Error).message);
    }
  };

  const roleLabel = (role?: string) => {
    const key = `admin.users.role.${role ?? 'user'}`;
    const label = t(key);
    return label === key ? role ?? '—' : label;
  };

  const roleTag = (role?: string) => {
    if (role === 'super_admin') return <Tag color="orange">{roleLabel(role)}</Tag>;
    if (role === 'system_admin') return <Tag color="blue">{roleLabel(role)}</Tag>;
    if (role === 'enterprise_admin') return <Tag color="violet">{roleLabel(role)}</Tag>;
    return <Tag color="grey">{roleLabel(role ?? 'user')}</Tag>;
  };

  const statusTag = (status?: string) => {
    const value = status ?? 'active';
    return value === 'active' ? (
      <Tag color="green">{t('admin.users.status.active')}</Tag>
    ) : value === 'suspended' ? (
      <Tag color="red">{t('admin.users.status.suspended')}</Tag>
    ) : (
      <Tag color="grey">{value}</Tag>
    );
  };

  const columns = [
    {
      title: t('admin.users.col.id'),
      dataIndex: 'id',
      width: 90,
      render: (v: string) => <span className="wg-mono-sm">{v.slice(0, 8)}</span>,
    },
    {
      title: t('admin.users.col.name'),
      dataIndex: 'name',
      render: (v: string, r: AdminUser) => <span className="wg-cell-strong">{v || r.email}</span>,
    },
    { title: t('admin.users.col.email'), dataIndex: 'email' },
    {
      title: t('admin.users.col.role'),
      dataIndex: 'role',
      width: 130,
      render: (v: string) => roleTag(v),
    },
    {
      title: t('admin.users.col.status'),
      dataIndex: 'status',
      width: 100,
      render: (v: string) => statusTag(v),
    },
    {
      title: t('admin.users.col.enterprise'),
      dataIndex: 'enterprise_name',
      render: (v: string) => v || <span className="wg-cell-dim">—</span>,
    },
    {
      title: t('admin.users.col.balance'),
      dataIndex: 'balance',
      width: 120,
      render: (v: number) => <span className="wg-num">{formatUSD((v ?? 0) / 1_000_000)}</span>,
    },
    {
      title: t('admin.users.col.createdAt'),
      dataIndex: 'created_at',
      width: 150,
      render: (v: string) => <span className="wg-cell-dim">{fmtTime(v)}</span>,
    },
    {
      title: t('common.actions'),
      width: 220,
      render: (_: unknown, r: AdminUser) => (
        <div className="wg-cell-actions">
          <Button theme="borderless" size="small" onClick={() => setEditTarget(r)}>
            {t('admin.users.action.edit')}
          </Button>
          <Button
            theme="borderless"
            size="small"
            onClick={() =>
              dangerConfirm({
                title: t('admin.users.resetTitle'),
                content: t('admin.users.resetDesc'),
                okText: t('admin.users.action.reset'),
                cancelText: t('common.cancelAction'),
                onOk: () => run(() => sendAdminPasswordReset(r.id), t('admin.users.resetSent')),
              })
            }
          >
            {t('admin.users.action.reset')}
          </Button>
          <Button
            theme="borderless"
            size="small"
            type="danger"
            onClick={() =>
              dangerConfirm({
                title: t('admin.users.deleteTitle'),
                content: t('admin.users.deleteDesc'),
                okText: t('common.delete'),
                cancelText: t('common.cancelAction'),
                onOk: () => run(() => deleteAdminUser(r.id), t('admin.users.deleted')),
              })
            }
          >
            {t('admin.users.action.delete')}
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div className="wg-page">
      <PageHead title={t('admin.users.title')} />

      <div className="wg-pro-card wg-filter-card">
        <Input
          style={{ width: 200 }}
          placeholder={t('admin.users.filter.emailPh')}
          value={draft.email}
          onChange={(v) => setDraft((d) => ({ ...d, email: v }))}
          showClear
        />
        <Input
          style={{ width: 180 }}
          placeholder={t('admin.users.filter.namePh')}
          value={draft.name}
          onChange={(v) => setDraft((d) => ({ ...d, name: v }))}
          showClear
        />
        <Select
          style={{ width: 150 }}
          value={draft.role}
          onChange={(v) => setDraft((d) => ({ ...d, role: (v as string) ?? 'all' }))}
          optionList={[
            { value: 'all', label: t('admin.common.all') },
            { value: 'user', label: t('admin.users.role.user') },
            { value: 'enterprise_admin', label: t('admin.users.role.enterprise_admin') },
            { value: 'system_admin', label: t('admin.users.role.system_admin') },
            { value: 'super_admin', label: t('admin.users.role.super_admin') },
          ]}
        />
        <Select
          style={{ width: 130 }}
          value={draft.status}
          onChange={(v) => setDraft((d) => ({ ...d, status: (v as string) ?? 'all' }))}
          optionList={[
            { value: 'all', label: t('admin.common.all') },
            { value: 'active', label: t('admin.users.status.active') },
            { value: 'suspended', label: t('admin.users.status.suspended') },
          ]}
        />
        <Input
          style={{ width: 180 }}
          placeholder={t('admin.users.filter.enterprisePh')}
          value={draft.enterprise}
          onChange={(v) => setDraft((d) => ({ ...d, enterprise: v }))}
          showClear
        />
        <div className="wg-filter-actions">
          <Button type="primary" theme="solid" icon={<IconSearch />} onClick={() => setApplied({ ...draft })}>
            {t('common.search')}
          </Button>
          <Button
            theme="light"
            icon={<IconRefresh />}
            onClick={() => {
              setDraft(EMPTY_FILTERS);
              setApplied({ ...EMPTY_FILTERS });
            }}
          >
            {t('common.reset')}
          </Button>
        </div>
      </div>

      <div className="wg-pro-card wg-table-card">
        <div className="wg-table-toolbar">
          <div className="wg-table-toolbar-left">
            <span className="wg-pro-card-title">{t('admin.users.tableTitle')}</span>
          </div>
          <Button icon={<IconPlus />} type="primary" theme="solid" onClick={() => setCreateOpen(true)}>
            {t('admin.users.create')}
          </Button>
        </div>
        <Table
          loading={loading}
          rowKey="id"
          columns={columns}
          dataSource={filtered}
          pagination={{ pageSize: PAGE_SIZE, showTotal: true }}
          tableLayout="fixed"
          empty={<Empty description={t('admin.users.empty')} />}
        />
      </div>

      {editTarget && (
        <EditUserDrawer
          target={editTarget}
          onClose={() => setEditTarget(null)}
          onSaved={() => load(applied)}
        />
      )}

      {createOpen && (
        <CreateUserDrawer onClose={() => setCreateOpen(false)} onCreated={() => load(applied)} />
      )}
    </div>
  );
}

function EditUserDrawer({
  target,
  onClose,
  onSaved,
}: {
  target: AdminUser;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(target.name ?? '');
  const [role, setRole] = useState(target.role ?? 'user');
  const [active, setActive] = useState((target.status ?? 'active') !== 'suspended');
  const [saving, setSaving] = useState(false);

  const adminTarget = ADMIN_ROLES.includes(target.role ?? '');
  const roleChanged = role !== (target.role ?? 'user');
  const statusChanged = active !== ((target.status ?? 'active') !== 'suspended');

  const save = async () => {
    setSaving(true);
    try {
      await updateAdminUser(target.id, {
        name: name.trim() || undefined,
        ...(roleChanged && !adminTarget ? { role, confirm_role: true } : {}),
      });
      if (statusChanged) await toggleAdminUserStatus(target.id);
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
      title={t('admin.users.editTitle')}
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
          <label className="wg-form-label">{t('admin.users.field.email')}</label>
          <Input value={target.email} disabled />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.users.field.name')}</label>
          <Input value={name} onChange={setName} placeholder={t('admin.users.namePh')} />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.users.field.role')}</label>
          <Select
            value={adminTarget ? target.role : role}
            disabled={adminTarget}
            onChange={(v) => setRole((v as string) ?? 'user')}
            optionList={[
              { value: 'user', label: t('admin.users.role.user') },
              { value: 'enterprise_admin', label: t('admin.users.role.enterprise_admin') },
            ]}
          />
          {adminTarget && <span className="wg-cell-dim">{t('admin.users.roleLocked')}</span>}
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.users.field.status')}</label>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <Switch checked={active} onChange={setActive} />
            <span className="wg-cell-dim">
              {active ? t('admin.users.enable') : t('admin.users.disable')}
            </span>
          </div>
          <span className="wg-cell-dim">{t('admin.users.statusHint')}</span>
        </div>
      </div>
    </SideSheet>
  );
}

function CreateUserDrawer({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const { t } = useI18n();
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState('user');
  const [invite, setInvite] = useState(false);
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    if (!email.trim()) {
      Toast.error(t('admin.users.emailRequired'));
      return;
    }
    if (!invite && !password) {
      Toast.error(t('admin.users.createPasswordRequired'));
      return;
    }
    setBusy(true);
    try {
      const finalPassword = invite ? `Wg-${crypto.randomUUID()}` : password;
      const id = await createAdminUser({
        email: email.trim(),
        name: name.trim() || undefined,
        password: finalPassword,
        role,
      });
      if (invite) await sendAdminPasswordReset(id);
      Toast.success(t(invite ? 'admin.users.invited' : 'admin.common.created'));
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
      title={t('admin.users.createTitle')}
      visible
      onCancel={onClose}
      width={600}
      closeOnEsc
      maskClosable={false}
      footer={
        <div className="wg-drawer-footer">
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          <Button type="primary" theme="solid" loading={busy} onClick={submit}>
            {t('admin.users.create')}
          </Button>
        </div>
      }
    >
      <div className="wg-field-grid">
        <div className="wg-form-field">
          <label className="wg-form-label">
            {t('admin.users.field.email')}
            <span className="wg-form-required">*</span>
          </label>
          <Input value={email} onChange={setEmail} placeholder="name@company.com" />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.users.field.name')}</label>
          <Input value={name} onChange={setName} placeholder={t('admin.users.namePh')} />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.users.field.role')}</label>
          <Select
            value={role}
            onChange={(v) => setRole((v as string) ?? 'user')}
            optionList={[
              { value: 'user', label: t('admin.users.role.user') },
              { value: 'enterprise_admin', label: t('admin.users.role.enterprise_admin') },
            ]}
          />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('admin.users.field.invite')}</label>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <Switch checked={invite} onChange={setInvite} />
            <span className="wg-cell-dim">{t('admin.users.field.inviteHint')}</span>
          </div>
        </div>
        {!invite && (
          <div className="wg-form-field">
            <label className="wg-form-label">
              {t('admin.users.field.password')}
              <span className="wg-form-required">*</span>
            </label>
            <Input mode="password" value={password} onChange={setPassword} />
          </div>
        )}
      </div>
    </SideSheet>
  );
}
