import { request } from './client';

/**
 * Voucher redemption + personal voucher ledger (internal/routes/vouchers_user.go).
 * Voucher `amount` fields are raw micro-USD quota.
 */
export interface VoucherRedeemResult {
  success: boolean;
  message?: string;
  /** micro-USD (balance vouchers) */
  amount?: number;
  newBalance?: number;
  plan_name?: string;
  quota?: number;
  duration_days?: number;
  subscription_id?: string;
  action?: string;
}

export async function redeemVoucher(code: string): Promise<VoucherRedeemResult> {
  return request<VoucherRedeemResult>('/api/voucher/redeem', { method: 'POST', body: { code } });
}

export interface VoucherRecord {
  id: string;
  code: string;
  type: string;
  /** micro-USD */
  amount: number;
  consumer_type: string;
  reward_granted: boolean;
  balance_before: number;
  balance_after: number;
  created_at: string;
}

export interface VoucherRecordPage {
  records: VoucherRecord[];
  total: number;
  page: number;
  total_pages: number;
}

export async function listVoucherRecords(page = 1, limit = 20): Promise<VoucherRecordPage> {
  return request<VoucherRecordPage>(`/api/voucher/records?page=${page}&limit=${limit}`);
}
