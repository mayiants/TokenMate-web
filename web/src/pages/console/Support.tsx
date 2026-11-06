import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Banner,
  Button,
  Empty,
  Form,
  Select,
  SideSheet,
  Table,
  Tabs,
  Tag,
  TextArea,
  Toast,
  Upload,
} from '@douyinfe/semi-ui';
import type { FormApi } from '@douyinfe/semi-ui/lib/es/form';
import { IconPlus, IconRefresh, IconUpload } from '@douyinfe/semi-icons';
import PageHead from '../../components/PageHead';
import { useI18n } from '../../i18n';
import { getToken } from '../../api/client';
import { formatDateTime } from '../../api/console';
import {
  createTicket,
  getTicket,
  listTickets,
  replyTicket,
  stageTicketAttachments,
  unstageTicketAttachment,
  type Ticket,
  type TicketAttachment,
  type TicketDetail,
} from '../../api/tickets';

const CATEGORIES = ['general', 'billing', 'technical', 'abuse', 'feature'];
const PRIORITIES = ['low', 'normal', 'high', 'urgent'];
const STATUS_TABS = ['all', 'open', 'pending', 'answered', 'resolved', 'closed'];

function statusColor(status: string): 'green' | 'orange' | 'blue' | 'grey' | 'red' {
  switch (status) {
    case 'open':
      return 'blue';
    case 'pending':
      return 'orange';
    case 'answered':
      return 'blue';
    case 'resolved':
      return 'green';
    case 'closed':
      return 'grey';
    default:
      return 'grey';
  }
}

export default function Support() {
  const { t } = useI18n();
  const [tab, setTab] = useState<'list' | 'new'>('list');
  const [tickets, setTickets] = useState<Ticket[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [statusFilter, setStatusFilter] = useState('all');
  const [appliedStatus, setAppliedStatus] = useState('all');

  const [detail, setDetail] = useState<TicketDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [viewVisible, setViewVisible] = useState(false);
  const [replyBody, setReplyBody] = useState('');
  const [replying, setReplying] = useState(false);

  const [newVisible, setNewVisible] = useState(false);
  const [creating, setCreating] = useState(false);
  const [formApi, setFormApi] = useState<FormApi<Record<string, unknown>> | null>(null);
  const [staged, setStaged] = useState<TicketAttachment[]>([]);
  const [uploading, setUploading] = useState(false);
  const messagesRef = useRef<HTMLDivElement | null>(null);

  const tl = (key: string, fallback: string) => {
    const v = t(key);
    return v === key ? fallback : v;
  };

  const statusLabel = (s: string) => tl(`support.status.${s}`, s);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setTickets(await listTickets(appliedStatus === 'all' ? undefined : appliedStatus));
    } catch (e) {
      setTickets([]);
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setLoading(false);
    }
  }, [appliedStatus, t]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    if (tab === 'new') {
      setNewVisible(true);
    }
  }, [tab]);

  useEffect(() => {
    if (detail?.messages.length && messagesRef.current) {
      messagesRef.current.scrollTop = messagesRef.current.scrollHeight;
    }
  }, [detail]);

  const openView = async (ticket: Ticket) => {
    setViewVisible(true);
    setDetail(null);
    setReplyBody('');
    setDetailLoading(true);
    try {
      setDetail(await getTicket(ticket.id));
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
      setViewVisible(false);
    } finally {
      setDetailLoading(false);
    }
  };

  const sendReply = async () => {
    if (!detail) return;
    const body = replyBody.trim();
    if (!body) {
      Toast.warning(t('support.replyRequired'));
      return;
    }
    setReplying(true);
    try {
      await replyTicket(detail.ticket.id, body);
      Toast.success(t('support.replied'));
      setReplyBody('');
      setDetail(await getTicket(detail.ticket.id));
      load();
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setReplying(false);
    }
  };

  const closeNew = () => {
    setNewVisible(false);
    setTab('list');
    setStaged([]);
    formApi?.reset();
  };

  const submitTicket = async () => {
    const v = (formApi?.getValues() ?? {}) as Record<string, unknown>;
    const subject = String(v.subject ?? '').trim();
    const body = String(v.body ?? '').trim();
    if (!subject || !body) {
      Toast.warning(t('support.requiredHint'));
      return;
    }
    setCreating(true);
    try {
      await createTicket({
        subject,
        body,
        category: String(v.category ?? 'general'),
        priority: String(v.priority ?? 'normal'),
        staged_attachment_ids: staged.map((a) => a.id),
      });
      Toast.success(t('support.created'));
      closeNew();
      await load();
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setCreating(false);
    }
  };

  const onUploaded = (response: unknown) => {
    const attachments = (response as { attachments?: TicketAttachment[] })?.attachments ?? [];
    setStaged((prev) => [...prev, ...attachments].slice(0, 5));
    setUploading(false);
  };

  const removeStaged = async (id: string) => {
    try {
      await unstageTicketAttachment(id);
      setStaged((prev) => prev.filter((a) => a.id !== id));
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    }
  };

  const columns = [
    {
      title: t('support.col.no'),
      dataIndex: 'ticket_no',
      width: 100,
      render: (_: unknown, r: Ticket) => <span className="wg-mono-sm">#{r.ticket_no}</span>,
    },
    {
      title: t('support.col.subject'),
      dataIndex: 'subject',
      render: (_: unknown, r: Ticket) => (
        <span className="wg-cell-ellipsis" style={{ display: 'inline-block', maxWidth: 420 }} title={r.subject}>
          {r.subject}
        </span>
      ),
    },
    {
      title: t('support.col.category'),
      dataIndex: 'category',
      width: 120,
      render: (_: unknown, r: Ticket) => <span className="wg-cell-dim">{tl(`support.category.${r.category}`, r.category)}</span>,
    },
    {
      title: t('support.col.status'),
      dataIndex: 'status',
      width: 110,
      render: (_: unknown, r: Ticket) => (
        <Tag color={statusColor(r.status)} size="small" shape="circle">
          {statusLabel(r.status)}
        </Tag>
      ),
    },
    {
      title: t('support.col.updated'),
      dataIndex: 'updated_at',
      width: 170,
      render: (_: unknown, r: Ticket) => (
        <span className="wg-num wg-cell-dim">{formatDateTime(r.updated_at)}</span>
      ),
    },
    {
      title: t('common.actions'),
      width: 90,
      render: (_: unknown, r: Ticket) => (
        <Button theme="borderless" size="small" onClick={() => openView(r)}>
          {t('common.view')}
        </Button>
      ),
    },
  ];

  return (
    <div className="wg-page">
      <PageHead
        title={t('acc.nav.support')}
        extra={
          <div className="wg-page-actions">
            <Button type="primary" theme="solid" icon={<IconPlus />} onClick={() => setTab('new')}>
              {t('support.new')}
            </Button>
          </div>
        }
      />

      <section className="wg-pro-card wg-table-card">
        <Tabs
          type="line"
          activeKey={tab}
          onChange={(k) => setTab(k as 'list' | 'new')}
        >
          <Tabs.TabPane itemKey="list" tab={t('support.tab.mine')}>
            <div className="wg-filter-card">
              <Select
                value={statusFilter}
                onChange={(v) => setStatusFilter(String(v))}
                style={{ width: 180 }}
                aria-label={t('support.col.status')}
                optionList={STATUS_TABS.map((s) => ({
                  value: s,
                  label: s === 'all' ? t('support.filter.allStatus') : statusLabel(s),
                }))}
              />
              <div className="wg-filter-actions">
                <Button type="primary" onClick={() => setAppliedStatus(statusFilter)}>
                  {t('common.search')}
                </Button>
                <Button
                  onClick={() => {
                    setStatusFilter('all');
                    setAppliedStatus('all');
                  }}
                >
                  {t('common.reset')}
                </Button>
              </div>
              <Button
                style={{ marginLeft: 'auto' }}
                theme="borderless"
                icon={<IconRefresh />}
                loading={loading}
                onClick={load}
              >
                {t('common.refresh')}
              </Button>
            </div>
            <Table
              loading={loading}
              columns={columns}
              dataSource={tickets ?? []}
              pagination={false}
              rowKey="id"
              tableLayout="fixed"
              empty={<Empty description={t('support.empty')} />}
            />
          </Tabs.TabPane>
          <Tabs.TabPane itemKey="new" tab={t('support.tab.new')}>
            <div style={{ padding: 40 }}>
              <Empty description={t('support.newHint')} />
            </div>
          </Tabs.TabPane>
        </Tabs>
      </section>

      <SideSheet
        title={detail ? `#${detail.ticket.ticket_no} ${detail.ticket.subject}` : t('support.detailTitle')}
        visible={viewVisible}
        onCancel={() => setViewVisible(false)}
        width="min(640px, 92vw)"
        closeOnEsc
        maskClosable={false}
        bodyStyle={{ padding: '12px 24px 24px' }}
        footer={
          <div className="wg-drawer-footer wg-drawer-footer-single">
            <Button onClick={() => setViewVisible(false)}>{t('common.close')}</Button>
          </div>
        }
      >
        {detailLoading || !detail ? (
          <div style={{ padding: 40 }}>
            <Empty description={detailLoading ? t('common.loading') : t('common.emptyData')} />
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
              <Tag color={statusColor(detail.ticket.status)} size="small" shape="circle">
                {statusLabel(detail.ticket.status)}
              </Tag>
              <Tag size="small">{tl(`support.category.${detail.ticket.category}`, detail.ticket.category)}</Tag>
              <Tag size="small">{tl(`support.priority.${detail.ticket.priority}`, detail.ticket.priority)}</Tag>
              <span className="wg-cell-dim wg-num">{formatDateTime(detail.ticket.created_at)}</span>
            </div>

            <div
              ref={messagesRef}
              style={{
                display: 'flex',
                flexDirection: 'column',
                gap: 12,
                maxHeight: 380,
                overflowY: 'auto',
                paddingRight: 4,
              }}
            >
              {detail.messages.map((m) => {
                const fromUser = m.author_role === 'user';
                return (
                  <div
                    key={m.id}
                    style={{
                      alignSelf: fromUser ? 'flex-end' : 'flex-start',
                      maxWidth: '86%',
                      background: fromUser ? 'var(--wg-primary-light)' : 'var(--wg-surface-2)',
                      border: '1px solid var(--wg-border)',
                      borderRadius: 10,
                      padding: '10px 12px',
                    }}
                  >
                    <div className="wg-cell-dim" style={{ marginBottom: 4 }}>
                      {fromUser ? t('support.authorYou') : t('support.authorSupport')} · {formatDateTime(m.created_at)}
                    </div>
                    <div style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{m.body}</div>
                    {!!m.attachments?.length && (
                      <div className="wg-cell-dim" style={{ marginTop: 6 }}>
                        {m.attachments.map((a) => a.filename).join(' · ')}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>

            <div className="wg-form-field">
              <label className="wg-form-label" htmlFor="support-reply">
                {t('support.reply')}
              </label>
              <TextArea
                id="support-reply"
                value={replyBody}
                onChange={setReplyBody}
                rows={4}
                maxLength={10000}
                placeholder={t('support.replyPlaceholder')}
              />
              <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                <Button type="primary" theme="solid" loading={replying} onClick={sendReply}>
                  {t('support.send')}
                </Button>
              </div>
            </div>
          </div>
        )}
      </SideSheet>

      <SideSheet
        title={t('support.newTitle')}
        visible={newVisible}
        onCancel={closeNew}
        width="min(600px, 92vw)"
        closeOnEsc
        maskClosable={false}
        bodyStyle={{ padding: '12px 24px 24px' }}
        footer={
          <div className="wg-drawer-footer">
            <Button theme="light" onClick={closeNew}>
              {t('common.cancel')}
            </Button>
            <Button type="primary" theme="solid" loading={creating} onClick={() => formApi?.submitForm()}>
              {t('support.submit')}
            </Button>
          </div>
        }
      >
        <Form
          getFormApi={(api) => setFormApi(api as FormApi<Record<string, unknown>>)}
          onSubmit={submitTicket}
          initValues={{ category: 'general', priority: 'normal' }}
        >
          <div className="wg-section-body">
            <Banner type="info" fullMode={false} description={t('support.formHint')} />
            <div className="wg-form-field">
              <label className="wg-form-label">
                {t('support.col.subject')}
                <span className="wg-form-required">*</span>
              </label>
              <Form.Input field="subject" maxLength={255} placeholder={t('support.subjectPlaceholder')} />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              <div className="wg-form-field">
                <label className="wg-form-label">{t('support.col.category')}</label>
                <Form.Select
                  field="category"
                  optionList={CATEGORIES.map((c) => ({ value: c, label: tl(`support.category.${c}`, c) }))}
                />
              </div>
              <div className="wg-form-field">
                <label className="wg-form-label">{t('support.priorityLabel')}</label>
                <Form.Select
                  field="priority"
                  optionList={PRIORITIES.map((p) => ({ value: p, label: tl(`support.priority.${p}`, p) }))}
                />
              </div>
            </div>
            <div className="wg-form-field">
              <label className="wg-form-label">
                {t('support.message')}
                <span className="wg-form-required">*</span>
              </label>
              <Form.TextArea field="body" rows={6} maxLength={10000} placeholder={t('support.messagePlaceholder')} />
            </div>
            <div className="wg-form-field">
              <label className="wg-form-label">{t('support.attachments')}</label>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <Upload
                  action="/api/tickets/attachments/stage"
                  headers={{ Authorization: `Bearer ${getToken() ?? ''}` }}
                  name="files"
                  multiple
                  showUploadList={false}
                  limit={5}
                  disabled={uploading || staged.length >= 5}
                  beforeUpload={() => {
                    setUploading(true);
                    return true;
                  }}
                  onSuccess={onUploaded}
                  onError={() => {
                    setUploading(false);
                    Toast.error(t('support.uploadFailed'));
                  }}
                >
                  <Button icon={<IconUpload />} loading={uploading} disabled={staged.length >= 5}>
                    {t('support.upload')}
                  </Button>
                </Upload>
                {staged.map((a) => (
                  <Tag key={a.id} closable onClose={() => removeStaged(a.id)}>
                    {a.filename}
                  </Tag>
                ))}
              </div>
              <span className="wg-cell-dim">{t('support.attachmentHint')}</span>
            </div>
          </div>
        </Form>
      </SideSheet>
    </div>
  );
}
