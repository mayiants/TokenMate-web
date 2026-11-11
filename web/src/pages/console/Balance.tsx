import { useCallback, useEffect, useState } from 'react';
import { Button, Empty, Spin, Table, Tabs, Tag, Tooltip } from '@douyinfe/semi-ui';
import { IconBolt, IconCreditCardStroked, IconTicketCode } from '@douyinfe/semi-icons';
import { useI18n } from '../../i18n';
import {
  formatDateTime,
  formatUSD,
  getBalance,
  getBalanceTransactions,
  getRechargeRecords,
  type Balance,
  type BalanceTransaction,
  type Paged,
  type RechargeRecord,
} from '../../api/console';

const RECHARGE_PAGE_SIZE = 10;

function amountText(v: number | undefined): string {
  return formatUSD(v ?? 0);
}

export default function BalancePage() {
  const { t } = useI18n();
  const [balance, setBalance] = useState<Balance | null>(null);
  const [totalRecharge, setTotalRecharge] = useState<number | null>(null);
  const [tab, setTab] = useState<'transactions' | 'recharge'>('transactions');

  const [txList, setTxList] = useState<BalanceTransaction[] | null>(null);
  const [recharge, setRecharge] = useState<Paged<RechargeRecord> | null>(null);
  const [rechargePage, setRechargePage] = useState(1);
  const [loadingTx, setLoadingTx] = useState(false);
  const [loadingRecharge, setLoadingRecharge] = useState(false);

  useEffect(() => {
    getBalance().then(setBalance).catch(() => setBalance(null));
    // cumulative top-up: sum of completed orders (single wide page is enough in practice)
    getRechargeRecords({ page: 1, page_size: 200, status: 'completed' })
      .then((res) => setTotalRecharge((res.records ?? []).reduce((sum, r) => sum + (r.amount ?? 0), 0)))
      .catch(() => setTotalRecharge(null));
  }, []);

  const loadTx = useCallback(async () => {
    setLoadingTx(true);
    try {
      const list = await getBalanceTransactions(false);
      setTxList(list);
    } catch {
      setTxList([]);
    } finally {
      setLoadingTx(false);
    }
  }, []);

  const loadRecharge = useCallback(async (page: number) => {
    setLoadingRecharge(true);
    try {
      const res = await getRechargeRecords({ page, page_size: RECHARGE_PAGE_SIZE });
      setRecharge(res);
    } catch {
      setRecharge(null);
    } finally {
      setLoadingRecharge(false);
    }
  }, []);

  useEffect(() => {
    loadTx();
  }, [loadTx]);

  useEffect(() => {
    loadRecharge(rechargePage);
  }, [loadRecharge, rechargePage]);

  /** t() falls back to the key itself; return the raw value for unknown enum members. */
  const tl = (key: string, fallback: string) => {
    const v = t(key);
    return v === key ? fallback : v;
  };

  const txTypeLabel = (type: string) => tl(`balance.txType.${type}`, type);
  const rechargeStatusPill = (s: string) => {
    const color = s === 'completed' || s === 'paid' ? 'green' : s === 'pending' ? 'orange' : 'red';
    return (
      <Tag color={color} size="small" shape="circle">
        {tl(`balance.rechargeStatus.${s}`, s)}
      </Tag>
    );
  };

  const stats = [
    {
      icon: <IconCreditCardStroked />,
      tint: 'wg-tint-blue',
      label: t('dash.balance.cash'),
      value: balance ? formatUSD(balance.balance_usd) : null,
      caption: t('dash.balance.cashCap'),
    },
    {
      icon: <IconTicketCode />,
      tint: 'wg-tint-amber',
      label: t('dash.balance.voucher'),
      value: balance ? formatUSD(balance.gift_balance_usd) : null,
      caption: t('balance.voucherNote'),
    },
    {
      icon: <IconBolt />,
      tint: 'wg-tint-green',
      label: t('balance.stat.totalRecharge'),
      value: totalRecharge === null ? null : formatUSD(totalRecharge),
      caption: t('balance.stat.totalRechargeCap'),
    },
  ];

  const txColumns = [
    {
      title: t('balance.col.time'),
      dataIndex: 'created_at',
      width: 170,
      render: (_: unknown, r: BalanceTransaction) => <span className="wg-num wg-cell-dim">{formatDateTime(r.created_at)}</span>,
    },
    {
      title: t('balance.col.type'),
      dataIndex: 'type',
      width: 120,
      render: (_: unknown, r: BalanceTransaction) => <Tag size="small">{txTypeLabel(r.type)}</Tag>,
    },
    {
      title: t('balance.col.amount'),
      dataIndex: 'amount_usd',
      width: 130,
      render: (_: unknown, r: BalanceTransaction) => (
        <span className={`wg-num ${r.amount_usd >= 0 ? 'wg-amount-pos' : 'wg-amount-neg'}`}>
          {r.amount_usd >= 0 ? '+' : ''}
          {formatUSD(Math.abs(r.amount_usd))}
        </span>
      ),
    },
    {
      title: t('balance.col.balanceAfter'),
      dataIndex: 'balance_after',
      width: 130,
      render: (_: unknown, r: BalanceTransaction) => <span className="wg-num">{formatUSD(r.balance_after)}</span>,
    },
    {
      title: t('balance.col.description'),
      dataIndex: 'description',
      render: (_: unknown, r: BalanceTransaction) => (
        <span className="wg-cell-ellipsis" title={r.description}>
          {r.description || '—'}
        </span>
      ),
    },
  ];

  const rechargeColumns = [
    {
      title: t('balance.col.orderId'),
      dataIndex: 'order_id',
      width: 180,
      render: (_: unknown, r: RechargeRecord) => <span className="wg-mono-sm">{r.order_id}</span>,
    },
    {
      title: t('balance.col.time'),
      dataIndex: 'created_at',
      width: 170,
      render: (_: unknown, r: RechargeRecord) => <span className="wg-num wg-cell-dim">{formatDateTime(r.created_at)}</span>,
    },
    {
      title: t('balance.col.amount'),
      dataIndex: 'amount',
      width: 130,
      render: (_: unknown, r: RechargeRecord) => <span className="wg-num">{amountText(r.amount)}</span>,
    },
    {
      title: t('balance.col.status'),
      dataIndex: 'status',
      width: 110,
      render: (_: unknown, r: RechargeRecord) => rechargeStatusPill(r.status),
    },
    {
      title: t('balance.col.method'),
      dataIndex: 'payment_method',
      width: 140,
      render: (_: unknown, r: RechargeRecord) => <span className="wg-cell-dim">{r.payment_method || '—'}</span>,
    },
  ];

  const total = recharge?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / RECHARGE_PAGE_SIZE));
  const from = total === 0 ? 0 : (rechargePage - 1) * RECHARGE_PAGE_SIZE + 1;
  const to = Math.min(rechargePage * RECHARGE_PAGE_SIZE, total);

  return (
    <div className="wg-page">
      <div className="wg-page-head">
        <div>
          <h2 className="wg-page-title">{t('console.nav.balance')}</h2>
        </div>
        <div className="wg-page-actions">
          <Tooltip content={t('balance.topupSoon')}>
            <span>
              <Button type="primary" theme="solid" disabled>
                {t('balance.topup')}
              </Button>
            </span>
          </Tooltip>
        </div>
      </div>

      <div className="wg-stat-grid">
        {stats.map((s) => (
          <div key={s.label} className="wg-pro-card wg-stat">
            <span className={`wg-stat-icon ${s.tint}`}>{s.icon}</span>
            <div className="wg-stat-body">
              <div className="wg-stat-label">{s.label}</div>
              <div className="wg-stat-value">{s.value ?? <Spin size="small" />}</div>
              <div className="wg-stat-cap">{s.caption}</div>
            </div>
          </div>
        ))}
      </div>

      <section className="wg-pro-card wg-table-card">
        <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as typeof tab)}>
          <Tabs.TabPane itemKey="transactions" tab={t('balance.tab.transactions')} />
          <Tabs.TabPane itemKey="recharge" tab={t('balance.tab.recharge')} />
        </Tabs>
        {tab === 'transactions' ? (
          <>
            <Table
              loading={loadingTx}
              columns={txColumns}
              dataSource={txList ?? []}
              pagination={false}
              rowKey={(r?: BalanceTransaction) => `${r?.created_at}-${r?.description}`}
              tableLayout="fixed"
              empty={<Empty description={t('balance.emptyTx')} />}
            />
            <div className="wg-table-footer">
              <span className="wg-cell-dim">{t('balance.txNote')}</span>
            </div>
          </>
        ) : (
          <>
            <Table
              loading={loadingRecharge}
              columns={rechargeColumns}
              dataSource={recharge?.records ?? []}
              pagination={false}
              rowKey="id"
              tableLayout="fixed"
              empty={<Empty description={t('balance.emptyRecharge')} />}
            />
            <div className="wg-table-footer">
              <span className="wg-cell-dim">
                {t('keys.showing').replace('{from}', String(from)).replace('{to}', String(to)).replace('{total}', String(total))}
              </span>
              <div className="wg-table-pager">
                <Button size="small" theme="light" disabled={rechargePage <= 1} onClick={() => setRechargePage(rechargePage - 1)}>
                  ‹
                </Button>
                <span className="wg-num">
                  {Math.min(rechargePage, totalPages)} / {totalPages}
                </span>
                <Button
                  size="small"
                  theme="light"
                  disabled={rechargePage >= totalPages}
                  onClick={() => setRechargePage(rechargePage + 1)}
                >
                  ›
                </Button>
              </div>
            </div>
          </>
        )}
      </section>
    </div>
  );
}
