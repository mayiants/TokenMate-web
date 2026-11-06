import { getToken, request } from './client';

/**
 * Support tickets (internal/routes/tickets.go + tickets_staging.go).
 * User surface: /api/tickets. Attachments use the immediate-upload staging
 * flow: POST /api/tickets/attachments/stage (multipart, field "files"),
 * ids are then linked on create/reply via staged_attachment_ids.
 */
export type TicketStatus = 'open' | 'pending' | 'answered' | 'resolved' | 'closed';

export interface Ticket {
  id: string;
  ticket_no: number;
  subject: string;
  category: string;
  priority: string;
  status: TicketStatus | string;
  last_reply_role?: string;
  last_reply_at?: string | null;
  created_at: string;
  updated_at: string;
  enterprise_id?: string;
  assignee_id?: string;
  requester_email?: string;
}

export interface TicketAttachment {
  id: string;
  filename: string;
  size_bytes?: number;
  mime_type?: string;
}

export interface TicketMessage {
  id: string;
  author_role: string;
  body: string;
  internal_note?: boolean;
  created_at: string;
  author_email?: string;
  attachments?: TicketAttachment[];
}

export interface TicketDetail {
  ticket: Ticket;
  messages: TicketMessage[];
}

export async function listTickets(status?: string): Promise<Ticket[]> {
  const q = status ? `?status=${encodeURIComponent(status)}` : '';
  const res = await request<{ tickets: Ticket[] }>(`/api/tickets${q}`);
  return res.tickets ?? [];
}

export async function getTicket(id: string): Promise<TicketDetail> {
  return request<TicketDetail>(`/api/tickets/${id}`);
}

export async function createTicket(payload: {
  subject: string;
  category: string;
  priority: string;
  body: string;
  staged_attachment_ids?: string[];
}): Promise<void> {
  await request<{ success: boolean }>('/api/tickets', { method: 'POST', body: payload });
}

export async function replyTicket(id: string, body: string, stagedAttachmentIds: string[] = []): Promise<void> {
  await request<{ success: boolean }>(`/api/tickets/${id}/messages`, {
    method: 'POST',
    body: { body, staged_attachment_ids: stagedAttachmentIds },
  });
}

export async function closeTicket(id: string): Promise<void> {
  await request<{ success: boolean }>(`/api/tickets/${id}/close`, { method: 'POST' });
}

/** Stage files immediately; returns server attachment metadata. */
export async function stageTicketAttachments(files: File[]): Promise<TicketAttachment[]> {
  const form = new FormData();
  files.forEach((f) => form.append('files', f));
  const token = getToken();
  const res = await fetch('/api/tickets/attachments/stage', {
    method: 'POST',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    body: form,
    credentials: 'same-origin',
  });
  const text = await res.text();
  let data: { attachments?: TicketAttachment[]; error?: { message?: string } } = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch {
    throw new Error(`Invalid response (${res.status})`);
  }
  if (!res.ok) throw new Error(data.error?.message ?? `HTTP ${res.status}`);
  return data.attachments ?? [];
}

export async function unstageTicketAttachment(id: string): Promise<void> {
  await request<{ success: boolean }>(`/api/tickets/attachments/staged/${id}`, { method: 'DELETE' });
}
