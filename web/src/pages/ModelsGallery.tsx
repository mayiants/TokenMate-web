import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import {
  Button,
  Empty,
  Select,
  SideSheet,
  Spin,
  Tabs,
  Toast,
  Tooltip,
} from '@douyinfe/semi-ui';
import {
  IconClose,
  IconCopy,
  IconGridView,
  IconInfoCircle,
  IconList,
  IconRefresh,
  IconSearch,
} from '@douyinfe/semi-icons';
import { listPublicModels, BASE_URL, CHAT_COMPLETIONS_PATH, type PublicModel } from '../api/models';
import { copyText, formatContext, formatPrice, fuzzyMatch } from '../utils/format';
import ProviderIcon from '../components/ProviderIcon';
import CopyButton from '../components/CopyButton';
import DrawerSections, { type DrawerSection } from '../components/DrawerSections';
import { useI18n } from '../i18n';

type SortKey = 'newest' | 'priceAsc' | 'priceDesc';
type ViewMode = 'grid' | 'list';

// The public catalog has 160+ models. Rendering every card up front put ~6000
// DOM nodes on the main thread (Lighthouse TBT 891ms, performance 31 vs 61 with
// an empty catalog). Render a first batch and append more as the sentinel at
// the end of the list scrolls into view.
const PAGE_SIZE = 24;

/* ---------- tolerant promo-field extraction (live API has none yet) ---------- */

function num(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

interface PromoInfo {
  hot: boolean;
  offLabel: string | null;
  /** 0-1 discount ratio, used to derive the struck-through original price */
  offRatio: number | null;
}

function promoOf(m: PublicModel): PromoInfo {
  const tags = (m.tags ?? []).map((t) => t.toLowerCase());
  const hot = tags.includes('hot') || m.is_hot === true || m.hot === true;

  const pct = num(m.discount_percent) ?? num(m.discount) ?? num(m.off_percent);
  const rawLabel =
    typeof m.discount_label === 'string' ? m.discount_label : null;
  const tagLabel = (m.tags ?? []).find((t) => /\d/.test(t) && t.includes('off')) ?? null;

  let offLabel: string | null = null;
  let offRatio: number | null = null;
  if (pct !== null && pct > 0) {
    const shown = pct >= 1 ? Math.round(pct) : Math.round(pct * 100);
    offLabel = `${shown}% off`;
    offRatio = shown / 100;
  } else if (rawLabel) {
    offLabel = rawLabel;
  } else if (tagLabel) {
    offLabel = tagLabel;
    const match = /(\d+(?:\.\d+)?)/.exec(tagLabel);
    if (match) offRatio = Number(match[1]) / 100;
  }
  return { hot, offLabel, offRatio };
}

/** original (list) input price — explicit field, else derived from the discount */
function origInputPrice(m: PublicModel): number | null {
  const explicit = num(m.input_price_original) ?? num(m.original_input_price);
  if (explicit !== null) return explicit;
  const promo = promoOf(m);
  const cur = m.input_price;
  if (cur != null && promo.offRatio !== null && promo.offRatio < 1) {
    return cur / (1 - promo.offRatio);
  }
  return null;
}

function isPerRequest(m: PublicModel): boolean {
  return m.billing_type === 'per_request';
}

function capsFlag(caps: Record<string, unknown> | null | undefined, key: string): boolean {
  return Boolean(caps && caps[key] === true);
}

function FireIcon() {
  return (
    <svg className="wg-fire" width="10" height="12" viewBox="0 0 10 12" fill="none" aria-hidden>
      <path
        d="M5 0c.3 1.9-.4 3-1.5 4C2.2 5.2 1 6.3 1 8.2A4 4 0 0 0 5 12a4 4 0 0 0 4-3.8c0-1-.4-1.9-1-2.6-.2.7-.6 1.2-1.2 1.5.2-2.1-.5-4.6-1.8-7.1Z"
        fill="#fff"
      />
    </svg>
  );
}

/* ---------- page ---------- */

// Semi's combobox points aria-controls at a popup that only exists while
// open; axe/Lighthouse flag the dangling id when closed. Stripping the
// attribute in closed state is the semantically correct fix.
function useStripDanglingAriaControls(dep: unknown) {
  useEffect(() => {
    const strip = () => {
      document
        .querySelectorAll('.semi-select [role="combobox"][aria-expanded="false"][aria-controls]')
        .forEach((el) => el.removeAttribute('aria-controls'));
    };
    strip();
    const timer = setTimeout(strip, 500);
    return () => clearTimeout(timer);
  }, [dep]);
}

export default function ModelsGallery() {
  useStripDanglingAriaControls(null);
  const { t } = useI18n();
  const [models, setModels] = useState<PublicModel[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [query, setQuery] = useState('');
  const [typeFilter, setTypeFilter] = useState<string[]>([]);
  const [sortKey, setSortKey] = useState<SortKey>('newest');
  const [view, setView] = useState<ViewMode>('grid');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [detail, setDetail] = useState<PublicModel | null>(null);

  useEffect(() => {
    reload();
  }, []);

  function reload() {
    setError(null);
    setModels(null);
    listPublicModels()
      .then((ms) => {
        setModels(ms);
        setSelected(new Set());
      })
      .catch((e: Error) => setError(e.message));
  }

  /** dynamic type options from actual API model_type / supported_types values */
  const typeOptions = useMemo(() => {
    const set = new Set<string>();
    (models ?? []).forEach((m) => {
      if (m.model_type) set.add(m.model_type);
      (m.supported_types ?? []).forEach((st) => set.add(st));
    });
    return Array.from(set).sort();
  }, [models]);

  const filtered = useMemo(() => {
    let list = models ?? [];
    if (query.trim()) {
      list = list.filter((m) =>
        fuzzyMatch(query, m.api_model_name, m.provider, m.manufacturer)
      );
    }
    if (typeFilter.length > 0) {
      const want = new Set(typeFilter);
      list = list.filter((m) => {
        if (m.model_type && want.has(m.model_type)) return true;
        return (m.supported_types ?? []).some((st) => want.has(st));
      });
    }
    if (sortKey !== 'newest') {
      const dir = sortKey === 'priceAsc' ? 1 : -1;
      list = [...list].sort((a, b) => {
        const pa = (a.input_price ?? a.fixed_price ?? Number.POSITIVE_INFINITY) * dir;
        const pb = (b.input_price ?? b.fixed_price ?? Number.POSITIVE_INFINITY) * dir;
        return pa - pb;
      });
    }
    return list;
  }, [models, query, typeFilter, sortKey]);

  const hasFilters = query.trim() !== '' || typeFilter.length > 0;

  // incremental rendering: show `limit` items, grow when the sentinel is visible
  const [limit, setLimit] = useState(PAGE_SIZE);
  const sentinelRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    setLimit(PAGE_SIZE);
  }, [query, typeFilter, sortKey, view, models]);
  const visibleModels = useMemo(() => filtered.slice(0, limit), [filtered, limit]);
  const hasMore = limit < filtered.length;
  const growList = useCallback(() => setLimit((n) => n + PAGE_SIZE), []);
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !hasMore) return;
    // No IntersectionObserver (very old browsers / some test envs): show everything.
    if (typeof IntersectionObserver === 'undefined') {
      setLimit(Number.MAX_SAFE_INTEGER);
      return;
    }
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) growList();
      },
      { rootMargin: '600px 0px' }
    );
    io.observe(el);
    return () => io.disconnect();
  }, [hasMore, limit, growList]);

  const selectedNames = useMemo(
    () => (models ?? []).filter((m) => selected.has(m.api_model_name)).map((m) => m.api_model_name),
    [models, selected]
  );

  function clearFilters() {
    setQuery('');
    setTypeFilter([]);
  }

  function toggleType(ty: string) {
    setTypeFilter((prev) => (prev.includes(ty) ? prev.filter((v) => v !== ty) : [...prev, ty]));
  }

  function toggleSelect(id: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function copySelected() {
    if (selectedNames.length === 0) return;
    const ok = await copyText(selectedNames.join('\n'));
    if (ok) Toast.success(t('models.selected.copied').replace('{n}', String(selectedNames.length)));
    else Toast.warning(t('common.copyFailed'));
  }

  return (
    <div className="wg-gallery">
      <div className="wg-container">
        {/* title area */}
        <div className="wg-gallery-title">
          <h1>{t('models.title')}</h1>
          {models !== null && (
            <span className="wg-count-badge">
              {t('models.countBadge').replace('{n}', String(models.length))}
            </span>
          )}
        </div>
        <p className="wg-gallery-sub">{t('models.subtitle')}</p>

        {/* filter bar */}
        <div className="wg-filterbar">
          <div className="wg-search">
            <IconSearch className="wg-search-icon" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t('models.searchPlaceholder')}
              aria-label={t('models.searchPlaceholder')}
              className="wg-search-input"
            />
          </div>
          <Tooltip content={t('models.selected.tip')} position="topLeft">
            <span>
              <button
                type="button"
                className="wg-toolbar-btn"
                disabled={selected.size === 0}
                onClick={copySelected}
              >
                <IconCopy />
                {t('models.selected.copy')}
                {selected.size > 0 ? ` (${selected.size})` : ''}
              </button>
            </span>
          </Tooltip>
          <Select
            value={sortKey}
            onChange={(v) => setSortKey(v as SortKey)}
            className="wg-sort"
            aria-label={t('models.sort.label')}
          >
            <Select.Option value="newest">{t('models.sort.newest')}</Select.Option>
            <Select.Option value="priceAsc">{t('models.sort.priceAsc')}</Select.Option>
            <Select.Option value="priceDesc">{t('models.sort.priceDesc')}</Select.Option>
          </Select>
          <div className="wg-filterbar-right">
            <Select
              multiple
              value={typeFilter}
              onChange={(v) => setTypeFilter(v as string[])}
              placeholder={t('models.types')}
              className="wg-types-select"
              aria-label={t('models.types')}
            >
              {typeOptions.map((ty) => (
                <Select.Option key={ty} value={ty}>
                  {t(`models.type.${ty}`)}
                </Select.Option>
              ))}
            </Select>
            <div className="wg-view-toggle" role="group" aria-label={t('models.view.grid')}>
              <button
                type="button"
                className={view === 'grid' ? 'wg-view-on' : ''}
                onClick={() => setView('grid')}
                aria-label={t('models.view.grid')}
              >
                <IconGridView size="small" />
              </button>
              <button
                type="button"
                className={view === 'list' ? 'wg-view-on' : ''}
                onClick={() => setView('list')}
                aria-label={t('models.view.list')}
              >
                <IconList size="small" />
              </button>
            </div>
          </div>
        </div>

        {/* active type chips (dynamically generated from the API) */}
        {typeOptions.length > 0 && (
          <div className="wg-chips" aria-label={t('models.types')}>
            {typeOptions.map((ty) => {
              const active = typeFilter.includes(ty);
              return (
                <button
                  key={ty}
                  type="button"
                  className={`wg-chip ${active ? 'wg-chip-active' : ''}`}
                  onClick={() => toggleType(ty)}
                  aria-pressed={active}
                >
                  {t(`models.type.${ty}`)}
                  {active && (
                    <span className="wg-chip-x" aria-hidden>
                      <IconClose size="extra-small" />
                    </span>
                  )}
                </button>
              );
            })}
          </div>
        )}
      </div>

      {/* states */}
      <div className="wg-container">
        {error !== null && (
          <div className="wg-center wg-column">
            <Empty
              image={<ErrorIllustration />}
              description={`${t('common.requestFailed')} ${error}`}
            />
            <Button icon={<IconRefresh />} type="primary" theme="solid" onClick={reload}>
              {t('common.retry')}
            </Button>
          </div>
        )}
        {error === null && models === null && (
          <div className="wg-center">
            <Spin size="large" tip={t('common.loading')} />
          </div>
        )}

        {error === null && models !== null && (
          <>
            {filtered.length === 0 ? (
              <Empty
                image={<ModelsEmptyIllustration />}
                title={t('models.empty.title')}
                description={t('models.empty.desc')}
                style={{ padding: '64px 0' }}
              >
                {hasFilters && (
                  <Button type="primary" theme="solid" onClick={clearFilters}>
                    {t('models.empty.action')}
                  </Button>
                )}
              </Empty>
            ) : view === 'grid' ? (
              <div className="wg-grid">
                {visibleModels.map((m) => (
                  <ModelCard
                    key={m.api_model_name}
                    model={m}
                    selected={selected.has(m.api_model_name)}
                    onToggleSelect={() => toggleSelect(m.api_model_name)}
                    onOpen={() => setDetail(m)}
                  />
                ))}
              </div>
            ) : (
              <div className="wg-list">
                {visibleModels.map((m) => (
                  <ModelRow
                    key={m.api_model_name}
                    model={m}
                    selected={selected.has(m.api_model_name)}
                    onToggleSelect={() => toggleSelect(m.api_model_name)}
                    onOpen={() => setDetail(m)}
                  />
                ))}
              </div>
            )}
            {hasMore && (
              <div
                ref={sentinelRef}
                style={{ display: 'flex', justifyContent: 'center', padding: '20px 0' }}
              >
                <Button theme="borderless" onClick={growList}>
                  {t('models.loadMore', { shown: visibleModels.length, total: filtered.length })}
                </Button>
              </div>
            )}
          </>
        )}
      </div>

      {/* detail SideSheet — 560px, no mask, single body scroll */}
      <SideSheet
        title={detail ? <SheetHead model={detail} /> : null}
        visible={detail !== null}
        onCancel={() => setDetail(null)}
        placement="right"
        width="min(560px, 90vw)"
        mask={false}
        headerStyle={{ borderBottom: '1px solid var(--wg-border)' }}
        bodyStyle={{ padding: 0 }}
      >
        {detail && <ModelSheet key={detail.api_model_name} model={detail} />}
      </SideSheet>
    </div>
  );
}

/* ---------- grid card ---------- */

function ModelCard({
  model,
  selected,
  onToggleSelect,
  onOpen,
}: {
  model: PublicModel;
  selected: boolean;
  onToggleSelect: () => void;
  onOpen: () => void;
}) {
  const { t } = useI18n();
  const promo = promoOf(model);
  const dark = promo.hot || promo.offLabel !== null;
  const typeLabel = t(`models.type.${model.model_type ?? 'other'}`);
  const billingLabel = isPerRequest(model) ? t('models.billing.perReq') : t('models.billing.payg');
  const unitKey = isPerRequest(model) ? 'common.unit.perReq' : 'common.unit.perM';

  return (
    <div
      className={`wg-card ${dark ? 'wg-card-dark' : ''} ${selected ? 'wg-card-selected' : ''}`}
    >
      <div className="wg-card-head">
        <span className="wg-provider-disc">
          <ProviderIcon provider={model.provider} manufacturer={model.manufacturer} size={22} />
        </span>
        <button type="button" className="wg-model-name wg-model-name-btn" title={model.api_model_name} onClick={onOpen}
          onDoubleClick={async (e) => {
            e.stopPropagation();
            if (await copyText(model.api_model_name)) Toast.success(t('common.copied'));
          }}>
          {model.api_model_name}</button>
        {promo.hot && (
          <span className="wg-badge-hot">
            <FireIcon />
            {t('models.badge.hot')}
          </span>
        )}
        {promo.offLabel && <span className="wg-badge-off">{promo.offLabel}</span>}
        <span className="wg-card-actions" onClick={(e) => e.stopPropagation()}>
          <CopyButton text={model.api_model_name} />
          <button
            type="button"
            className="wg-icon-btn"
            onClick={onToggleSelect}
            aria-label={t('models.selected.copy')}
          >
            <span className={`wg-check ${selected ? 'wg-check-on' : ''}`} />
          </button>
        </span>
      </div>

      <div className="wg-price-rows">
        {isPerRequest(model) ? (
          <PriceRow
            labelKey="models.price.fixed"
            orig={null}
            cur={formatPrice(model.fixed_price)}
            unitKey="common.unit.perReq"
            tipKey="tooltip.priceUnit"
          />
        ) : (
          <>
            <PriceRow
              labelKey="models.price.input"
              orig={formatPrice(origInputPrice(model))}
              cur={formatPrice(model.input_price)}
              unitKey={unitKey}
              tipKey="tooltip.priceUnit"
            />
            <PriceRow
              labelKey="models.price.output"
              orig={null}
              cur={formatPrice(model.output_price)}
              unitKey={unitKey}
              tipKey="tooltip.priceUnit"
            />
            <PriceRow
              labelKey="models.price.cache"
              orig={null}
              cur={formatPrice(model.input_price_cache_hit)}
              unitKey={unitKey}
              tipKey="tooltip.cacheRead"
            />
          </>
        )}
      </div>

      <div className="wg-card-foot">
        <Tooltip content={t('tooltip.billing')} position="topLeft">
          <span className="wg-billing-pill">{billingLabel}</span>
        </Tooltip>
        <span className="wg-type-tag">{typeLabel}</span>
      </div>
    </div>
  );
}

function PriceRow({
  labelKey,
  orig,
  cur,
  unitKey,
  tipKey,
}: {
  labelKey: string;
  orig: string | null;
  cur: string | null;
  unitKey: string;
  tipKey: string;
}) {
  const { t } = useI18n();
  if (cur === null && orig === null) return null;
  return (
    <div className="wg-price-row">
      <Tooltip content={t(tipKey)} position="topLeft">
        <span className="wg-price-label wg-tip-label">
          {t(labelKey)}
          <IconInfoCircle size="small" className="wg-info-icon" />
        </span>
      </Tooltip>
      {orig !== null && <span className="wg-price-orig">${orig}</span>}
      <span className="wg-price-cur">{cur !== null ? `$${cur}` : '—'}</span>
      <Tooltip content={t('tooltip.priceUnit')} position="topLeft">
        <span className="wg-price-unit">{t(unitKey)}</span>
      </Tooltip>
    </div>
  );
}

/* ---------- list row ---------- */

function ModelRow({
  model,
  selected,
  onToggleSelect,
  onOpen,
}: {
  model: PublicModel;
  selected: boolean;
  onToggleSelect: () => void;
  onOpen: () => void;
}) {
  const { t } = useI18n();
  const typeLabel = t(`models.type.${model.model_type ?? 'other'}`);
  const input = formatPrice(model.input_price ?? model.fixed_price);
  const output = formatPrice(model.output_price);

  return (
    <div
      className={`wg-row ${selected ? 'wg-card-selected' : ''}`}
    >
      <span className="wg-provider-disc">
        <ProviderIcon provider={model.provider} manufacturer={model.manufacturer} size={22} />
      </span>
      <button type="button" className="wg-model-name wg-model-name-btn" title={model.api_model_name} onClick={onOpen}
          onDoubleClick={async (e) => {
            e.stopPropagation();
            if (await copyText(model.api_model_name)) Toast.success(t('common.copied'));
          }}>
        {model.api_model_name}</button>
      <span className="wg-row-provider">{model.provider}</span>
      <div className="wg-row-prices">
        <Tooltip content={t('tooltip.inputPrice')} position="topLeft">
          <span className="wg-price-cur wg-num">
            {input !== null ? `$${input}` : '—'}
            <span className="wg-price-unit">{t('common.unit.perM')}</span>
          </span>
        </Tooltip>
        <Tooltip content={t('tooltip.outputPrice')} position="topLeft">
          <span className="wg-price-cur wg-num">
            {output !== null ? `$${output}` : '—'}
            <span className="wg-price-unit">{t('common.unit.perM')}</span>
          </span>
        </Tooltip>
      </div>
      <span className="wg-type-tag">{typeLabel}</span>
      <span className="wg-card-actions" onClick={(e) => e.stopPropagation()}>
        <CopyButton text={model.api_model_name} />
        <button
          type="button"
          className="wg-icon-btn"
          onClick={onToggleSelect}
          aria-label={t('models.selected.copy')}
        >
          <span className={`wg-check ${selected ? 'wg-check-on' : ''}`} />
        </button>
      </span>
    </div>
  );
}

/* ---------- detail sheet ---------- */

function SheetHead({ model }: { model: PublicModel }) {
  const { t } = useI18n();
  return (
    <div className="wg-sheet-head">
      <span className="wg-provider-disc">
        <ProviderIcon provider={model.provider} manufacturer={model.manufacturer} size={22} />
      </span>
      <span className="wg-model-name" title={model.api_model_name}>
        {model.api_model_name}
      </span>
      <CopyButton text={model.api_model_name} />
      <span className="wg-sheet-type">{t(`models.type.${model.model_type ?? 'other'}`)}</span>
    </div>
  );
}

function ModelSheet({ model }: { model: PublicModel }) {
  const { t } = useI18n();
  const [tab, setTab] = useState<'python' | 'curl'>('python');

  const caps = model.capabilities ?? undefined;
  const effort = Array.isArray(caps?.reasoning_efforts) ? (caps.reasoning_efforts as string[]) : null;
  const ctx = formatContext(model.context_window);
  const maxOut = model.max_output_tokens ? formatContext(model.max_output_tokens) : null;
  const promo = promoOf(model);
  const origInput = formatPrice(origInputPrice(model));
  const perReq = isPerRequest(model);
  const unitLabel = t(perReq ? 'common.unit.perReq' : 'common.unit.perM');

  const pythonCode = `from openai import OpenAI

client = OpenAI(
    base_url="${BASE_URL}",
    api_key="<YOUR_API_KEY>",
)

messages = [
    {"role": "system", "content": "You are an intelligent assistant, please reply concisely."},
    {"role": "user", "content": "Hello, what kind of model are you?"},
]

stream = client.chat.completions.create(
    model="${model.api_model_name}",
    messages=messages,
    stream=True,
)

for chunk in stream:
    delta = chunk.choices[0].delta.content
    if delta:
        print(delta, end="")`;

  const curlCode = `curl -X POST "${BASE_URL}/chat/completions" \\
  -H "Authorization: Bearer <YOUR_API_KEY>" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${model.api_model_name}",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "Hello!"}
    ],
    "stream": true
  }'`;

  const code = tab === 'python' ? pythonCode : curlCode;

  const sections: DrawerSection[] = [
    {
      key: 'endpoints',
      title: t('models.sheet.endpoints.title'),
      desc: t('models.sheet.endpoints.desc'),
      tip: t('models.sheet.endpointNote'),
      children: (
        <div className="wg-endpoint-line">
          <span>
            <b>{t('models.sheet.endpoint')}</b>
            <code>: {CHAT_COMPLETIONS_PATH}</code>
          </span>
          <span className="wg-endpoint-method">POST</span>
        </div>
      ),
    },
    {
      key: 'quickstart',
      title: t('models.sheet.quickstart.title'),
      desc: t('models.sheet.quickstart.desc'),
      children: (
        <div className="wg-codeblock">
          <Tabs type="button" activeKey={tab} onChange={(k) => setTab(k as 'python' | 'curl')}>
            <Tabs.TabPane itemKey="python" tab={t('models.sheet.tab.python')} />
            <Tabs.TabPane itemKey="curl" tab={t('models.sheet.tab.curl')} />
          </Tabs>
          <pre>{highlightCode(code, tab)}</pre>
          <CopyButton className="wg-code-copy" text={code} />
        </div>
      ),
    },
    {
      key: 'price',
      title: t('models.sheet.price.title'),
      desc: t('models.sheet.price.desc'),
      children: (
        <>
          <table className="wg-price-table">
            <thead>
              <tr>
                <th>{t('models.sheet.billing')}</th>
                <th>
                  <Tooltip content={t('tooltip.inputPrice')} position="topLeft">
                    <span className="wg-tip-label">
                      {t('models.price.input')}
                      <IconInfoCircle size="small" className="wg-info-icon" />
                    </span>
                  </Tooltip>
                </th>
                <th>
                  <Tooltip content={t('tooltip.outputPrice')} position="topLeft">
                    <span className="wg-tip-label">
                      {t('models.price.output')}
                      <IconInfoCircle size="small" className="wg-info-icon" />
                    </span>
                  </Tooltip>
                </th>
                <th>
                  <Tooltip content={t('tooltip.cacheRead')} position="topLeft">
                    <span className="wg-tip-label">
                      {t('models.price.cache')}
                      <IconInfoCircle size="small" className="wg-info-icon" />
                    </span>
                  </Tooltip>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td>
                  <Tooltip content={t('tooltip.billing')} position="topLeft">
                    <span className="wg-tip-label">
                      {t(perReq ? 'models.billing.perReq' : 'models.billing.payg')}
                      <IconInfoCircle size="small" className="wg-info-icon" />
                    </span>
                  </Tooltip>
                </td>
                <td className="wg-price-col">
                  <span className="wg-price-cell">
                    {origInput !== null && <span className="wg-price-orig">${origInput}</span>}
                    <span className="wg-price-cur">
                      {formatPrice(model.input_price ?? model.fixed_price) !== null
                        ? `$${formatPrice(model.input_price ?? model.fixed_price)}`
                        : '—'}
                    </span>
                    <span className="wg-price-unit">{unitLabel}</span>
                  </span>
                </td>
                <td className="wg-price-col">
                  <span className="wg-price-cell">
                    <span className="wg-price-cur">
                      {formatPrice(model.output_price) !== null ? `$${formatPrice(model.output_price)}` : '—'}
                    </span>
                    {!perReq && <span className="wg-price-unit">{unitLabel}</span>}
                  </span>
                </td>
                <td className="wg-price-col">
                  <span className="wg-price-cell">
                    <span className="wg-price-cur">
                      {formatPrice(model.input_price_cache_hit) !== null
                        ? `$${formatPrice(model.input_price_cache_hit)}`
                        : '—'}
                    </span>
                    {!perReq && <span className="wg-price-unit">{unitLabel}</span>}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
          {model.reasoning_price != null && (
            <p className="wg-sheet-note">
              <Tooltip content={t('tooltip.reasoning')} position="topLeft">
                <span className="wg-tip-label">
                  {t('models.price.reasoning')}
                  <IconInfoCircle size="small" className="wg-info-icon" />
                </span>
              </Tooltip>
              {' : '}
              <b className="wg-num">${formatPrice(model.reasoning_price)}</b> {unitLabel}
            </p>
          )}
          {promo.offLabel && (
            <p className="wg-sheet-note">
              <span className="wg-badge-off">{promo.offLabel}</span>
            </p>
          )}
        </>
      ),
    },
  ];

  if (ctx !== null || maxOut !== null || caps) {
    sections.push({
      key: 'caps',
      title: t('models.sheet.caps'),
      desc: t('models.sheet.capsDesc'),
      children: (
        <>
          <div className="wg-detail-capline">
            {ctx !== null && (
              <span>
                {t('models.sheet.context')}: <b className="wg-num">{ctx}</b>
              </span>
            )}
            {maxOut !== null && (
              <span>
                {t('models.sheet.maxOutput')}: <b className="wg-num">{maxOut}</b>
              </span>
            )}
          </div>
          {caps && (
            <div className="wg-sheet-tags">
              {capsFlag(caps, 'supports_streaming') && <MiniTag>{t('common.caps.streaming')}</MiniTag>}
              {capsFlag(caps, 'supports_tool_calls') && <MiniTag>{t('common.caps.tools')}</MiniTag>}
              {capsFlag(caps, 'supports_parallel_tool_calls') && <MiniTag>{t('common.caps.parallelTools')}</MiniTag>}
              {capsFlag(caps, 'supports_vision') && <MiniTag>{t('common.caps.vision')}</MiniTag>}
              {capsFlag(caps, 'supports_thinking') && <MiniTag>{t('common.caps.thinking')}</MiniTag>}
              {capsFlag(caps, 'supports_json_mode') && <MiniTag>{t('common.caps.jsonMode')}</MiniTag>}
              {effort && effort.length > 0 && (
                <MiniTag>
                  {t('common.caps.reasoningEfforts')}: {effort.join(' / ')}
                </MiniTag>
              )}
            </div>
          )}
          {model.description && <p className="wg-sheet-note">{model.description}</p>}
        </>
      ),
    });
  }

  return (
    <div className="wg-sheet-body">
      <DrawerSections sections={sections} />
    </div>
  );
}

function MiniTag({ children }: { children: ReactNode }) {
  return <span className="wg-billing-pill">{children}</span>;
}

/* ---------- minimal syntax highlighting ---------- */

const PY_RE =
  /(#[^\n]*)|("""[\s\S]*?"""|"[^"\n]*"|'[^'\n]*')|\b(import|from|for|in|if|not|else|return|with|as|True|False|None)\b|\b(\d+(?:\.\d+)?)\b|\b(OpenAI|print)\b/g;
const SH_RE = /(\\\n)|("[\s\S]*?"|'[\s\S]*?')|\b(true|false|null)\b|(?<![\w-])(-[A-Za-z])/g;

function highlightCode(code: string, lang: 'python' | 'curl'): ReactNode[] {
  const re = new RegExp(lang === 'python' ? PY_RE.source : SH_RE.source, 'g');
  const out: ReactNode[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  let i = 0;
  while ((m = re.exec(code))) {
    if (m.index > last) out.push(code.slice(last, m.index));
    let cls: string | null = null;
    if (lang === 'python') {
      if (m[1]) cls = 'tok-c';
      else if (m[2]) cls = 'tok-s';
      else if (m[3]) cls = 'tok-k';
      else if (m[4]) cls = 'tok-n';
      else if (m[5]) cls = 'tok-f';
    } else {
      if (m[1]) cls = null;
      else if (m[2]) cls = 'tok-s';
      else if (m[3]) cls = 'tok-k';
      else if (m[4]) cls = 'tok-k';
    }
    out.push(
      cls ? (
        <span key={i++} className={cls}>
          {m[0]}
        </span>
      ) : (
        m[0]
      )
    );
    last = m.index + m[0].length;
  }
  if (last < code.length) out.push(code.slice(last));
  return out;
}

/* ---------- illustrations ---------- */

function ModelsEmptyIllustration() {
  return (
    <svg width="160" height="120" viewBox="0 0 160 120" fill="none" aria-hidden>
      <circle cx="80" cy="58" r="34" fill="#EEF2FF" />
      <circle cx="68" cy="52" r="10" fill="#C7D2FE" />
      <circle cx="92" cy="52" r="10" fill="#C7D2FE" />
      <path d="M66 66q14 10 28 0" stroke="#818CF8" strokeWidth="3" strokeLinecap="round" />
      <rect x="26" y="20" width="26" height="16" rx="8" fill="#E0E7FF" />
      <rect x="112" y="88" width="26" height="16" rx="8" fill="#E0E7FF" />
      <path d="M28 96q10-16 22-18" stroke="#E0E7FF" strokeWidth="3" strokeLinecap="round" fill="none" />
      <path d="M132 26q-10 16-22 18" stroke="#E0E7FF" strokeWidth="3" strokeLinecap="round" fill="none" />
    </svg>
  );
}

function ErrorIllustration() {
  return (
    <svg width="160" height="120" viewBox="0 0 160 120" fill="none" aria-hidden>
      <circle cx="80" cy="58" r="34" fill="#FEF2F2" />
      <path d="M80 38v24" stroke="#F43F5E" strokeWidth="4" strokeLinecap="round" />
      <circle cx="80" cy="74" r="3" fill="#F43F5E" />
      <path d="M40 24l-8-8M120 24l8-8M40 92l-8 8M120 92l8 8" stroke="#FECDD3" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}
