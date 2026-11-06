import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Empty, Select, Table, Tag, Toast } from '@douyinfe/semi-ui';
import { IconRefresh } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import {
  listNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  type NotificationItem,
} from '../../api/notifications';
import { formatDateTime } from '../../api/console';

const PAGE_SIZE = 10;
const MAX_PAGES = 5; // backend fixes per_page=20; cap the client-side pull at 100

export default function Notifications() {
  const { t } = useI18n();
  const [items, setItems] = useState<NotificationItem[] | null>(null);
  const [unread, setUnread] = useState(0);
  const [loading, setLoading] = useState(false);
  const [typeFilter, setTypeFilter] = useState<string>('all');
  const [readFilter, setReadFilter] = useState<string>('all');
  const [appliedType, setAppliedType] = useState<string>('all');
  const [appliedRead, setAppliedRead] = useState<string>('all');
  const [page, setPage] = useState(1);

  const tl = (key: string, fallback: string) => {
    const v = t(key);
    return v === key ? fallback : v;
  };

  const typeLabel = (type: string) => tl(`notify.type.${type}`, type);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const collected: NotificationItem[] = [];
      let lastUnread = 0;
      for (let p = 1; p <= MAX_PAGES; p += 1) {
        const res = await listNotifications(p);
        collected.push(...res.items);
        lastUnread = res.unread;
        if (res.items.length < res.per_page) break;
      }
      setItems(collected);
      setUnread(lastUnread);
    } catch (e) {
      setItems([]);
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    load();
  }, [load]);

  const typeOptions = useMemo(() => {
    const seen = new Set<string>();
    (items ?? []).forEach((i) => seen.add(i.type));
    return [
      { value: 'all', label: t('notify.filter.allTypes') },
      ...Array.from(seen)
        .sort()
        .map((type) => ({ value: type, label: typeLabel(type) })),
    ];
    // typeLabel is derived from t(); items is the only real dependency
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, t]);

  const filtered = useMemo(() => {
    let list = items ?? [];
    if (appliedType !== 'all') list = list.filter((i) => i.type === appliedType);
    if (appliedRead === 'unread') list = list.filter((i) => !i.read_at);
    if (appliedRead === 'read') list = list.filter((i) => !!i.read_at);
    return list;
  }, [items, appliedType, appliedRead]);

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const pageRows = filtered.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE);
  const from = filtered.length === 0 ? 0 : (safePage - 1) * PAGE_SIZE + 1;
  const to = Math.min(safePage * PAGE_SIZE, filtered.length);

  const onSearch = () => {
    setAppliedType(typeFilter);
    setAppliedRead(readFilter);
    setPage(1);
  };

  const onReset = () => {
    setTypeFilter('all');
    setReadFilter('all');
    setAppliedType('all');
    setAppliedRead('all');
    setPage(1);
  };

  const onMarkRead = async (item: NotificationItem) => {
    try {
      await markNotificationRead(item.id);
      setItems((prev) =>
        (prev ?? []).map((i) => (i.id === item.id ? { ...i, read_at: new Date().toISOString() } : i))
      );
      setUnread((n) => Math.max(0, n - 1));
      Toast.success(t('notify.markedRead'));
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    }
  };

  const onMarkAll = async () => {
    try {
      await markAllNotificationsRead();
      const now = new Date().toISOString();
      setItems((prev) => (prev ?? []).map((i) => (i.read_at ? i : { ...i, read_at: now })));
      setUnread(0);
      Toast.success(t('notify.allMarkedRead'));
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    }
  };

  const columns = [
    {
      title: t('notify.col.title'),
      dataIndex: 'title',
      render: (_: unknown, r: NotificationItem) => (
        <div style={{ minWidth: 0 }}>
          <div className="wg-cell-strong wg-cell-ellipsis" style={{ maxWidth: 360 }}>
            {r.title || '—'}
          </div>
          <div className="wg-cell-dim wg-cell-ellipsis" style={{ maxWidth: 360 }}>
            {typeLabel(r.type)}
          </div>
        </div>
      ),
    },
    {
      title: t('notify.col.body'),
      dataIndex: 'body',
      render: (_: unknown, r: NotificationItem) => (
        <span className="wg-cell-ellipsis" style={{ display: 'inline-block', maxWidth: 420 }} title={r.body}>
          {r.body || '—'}
        </span>
      ),
    },
    {
      title: t('notify.col.time'),
      dataIndex: 'created_at',
      width: 170,
      render: (_: unknown, r: NotificationItem) => (
        <span className="wg-num wg-cell-dim">{formatDateTime(r.created_at)}</span>
      ),
    },
    {
      title: t('notify.col.status'),
      dataIndex: 'read_at',
      width: 100,
      render: (_: unknown, r: NotificationItem) => (
        <Tag color={r.read_at ? 'grey' : 'orange'} size="small" shape="circle">
          {r.read_at ? t('notify.read') : t('notify.unread')}
        </Tag>
      ),
    },
    {
      title: t('common.actions'),
      width: 110,
      render: (_: unknown, r: NotificationItem) =>
        r.read_at ? (
          <span className="wg-cell-dim">—</span>
        ) : (
          <Button theme="borderless" size="small" onClick={() => onMarkRead(r)}>
            {t('notify.markRead')}
          </Button>
        ),
    },
  ];

  return (
    <div className="wg-page">
      <PageHead title={t('acc.nav.notifications')} />

      <section className="wg-pro-card wg-filter-card">
        <Select
          value={typeFilter}
          onChange={(v) => setTypeFilter(String(v))}
          optionList={typeOptions}
          style={{ width: 200 }}
          aria-label={t('notify.filter.type')}
        />
        <Select
          value={readFilter}
          onChange={(v) => setReadFilter(String(v))}
          style={{ width: 160 }}
          aria-label={t('notify.filter.readState')}
          optionList={[
            { value: 'all', label: t('notify.filter.allStates') },
            { value: 'unread', label: t('notify.unread') },
            { value: 'read', label: t('notify.read') },
          ]}
        />
        <div className="wg-filter-actions">
          <Button type="primary" onClick={onSearch}>
            {t('common.search')}
          </Button>
          <Button onClick={onReset}>{t('common.reset')}</Button>
        </div>
      </section>

      <section className="wg-pro-card wg-table-card">
        <div className="wg-table-toolbar">
          <div className="wg-table-toolbar-left">
            <span className="wg-cell-dim">
              {t('notify.unreadCount').replace('{n}', String(unread))}
            </span>
            <Button size="small" theme="borderless" icon={<IconRefresh />} onClick={load} loading={loading}>
              {t('common.refresh')}
            </Button>
          </div>
          <Button type="primary" theme="light" onClick={onMarkAll} disabled={unread === 0}>
            {t('notify.markAll')}
          </Button>
        </div>
        <Table
          loading={loading}
          columns={columns}
          dataSource={pageRows}
          pagination={false}
          rowKey="id"
          tableLayout="fixed"
          empty={<Empty description={t('notify.empty')} />}
        />
        <div className="wg-table-footer">
          <span className="wg-cell-dim">
            {t('keys.showing')
              .replace('{from}', String(from))
              .replace('{to}', String(to))
              .replace('{total}', String(filtered.length))}
          </span>
          <div className="wg-table-pager">
            <Button size="small" theme="light" disabled={safePage <= 1} onClick={() => setPage(safePage - 1)}>
              ‹
            </Button>
            <span className="wg-num">
              {safePage} / {totalPages}
            </span>
            <Button
              size="small"
              theme="light"
              disabled={safePage >= totalPages}
              onClick={() => setPage(safePage + 1)}
            >
              ›
            </Button>
          </div>
        </div>
      </section>
    </div>
  );
}
