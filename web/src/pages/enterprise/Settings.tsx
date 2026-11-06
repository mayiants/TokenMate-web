import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Card,
  Empty,
  Form,
  Input,
  Table,
  Tag,
  Toast,
} from '@douyinfe/semi-ui';
import type { FormApi } from '@douyinfe/semi-ui/lib/es/form';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { useEnterprise } from './EntLayout';
import DrawerSections from '../../components/DrawerSections';
import {
  listMyChangeRequests,
  submitChangeRequest,
  updateEnterpriseProfile,
  type ChangeRequest,
} from '../../api/enterprise';

export default function EntSettings() {
  const { t } = useI18n();
  const { info, isAdmin, reload } = useEnterprise();
  const [profileApi, setProfileApi] = useState<FormApi<Record<string, unknown>> | null>(null);
  const [crApi, setCrApi] = useState<FormApi<Record<string, unknown>> | null>(null);
  const [requests, setRequests] = useState<ChangeRequest[] | null>(null);
  const [savingProfile, setSavingProfile] = useState(false);
  const [sendingCr, setSendingCr] = useState(false);

  const loadRequests = useCallback(async () => {
    try {
      setRequests(await listMyChangeRequests());
    } catch {
      setRequests([]);
    }
  }, []);

  useEffect(() => {
    loadRequests();
  }, [loadRequests]);

  return (
    <div className="wg-console-stack">
      <PageHead title={t('ent.nav.settings')} />
      <Card bordered title={t('ent.nav.settings')}>
        <DrawerSections
          sections={[
            {
              key: 'profile',
              title: t('ent.settings.profile'),
              desc: t('ent.settings.profileDesc'),
              tip: t('ent.settings.profileTip'),
              children: (
                <Form
                  layout="vertical"
                  getFormApi={(api) => setProfileApi(api)}
                  initValues={{ name: info.name, contact_email: info.contact_email ?? '' }}
                >
                  <Form.Input field="name" label={t('ent.settings.name')} disabled={!isAdmin} />
                  <Form.Input field="contact_email" label={t('ent.settings.contactEmail')} disabled={!isAdmin} />
                  {isAdmin && (
                    <Button
                      type="primary"
                      theme="solid"
                      loading={savingProfile}
                      onClick={async () => {
                        const v = (profileApi?.getValues() ?? {}) as {
                          name?: string;
                          contact_email?: string;
                        };
                        setSavingProfile(true);
                        try {
                          await updateEnterpriseProfile({
                            name: v.name,
                            contact_email: v.contact_email,
                          });
                          Toast.success(t('ent.settings.saved'));
                          await reload();
                        } catch (e) {
                          Toast.error((e as Error).message);
                        } finally {
                          setSavingProfile(false);
                        }
                      }}
                    >
                      {t('common.save')}
                    </Button>
                  )}
                </Form>
              ),
            },
            {
              key: 'change-request',
              title: t('ent.settings.changeRequest'),
              desc: t('ent.settings.changeRequestDesc'),
              children: (
                <div className="wg-field-grid">
                  <Form layout="vertical" getFormApi={(api) => setCrApi(api)}>
                    <Form.Select field="type" label={t('ent.settings.requestType')} rules={[{ required: true }]}>
                      <Form.Select.Option value="profile">
                        {t('ent.settings.requestTypeProfile')}
                      </Form.Select.Option>
                      <Form.Select.Option value="quota">
                        {t('ent.settings.requestTypeQuota')}
                      </Form.Select.Option>
                      <Form.Select.Option value="other">
                        {t('ent.settings.requestTypeOther')}
                      </Form.Select.Option>
                    </Form.Select>
                    <Form.TextArea
                      field="detail"
                      label={t('ent.settings.requestDetail')}
                      placeholder={t('ent.settings.requestDetailHint')}
                      rules={[{ required: true }]}
                    />
                  </Form>
                  {isAdmin && (
                    <Button
                      type="primary"
                      loading={sendingCr}
                      onClick={async () => {
                        const v = (crApi?.getValues() ?? {}) as { type?: string; detail?: string };
                        if (!v.type || !v.detail) return;
                        setSendingCr(true);
                        try {
                          await submitChangeRequest({ type: v.type, payload: { detail: v.detail } });
                          Toast.success(t('ent.settings.requestSent'));
                          crApi?.reset();
                          await loadRequests();
                        } catch (e) {
                          Toast.error((e as Error).message);
                        } finally {
                          setSendingCr(false);
                        }
                      }}
                    >
                      {t('ent.settings.submitRequest')}
                    </Button>
                  )}
                  {requests && requests.length > 0 && (
                    <Table
                      rowKey="id"
                      columns={[
                        { title: t('ent.settings.requestType'), dataIndex: 'type' },
                        {
                          title: t('ent.members.status'),
                          dataIndex: 'status',
                          render: (v: string) => (
                            <Tag color={v === 'approved' ? 'green' : v === 'rejected' ? 'red' : 'orange'}>
                              {v ?? '—'}
                            </Tag>
                          ),
                        },
                        { title: t('ent.members.createdAt'), dataIndex: 'created_at' },
                      ]}
                      dataSource={requests}
                      pagination={false}
                    />
                  )}
                </div>
              ),
            },
          ]}
        />
      </Card>
    </div>
  );
}
