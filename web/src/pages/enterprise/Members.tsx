import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Card,
  Empty,
  Form,
  Input,
  SideSheet,
  Spin,
  Table,
  Tabs,
  Tag,
  Toast,
  Typography,
} from '@douyinfe/semi-ui';
import type { FormApi } from '@douyinfe/semi-ui/lib/es/form';
import { IconDelete, IconPlus, IconUserStroked } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { useEnterprise } from './EntLayout';
import dangerConfirm from '../../components/dangerConfirm';
import DrawerSections from '../../components/DrawerSections';
import {
  cancelInvitation,
  inviteEnterpriseMember,
  listEnterpriseInvitations,
  listMyEnterpriseMembers,
  removeEnterpriseMember,
  resendInvitation,
  reviewInvitation,
  setMemberCreditLimit,
  setMemberQuotaLimit,
  setMemberRole,
  toggleMemberStatus,
  topupMemberQuota,
  updateMemberName,
  type EnterpriseInvitation,
  type EnterpriseMember,
} from '../../api/enterprise';
import { getToken } from '../../api/client';

async function downloadCsv(path: string, filename: string) {
  const res = await fetch(path, { headers: { Authorization: `Bearer ${getToken() ?? ''}` } });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

export default function EntMembers() {
  const { t } = useI18n();
  const { isAdmin, user } = useEnterprise();
  const [members, setMembers] = useState<EnterpriseMember[] | null>(null);
  const [invitations, setInvitations] = useState<EnterpriseInvitation[] | null>(null);
  const [tab, setTab] = useState('members');
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteBusy, setInviteBusy] = useState(false);
  const [inviteApi, setInviteApi] = useState<FormApi<Record<string, unknown>> | null>(null);
  const [manageTarget, setManageTarget] = useState<EnterpriseMember | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setMembers(await listMyEnterpriseMembers());
    } catch {
      setMembers([]);
    }
  }, []);
  const loadInvitations = useCallback(async () => {
    try {
      setInvitations(await listEnterpriseInvitations());
    } catch {
      setInvitations([]);
    }
  }, []);

  useEffect(() => {
    load();
    loadInvitations();
  }, [load, loadInvitations]);

  const run = async (fn: () => Promise<unknown>, okMsg: string) => {
    if (busy) return;
    setBusy(true);
    try {
      await fn();
      Toast.success(t(okMsg));
      await load();
      await loadInvitations();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const myId = user?.id;

  const memberColumns = [
    { title: t('ent.members.name'), dataIndex: 'name', render: (v: string, r: EnterpriseMember) => v || r.email },
    { title: t('ent.members.email'), dataIndex: 'email' },
    {
      title: t('ent.members.role'),
      dataIndex: 'role',
      render: (v: string) =>
        v === 'admin' || v === 'enterprise_admin' ? (
          <Tag color="blue">{t('ent.members.roleAdmin')}</Tag>
        ) : (
          <Tag color="grey">{t('ent.members.roleMember')}</Tag>
        ),
    },
    {
      title: t('ent.members.status'),
      dataIndex: 'status',
      render: (v: string) =>
        v === 'active' ? (
          <Tag color="green">{t('ent.members.active')}</Tag>
        ) : (
          <Tag color="red">{t('ent.members.inactive')}</Tag>
        ),
    },
    {
      title: t('ent.members.quota'),
      render: (_: unknown, r: EnterpriseMember) =>
        `${(r as { quota_used?: number }).quota_used ?? 0} / ${r.quota_limit ?? 0}`,
    },
    {
      title: t('ent.members.credit'),
      render: (_: unknown, r: EnterpriseMember) => (r as { credit_limit?: number }).credit_limit ?? '—',
    },
    ...(isAdmin
      ? [
          {
            title: t('common.actions'),
            render: (_: unknown, r: EnterpriseMember) => (
              <>
                <Button theme="borderless" size="small" onClick={() => setManageTarget(r)}>
                  {t('ent.members.manage')}
                </Button>
                <Button
                  theme="borderless"
                  size="small"
                  type="danger"
                  onClick={() =>
                    dangerConfirm({
                      title: t('ent.members.removeConfirm'),
                      okText: t('common.delete'),
                      cancelText: t('common.cancelAction'),
                      onOk: () => run(() => removeEnterpriseMember(r.id), 'ent.members.removed'),
                    })
                  }
                >
                  {t('common.delete')}
                </Button>
              </>
            ),
          },
        ]
      : []),
  ];

  const invitationColumns = [
    { title: t('ent.members.email'), dataIndex: 'email' },
    {
      title: t('ent.members.role'),
      dataIndex: 'role',
      render: (v: string) => <Tag color="grey">{v ?? '—'}</Tag>,
    },
    {
      title: t('ent.members.status'),
      dataIndex: 'status',
      render: (v: string) => <Tag color={v === 'pending' ? 'orange' : 'grey'}>{v ?? '—'}</Tag>,
    },
    { title: t('ent.members.createdAt'), dataIndex: 'created_at' },
    ...(isAdmin
      ? [
          {
            title: t('common.actions'),
            render: (_: unknown, r: EnterpriseInvitation) => (
              <>
                {r.status === 'pending' && (
                  <>
                    <Button
                      theme="borderless"
                      size="small"
                      onClick={() => run(() => reviewInvitation(r.id, 'approve'), 'ent.members.inviteApproved')}
                    >
                      {t('ent.members.approve')}
                    </Button>
                    <Button
                      theme="borderless"
                      size="small"
                      type="danger"
                      onClick={() => run(() => reviewInvitation(r.id, 'reject'), 'ent.members.inviteRejected')}
                    >
                      {t('ent.members.reject')}
                    </Button>
                  </>
                )}
                <Button
                  theme="borderless"
                  size="small"
                  onClick={() => run(() => resendInvitation(r.id), 'ent.members.inviteResent')}
                >
                  {t('ent.members.resend')}
                </Button>
                <Button
                  theme="borderless"
                  size="small"
                  type="danger"
                  onClick={() => run(() => cancelInvitation(r.id), 'ent.members.inviteCancelled')}
                >
                  {t('common.cancel')}
                </Button>
              </>
            ),
          },
        ]
      : []),
  ];

  return (
    <div className="wg-console-stack">
      <PageHead title={t('ent.nav.members')} />
      <Card
        bordered
        headerExtraContent={
          isAdmin ? (
            <>
              <Button
                icon={<IconUserStroked />}
                onClick={() =>
                  downloadCsv('/api/enterprise/members/export', 'members.csv').catch((e) =>
                    Toast.error((e as Error).message)
                  )
                }
              >
                {t('ent.members.export')}
              </Button>
              <Button icon={<IconPlus />} type="primary" theme="solid" onClick={() => setInviteOpen(true)}>
                {t('ent.members.invite')}
              </Button>
            </>
          ) : null
        }
      >
        {members === null ? (
          <Spin />
        ) : (
          <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as string)}>
            <Tabs.TabPane itemKey="members" tab={t('ent.nav.members')}>
              <Table
                rowKey="id"
                columns={memberColumns}
                dataSource={members}
                empty={<Empty description={t('ent.members.empty')} />}
                pagination={{ pageSize: 10, showTotal: true }}
              />
            </Tabs.TabPane>
            <Tabs.TabPane itemKey="invitations" tab={t('ent.members.invitations')}>
              {invitations === null ? (
                <Spin />
              ) : (
                <Table
                  rowKey="id"
                  columns={invitationColumns}
                  dataSource={invitations}
                  empty={<Empty description={t('ent.members.noInvitations')} />}
                  pagination={{ pageSize: 10, showTotal: true }}
                />
              )}
            </Tabs.TabPane>
          </Tabs>
        )}
      </Card>

      {inviteOpen && (
      <SideSheet
        title={t('ent.members.invite')}
        visible
        onCancel={() => setInviteOpen(false)}
        width={600}
        closeOnEsc
        maskClosable={false}
        footer={
          <div className="wg-drawer-footer">
            <Button onClick={() => setInviteOpen(false)}>{t('common.cancel')}</Button>
            <Button
              type="primary"
              theme="solid"
              loading={inviteBusy}
              onClick={async () => {
                const values = (inviteApi?.getValues() ?? {}) as { email?: string; name?: string; role?: string };
                if (!values.email) return;
                setInviteBusy(true);
                try {
                  await inviteEnterpriseMember({
                    email: values.email,
                    name: values.name,
                    role: values.role || 'member',
                  });
                  Toast.success(t('ent.members.invited'));
                  setInviteOpen(false);
                  await loadInvitations();
                } catch (e) {
                  Toast.error((e as Error).message);
                } finally {
                  setInviteBusy(false);
                }
              }}
            >
              {t('ent.members.sendInvite')}
            </Button>
          </div>
        }
      >
        <div id="ent-invite-form">
          <Form layout="vertical" getFormApi={(api) => setInviteApi(api)}>
            <Form.Input
              field="email"
              label={t('auth.email')}
              placeholder="name@company.com"
              rules={[{ required: true, type: 'email' }]}
            />
            <Form.Input field="name" label={t('ent.members.displayName')} placeholder={t('ent.members.displayNameHint')} />
            <Form.Select field="role" label={t('ent.members.role')} initValue="member">
              <Form.Select.Option value="member">{t('ent.members.roleMember')}</Form.Select.Option>
              <Form.Select.Option value="admin">{t('ent.members.roleAdmin')}</Form.Select.Option>
            </Form.Select>
          </Form>
        </div>
      </SideSheet>
      )}

      <MemberManageDrawer
        target={manageTarget}
        isSelf={manageTarget?.id === myId}
        busy={busy}
        onClose={() => setManageTarget(null)}
        onAction={(fn, msg) => run(fn, msg)}
      />
    </div>
  );
}

function MemberManageDrawer({
  target,
  isSelf,
  busy,
  onClose,
  onAction,
}: {
  target: EnterpriseMember | null;
  isSelf: boolean;
  busy: boolean;
  onClose: () => void;
  onAction: (fn: () => Promise<unknown>, okMsg: string) => Promise<void>;
}) {
  const { t } = useI18n();
  const { info } = useEnterprise();
  const poolAvailable = Math.max(0, (info.quota_total ?? 0) - (info.quota_used ?? 0));
  const [nameDraft, setNameDraft] = useState('');
  const [topupAmount, setTopupAmount] = useState('');
  const [quotaLimit, setQuotaLimit] = useState('');
  const [creditLimit, setCreditLimit] = useState('');

  useEffect(() => {
    if (target) {
      setNameDraft(target.name ?? '');
      setQuotaLimit(String((target as { quota_limit?: number }).quota_limit ?? 0));
      setCreditLimit(String((target as { credit_limit?: number }).credit_limit ?? 0));
      setTopupAmount('');
    }
  }, [target]);

  return (
    <>
    {target && (
    <SideSheet
      title={`${t('ent.members.manage')} — ${target.name || target.email}`}
      visible
      onCancel={onClose}
      width={600}
      closeOnEsc
      maskClosable={false}
      footer={
        <div className="wg-drawer-footer">
          <Button onClick={onClose}>{t('common.close')}</Button>
        </div>
      }
    >
      {target && (
        <DrawerSections
          sections={[
            {
              key: 'basic',
              title: t('ent.members.sectionBasic'),
              desc: t('ent.members.sectionBasicDesc'),
              children: (
                <div className="wg-field-grid">
                  <div>
                    <Typography.Text type="secondary">{t('ent.members.displayName')}</Typography.Text>
                    <Input value={nameDraft} onChange={setNameDraft} />
                  </div>
                  <div>
                    <Typography.Text type="secondary">{t('ent.members.role')}</Typography.Text>
                    <div style={{ display: 'flex', gap: 8, marginTop: 6 }}>
                      <Button
                        size="small"
                        disabled={isSelf || busy}
                        onClick={() =>
                          onAction(
                            () => setMemberRole(target.id, 'admin'),
                            'ent.members.roleUpdated'
                          ).then(onClose)
                        }
                      >
                        {t('ent.members.makeAdmin')}
                      </Button>
                      <Button
                        size="small"
                        disabled={isSelf || busy}
                        onClick={() =>
                          onAction(
                            () => setMemberRole(target.id, 'member'),
                            'ent.members.roleUpdated'
                          ).then(onClose)
                        }
                      >
                        {t('ent.members.makeMember')}
                      </Button>
                    </div>
                  </div>
                  <Button
                    type="primary"
                    disabled={busy || nameDraft === (target.name ?? '')}
                    onClick={() =>
                      onAction(() => updateMemberName(target.id, nameDraft), 'ent.members.nameUpdated')
                    }
                  >
                    {t('ent.members.saveName')}
                  </Button>
                </div>
              ),
            },
            {
              key: 'quota',
              title: t('ent.members.sectionQuota'),
              desc: t('ent.members.sectionQuotaDesc'),
              tip: t('ent.members.poolRemaining', { amount: poolAvailable.toLocaleString() }),
              children: (
                <div className="wg-field-grid">
                  <div>
                    <Typography.Text type="secondary">
                      {t('ent.members.topupAmount')}
                    </Typography.Text>
                    <Input value={topupAmount} onChange={setTopupAmount} placeholder="1000000" />
                  </div>
                  <Button
                    disabled={busy || !topupAmount}
                    onClick={() =>
                      onAction(
                        () => topupMemberQuota(target.id, Number(topupAmount)),
                        'ent.members.topupOk'
                      )
                    }
                  >
                    {t('ent.members.topup')}
                  </Button>
                  <div>
                    <Typography.Text type="secondary">{t('ent.members.quotaLimit')}</Typography.Text>
                    <Input value={quotaLimit} onChange={setQuotaLimit} />
                  </div>
                  <Button
                    disabled={busy}
                    onClick={() =>
                      onAction(
                        () => setMemberQuotaLimit(target.id, Number(quotaLimit) || 0),
                        'ent.members.quotaLimitOk'
                      )
                    }
                  >
                    {t('ent.members.saveLimit')}
                  </Button>
                </div>
              ),
            },
            {
              key: 'credit',
              title: t('ent.members.sectionCredit'),
              desc: t('ent.members.sectionCreditDesc'),
              children: (
                <div className="wg-field-grid">
                  <div>
                    <Typography.Text type="secondary">{t('ent.members.creditLimit')}</Typography.Text>
                    <Input value={creditLimit} onChange={setCreditLimit} />
                  </div>
                  <Button
                    disabled={busy}
                    onClick={() =>
                      onAction(
                        () => setMemberCreditLimit(target.id, Number(creditLimit) || 0),
                        'ent.members.creditLimitOk'
                      )
                    }
                  >
                    {t('ent.members.saveLimit')}
                  </Button>
                </div>
              ),
            },
            {
              key: 'status',
              title: t('ent.members.sectionStatus'),
              desc: t('ent.members.sectionStatusDesc'),
              children: (
                <Button
                  type="danger"
                  theme="solid"
                  disabled={isSelf || busy}
                  onClick={() =>
                    dangerConfirm({
                      title: t('ent.members.toggleConfirm'),
                      okText: t('common.ok'),
                      cancelText: t('common.cancelAction'),
                      onOk: () =>
                        onAction(() => toggleMemberStatus(target.id), 'ent.members.statusUpdated').then(onClose),
                    })
                  }
                >
                  {target.status === 'active'
                    ? t('ent.members.deactivate')
                    : t('ent.members.activate')}
                </Button>
              ),
            },
          ]}
        />
      )}
    </SideSheet>
    )}
    </>
  );
}
