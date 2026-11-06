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
  Tag,
  Toast,
} from '@douyinfe/semi-ui';
import { IconPlus } from '@douyinfe/semi-icons';
import dangerConfirm from '../../components/dangerConfirm';
import type { FormApi} from '@douyinfe/semi-ui/lib/es/form';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { useEnterprise } from './EntLayout';
import {
  createEnterpriseGroup,
  deleteEnterpriseGroup,
  getEnterpriseAvailableModels,
  getPlatformGroups,
  listEnterpriseGroups,
  updateEnterpriseGroup,
  type EnterpriseGroup,
  type PlatformGroup,
} from '../../api/enterprise';

export default function EntGroups() {
  const { t } = useI18n();
  const { isAdmin } = useEnterprise();
  const [groups, setGroups] = useState<EnterpriseGroup[] | null>(null);
  const [platformGroups, setPlatformGroups] = useState<PlatformGroup[]>([]);
  const [models, setModels] = useState<string[]>([]);
  const [editing, setEditing] = useState<EnterpriseGroup | 'new' | null>(null);
  const [formApi, setFormApi] = useState<FormApi<Record<string, unknown>> | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setGroups(await listEnterpriseGroups());
    } catch {
      setGroups([]);
    }
  }, []);

  useEffect(() => {
    load();
    getPlatformGroups().then(setPlatformGroups).catch(() => setPlatformGroups([]));
    getEnterpriseAvailableModels().then(setModels).catch(() => setModels([]));
  }, [load]);

  const submit = async () => {
    const v = (formApi?.getValues() ?? {}) as Record<string, unknown>;
    setBusy(true);
    try {
      if (editing === 'new') {
        await createEnterpriseGroup({
          name: v.name as string,
          platform_group_id: v.platform_group_id as string,
          models: (v.models as string[]) ?? [],
          rpm: Number(v.rpm) || 0,
          tpm: Number(v.tpm) || 0,
          concurrent_limit: v.concurrent_limit ? Number(v.concurrent_limit) : undefined,
        });
      } else if (editing) {
        await updateEnterpriseGroup(editing.id, {
          name: v.name as string,
          models: (v.models as string[]) ?? [],
          rpm: Number(v.rpm) || 0,
          tpm: Number(v.tpm) || 0,
          concurrent_limit: v.concurrent_limit ? Number(v.concurrent_limit) : undefined,
        });
      }
      Toast.success(t('ent.groups.saved'));
      setEditing(null);
      await load();
    } catch (e) {
      Toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const columns = [
    { title: t('ent.groups.name'), dataIndex: 'name' },
    {
      title: 'RPM',
      render: (_: unknown, g: EnterpriseGroup) => (g as { rpm?: number }).rpm ?? g.rate_limit_rpm ?? '—',
    },
    {
      title: 'TPM',
      render: (_: unknown, g: EnterpriseGroup) => (g as { tpm?: number }).tpm ?? g.rate_limit_tpm ?? '—',
    },
    {
      title: t('ent.groups.models'),
      render: (_: unknown, g: EnterpriseGroup) =>
        Array.isArray(g.models) ? (
          <Tag color="blue">{g.models.length}</Tag>
        ) : (
          <Tag color="grey">{t('ent.groups.allModels')}</Tag>
        ),
    },
    ...(isAdmin
      ? [
          {
            title: t('common.actions'),
            render: (_: unknown, g: EnterpriseGroup) => (
              <>
                <Button theme="borderless" size="small" onClick={() => setEditing(g)}>
                  {t('common.edit')}
                </Button>
                <Button
                  theme="borderless"
                  size="small"
                  type="danger"
                  onClick={() =>
                    dangerConfirm({
                      title: t('ent.groups.deleteConfirm'),
                      okText: t('common.delete'),
                      cancelText: t('common.cancelAction'),
                      onOk: async () => {
                        try {
                          await deleteEnterpriseGroup(g.id);
                          Toast.success(t('ent.groups.deleted'));
                          await load();
                        } catch (e) {
                          Toast.error((e as Error).message);
                        }
                      },
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

  return (
    <div className="wg-console-stack">
      <PageHead title={t('ent.nav.groups')} />
      <Card
        bordered
        headerExtraContent={
          isAdmin ? (
            <Button icon={<IconPlus />} type="primary" theme="solid" onClick={() => setEditing('new')}>
              {t('ent.groups.create')}
            </Button>
          ) : null
        }
      >
        {groups === null ? (
          <Spin />
        ) : (
          <Table
            rowKey="id"
            columns={columns}
            dataSource={groups}
            empty={<Empty description={t('ent.groups.empty')} />}
            pagination={false}
          />
        )}
      </Card>

      {editing !== null && (
      <SideSheet
        title={editing === 'new' ? t('ent.groups.create') : t('ent.groups.edit')}
        visible
        onCancel={() => setEditing(null)}
        width={600}
        closeOnEsc
        maskClosable={false}
        footer={
          <div className="wg-drawer-footer">
            <Button onClick={() => setEditing(null)}>{t('common.cancel')}</Button>
            <Button type="primary" theme="solid" loading={busy} onClick={submit}>
              {t('common.save')}
            </Button>
          </div>
        }
      >
        <Form
          layout="vertical"
          getFormApi={(api) => setFormApi(api)}
          initValues={
            editing && editing !== 'new'
              ? {
                  name: editing.name,
                  rpm: (editing as { rpm?: number }).rpm ?? editing.rate_limit_rpm ?? 0,
                  tpm: (editing as { tpm?: number }).tpm ?? editing.rate_limit_tpm ?? 0,
                  concurrent_limit: (editing as { concurrent_limit?: number }).concurrent_limit ?? undefined,
                  models: editing.models ?? [],
                }
              : undefined
          }
        >
          <Form.Input field="name" label={t('ent.groups.name')} rules={[{ required: true }]} />
          {editing === 'new' && (
            <Form.Select
              field="platform_group_id"
              label={t('ent.groups.platformGroup')}
              rules={[{ required: true }]}
              placeholder={t('ent.groups.platformGroupHint')}
            >
              {platformGroups.map((g) => (
                <Form.Select.Option key={g.id} value={g.id}>
                  {g.name} · {g.rpm ?? '—'} RPM · {g.model_count ?? 0} {t('ent.groups.models')}
                </Form.Select.Option>
              ))}
            </Form.Select>
          )}
          <Form.InputNumber field="rpm" label="RPM" />
          <Form.InputNumber field="tpm" label="TPM" />
          <Form.InputNumber
            field="concurrent_limit"
            label={t('ent.groups.concurrent')}
            extraText={t('ent.groups.concurrentHint')}
          />
          <Form.Select
            field="models"
            label={t('ent.groups.allowedModels')}
            multiple
            placeholder={t('ent.groups.allModelsHint')}
          >
            {models.map((m) => (
              <Form.Select.Option key={m} value={m}>
                {m}
              </Form.Select.Option>
            ))}
          </Form.Select>
          <Input style={{ display: 'none' }} />
        </Form>
      </SideSheet>
      )}
    </div>
  );
}
