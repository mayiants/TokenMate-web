import { useCallback, useEffect, useState } from 'react';
import { Button, Empty, Modal, Spin, Table, Tabs, Tag, Toast, Tooltip } from '@douyinfe/semi-ui';
import { IconRefresh } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { formatDate, formatUSD } from '../../api/console';
import {
  listAllMySubscriptions,
  listPublicPlans,
  planModelNames,
  purchasePlan,
  type SubscriptionPlan,
  type UserSubscription,
} from '../../api/subscriptions';

type TabKey = 'plans' | 'mine';

export default function Subscriptions() {
  const { t } = useI18n();
  const [tab, setTab] = useState<TabKey>('plans');
  const [plans, setPlans] = useState<SubscriptionPlan[] | null>(null);
  const [mine, setMine] = useState<UserSubscription[] | null>(null);
  const [loadingPlans, setLoadingPlans] = useState(false);
  const [loadingMine, setLoadingMine] = useState(false);
  const [purchasing, setPurchasing] = useState<string | null>(null);

  const tl = (key: string, fallback: string) => {
    const v = t(key);
    return v === key ? fallback : v;
  };

  const loadPlans = useCallback(async () => {
    setLoadingPlans(true);
    try {
      setPlans(await listPublicPlans());
    } catch {
      setPlans([]);
    } finally {
      setLoadingPlans(false);
    }
  }, []);

  const loadMine = useCallback(async () => {
    setLoadingMine(true);
    try {
      setMine(await listAllMySubscriptions());
    } catch {
      setMine([]);
    } finally {
      setLoadingMine(false);
    }
  }, []);

  useEffect(() => {
    loadPlans();
    loadMine();
  }, [loadPlans, loadMine]);

  const confirmPurchase = (plan: SubscriptionPlan) => {
    Modal.confirm({
      title: t('subs.purchaseTitle').replace('{name}', plan.name),
      content: t('subs.purchaseContent')
        .replace('{price}', formatUSD((plan.price ?? 0) / 1_000_000))
        .replace('{duration}', plan.duration_display ?? '—'),
      okText: t('subs.purchase'),
      cancelText: t('common.cancel'),
      onOk: async () => {
        setPurchasing(plan.id);
        try {
          const res = await purchasePlan(plan.id);
          Toast.success(t('subs.purchaseOk').replace('{order}', res.order_id ?? '—'));
          await loadMine();
        } catch (e) {
          Toast.error(e instanceof Error ? e.message : t('common.error'));
          throw e;
        } finally {
          setPurchasing(null);
        }
      },
    });
  };

  const statusPill = (status: string) => {
    const color =
      status === 'active' ? 'green' : status === 'expired' ? 'grey' : status === 'cancelled' ? 'red' : 'orange';
    return (
      <Tag color={color} size="small" shape="circle">
        {tl(`subs.status.${status}`, status)}
      </Tag>
    );
  };

  const myColumns = [
    {
      title: t('subs.col.plan'),
      dataIndex: 'plan_name',
      render: (_: unknown, r: UserSubscription) => (
        <div>
          <div className="wg-cell-strong">{r.plan_name || '—'}</div>
          <div className="wg-cell-dim">{tl(`subs.planType.${r.plan_type ?? ''}`, r.plan_type ?? '—')}</div>
        </div>
      ),
    },
    {
      title: t('subs.col.quota'),
      dataIndex: 'quota_remaining',
      width: 180,
      render: (_: unknown, r: UserSubscription) => (
        <span className="wg-num">
          {formatUSD((r.quota_remaining ?? 0) / 1_000_000)}
          <span className="wg-cell-dim"> / {formatUSD((r.quota_total ?? 0) / 1_000_000)}</span>
        </span>
      ),
    },
    {
      title: t('subs.col.started'),
      dataIndex: 'started_at',
      width: 140,
      render: (_: unknown, r: UserSubscription) => (
        <span className="wg-num wg-cell-dim">{formatDate(r.started_at)}</span>
      ),
    },
    {
      title: t('subs.col.expires'),
      dataIndex: 'expires_at',
      width: 140,
      render: (_: unknown, r: UserSubscription) => (
        <span className="wg-num wg-cell-dim">{r.expires_at ? formatDate(r.expires_at) : t('keys.neverExpires')}</span>
      ),
    },
    {
      title: t('subs.col.status'),
      dataIndex: 'status',
      width: 110,
      render: (_: unknown, r: UserSubscription) => statusPill(r.status ?? ''),
    },
  ];

  return (
    <div className="wg-page">
      <PageHead title={t('acc.nav.subscriptions')} />

      <section className="wg-pro-card wg-table-card">
        <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as TabKey)}>
          <Tabs.TabPane itemKey="plans" tab={t('subs.tab.plans')} />
          <Tabs.TabPane itemKey="mine" tab={t('subs.tab.mine')} />
        </Tabs>
      </section>

      {tab === 'plans' ? (
        loadingPlans && plans === null ? (
          <div style={{ display: 'flex', justifyContent: 'center', padding: 60 }}>
            <Spin size="large" />
          </div>
        ) : (plans ?? []).length === 0 ? (
          <section className="wg-pro-card" style={{ padding: 40 }}>
            <Empty description={t('subs.emptyPlans')} />
          </section>
        ) : (
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))',
              gap: 16,
            }}
          >
            {(plans ?? []).map((plan) => {
              const models = planModelNames(plan);
              const price = formatUSD((plan.price ?? 0) / 1_000_000);
              return (
                <section key={plan.id} className="wg-pro-card" style={{ display: 'flex', flexDirection: 'column' }}>
                  <div className="wg-pro-card-head">
                    <span className="wg-pro-card-title">{plan.name}</span>
                    {plan.plan_type && (
                      <Tag size="small" color="blue">
                        {tl(`subs.planType.${plan.plan_type}`, plan.plan_type)}
                      </Tag>
                    )}
                  </div>
                  <div className="wg-pro-card-body" style={{ display: 'flex', flexDirection: 'column', gap: 14, flex: 1 }}>
                    <div className="wg-stat-value" style={{ fontSize: 28 }}>
                      {price}
                      <span className="wg-cell-dim" style={{ fontSize: 13, fontWeight: 400 }}>
                        {' '}
                        / {plan.duration_display ?? '—'}
                      </span>
                    </div>
                    {plan.description && (
                      <p className="wg-cell-dim" style={{ margin: 0 }}>
                        {plan.description}
                      </p>
                    )}
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, fontSize: 13 }}>
                      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
                        <span className="wg-cell-dim">{t('subs.plan.quota')}</span>
                        <span className="wg-num">{formatUSD((plan.quota ?? 0) / 1_000_000)}</span>
                      </div>
                      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
                        <span className="wg-cell-dim">{t('subs.plan.models')}</span>
                        <span className="wg-num">
                          {models.length > 0 ? models.length : t('subs.plan.allModels')}
                        </span>
                      </div>
                      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
                        <span className="wg-cell-dim">{t('subs.plan.limits')}</span>
                        <span className="wg-num">
                          {plan.rpm_limit ?? 0} RPM · {plan.tpm_limit ?? 0} TPM
                        </span>
                      </div>
                    </div>
                    <div style={{ marginTop: 'auto', paddingTop: 8 }}>
                      <Button
                        type="primary"
                        theme="solid"
                        block
                        loading={purchasing === plan.id}
                        onClick={() => confirmPurchase(plan)}
                      >
                        {t('subs.purchase')}
                      </Button>
                    </div>
                  </div>
                </section>
              );
            })}
          </div>
        )
      ) : (
        <section className="wg-pro-card wg-table-card">
          <div className="wg-table-toolbar">
            <span className="wg-cell-dim">{t('subs.mineNote')}</span>
            <Button size="small" theme="borderless" icon={<IconRefresh />} loading={loadingMine} onClick={loadMine}>
              {t('common.refresh')}
            </Button>
          </div>
          <Table
            loading={loadingMine}
            columns={myColumns}
            dataSource={mine ?? []}
            pagination={false}
            rowKey="id"
            tableLayout="fixed"
            empty={<Empty description={t('subs.emptyMine')} />}
          />
          <div className="wg-table-footer">
            <span className="wg-cell-dim">
              {t('subs.count').replace('{n}', String((mine ?? []).length))}
            </span>
            <Tooltip content={t('subs.purchaseHint')}>
              <span className="wg-cell-dim">{t('subs.purchaseHintShort')}</span>
            </Tooltip>
          </div>
        </section>
      )}
    </div>
  );
}
