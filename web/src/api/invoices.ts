import { download, request } from './client';

/**
 * User-side invoice requests (internal/routes/invoices.go).
 * GET /api/invoices/requests returns the caller's last 50 requests;
 * `amount` is integer USD cents. GET .../:id/download streams the PDF.
 */
export interface InvoiceRequest {
  id: string;
  title: string;
  /** integer USD cents */
  amount: number;
  amount_cents: number;
  status: string;
  delivery_method?: string;
  delivery_email?: string;
  profile_name?: string;
  invoice_number?: string;
  rejection_reason?: string;
  enterprise_id?: string;
  created_at: string;
  invoice_type?: string;
  has_pdf?: boolean;
}

export async function listInvoiceRequests(scope?: 'enterprise' | 'personal'): Promise<InvoiceRequest[]> {
  const res = await request<{ requests: InvoiceRequest[] }>(
    `/api/invoices/requests${scope ? `?scope=${scope}` : ''}`
  );
  return res.requests ?? [];
}

export async function downloadInvoice(id: string): Promise<void> {
  await download(`/api/invoices/requests/${id}/download`);
}
