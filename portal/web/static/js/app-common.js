/**
 * app-common.js - Shared utilities extracted from templates
 *
 * This file contains helper functions used across multiple pages.
 * Keep it dependency-free (only relies on layout.html's _t() and showMessage/showConfirm).
 */

// ------------------------------------------------------------------
// HTML escaping
// ------------------------------------------------------------------
function esc(s) {
  return (s || '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// ------------------------------------------------------------------
// Modal helpers
// ------------------------------------------------------------------
function closeModal(id) {
  if (id) {
    var el = document.getElementById(id);
    if (el) el.style.display = 'none';
  }
}

// ------------------------------------------------------------------
// Clipboard
// ------------------------------------------------------------------
function copyText(text, btn) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(function() {
      if (btn) showCopyOK(btn);
      else showMessage(_t('Notice'), _t('Copied to clipboard'));
    }).catch(function() {
      fallbackCopyText(text, btn);
    });
  } else {
    fallbackCopyText(text, btn);
  }
}

function fallbackCopyText(text, btn) {
  var textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.style.position = 'fixed';
  textarea.style.left = '-9999px';
  document.body.appendChild(textarea);
  textarea.select();
  try {
    document.execCommand('copy');
    if (btn) showCopyOK(btn);
    else showMessage(_t('Notice'), _t('Copied to clipboard'));
  } catch (e) {
    showMessage(_t('Copy failed'), _t('Please copy manually'));
  }
  document.body.removeChild(textarea);
}

function showCopyOK(btn) {
  if (btn) {
    var orig = btn.textContent;
    btn.textContent = _t('Copied');
    setTimeout(function() { btn.textContent = orig; }, 1200);
  }
}

// ------------------------------------------------------------------
// Date / Time formatting (timezone-aware)
// ------------------------------------------------------------------
function formatDateTime(dateStr) {
  if (!dateStr) return '-';
  var d = new Date(dateStr);
  if (isNaN(d.getTime())) return '-';
  var tz = window.userTimezone || 'UTC';
  // formatToParts: zero-padded components and 24-hour clock (h23).
  // (Per-field toLocaleString inherits the en-US 12-hour clock and drops padding.)
  var parts = {};
  new Intl.DateTimeFormat('en-US', {
    timeZone: tz, hourCycle: 'h23',
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit'
  }).formatToParts(d).forEach(function(part) { parts[part.type] = part.value; });
  return parts.year + '-' + parts.month + '-' + parts.day + ' ' +
         parts.hour + ':' + parts.minute + ':' + parts.second;
}

function formatDate(dateStr) {
  if (!dateStr) return '-';
  var d = new Date(dateStr);
  if (isNaN(d.getTime())) return '-';
  var tz = window.userTimezone || 'UTC';
  var parts = {};
  new Intl.DateTimeFormat('en-US', {
    timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit'
  }).formatToParts(d).forEach(function(part) { parts[part.type] = part.value; });
  return parts.year + '-' + parts.month + '-' + parts.day;
}

// ------------------------------------------------------------------
// Simple JSON API wrapper
// ------------------------------------------------------------------
function api(url, opts) {
  opts = opts || {};
  opts.credentials = 'same-origin';
  opts.headers = opts.headers || {};
  if (opts.body && typeof opts.body === 'object') {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(opts.body);
  }
  return fetch(url, opts).then(function(r) { return r.json(); });
}

// ------------------------------------------------------------------
// Export helper — unified CSV/JSON download with error handling
// ------------------------------------------------------------------
function formatExportTimestamp() {
  var d = new Date();
  var pad = function(n) { return n < 10 ? '0' + n : '' + n; };
  return '' + d.getFullYear() + pad(d.getMonth() + 1) + pad(d.getDate()) +
         pad(d.getHours()) + pad(d.getMinutes());
}

// Open the browser "Save As" dialog (File System Access API, Chromium +
// secure context). Returns a file handle, null when the user cancelled,
// or undefined when the picker is unsupported/failed (use legacy download).
// A 30s watchdog falls back to the legacy download when the native picker
// never settles (a broken desktop portal can leave it pending forever).
async function pickSaveHandle(filename) {
  if (!window.showSaveFilePicker) return undefined;
  var ext = (filename.match(/\.[a-z0-9]+$/i) || [''])[0];
  var opts = { suggestedName: filename };
  if (ext) { opts.types = [{ description: ext.slice(1).toUpperCase() + ' file', accept: { 'application/octet-stream': [ext] } }]; }
  var watchdog = new Promise(function(resolve) { setTimeout(function() { resolve('__picker_timeout__'); }, 30000); });
  var result = await Promise.race([
    window.showSaveFilePicker(opts).then(function(h) { return h; }, function(e) { return e; }),
    watchdog
  ]);
  if (result === '__picker_timeout__') return undefined;
  if (result && typeof result.createWritable === 'function') return result;
  if (result && result.name === 'AbortError') return null;
  return undefined;
}

async function writeSaveHandle(handle, blob) {
  var writable = await handle.createWritable();
  await writable.write(blob);
  await writable.close();
}

function legacyDownload(blob, filename) {
  var url = window.URL.createObjectURL(blob);
  var a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.URL.revokeObjectURL(url);
}

// Save an in-memory Blob via the Save As dialog. Returns true when saved,
// false when the user cancelled the picker.
async function saveBlobAs(blob, filename) {
  var handle = await pickSaveHandle(filename);
  if (handle === null) return false;
  if (handle) {
    await writeSaveHandle(handle, blob);
    return true;
  }
  legacyDownload(blob, filename);
  return true;
}

// Unified export: fetch the file first (toast on failure), then offer the
// Save As dialog; falls back to a regular download when the picker is
// unavailable, errors out, or never settles.
async function downloadExport(url, filename) {
  filename = filename || 'export-' + formatExportTimestamp() + '.csv';
  showToast('info', _t('Exporting...'));
  var blob;
  try {
    var r = await fetch(url, { credentials: 'same-origin' });
    if (!r.ok) throw new Error('export failed');
    blob = await r.blob();
  } catch (e) {
    showToast('error', _t('Export failed, please try again'));
    return;
  }
  var saved = await saveBlobAs(blob, filename);
  if (saved) showToast('success', _t('Export successful, file downloaded'));
}

// ------------------------------------------------------------------
// Currency / quota formatting (micro-USD → display)
// ------------------------------------------------------------------
// Global defaults; layout.html injects the user's actual currency/rate.
window.userCurrency = window.userCurrency || 'USD';
window.exchangeRate = window.exchangeRate || 1.0;

/**
 * Unified currency formatting object.
 *
 * All amounts are stored/transferred as micro-USD quota (1 quota = 0.000001 USD).
 * Display formatting respects the user's preferred currency and exchange rate.
 *
 * Examples:
 *   Currency.format(1000000)        // "¥7.20" or "$1.000000"
 *   Currency.formatShort(1000000)   // "¥7.20" or "$1.00"
 *   Currency.formatValue(1000000)   // "7.20"  or "1.000000"
 *   Currency.symbol()               // "¥"     or "$"
 *   Currency.unit()                 // "元"    or "USD"
 *   Currency.formatRaw(10000)       // "¥100.00" (already target-currency cents)
 *   Currency.parseToQuota(7.20)     // 1000000 (CNY) or inverse (USD)
 */
window.Currency = {
  get code() { return window.userCurrency || 'USD'; },
  get rate() { return window.exchangeRate || 1.0; },

  symbols: {
    'USD': '$',
    'CNY': '¥',
    'SGD': 'S$',
    'EUR': '€',
    'GBP': '£',
    'JPY': '¥',
    'KRW': '₩',
    'MYR': 'RM',
    'IDR': 'Rp',
    'THB': '฿',
    'VND': '₫',
    'TWD': 'NT$',
    'HKD': 'HK$',
    'MOP': 'MOP$',
    'AUD': 'A$',
    'NZD': 'NZ$',
    'RUB': '₽'
  },

  symbol: function(currency) {
    return this.symbols[currency || this.code] || (currency || this.code) + ' ';
  },

  unit: function(currency) {
    return (currency || this.code) === 'USD' ? 'USD' : _t(currency || this.code);
  },

  /**
   * Convert micro-USD quota to a display amount in the target currency.
   * Returns a float (not formatted string).
   */
  toAmount: function(quota, opts) {
    opts = opts || {};
    var currency = opts.currency || this.code;
    var rate = opts.exchangeRate || this.rate;
    var usd = Number(quota || 0) / 1000000;
    return currency === 'USD' ? usd : usd * rate;
  },

  /**
   * Format the numeric value only (no symbol).
   */
  formatValue: function(quota, opts) {
    opts = opts || {};
    var currency = opts.currency || this.code;
    var decimals = opts.decimals;
    if (decimals === undefined) {
      decimals = currency === 'USD' ? 6 : 2;
    }
    return this.toAmount(quota, opts).toFixed(decimals);
  },

  /**
   * Format quota with symbol (default precision).
   */
  format: function(quota, opts) {
    opts = opts || {};
    var currency = opts.currency || this.code;
    var symbol = opts.symbol !== false ? this.symbol(currency) : '';
    return symbol + this.formatValue(quota, opts);
  },

  /**
   * Format quota with symbol, fixed 2 decimals (most common UI display).
   */
  formatShort: function(quota, opts) {
    return this.format(quota, Object.assign({ decimals: 2 }, opts || {}));
  },

  /**
   * Format quota with symbol. For tiny amounts (< 0.01 in target currency),
   * use higher precision so users can see non-zero costs.
   */
  formatPrecise: function(quota, opts) {
    opts = opts || {};
    var currency = opts.currency || this.code;
    var amount = this.toAmount(quota, opts);
    var decimals = 2;
    if (Math.abs(amount) > 0 && Math.abs(amount) < 0.01) {
      decimals = currency === 'USD' ? 6 : 4;
    }
    return this.format(quota, Object.assign({ decimals: decimals }, opts || {}));
  },

  /**
   * Format an amount that is already in the target currency's smallest unit
   * (e.g. voucher face value stored in target-currency cents).
   */
  formatRaw: function(cents, opts) {
    opts = opts || {};
    var currency = opts.currency || this.code;
    var amount = Number(cents || 0) / 100;
    var decimals = opts.decimals !== undefined ? opts.decimals : 2;
    var symbol = opts.symbol !== false ? this.symbol(currency) : '';
    return symbol + amount.toFixed(decimals);
  },

  /**
   * Parse a user-input amount back into micro-USD quota.
   */
  parseToQuota: function(amount, opts) {
    opts = opts || {};
    var currency = opts.currency || this.code;
    var rate = opts.exchangeRate || this.rate;
    var val = parseFloat(amount) || 0;
    if (currency === 'USD') {
      return Math.round(val * 1000000);
    }
    return Math.round((val / rate) * 1000000);
  }
};

// Backwards-compatible helper; now delegates to Currency.
function formatQuota(quota) {
  if (quota === undefined || quota === null || quota === 0) return '0';
  var n = Number(quota);
  if (isNaN(n)) return String(quota);
  return Currency.formatShort(n);
}

// ------------------------------------------------------------------
// Number / token / latency formatting
// ------------------------------------------------------------------
function fmtNum(n) {
  if (n === undefined || n === null || n === '') return '0';
  var v = Number(n);
  if (isNaN(v)) return String(n);
  if (v >= 1000000000) return (v / 1000000000).toFixed(1) + 'B';
  if (v >= 1000000) return (v / 1000000).toFixed(1) + 'M';
  if (v >= 1000) return (v / 1000).toFixed(0) + 'K';
  return v.toLocaleString();
}

function fmtTokens(n) {
  if (n === undefined || n === null || n === '') return '0';
  var v = Number(n);
  if (isNaN(v)) return String(n);
  if (v >= 1000000000) return (v / 1000000000).toFixed(2) + 'B';
  if (v >= 1000000) return (v / 1000000).toFixed(2) + 'M';
  return Math.round(v).toLocaleString();
}

function fmtLatency(ms) {
  if (ms === undefined || ms === null || ms === '') return '-';
  var v = Number(ms);
  if (isNaN(v) || v <= 0) return '-';
  if (v >= 1000) return (v / 1000).toFixed(2) + 's';
  return Math.round(v) + 'ms';
}

function fmtPercent(n) {
  if (n === undefined || n === null || n === '') return '0.0%';
  var v = Number(n);
  if (isNaN(v)) return String(n);
  return v.toFixed(1) + '%';
}

// ------------------------------------------------------------------
// Badge / pill helpers
// ------------------------------------------------------------------
function badge(label, variant) {
  var cls = 'badge';
  if (variant) cls += ' badge-' + variant;
  return '<span class="' + cls + '">' + esc(label) + '</span>';
}

function statusBadge(status) {
  if (!status || status === 'success') return badge(_t('Success'), 'success');
  if (status === 'error' || status === 'failed') return badge(_t('Failed'), 'error');
  if (status === 'timeout') return badge(_t('Timeout'), 'warning');
  if (status === 'rate_limited') return badge(_t('Rate Limited'), 'warning');
  return badge(status, 'secondary');
}

function typeBadge(isStreaming) {
  return isStreaming ? badge(_t('Streaming'), 'info') : badge(_t('Non-streaming'), 'secondary');
}

// ------------------------------------------------------------------
// Chart color palette (syncs with CSS variables)
// ------------------------------------------------------------------
function chartColor(name, alpha) {
  var rootStyle = getComputedStyle(document.documentElement);
  var c = rootStyle.getPropertyValue(name).trim();
  if (!c) return name;
  if (c.charAt(0) === '#') {
    var r, g, b;
    if (c.length === 4) {
      r = parseInt(c.charAt(1) + c.charAt(1), 16);
      g = parseInt(c.charAt(2) + c.charAt(2), 16);
      b = parseInt(c.charAt(3) + c.charAt(3), 16);
    } else {
      r = parseInt(c.substr(1, 2), 16);
      g = parseInt(c.substr(3, 2), 16);
      b = parseInt(c.substr(5, 2), 16);
    }
    return typeof alpha === 'number' ? 'rgba(' + r + ',' + g + ',' + b + ',' + alpha + ')' : 'rgb(' + r + ',' + g + ',' + b + ')';
  }
  var m = c.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
  if (m) {
    return typeof alpha === 'number' ? 'rgba(' + m[1] + ',' + m[2] + ',' + m[3] + ',' + alpha + ')' : 'rgb(' + m[1] + ',' + m[2] + ',' + m[3] + ')';
  }
  return c;
}

var chartPalette = [
  '--color-primary', '--color-violet', '--color-success', '--color-warning',
  '--color-error', '--color-cyan', '--color-orange', '--color-indigo'
];

// ------------------------------------------------------------------
// Flatpickr date / datetime / range pickers
// ------------------------------------------------------------------
function fpFormatDate(d) {
  var year = d.getFullYear();
  var month = ('0' + (d.getMonth() + 1)).slice(-2);
  var day = ('0' + d.getDate()).slice(-2);
  return year + '-' + month + '-' + day;
}

function fpFormatDateTime(d) {
  return fpFormatDate(d) + ' ' +
    ('0' + d.getHours()).slice(-2) + ':' +
    ('0' + d.getMinutes()).slice(-2);
}

function initFlatpickr() {
  if (typeof flatpickr === 'undefined') return;

  var lang = document.documentElement.lang === 'zh' ? 'zh' : 'default';
  var isDark = document.documentElement.getAttribute('data-theme') === 'dark';

  // Single date
  document.querySelectorAll('.fp-date').forEach(function(el) {
    flatpickr(el, {
      locale: lang,
      dateFormat: 'Y-m-d',
      theme: isDark ? 'dark' : 'light',
      allowInput: true
    });
  });

  // Single datetime (ISO-local "YYYY-MM-DDTHH:MM" to match native datetime-local)
  document.querySelectorAll('.fp-datetime').forEach(function(el) {
    flatpickr(el, {
      locale: lang,
      dateFormat: 'Y-m-dTH:i',
      enableTime: true,
      time_24hr: true,
      theme: isDark ? 'dark' : 'light',
      allowInput: true
    });
  });

  // Date range: one visible input, syncs to two hidden inputs
  document.querySelectorAll('.fp-range').forEach(function(el) {
    var fromId = el.dataset.from;
    var toId = el.dataset.to;
    var initialFrom = fromId ? document.getElementById(fromId).value : '';
    var initialTo = toId ? document.getElementById(toId).value : '';
    var defaultDate = [];
    if (initialFrom) defaultDate.push(initialFrom);
    if (initialTo) defaultDate.push(initialTo);

    var fp = flatpickr(el, {
      mode: 'range',
      locale: lang,
      dateFormat: 'Y-m-d',
      theme: isDark ? 'dark' : 'light',
      allowInput: true,
      showMonths: 2,
      defaultDate: defaultDate.length ? defaultDate : undefined,
      onChange: function(selectedDates) {
        if (fromId) {
          document.getElementById(fromId).value = selectedDates[0] ? fpFormatDate(selectedDates[0]) : '';
        }
        if (toId) {
          document.getElementById(toId).value = selectedDates[1] ? fpFormatDate(selectedDates[1]) : '';
        }
        if (el.dataset.onchange) {
          var fn = window[el.dataset.onchange];
          if (typeof fn === 'function') fn();
        }
      }
    });

    // Expose instance for page-specific reset/clear
    el._fpInstance = fp;
  });
}

// Auto-init on DOM ready
document.addEventListener('DOMContentLoaded', initFlatpickr);

// ------------------------------------------------------------------
// Policy / legal document loader (P2: document-based terms & privacy)
// ------------------------------------------------------------------
function loadPolicyDoc(container, docName) {
  if (!container) return;
  var lang = document.documentElement.lang || 'en';
  var url = '/static/docs/' + docName + '-' + lang + '.html?v=2';
  fetch(url)
    .then(function(r) {
      if (!r.ok) throw new Error('load failed');
      return r.text();
    })
    .then(function(html) {
      container.innerHTML = html;
    })
    .catch(function() {
      // Fallback to English
      fetch('/static/docs/' + docName + '-en.html?v=2')
        .then(function(r) { return r.text(); })
        .then(function(html) { container.innerHTML = html; })
        .catch(function() {
          container.innerHTML = '<p class="text-muted">' + _t('Document not available') + '</p>';
        });
    });
}

// Auto-load any policy-doc containers found on the page
document.addEventListener('DOMContentLoaded', function() {
  document.querySelectorAll('[data-policy-doc]').forEach(function(el) {
    loadPolicyDoc(el, el.getAttribute('data-policy-doc'));
  });
});

// ------------------------------------------------------------------
// Scrollable table wrapper keyboard accessibility
// Makes horizontal-scroll tables reachable by keyboard (axe-core
// scrollable-region-focusable rule).
// ------------------------------------------------------------------
function debounce(fn, wait) {
  var t;
  return function() {
    clearTimeout(t);
    var args = arguments;
    var self = this;
    t = setTimeout(function() { fn.apply(self, args); }, wait);
  };
}

function initScrollableTables() {
  document.querySelectorAll('.data-table-wrapper').forEach(function(wrapper) {
    if (wrapper.scrollWidth > wrapper.clientWidth + 1) {
      if (!wrapper.hasAttribute('tabindex')) {
        wrapper.setAttribute('tabindex', '0');
        wrapper.setAttribute('role', 'region');
        if (!wrapper.hasAttribute('aria-label')) {
          wrapper.setAttribute('aria-label', (_t && _t('Data table')) || 'Data table');
        }
      }
    }
  });
}

document.addEventListener('DOMContentLoaded', initScrollableTables);
window.addEventListener('resize', debounce(initScrollableTables, 250));

// ------------------------------------------------------------------
// Empty state helpers
// ------------------------------------------------------------------
function emptyStateHTML(message, img, help) {
  var imgTag = img ? '<img src="' + esc(img) + '" alt="" aria-hidden="true">' : '';
  var helpTag = help ? '<p>' + esc(help) + '</p>' : '';
  return '<div class="empty-state-illustrated">' +
    imgTag +
    '<h3>' + esc(message) + '</h3>' +
    helpTag +
    '</div>';
}

function emptyStateSmHTML(message, img) {
  var imgTag = img ? '<img src="' + esc(img) + '" alt="" aria-hidden="true" style="width:48px;height:48px;margin-bottom:8px;opacity:0.6">' : '';
  return '<div class="empty-state-sm">' +
    imgTag +
    '<p class="empty-state-title">' + esc(message) + '</p>' +
    '</div>';
}

// ------------------------------------------------------------------
// Shared pagination — the single site-wide pager for JS-driven tables.
// Info format (must stay identical everywhere): "Total N items, Page X / Y".
//
// Container contract (fixed HTML in the page, no inline onclick):
//   <div class="pagination" id="X-pagination" style="display:none">
//     <button class="btn btn-sm btn-secondary" data-page="prev"></button>
//     <span class="pagination-info"></span>
//     <span class="page-numbers"></span>   <!-- only when showPageNumbers -->
//     <button class="btn btn-sm btn-secondary" data-page="next"></button>
//   </div>
// renderPagination() fills labels, disabled states and visibility, and binds
// click handlers exactly once (state lives on the wrap element).
// Returns goto(p) so callers can jump programmatically.
// ------------------------------------------------------------------
window.renderPagination = function(opts) {
  var wrap = document.getElementById(opts.wrapId);
  if (!wrap) return function() {};
  var state = wrap._pagState || (wrap._pagState = { page: 1, totalPages: 1, onPage: null });
  state.page = opts.page || 1;
  state.totalPages = opts.totalPages || 1;
  state.onPage = opts.onPage;

  function goto(p) {
    if (p < 1 || p > state.totalPages) return;
    if (state.onPage) state.onPage(p);
  }

  var prevBtn = wrap.querySelector('[data-page="prev"]');
  var nextBtn = wrap.querySelector('[data-page="next"]');
  var infoEl = wrap.querySelector('.pagination-info');
  var numsEl = wrap.querySelector('.page-numbers');

  if (prevBtn && !prevBtn._pagBound) {
    prevBtn._pagBound = true;
    prevBtn.addEventListener('click', function() { goto(state.page - 1); });
  }
  if (nextBtn && !nextBtn._pagBound) {
    nextBtn._pagBound = true;
    nextBtn.addEventListener('click', function() { goto(state.page + 1); });
  }
  if (numsEl && !numsEl._pagBound) {
    numsEl._pagBound = true;
    numsEl.addEventListener('click', function(e) {
      var btn = e.target.closest('[data-goto]');
      if (btn) goto(parseInt(btn.getAttribute('data-goto'), 10));
    });
  }

  if (prevBtn) {
    prevBtn.textContent = _t('Previous');
    prevBtn.disabled = state.page <= 1;
  }
  if (nextBtn) {
    nextBtn.textContent = _t('Next');
    nextBtn.disabled = state.page >= state.totalPages;
  }
  if (infoEl) {
    infoEl.textContent = _t('Total') + ' ' + (opts.total != null ? opts.total : 0) + ' ' + _t('items') + ', ' + _t('Page') + ' ' + state.page + ' / ' + state.totalPages;
  }
  if (numsEl) {
    if (opts.showPageNumbers && state.totalPages > 1) {
      var html = '';
      for (var p = 1; p <= state.totalPages; p++) {
        html += '<button class="page-num' + (p === state.page ? ' active' : '') + '" data-goto="' + p + '">' + p + '</button>';
      }
      numsEl.innerHTML = html;
    } else {
      numsEl.innerHTML = '';
    }
  }
  wrap.style.display = state.totalPages > 1 ? 'flex' : 'none';
  return goto;
};

// ------------------------------------------------------------------
// Debounced input helper — the single site-wide pattern for toolbar
// filters. Returns a wrapped fn that delays execution until input has
// been idle for `ms` (default 300), so per-keystroke filters do not
// re-render the table on every key.
// ------------------------------------------------------------------
function debounceInput(fn, ms) {
  var timer = null;
  var delay = ms || 300;
  return function() {
    var self = this;
    var args = arguments;
    clearTimeout(timer);
    timer = setTimeout(function() { fn.apply(self, args); }, delay);
  };
}

// ------------------------------------------------------------------
// Client-app identification from the raw User-Agent.
// Maps known client UAs to a friendly name; unknown UAs show a trimmed
// fragment (full UA on hover via title). Keep the table here so new clients
// are added in one place. Returns {name, title}.
// ------------------------------------------------------------------
function mapClientApp(ua) {
  if (!ua) return { name: '—', title: '' };
  var u = ua.toLowerCase();
  // Ordered: most specific / branded first.
  var table = [
    { m: 'claude-cli', name: 'Claude Code' },
    { m: 'cherrystudio', name: 'Cherry Studio' },
    { m: 'cherry-studio', name: 'Cherry Studio' },
    { m: 'opencode', name: 'opencode' },
    { m: 'aider', name: 'Aider' },
    { m: 'kilo', name: 'Kilo Code' },
    { m: 'codex', name: 'Codex CLI' },
    { m: 'kimi', name: 'Kimi Code' },
    { m: 'codebuddy', name: 'CodeBuddy' },
    { m: 'trae', name: 'Trae' },
    { m: 'comate', name: 'Comate' },
    { m: 'electron', name: 'VS Code / Copilot' },
    { m: 'colly', name: 'Windsurf' },
  ];
  for (var i = 0; i < table.length; i++) {
    if (u.indexOf(table[i].m) !== -1) return { name: table[i].name, title: ua };
  }
  // Unknown: show a trimmed raw fragment, full UA on hover.
  var frag = ua.length > 40 ? ua.slice(0, 40) + '…' : ua;
  return { name: frag, title: ua };
}

// renderClientApp returns an escaped HTML snippet for a UA (name + title tooltip).
function renderClientApp(ua) {
  var r = mapClientApp(ua);
  if (r.name === '—') return '—';
  return '<span title="' + esc(r.title) + '">' + esc(r.name) + '</span>';
}

// ------------------------------------------------------------------
// Agent-tool display from the raw tool list reported by the proxy.
// The proxy stores the request's tool names (deduped, comma-separated);
// empty string means a plain chat request. Renders an "Agent · N" badge with
// the full tool list on hover.
// ------------------------------------------------------------------
// renderAgentTools merges request-side declared tools with response-side
// actually-called tools (proxy format "name:count,name:count"):
//   called present -> "read_file ×3, write_file ×1 · declared 10"
//   declared only  -> "Agent · 10 tools"
//   neither        -> '—'
function renderAgentTools(declaredRaw, calledRaw) {
  var declared = String(declaredRaw || '').split(',').map(function(s) { return s.trim(); }).filter(Boolean);
  var called = [];
  String(calledRaw || '').split(',').forEach(function(pair) {
    pair = pair.trim();
    if (!pair) return;
    var i = pair.lastIndexOf(':');
    if (i <= 0) { called.push([pair, 1]); return; }
    var n = parseInt(pair.slice(i + 1), 10);
    called.push([pair.slice(0, i), isNaN(n) || n < 1 ? 1 : n]);
  });
  if (!declared.length && !called.length) return '—';
  var labelParts = [], titleParts = [];
  if (called.length) {
    labelParts.push(called.map(function(p) { return esc(p[0]) + ' ×' + p[1]; }).join(', '));
    titleParts.push('Called: ' + called.map(function(p) { return p[0] + ' ×' + p[1]; }).join(', '));
  }
  if (declared.length) {
    labelParts.push('declared ' + declared.length);
    titleParts.push('Declared: ' + declared.join(', '));
  }
  return '<span title="' + esc(titleParts.join(' · ')) + '">' + labelParts.join(' · ') + '</span>';
}
