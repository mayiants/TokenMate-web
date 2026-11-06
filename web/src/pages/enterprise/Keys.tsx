import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Card,
  Empty,
  Form,
  Modal,
  Select,
  SideSheet,
  Spin,
  Table,
  Tag,
  Toast,
} from '@douyinfe/semi-ui';
import { IconPlus } from '@douyinfe/semi-icons';
import type { FormApi } from '@douyinfe/semi-ui/lib/es/form';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { useEnterprise } from './EntLayout';
import CopyButton from '../../components/CopyButton';
import dangerConfirm from '../../components/dangerConfirm';
import {
  createEnterpriseKey,
  listEnterpriseGroups,
  listEnterpriseKeys,
  listMyEnterpriseMembers,
  reactivateEnterpriseKey,
  reassignEnterpriseKey,
  revokeEnterpriseKey,
  type EnterpriseGroup,
  type EnterpriseKey,
  type EnterpriseMember,
} from '../../api/enterprise';
import { formatDateTime } from '../../api/console';

/** Enterprise keys. Admins additionally see an owner column and can create a
 *  key directly (POST /api/enterprise/keys), reassign, revoke and reactivate;
 *  plain members only see their own rows (backend filters). */
export default function EntKeys() {
  const { t } = useI18n();
  const { isAdmin } = useEnterprise();
  const [keys, setKeys] = useState<EnterpriseKey[] | null>(null);
  const [members, setMembers] = useState<EnterpriseMember[]>([]);
  const [groups, setGroups] = useState<EnterpriseGroup[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [createBusy, setCreateBusy] = useState(false);
  const [formApi, setFormApi] = useState<FormApi<Record<string, unknown>> | null>(null);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [expiresIn, setExpiresIn] = useState<0 | 30 | 90 | 365>(0);
  const [reassignKey, setReassignKey] = useState<EnterpriseKey | null>(null);
  const [reassignTo, setReassignTo] = useState<string | undefined>(undefined);
  const [reassignBusy, setReassignBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setKeys(await listEnterpriseKeys());
    } catch {
      setKeys([]);
    }
  }, []);

  useEffect(() => {
    load();
    if (isAdmin) {
      listMyEnterpriseMembers().then(setMembers).catch(() => setMembers([]));
      listEnterpriseGroups().then(setGroups).catch(() => setGroups([]));
    }
  }, [load, isAdmin]);

  const submitCreate = async () => {
    const v = (formApi?.getValues() ?? {}) as {
      name?: string;
      assigned_to?: string;
      group_id?: string;
      expires_in?: number;
    };
    if (!v.name || !v.assigned_to) return;
    setCreateBusy(true);
    try {
      const res = await createEnterpriseKey({
        name: v.name,
        assigned_to: v.assigned_to,
        group_id: v.group_id,
        expires_in: expiresIn,
      });
      setCreatedKey(res.key);
      await load();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setCreateBusy(false);
    }
  };

  const act = async (fn: () => Promise<unknown>, okMsg: string) => {
    try {
      await fn();
      Toast.success(t(okMsg));
      await load();
    } catch (e) {
      Toast.error((e as Error).message);
    }
  };

  const columns = [
    { title: t('console.keys.name'), dataIndex: 'name', render: (v: string) => v || '—' },
    {
      title: 'Key',
      dataIndex: 'key_prefix',
      render: (v: string) => (
        <span className="wg-mono">
          {v}
          {v && !v.endsWith('…') ? '…' : ''}
        </span>
      ),
    },
    ...(isAdmin
      ? [
          {
            title: t('ent.keys.owner'),
            render: (_: unknown, k: EnterpriseKey) =>
              (k as { user_name?: string }).user_name || (k as { user_email?: string }).user_email || '—',
          },
        ]
      : []),
    {
      title: t('console.keys.status'),
      dataIndex: 'status',
      render: (v: string) =>
        v === 'active' ? (
          <Tag color="green">{t('console.keys.enabled')}</Tag>
        ) : (
          <Tag color="red">{t('console.keys.disabled')}</Tag>
        ),
    },
    {
      title: t('console.keys.models'),
      render: (_: unknown, k: EnterpriseKey) => {
        const models = Array.isArray(k.allowed_models) ? k.allowed_models : [];
        return models.length > 0 ? (
          <Tag color="blue">{models.length}</Tag>
        ) : (
          <Tag color="grey">{t('console.keys.unlimited')}</Tag>
        );
      },
    },
    { title: t('console.keys.createdAt'), dataIndex: 'created_at', render: (v: string) => formatDateTime(v) },
    {
      title: t('console.keys.expiresAt'),
      dataIndex: 'expires_at',
      render: (v: string | null) => (v ? formatDateTime(v) : t('console.keys.neverExpires')),
    },
    ...(isAdmin
      ? [
          {
            title: t('common.actions'),
            render: (_: unknown, k: EnterpriseKey) => (
              <>
                <Button theme="borderless" size="small" onClick={() => setReassignKey(k)}>
                  {t('ent.keys.reassign')}
                </Button>
                {k.status === 'active' ? (
                  <Button
                    theme="borderless"
                    size="small"
                    type="danger"
                    onClick={() =>
                      dangerConfirm({
                        title: t('ent.keys.revoke'),
                        okText: t('ent.keys.revoke'),
                        cancelText: t('common.cancelAction'),
                        onOk: () => act(() => revokeEnterpriseKey(k.id), 'ent.keys.revoked'),
                      })
                    }
                  >
                    {t('ent.keys.revoke')}
                  </Button>
                ) : (
                  <Button
                    theme="borderless"
                    size="small"
                    onClick={() => act(() => reactivateEnterpriseKey(k.id), 'ent.keys.reactivated')}
                  >
                    {t('ent.keys.reactivate')}
                  </Button>
                )}
              </>
            ),
          },
        ]
      : []),
  ];

  return (
    <div className="wg-console-stack">
      <PageHead title={t('ent.nav.keys')} />
      <Card
      bordered
      headerExtraContent={
        isAdmin ? (
          <Button
            icon={<IconPlus />}
            type="primary"
            theme="solid"
            onClick={() => {
              setExpiresIn(0);
              setCreatedKey(null);
              setCreateOpen(true);
            }}
          >
            {t('ent.keys.create')}
          </Button>
        ) : undefined
      }
    >
      {keys === null ? (
        <Spin />
      ) : (
        <Table
          rowKey="id"
          columns={columns}
          dataSource={keys}
          empty={<Empty description={t('ent.keys.empty')} />}
          pagination={{ pageSize: 10, showTotal: true }}
        />
      )}

      {createOpen && (
      <SideSheet
        title={t('ent.keys.create')}
        visible
        onCancel={() => setCreateOpen(false)}
        width={600}
        closeOnEsc
        maskClosable={false}
        footer={
          <div className="wg-drawer-footer">
            <Button onClick={() => setCreateOpen(false)}>{t('common.cancel')}</Button>
            <Button type="primary" theme="solid" loading={createBusy} onClick={submitCreate}>
              {t('common.save')}
            </Button>
          </div>
        }
      >
        {createdKey ? (
          <div className="wg-field-grid">
            <p>{t('ent.keys.createdOnce')}</p>
            <code className="wg-mono" style={{ wordBreak: 'break-all', background: 'rgba(128,128,128,0.08)', padding: 12, borderRadius: 8 }}>
              {createdKey}
            </code>
            <CopyButton text={createdKey} />
            <Button type="primary" theme="solid" onClick={() => { setCreatedKey(null); setCreateOpen(false); }}>
              {t('ent.keys.copyNow')}
            </Button>
          </div>
        ) : (
        <Form layout="vertical" getFormApi={(api) => setFormApi(api)}>
          <Form.Input field="name" label={t('console.keys.name')} rules={[{ required: true }]} />
          <Form.Select field="assigned_to" label={t('ent.keys.assignedTo')} rules={[{ required: true }]}>
            {members.map((m) => (
              <Form.Select.Option key={m.id} value={m.id}>
                {m.name || m.email}
              </Form.Select.Option>
            ))}
          </Form.Select>
          <Form.Select field="group_id" label={t('ent.keys.group')} rules={[{ required: true }]}>
            {groups.map((g) => (
              <Form.Select.Option key={g.id} value={g.id}>
                {g.name}
              </Form.Select.Option>
            ))}
          </Form.Select>
        </Form>
        )}
        {!createdKey && (
        <div className="wg-form-field" style={{ marginTop: 16 }}>
          <span className="wg-form-label">{t('ent.keys.expiry')}</span>
          <div className="wg-form-pills">
            {([0, 30, 90, 365] as const).map((d) => (
              <button
                key={d}
                type="button"
                className={`wg-range-pill${expiresIn === d ? ' active' : ''}`}
                onClick={() => setExpiresIn(d)}
              >
                {t(`keys.drawer.expire.${d}`)}
              </button>
            ))}
          </div>
        </div>
        )}
        <p style={{ color: 'var(--wg-text-2)', fontSize: 12 }}>{t('ent.keys.createDesc')}</p>
      </SideSheet>
      )}

      <Modal
        title={t('ent.keys.reassign')}
        visible={!!reassignKey}
        onCancel={() => setReassignKey(null)}
        footer={
          <>
            <Button onClick={() => setReassignKey(null)}>{t('common.cancel')}</Button>
            <Button
              type="primary"
              theme="solid"
              loading={reassignBusy}
              disabled={!reassignTo}
              onClick={async () => {
                if (!reassignKey || !reassignTo) return;
                setReassignBusy(true);
                try {
                  await reassignEnterpriseKey(reassignKey.id, reassignTo);
                  Toast.success(t('ent.keys.reassigned'));
                  setReassignKey(null);
                  setReassignTo(undefined);
                  await load();
                } catch (e) {
                  Toast.error((e as Error).message);
                } finally {
                  setReassignBusy(false);
                }
              }}
            >
              {t('common.save')}
            </Button>
          </>
        }
        maskClosable={false}
      >
        <Select
          placeholder={t('ent.keys.assignedTo')}
          value={reassignTo}
          onChange={(v) => setReassignTo(v as string)}
          style={{ width: '100%' }}
        >
          {members.map((m) => (
            <Select.Option key={m.id} value={m.id}>
              {m.name || m.email}
            </Select.Option>
          ))}
        </Select>
      </Modal>
    </Card>
    </div>
  );
}
