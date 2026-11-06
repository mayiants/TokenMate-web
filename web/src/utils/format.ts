/** Shared formatting helpers for model pricing display. */

/** Price with up to 3 decimal places, no trailing zeros. */
export function formatPrice(v: number | null | undefined): string | null {
  if (v === null || v === undefined || Number.isNaN(v)) return null;
  const rounded = Math.round(v * 1000) / 1000;
  return String(rounded);
}

/** Compact context window, e.g. 1048576 -> "1M". */
export function formatContext(v: string | number | null | undefined): string | null {
  const n = typeof v === 'string' ? Number(v) : v;
  if (n === null || n === undefined || Number.isNaN(n) || n <= 0) return null;
  if (n >= 1_000_000 && n % 1_000_000 === 0) return `${n / 1_000_000}M`;
  if (n >= 1_000 && n % 1_000 === 0) return `${n / 1_000}K`;
  return String(n);
}

/** tokens→display unit label: $/M tokens for usage billing, $/request for fixed. */
export function unitForBilling(billingType: string | null | undefined): '/M tokens' | '/request' {
  return billingType === 'per_request' ? '/request' : '/M tokens';
}

/** Fuzzy match on model id / provider / manufacturer. */
export function fuzzyMatch(query: string, ...fields: Array<string | null | undefined>): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  const lower = q.replace(/\s+/g, '');
  return fields.some((f) => {
    const s = (f ?? '').toLowerCase();
    return s.includes(q) || (lower.length > 1 && s.replace(/\s+/g, '').includes(lower));
  });
}

export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // fall through to legacy path
  }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}
