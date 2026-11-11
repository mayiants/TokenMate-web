import { useCallback, useEffect, useState } from 'react';
import { Button, Empty, Input, Table, Tabs, Tag, Toast, Tooltip } from '@douyinfe/semi-ui';
import { IconBolt, IconCreditCardStroked, IconDownload, IconRefresh, IconTicketCode } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { download } from '../../api/client';
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
import { downloadInvoice, listInvoiceRequests, type InvoiceRequest } from '../../api/invoices';
import { listVoucherRecords, redeemVoucher, type VoucherRecord } from '../../api/vouchers';

const PAGE_SIZE = 10;

type TabKey = 'tx' | 'recharge' | 'invoices' | 'voucher';

export default function Billing() {
  const { t } = useI18n();
  const [tab, setTab] = useState<TabKey>('tx');

  const [balance, setBalance] = useState<Balance | null>(null);
  const [totalRecharge, setTotalRecharge] = useState<number | null>(null);

  const [txList, setTxList] = useState<BalanceTransaction[] | null>(null);
  const [loadingTx, setLoadingTx] = useState(false);

  const [recharge, setRecharge] = useState<Paged<RechargeRecord> | null>(null);
  const [rechargePage, setRechargePage] = useState(1);
  const [loadingRecharge, setLoadingRecharge] = useState(false);

  const [invoices, setInvoices] = useState<InvoiceRequest[] | null>(null);
  const [loadingInvoices, setLoadingInvoices] = useState(false);
  const [downloadingId, setDownloadingId] = useState<string | null>(null);

  const [vouchers, setVouchers] = useState<{ records: VoucherRecord[]; total: number; total_pages: number } | null>(null);
  const [voucherPage, setVoucherPage] = useState(1);
  const [loadingVouchers, setLoadingVouchers] = useState(false);
  const [redeemCode, setRedeemCode] = useState('');
  const [redeeming, setRedeeming] = useState(false);

  const tl = (key: string, fallback: string) => {
    const v = t(key);
    return v === key ? fallback : v;
  };

  const loadSummary = useCallback(async () => {
    try {
      setBalance(await getBalance());
    } catch {
      setBalance(null);
    }
    try {
      const res = await getRechargeRecords({ page: 1, page_size: 200, status: 'completed' });
      setTotalRecharge((res.records ?? []).reduce((sum, r) => sum + (r.amount ?? 0), 0));
    } catch {
      setTotalRecharge(null);
    }
  }, []);

  const loadTx = useCallback(async () => {
    setLoadingTx(true);
    try {
      setTxList(await getBalanceTransactions(false));
    } catch {
      setTxList([]);
    } finally {
      setLoadingTx(false);
    }
  }, []);

  const loadRecharge = useCallback(async (page: number) => {
    setLoadingRecharge(true);
    try {
      setRecharge(await getRechargeRecords({ page, page_size: PAGE_SIZE }));
    } catch {
      setRecharge(null);
    } finally {
      setLoadingRecharge(false);
    }
  }, []);

  const loadInvoices = useCallback(async () => {
    setLoadingInvoices(true);
    try {
      setInvoices(await listInvoiceRequests());
    } catch {
      setInvoices([]);
    } finally {
      setLoadingInvoices(false);
    }
  }, []);

  const loadVouchers = useCallback(async (page: number) => {
    setLoadingVouchers(true);
    try {
      const res = await listVoucherRecords(page, PAGE_SIZE);
      setVouchers({ records: res.records, total: res.total, total_pages: res.total_pages });
    } catch {
      setVouchers({ records: [], total: 0, total_pages: 0 });
    } finally {
      setLoadingVouchers(false);
    }
  }, []);

  useEffect(() => {
    loadSummary();
  }, [loadSummary]);

  useEffect(() => {
    if (tab === 'tx' && txList === null) loadTx();
  }, [tab, txList, loadTx]);

  useEffect(() => {
    if (tab === 'recharge') loadRecharge(rechargePage);
  }, [tab, rechargePage, loadRecharge]);

  useEffect(() => {
    if (tab === 'invoices' && invoices === null) loadInvoices();
  }, [tab, invoices, loadInvoices]);

  useEffect(() => {
    if (tab === 'voucher') loadVouchers(voucherPage);
  }, [tab, voucherPage, loadVouchers]);

  const doRedeem = async () => {
    const code = redeemCode.trim();
    if (!code) {
      Toast.warning(t('billing.voucher.codeRequired'));
      return;
    }
    setRedeeming(true);
    try {
      const res = await redeemVoucher(code);
      if (res.plan_name) {
        Toast.success(t('billing.voucher.planOk').replace('{name}', res.plan_name));
      } else {
        Toast.success(
          t('billing.voucher.balanceOk').replace('{amount}', formatUSD((res.amount ?? 0) / 1_000_000))
        );
      }
      setRedeemCode('');
      setVoucherPage(1);
      await Promise.all([loadVouchers(1), loadSummary()]);
      setTab('voucher');
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setRedeeming(false);
    }
  };

  const doExportRecharge = async () => {
    try {
      await download('/api/billing/recharge-records/export');
      Toast.success(t('common.exportDone'));
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    }
  };

  const doDownloadInvoice = async (row: InvoiceRequest) => {
    setDownloadingId(row.id);
    try {
      await downloadInvoice(row.id);
      Toast.success(t('billing.invoice.downloadDone'));
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setDownloadingId(null);
    }
  };

  const stats = [
    {
      icon: <IconCreditCardStroked />,
      tint: 'wg-tint-blue',
      label: t('billing.stat.cash'),
      value: balance ? formatUSD(balance.balance_usd) : null,
      caption: t('billing.stat.cashCap'),
    },
    {
      icon: <IconTicketCode />,
      tint: 'wg-tint-amber',
      label: t('billing.stat.voucher'),
      value: balance ? formatUSD(balance.gift_balance_usd) : null,
      caption: t('billing.stat.voucherCap'),
    },
    {
      icon: <IconBolt />,
      tint: 'wg-tint-green',
      label: t('billing.stat.totalRecharge'),
      value: totalRecharge === null ? null : formatUSD(totalRecharge),
      caption: t('billing.stat.totalRechargeCap'),
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
      render: (_: unknown, r: BalanceTransaction) => <Tag size="small">{tl(`balance.txType.${r.type}`, r.type)}</Tag>,
    },
    {
      title: t('billing.col.balanceType'),
      dataIndex: 'balance_type',
      width: 100,
      render: (_: unknown, r: BalanceTransaction) => (
        <span className="wg-cell-dim">{tl(`billing.balanceType.${r.balance_type}`, r.balance_type)}</span>
      ),
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
        <span className="wg-cell-ellipsis" style={{ display: 'inline-block', maxWidth: 360 }} title={r.description}>
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
      render: (_: unknown, r: RechargeRecord) => <span className="wg-num">{formatUSD(r.amount ?? 0)}</span>,
    },
    {
      title: t('balance.col.status'),
      dataIndex: 'status',
      width: 110,
      render: (_: unknown, r: RechargeRecord) => {
        const color = r.status === 'completed' || r.status === 'paid' ? 'green' : r.status === 'pending' ? 'orange' : 'red';
        return (
          <Tag color={color} size="small" shape="circle">
            {tl(`balance.rechargeStatus.${r.status}`, r.status)}
          </Tag>
        );
      },
    },
    {
      title: t('balance.col.method'),
      dataIndex: 'payment_method',
      width: 140,
      render: (_: unknown, r: RechargeRecord) => <span className="wg-cell-dim">{r.payment_method || '—'}</span>,
    },
  ];

  const invoiceColumns = [
    {
      title: t('billing.col.invoiceTitle'),
      dataIndex: 'title',
      render: (_: unknown, r: InvoiceRequest) => (
        <span className="wg-cell-ellipsis" style={{ display: 'inline-block', maxWidth: 300 }} title={r.title}>
          {r.title || r.invoice_number || '—'}
        </span>
      ),
    },
    {
      title: t('billing.col.amount'),
      dataIndex: 'amount',
      width: 130,
      render: (_: unknown, r: InvoiceRequest) => <span className="wg-num">{formatUSD((r.amount_cents ?? r.amount) / 100)}</span>,
    },
    {
      title: t('billing.col.invoiceStatus'),
      dataIndex: 'status',
      width: 120,
      render: (_: unknown, r: InvoiceRequest) => {
        const color =
          r.status === 'issued' || r.status === 'approved'
            ? 'green'
            : r.status === 'rejected'
              ? 'red'
              : r.status === 'pending'
                ? 'orange'
                : 'grey';
        return (
          <Tag color={color} size="small" shape="circle">
            {tl(`billing.invoiceStatus.${r.status}`, r.status)}
          </Tag>
        );
      },
    },
    {
      title: t('billing.col.invoiceNo'),
      dataIndex: 'invoice_number',
      width: 160,
      render: (_: unknown, r: InvoiceRequest) => <span className="wg-mono-sm">{r.invoice_number || '—'}</span>,
    },
    {
      title: t('balance.col.time'),
      dataIndex: 'created_at',
      width: 170,
      render: (_: unknown, r: InvoiceRequest) => <span className="wg-num wg-cell-dim">{formatDateTime(r.created_at)}</span>,
    },
    {
      title: t('common.actions'),
      width: 110,
      render: (_: unknown, r: InvoiceRequest) =>
        r.has_pdf ? (
          <Button
            theme="borderless"
            size="small"
            loading={downloadingId === r.id}
            onClick={() => doDownloadInvoice(r)}
          >
            {t('billing.invoice.download')}
          </Button>
        ) : (
          <span className="wg-cell-dim">—</span>
        ),
    },
  ];

  const voucherColumns = [
    {
      title: t('billing.col.voucherCode'),
      dataIndex: 'code',
      width: 180,
      render: (_: unknown, r: VoucherRecord) => <span className="wg-mono-sm">{r.code}</span>,
    },
    {
      title: t('billing.col.voucherType'),
      dataIndex: 'type',
      width: 120,
      render: (_: unknown, r: VoucherRecord) => <Tag size="small">{tl(`billing.voucherType.${r.type}`, r.type)}</Tag>,
    },
    {
      title: t('billing.col.amount'),
      dataIndex: 'amount',
      width: 130,
      render: (_: unknown, r: VoucherRecord) => (
        <span className="wg-num wg-amount-pos">
          +{formatUSD((r.amount ?? 0) / 1_000_000)}
        </span>
      ),
    },
    {
      title: t('billing.col.consumerType'),
      dataIndex: 'consumer_type',
      width: 120,
      render: (_: unknown, r: VoucherRecord) => (
        <span className="wg-cell-dim">{tl(`billing.consumerType.${r.consumer_type}`, r.consumer_type)}</span>
      ),
    },
    {
      title: t('billing.col.balanceAfter'),
      dataIndex: 'balance_after',
      width: 130,
      render: (_: unknown, r: VoucherRecord) => <span className="wg-num">{formatUSD(r.balance_after / 1_000_000)}</span>,
    },
    {
      title: t('balance.col.time'),
      dataIndex: 'created_at',
      width: 170,
      render: (_: unknown, r: VoucherRecord) => <span className="wg-num wg-cell-dim">{formatDateTime(r.created_at)}</span>,
    },
  ];

  const rechargeTotal = recharge?.total ?? 0;
  const rechargePages = Math.max(1, Math.ceil(rechargeTotal / PAGE_SIZE));
  const voucherPages = Math.max(1, vouchers?.total_pages ?? 1);

  return (
    <div className="wg-page">
      <PageHead title={t('acc.nav.billing')} />

      <div className="wg-stat-grid" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))' }}>
        {stats.map((s) => (
          <div key={s.label} className="wg-pro-card wg-stat">
            <span className={`wg-stat-icon ${s.tint}`}>{s.icon}</span>
            <div className="wg-stat-body">
              <div className="wg-stat-label">{s.label}</div>
              <div className="wg-stat-value">{s.value ?? '—'}</div>
              <div className="wg-stat-cap">{s.caption}</div>
            </div>
          </div>
        ))}
      </div>

      <section className="wg-pro-card wg-table-card">
        <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as TabKey)}>
          <Tabs.TabPane itemKey="tx" tab={t('billing.tab.tx')} />
          <Tabs.TabPane itemKey="recharge" tab={t('billing.tab.recharge')} />
          <Tabs.TabPane itemKey="invoices" tab={t('billing.tab.invoices')} />
          <Tabs.TabPane itemKey="voucher" tab={t('billing.tab.voucher')} />
        </Tabs>

        {tab === 'tx' && (
          <>
            <div className="wg-table-toolbar">
              <span className="wg-cell-dim">{t('billing.txNote')}</span>
              <Button size="small" theme="borderless" icon={<IconRefresh />} onClick={loadTx} loading={loadingTx}>
                {t('common.refresh')}
              </Button>
            </div>
            <Table
              loading={loadingTx}
              columns={txColumns}
              dataSource={txList ?? []}
              pagination={false}
              rowKey={(r?: BalanceTransaction) => `${r?.created_at}-${r?.description}-${r?.amount_micro}`}
              tableLayout="fixed"
              empty={<Empty description={t('balance.emptyTx')} />}
            />
            <div className="wg-table-footer">
              <span className="wg-cell-dim">{t('billing.txLimit')}</span>
            </div>
          </>
        )}

        {tab === 'recharge' && (
          <>
            <div className="wg-table-toolbar">
              <span className="wg-cell-dim">{t('billing.rechargeNote')}</span>
              <div className="wg-pro-card-extra">
                <Button size="small" theme="borderless" icon={<IconRefresh />} onClick={() => loadRecharge(rechargePage)} loading={loadingRecharge}>
                  {t('common.refresh')}
                </Button>
                <Button size="small" type="primary" theme="light" icon={<IconDownload />} onClick={doExportRecharge}>
                  {t('common.export')}
                </Button>
              </div>
            </div>
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
                {t('keys.showing')
                  .replace('{from}', String(rechargeTotal === 0 ? 0 : (rechargePage - 1) * PAGE_SIZE + 1))
                  .replace('{to}', String(Math.min(rechargePage * PAGE_SIZE, rechargeTotal)))
                  .replace('{total}', String(rechargeTotal))}
              </span>
              <div className="wg-table-pager">
                <Button size="small" theme="light" disabled={rechargePage <= 1} onClick={() => setRechargePage(rechargePage - 1)}>
                  ‹
                </Button>
                <span className="wg-num">
                  {Math.min(rechargePage, rechargePages)} / {rechargePages}
                </span>
                <Button
                  size="small"
                  theme="light"
                  disabled={rechargePage >= rechargePages}
                  onClick={() => setRechargePage(rechargePage + 1)}
                >
                  ›
                </Button>
              </div>
            </div>
          </>
        )}

        {tab === 'invoices' && (
          <>
            <div className="wg-table-toolbar">
              <span className="wg-cell-dim">{t('billing.invoiceNote')}</span>
              <Button size="small" theme="borderless" icon={<IconRefresh />} onClick={loadInvoices} loading={loadingInvoices}>
                {t('common.refresh')}
              </Button>
            </div>
            <Table
              loading={loadingInvoices}
              columns={invoiceColumns}
              dataSource={invoices ?? []}
              pagination={false}
              rowKey="id"
              tableLayout="fixed"
              empty={<Empty description={t('billing.invoiceEmpty')} />}
            />
            <div className="wg-table-footer">
              <span className="wg-cell-dim">{t('billing.invoiceLimit')}</span>
            </div>
          </>
        )}

        {tab === 'voucher' && (
          <>
            <div className="wg-filter-card">
              <Input
                value={redeemCode}
                onChange={setRedeemCode}
                placeholder={t('billing.voucher.placeholder')}
                style={{ width: 260 }}
                aria-label={t('billing.voucher.code')}
              />
              <div className="wg-filter-actions">
                <Button type="primary" theme="solid" loading={redeeming} onClick={doRedeem}>
                  {t('billing.voucher.redeem')}
                </Button>
              </div>
              <Tooltip content={t('billing.voucher.note')}>
                <span className="wg-cell-dim" style={{ marginLeft: 'auto' }}>
                  {t('billing.voucher.noteShort')}
                </span>
              </Tooltip>
            </div>
            <Table
              loading={loadingVouchers}
              columns={voucherColumns}
              dataSource={vouchers?.records ?? []}
              pagination={false}
              rowKey="id"
              tableLayout="fixed"
              empty={<Empty description={t('billing.voucher.empty')} />}
            />
            <div className="wg-table-footer">
              <span className="wg-cell-dim">
                {t('keys.showing')
                  .replace('{from}', String((vouchers?.total ?? 0) === 0 ? 0 : (voucherPage - 1) * PAGE_SIZE + 1))
                  .replace('{to}', String(Math.min(voucherPage * PAGE_SIZE, vouchers?.total ?? 0)))
                  .replace('{total}', String(vouchers?.total ?? 0))}
              </span>
              <div className="wg-table-pager">
                <Button size="small" theme="light" disabled={voucherPage <= 1} onClick={() => setVoucherPage(voucherPage - 1)}>
                  ‹
                </Button>
                <span className="wg-num">
                  {Math.min(voucherPage, voucherPages)} / {voucherPages}
                </span>
                <Button
                  size="small"
                  theme="light"
                  disabled={voucherPage >= voucherPages}
                  onClick={() => setVoucherPage(voucherPage + 1)}
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
