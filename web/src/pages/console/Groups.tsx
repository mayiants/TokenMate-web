import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Empty, Spin, Table, Tag, Tooltip } from '@douyinfe/semi-ui';
import { IconFolderStroked } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { groupModels, listMyGroups, type Group } from '../../api/groups';
import { listKeys, type ApiKey } from '../../api/keys';

export default function Groups() {
  const { t } = useI18n();
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);

  const tl = (key: string, fallback: string) => {
    const v = t(key);
    return v === key ? fallback : v;
  };

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [gs, ks] = await Promise.all([listMyGroups(), listKeys().catch(() => [])]);
      setGroups(gs);
      setKeys(ks);
    } catch {
      setGroups([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // There is no "current user's group" endpoint on the user surface; the
  // authoritative link the caller can see is the group bound to their keys.
  const myGroups = useMemo(() => {
    if (!groups) return [];
    const bound = new Map<string, Group>();
    keys.forEach((k) => {
      if (!k.group_id) return;
      const g = groups.find((x) => x.id === k.group_id);
      if (g) bound.set(g.id, g);
    });
    return Array.from(bound.values());
  }, [groups, keys]);

  const columns = [
    {
      title: t('groups.col.name'),
      dataIndex: 'name',
      render: (_: unknown, g: Group) => (
        <div>
          <div className="wg-cell-strong">{g.name}</div>
          {g.description && <div className="wg-cell-dim wg-cell-ellipsis" style={{ maxWidth: 360 }}>{g.description}</div>}
        </div>
      ),
    },
    {
      title: t('groups.col.type'),
      dataIndex: 'group_type',
      width: 130,
      render: (_: unknown, g: Group) => (
        <Tag size="small" color="blue">
          {tl(`groups.type.${g.group_type}`, g.group_type)}
        </Tag>
      ),
    },
    {
      title: t('groups.col.billing'),
      dataIndex: 'billing_mode',
      width: 120,
      render: (_: unknown, g: Group) => (
        <span className="wg-cell-dim">{tl(`groups.billing.${g.billing_mode ?? ''}`, g.billing_mode ?? '—')}</span>
      ),
    },
    {
      title: t('groups.col.models'),
      width: 110,
      render: (_: unknown, g: Group) => {
        const n = groupModels(g).length;
        return n > 0 ? <Tag color="blue">{n}</Tag> : <Tag color="grey">{t('groups.allModels')}</Tag>;
      },
    },
    {
      title: 'RPM / TPM',
      width: 150,
      render: (_: unknown, g: Group) => (
        <span className="wg-num">
          {g.rate_limit_rpm || 0} / {g.rate_limit_tpm || '—'}
        </span>
      ),
    },
    {
      title: t('common.actions'),
      width: 110,
      render: () => (
        <Tooltip content={t('groups.joinSoon')}>
          <span>
            <Button theme="borderless" size="small" disabled>
              {t('groups.join')}
            </Button>
          </span>
        </Tooltip>
      ),
    },
  ];

  return (
    <div className="wg-page">
      <PageHead title={t('acc.nav.groups')} />

      <div className="wg-sec-row">
        <span className="wg-sec-row-title">
          <IconFolderStroked />
          {t('groups.sectionMine')}
        </span>
      </div>

      {loading && groups === null ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 40 }}>
          <Spin size="large" />
        </div>
      ) : myGroups.length === 0 ? (
        <section className="wg-pro-card" style={{ padding: 32 }}>
          <Empty description={t('groups.emptyMine')} />
        </section>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: 16 }}>
          {myGroups.map((g) => {
            const models = groupModels(g);
            return (
              <section key={g.id} className="wg-pro-card">
                <div className="wg-pro-card-head">
                  <span className="wg-pro-card-title">{g.name}</span>
                  <Tag size="small" color="blue">
                    {tl(`groups.type.${g.group_type}`, g.group_type)}
                  </Tag>
                </div>
                <div className="wg-pro-card-body" style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
                    <span className="wg-cell-dim">RPM</span>
                    <span className="wg-num">{g.rate_limit_rpm || t('groups.inherit')}</span>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
                    <span className="wg-cell-dim">TPM</span>
                    <span className="wg-num">{g.rate_limit_tpm || t('groups.inherit')}</span>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
                    <span className="wg-cell-dim">{t('groups.col.models')}</span>
                    <span className="wg-num">{models.length > 0 ? models.length : t('groups.allModels')}</span>
                  </div>
                </div>
              </section>
            );
          })}
        </div>
      )}

      <div className="wg-sec-row">
        <span className="wg-sec-row-title">{t('groups.sectionPublic')}</span>
      </div>

      <section className="wg-pro-card wg-table-card">
        <Table
          loading={loading}
          columns={columns}
          dataSource={groups ?? []}
          pagination={false}
          rowKey="id"
          tableLayout="fixed"
          empty={<Empty description={t('groups.emptyPublic')} />}
        />
      </section>
    </div>
  );
}
