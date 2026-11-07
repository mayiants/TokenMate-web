import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { Banner, Button, Input, InputNumber, Select, SideSheet, Toast, Tooltip } from '@douyinfe/semi-ui';
import { IconCopyStroked, IconCreditCard, IconInfoCircle, IconPriceTag, IconUserGroup } from '@douyinfe/semi-icons';
import { copyText } from '../utils/format';
import { useI18n } from '../i18n';
import {
  createKey,
  updateKey,
  type ApiKey,
  type CreatedKey,
  type KeyExpiresIn,
  type KeyRoutingMode,
  type KeyType,
} from '../api/keys';
import { compatibleGroupTypes, groupModels, listMyGroups, type Group } from '../api/groups';
import { listMySubscriptions, planModels, type UserSubscription } from '../api/subscriptions';
import { listMyEnterpriseMembers, type EnterpriseMember } from '../api/enterprise';
import { fetchMe, type AuthUser } from '../api/auth';
import { formatDateTime } from '../api/console';
import DrawerSections, { type DrawerSection } from './DrawerSections';

interface Props {
  visible: boolean;
  mode: 'create' | 'edit';
  /** edit target */
  keyData?: ApiKey | null;
  modelOptions: string[];
  onClose: () => void;
  onSaved: () => void;
}

const EXPIRE_OPTIONS: KeyExpiresIn[] = [0, 30, 90, 365];
const ROUTING_OPTIONS: KeyRoutingMode[] = ['single', 'round_robin', 'priority'];

function fill(template: string, vars: Record<string, string | number>): string {
  return Object.entries(vars).reduce((acc, [k, v]) => acc.replace(`{${k}}`, String(v)), template);
}

function compactNumber(n: number): string {
  if (n >= 1_000_000) {
    const m = n / 1_000_000;
    return `${Number.isInteger(m) ? m : m.toFixed(1)}M`;
  }
  return n.toLocaleString('en-US');
}

function Meta({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="wg-key-meta">
      <span className="wg-key-meta-k">{label}</span>
      <span className="wg-key-meta-v">{value}</span>
    </div>
  );
}

export default function KeyDrawer({ visible, mode, keyData, modelOptions, onClose, onSaved }: Props) {
  const { t } = useI18n();
  const isCreate = mode === 'create';

  const [name, setName] = useState('');
  const [keyType, setKeyType] = useState<KeyType>('general');
  const [groupId, setGroupId] = useState<string>();
  const [subscriptionId, setSubscriptionId] = useState<string>();
  const [routing, setRouting] = useState<KeyRoutingMode>('single');
  const [currentModel, setCurrentModel] = useState<string>();
  const [multiModels, setMultiModels] = useState<string[]>([]);
  const [refinedModels, setRefinedModels] = useState<string[]>([]);
  const [expiresIn, setExpiresIn] = useState<KeyExpiresIn>(0);
  const [rpm, setRpm] = useState<number>();
  const [tpm, setTpm] = useState<number>();

  const [groups, setGroups] = useState<Group[]>([]);
  const [subscriptions, setSubscriptions] = useState<UserSubscription[]>([]);
  const [members, setMembers] = useState<EnterpriseMember[]>([]);
  const [me, setMe] = useState<AuthUser | null>(null);
  const [dataLoaded, setDataLoaded] = useState(false);

  const [submitting, setSubmitting] = useState(false);
  const [created, setCreated] = useState<CreatedKey | null>(null);
  const [openSections, setOpenSections] = useState<string[]>([]);

  /* ---------- data: groups + subscriptions + caller ---------- */

  useEffect(() => {
    if (!visible) return;
    let alive = true;
    setCreated(null);
    setSubmitting(false);
    setOpenSections([]);
    setDataLoaded(false);
    Promise.all([
      listMyGroups().catch(() => [] as Group[]),
      listMySubscriptions().catch(() => [] as UserSubscription[]),
      fetchMe().catch(() => null),
    ]).then(([g, s, user]) => {
      if (!alive) return;
      setGroups(g);
      setSubscriptions(s);
      setMe(user);
      setDataLoaded(true);
    });
    return () => {
      alive = false;
    };
  }, [visible]);

  const subsetFor = useMemo(() => {
    const byType = (types: string[]) => groups.filter((g) => types.includes(g.group_type));
    return {
      general: byType(compatibleGroupTypes('general')),
      enterprise: byType(compatibleGroupTypes('enterprise')),
    };
  }, [groups]);

  // Initialize the form once the domain data (and the edit target) is known.
  useEffect(() => {
    if (!visible || !dataLoaded) return;
    setMultiModels([]);
    setRefinedModels([]);
    if (mode === 'edit' && keyData) {
      setName(keyData.name ?? '');
      setKeyType((keyData.key_type as KeyType) ?? 'general');
      setGroupId(keyData.group_id);
      setSubscriptionId(undefined);
      setRouting((keyData.model_routing_mode as KeyRoutingMode) ?? 'single');
      setCurrentModel(keyData.current_model ?? undefined);
      setMultiModels(keyData.selected_models ?? []);
      setRefinedModels(keyData.allowed_models ?? []);
      setRpm(keyData.rate_limit_rpm || undefined);
      setTpm(keyData.rate_limit_tpm || undefined);
      return;
    }
    setName('');
    setKeyType('general');
    setSubscriptionId(undefined);
    setRouting('single');
    setCurrentModel(undefined);
    setExpiresIn(0);
    setRpm(undefined);
    setTpm(undefined);
    setGroupId(subsetFor.general[0]?.id);
  }, [visible, dataLoaded, mode, keyData, subsetFor]);

  // Enterprise member select is only meaningful for enterprise keys.
  useEffect(() => {
    if (!visible || keyType !== 'enterprise') return;
    let alive = true;
    listMyEnterpriseMembers()
      .then((list) => alive && setMembers(list))
      .catch(() => alive && setMembers([]));
    return () => {
      alive = false;
    };
  }, [visible, keyType]);

  /* ---------- derived domain values ---------- */

  const group = useMemo(() => groups.find((g) => g.id === groupId) ?? null, [groups, groupId]);
  const subscription = useMemo(
    () => subscriptions.find((s) => s.id === subscriptionId) ?? null,
    [subscriptions, subscriptionId]
  );

  const editingPool = keyData?.allowed_models ?? [];
  const pool = useMemo(() => {
    if (keyType === 'subscription') {
      return subscription ? planModels(subscription) : isCreate ? [] : editingPool;
    }
    if (group) return groupModels(group);
    return isCreate ? [] : editingPool;
  }, [keyType, subscription, group, isCreate, editingPool]);

  const availableModels = useMemo(() => (pool.length ? pool : modelOptions), [pool, modelOptions]);
  /** the model pool only exists once its owner (group / subscription) is chosen */
  const poolReady = keyType === 'subscription' ? !!subscription : !!group;
  const modelSelectData = useMemo(() => availableModels.map((m) => ({ label: m, value: m })), [availableModels]);
  const refinedSelectData = useMemo(() => {
    const extra = refinedModels.filter((m) => !availableModels.includes(m));
    return [...extra, ...availableModels].map((m) => ({ label: m, value: m }));
  }, [availableModels, refinedModels]);

  const groupOptions = useMemo(() => {
    const list = keyType === 'general' ? subsetFor.general : subsetFor.enterprise;
    return list.map((g) => {
      const rpmText = g.rate_limit_rpm ? compactNumber(g.rate_limit_rpm) : t('keys.limits.default');
      const count = groupModels(g).length;
      const modelsText = count ? fill(t('keys.group.inheritModels'), { n: count }) : t('keys.group.inheritModelsAll');
      return { label: `${g.name} · ${fill(t('keys.group.optionSummary'), { rpm: rpmText, models: modelsText })}`, value: g.id };
    });
  }, [keyType, subsetFor, t]);

  const subscriptionOptions = useMemo(
    () =>
      subscriptions.map((s) => {
        const count = planModels(s).length;
        const models = count ? fill(t('keys.group.inheritModels'), { n: count }) : t('keys.group.inheritModelsAll');
        return {
          label: `${s.plan_name || s.plan_type || s.id} · ${models}`,
          value: s.id,
        };
      }),
    [subscriptions, t]
  );

  // A self-service enterprise key is always owned by the caller; the select is
  // rendered for domain completeness but pinned to the current member.
  const memberOptions = useMemo(() => {
    const self = members.find((m) => m.id === me?.id);
    if (self) return [{ label: self.name ? `${self.name} (${self.email})` : self.email, value: self.id }];
    if (me) return [{ label: me.name ? `${me.name} (${me.email})` : me.email, value: me.id }];
    return [];
  }, [members, me]);

  const billingLabel = (mode?: string): string => {
    switch (mode) {
      case 'request':
        return t('keys.billing.request');
      case 'subscription':
        return t('keys.billing.subscription');
      case 'token':
        return t('keys.billing.token');
      default:
        return mode || t('keys.billing.token');
    }
  };

  const typeLabel = (kt?: string): string => {
    if (kt === 'subscription') return t('keys.type.subscription');
    if (kt === 'enterprise') return t('keys.type.enterprise');
    return t('keys.type.general');
  };

  const routingLabel = (m?: string): string => {
    if (m === 'round_robin') return t('keys.routing.round_robin');
    if (m === 'priority') return t('keys.routing.priority');
    return t('keys.routing.single');
  };

  const routingDesc =
    routing === 'single'
      ? t('keys.routing.singleDesc')
      : routing === 'round_robin'
        ? t('keys.routing.roundRobinDesc')
        : `${t('keys.routing.priorityDesc')} · ${t('keys.routing.orderHint')}`;

  const modelPlaceholder = !poolReady
    ? keyType === 'subscription'
      ? t('keys.routing.pickSubscription')
      : t('keys.routing.pickGroup')
    : routing === 'single'
      ? t('keys.routing.modelPlaceholder')
      : t('keys.routing.modelsPlaceholder');

  const inheritText = useMemo(() => {
    if (!group) return '';
    const parts = [
      fill(t('keys.group.inheritRpm'), {
        v: group.rate_limit_rpm ? compactNumber(group.rate_limit_rpm) : t('keys.limits.default'),
      }),
    ];
    if (group.rate_limit_tpm) parts.push(fill(t('keys.group.inheritTpm'), { v: compactNumber(group.rate_limit_tpm) }));
    parts.push(fill(t('keys.group.inheritBilling'), { v: billingLabel(group.billing_mode) }));
    const count = groupModels(group).length;
    parts.push(count ? fill(t('keys.group.inheritModels'), { n: count }) : t('keys.group.inheritModelsAll'));
    return parts.join(' · ');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [group, t]);

  /* ---------- type / group / routing coupling ---------- */

  const resetModelChoice = () => {
    setCurrentModel(undefined);
    setMultiModels([]);
    setRefinedModels([]);
  };

  const onKeyType = (next: KeyType) => {
    if (next === keyType) return;
    setKeyType(next);
    resetModelChoice();
    if (next === 'subscription') {
      setGroupId(undefined);
      return;
    }
    const list = next === 'enterprise' ? subsetFor.enterprise : subsetFor.general;
    setGroupId(list.some((g) => g.id === groupId) ? groupId : list[0]?.id);
  };

  const onGroupChange = (id?: string) => {
    setGroupId(id);
    resetModelChoice();
  };

  const onRouting = (next: KeyRoutingMode) => {
    setRouting(next);
    if (next !== 'single' && currentModel && multiModels.length === 0) {
      setMultiModels([currentModel]);
    }
  };

  /* ---------- submit ---------- */

  const submit = async () => {
    if (!name.trim()) {
      Toast.warning(t('keys.drawer.nameRequired'));
      return;
    }
    if (isCreate) {
      if (keyType === 'subscription' && !subscriptionId) {
        Toast.warning(t('keys.sub.required'));
        return;
      }
      if (keyType !== 'subscription' && !groupId) {
        Toast.warning(t('keys.group.required'));
        return;
      }
      if (routing === 'single' && !currentModel) {
        Toast.warning(t('keys.routing.required'));
        return;
      }
      if (routing !== 'single' && multiModels.length === 0) {
        Toast.warning(t('keys.routing.multiRequired'));
        return;
      }
    } else if (routing !== 'single' && refinedModels.length === 0) {
      // PATCH writes selected_models and allowed_models together: an empty list
      // would leave a multi-model key with nothing to route to.
      Toast.warning(t('keys.routing.multiRequired'));
      return;
    }

    setSubmitting(true);
    try {
      if (isCreate) {
        const routed = routing === 'single' ? (currentModel ? [currentModel] : []) : multiModels;
        // The whitelist may be narrower than the group, never wider; keep every
        // routed model inside it so validate never rejects the key's own traffic.
        const allowed = refinedModels.length ? Array.from(new Set([...refinedModels, ...routed])) : undefined;
        const res = await createKey({
          name: name.trim(),
          key_type: keyType,
          expires_in: expiresIn,
          model_routing_mode: routing,
          current_model: routing === 'single' ? currentModel : routed[0],
          selected_models: routed,
          allowed_models: allowed,
          group_priority: keyType === 'subscription' ? undefined : groupId ? [groupId] : undefined,
          subscription_id: keyType === 'subscription' ? subscriptionId : undefined,
          rate_limit_rpm: rpm ?? 0,
          rate_limit_tpm: tpm ?? 0,
        });
        setCreated(res);
      } else if (keyData) {
        await updateKey(keyData.id, {
          name: name.trim(),
          selected_models: refinedModels,
        });
        Toast.success(t('keys.drawer.updated'));
        onSaved();
        onClose();
      }
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : t('common.error'));
    } finally {
      setSubmitting(false);
    }
  };

  const copyCreated = async () => {
    if (!created?.key) return;
    const ok = await copyText(created.key);
    Toast[ok ? 'success' : 'error'](ok ? t('common.copied') : t('common.copyFailed'));
  };

  /* ---------- folded sections (low-frequency only) ---------- */

  const limitsSection: DrawerSection = {
    key: 'limits',
    title: t('keys.drawer.limits'),
    desc: t('keys.drawer.limitsDesc'),
    tip: t('keys.drawer.rpmTip'),
    children: isCreate ? (
      <div className="wg-form-grid">
        <div className="wg-form-field">
          <label className="wg-form-label" htmlFor="wg-key-rpm">
            {t('keys.drawer.rpm')}
            <Tooltip content={t('keys.drawer.rpmTip')} position="topLeft">
              <span className="wg-info-icon">
                <IconInfoCircle size="small" />
              </span>
            </Tooltip>
          </label>
          <InputNumber
            id="wg-key-rpm"
            value={rpm}
            onChange={(v) => setRpm(typeof v === 'number' && v > 0 ? v : undefined)}
            min={0}
            max={1_000_000}
            placeholder={t('keys.drawer.rpmPlaceholder')}
            style={{ width: '100%' }}
          />
        </div>
        <div className="wg-form-field">
          <label className="wg-form-label" htmlFor="wg-key-tpm">
            {t('keys.drawer.tpm')}
            <Tooltip content={t('keys.drawer.tpmTip')} position="topLeft">
              <span className="wg-info-icon">
                <IconInfoCircle size="small" />
              </span>
            </Tooltip>
          </label>
          <InputNumber
            id="wg-key-tpm"
            value={tpm}
            onChange={(v) => setTpm(typeof v === 'number' && v > 0 ? v : undefined)}
            min={0}
            max={1_000_000_000}
            placeholder={t('keys.drawer.tpmPlaceholder')}
            style={{ width: '100%' }}
          />
        </div>
      </div>
    ) : (
      <>
        <div className="wg-key-meta-grid">
          <Meta label={t('keys.drawer.rpm')} value={<span className="wg-num">{compactNumber(rpm ?? 0)}</span>} />
          <Meta label={t('keys.drawer.tpm')} value={<span className="wg-num">{compactNumber(tpm ?? 0)}</span>} />
        </div>
        <p className="wg-field-hint">{t('keys.drawer.limitsReadonly')}</p>
      </>
    ),
  };

  const modelsSection: DrawerSection = {
    key: 'models',
    title: t('keys.drawer.models'),
    desc: t('keys.drawer.modelsDesc'),
    children: (
      <>
        <div className="wg-form-field">
          <label className="wg-form-label">{t('keys.drawer.allowedModels')}</label>
          <Select<string>
            multiple
            filter
            value={refinedModels}
            onChange={(v) => setRefinedModels((v as string[] | undefined) ?? [])}
            optionList={refinedSelectData}
            placeholder={t('keys.drawer.modelsPlaceholder')}
            style={{ width: '100%' }}
            maxTagCount={4}
            disabled={!availableModels.length}
          />
          <p className="wg-field-hint">{fill(t('keys.drawer.modelsHint'), { n: availableModels.length })}</p>
        </div>
        {!isCreate && <p className="wg-field-hint">{t('keys.drawer.saveModelsOnly')}</p>}
      </>
    ),
  };

  const ipSection: DrawerSection = {
    key: 'ip',
    title: t('keys.drawer.ip'),
    desc: t('keys.drawer.ipDesc'),
    children: (
      <div className="wg-form-field">
        <Input disabled placeholder={t('keys.drawer.ipPlaceholder')} />
        <p className="wg-field-hint">{t('keys.drawer.ipUnavailable')}</p>
      </div>
    ),
  };

  /* ---------- main path (always visible, no expansion needed) ---------- */

  const typeCards: { value: KeyType; icon: ReactNode; tint: string; disabled: boolean; title: string; desc: string }[] = [
    { value: 'general', icon: <IconPriceTag />, tint: 'wg-tint-blue', disabled: false, title: t('keys.type.general'), desc: t('keys.type.generalDesc') },
    {
      value: 'subscription',
      icon: <IconCreditCard />,
      tint: 'wg-tint-violet',
      disabled: dataLoaded && subscriptions.length === 0,
      title: t('keys.type.subscription'),
      desc: dataLoaded && subscriptions.length === 0 ? t('keys.type.noSubscription') : t('keys.type.subscriptionDesc'),
    },
    {
      value: 'enterprise',
      icon: <IconUserGroup />,
      tint: 'wg-tint-amber',
      disabled: dataLoaded && subsetFor.enterprise.length === 0,
      title: t('keys.type.enterprise'),
      desc: dataLoaded && subsetFor.enterprise.length === 0 ? t('keys.type.noEnterprise') : t('keys.type.enterpriseDesc'),
    },
  ];

  const createMain = (
    <div className="wg-key-main">
      <div className="wg-form-field">
        <label className="wg-form-label" htmlFor="wg-key-name">
          {t('keys.drawer.name')}
          <span className="wg-form-required">*</span>
        </label>
        <Input
          id="wg-key-name"
          value={name}
          onChange={setName}
          placeholder={t('keys.drawer.namePlaceholder')}
          maxLength={64}
        />
      </div>

      <div className="wg-form-field">
        <span className="wg-form-label">
          {t('keys.type.label')}
          <Tooltip content={t('keys.type.hint')} position="topLeft">
            <span className="wg-info-icon">
              <IconInfoCircle size="small" />
            </span>
          </Tooltip>
        </span>
        <div className="wg-keytype-grid" role="radiogroup" aria-label={t('keys.type.label')}>
          {typeCards.map((c) => (
            <button
              key={c.value}
              type="button"
              role="radio"
              aria-checked={keyType === c.value}
              disabled={c.disabled}
              className={`wg-keytype-card${keyType === c.value ? ' active' : ''}${c.disabled ? ' is-disabled' : ''}`}
              onClick={() => !c.disabled && onKeyType(c.value)}
            >
              <span className={`wg-keytype-icon ${c.tint}`}>{c.icon}</span>
              <span className="wg-keytype-title">{c.title}</span>
              <span className="wg-keytype-desc">{c.desc}</span>
            </button>
          ))}
        </div>
      </div>

      {keyType === 'subscription' ? (
        <div className="wg-form-field">
          <span className="wg-form-label">
            {t('keys.sub.label')}
            <span className="wg-form-required">*</span>
          </span>
          <Select
            value={subscriptionId}
            onChange={(v) => {
              setSubscriptionId(v as string | undefined);
              resetModelChoice();
            }}
            optionList={subscriptionOptions}
            loading={!dataLoaded}
            placeholder={t('keys.sub.placeholder')}
            emptyContent={t('keys.sub.empty')}
            style={{ width: '100%' }}
          />
          <p className="wg-field-hint">
            {subscription
              ? planModels(subscription).length
                ? fill(t('keys.sub.models'), { n: planModels(subscription).length })
                : t('keys.sub.modelsAll')
              : t('keys.sub.empty')}
          </p>
        </div>
      ) : (
        <div className="wg-form-field">
          <span className="wg-form-label">
            {t('keys.group.label')}
            <span className="wg-form-required">*</span>
          </span>
          <Select
            value={groupId}
            onChange={(v) => onGroupChange(v as string | undefined)}
            optionList={groupOptions}
            loading={!dataLoaded}
            placeholder={t('keys.group.placeholder')}
            emptyContent={t('keys.group.empty')}
            style={{ width: '100%' }}
          />
          {group && (
            <div className="wg-inherit-bar">
              <Tooltip content={t('keys.group.inheritTip')} position="topLeft">
                <IconInfoCircle size="small" />
              </Tooltip>
              <span>{inheritText}</span>
            </div>
          )}
        </div>
      )}

      {keyType === 'enterprise' && (
        <div className="wg-form-field">
          <span className="wg-form-label">{t('keys.enterprise.member')}</span>
          <Select
            disabled
            value={me?.id}
            optionList={memberOptions}
            placeholder={t('keys.enterprise.member')}
            style={{ width: '100%' }}
          />
          <p className="wg-field-hint">{t('keys.enterprise.memberHint')}</p>
        </div>
      )}

      <div className="wg-form-field">
        <span className="wg-form-label">{t('keys.routing.label')}</span>
        <div className="wg-radio-chips" role="radiogroup" aria-label={t('keys.routing.label')}>
          {ROUTING_OPTIONS.map((m) => (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={routing === m}
              className={`wg-chip${routing === m ? ' active' : ''}`}
              onClick={() => onRouting(m)}
            >
              {routingLabel(m)}
            </button>
          ))}
        </div>
        {routing === 'single' ? (
          <Select<string>
            filter
            value={currentModel}
            onChange={(v) => setCurrentModel(v as string | undefined)}
            optionList={modelSelectData}
            placeholder={modelPlaceholder}
            style={{ width: '100%' }}
            disabled={!poolReady || !availableModels.length}
          />
        ) : (
          <Select<string>
            multiple
            filter
            value={multiModels}
            onChange={(v) => setMultiModels((v as string[] | undefined) ?? [])}
            optionList={modelSelectData}
            placeholder={modelPlaceholder}
            style={{ width: '100%' }}
            maxTagCount={4}
            disabled={!poolReady || !availableModels.length}
          />
        )}
        <p className="wg-field-hint">{routingDesc}</p>
        {dataLoaded && poolReady && !availableModels.length && (
          <p className="wg-field-hint wg-field-warn">{t('keys.routing.noModels')}</p>
        )}
      </div>

      <div className="wg-form-field">
        <span className="wg-form-label">
          {t('keys.drawer.expiration')}
          <Tooltip content={t('keys.drawer.expireHint')} position="topLeft">
            <span className="wg-info-icon">
              <IconInfoCircle size="small" />
            </span>
          </Tooltip>
        </span>
        <div className="wg-form-pills">
          {EXPIRE_OPTIONS.map((d) => (
            <button
              key={d}
              type="button"
              className={`wg-range-pill${expiresIn === d ? ' active' : ''}`}
              onClick={() => setExpiresIn(d)}
            >
              {t(`keys.drawer.expire.${d}`)}
            </button>
          ))}
        </div>
      </div>
    </div>
  );

  const editMain = (
    <div className="wg-key-main">
      <div className="wg-form-field">
        <label className="wg-form-label" htmlFor="wg-key-name">
          {t('keys.drawer.name')}
          <span className="wg-form-required">*</span>
        </label>
        <Input
          id="wg-key-name"
          value={name}
          onChange={setName}
          placeholder={t('keys.drawer.namePlaceholder')}
          maxLength={64}
        />
      </div>

      <div className="wg-form-field">
        <div className="wg-key-meta-grid">
          <Meta label={t('keys.type.label')} value={typeLabel(keyType)} />
          <Meta label={t('keys.group.label')} value={group?.name || keyData?.group_name || t('keys.drawer.noGroup')} />
          <Meta label={t('keys.routing.label')} value={routingLabel(routing)} />
          <Meta
            label={routing === 'single' ? t('keys.routing.model') : t('keys.routing.models')}
            value={
              routing === 'single'
                ? currentModel || '—'
                : fill(t('keys.group.inheritModels'), { n: multiModels.length })
            }
          />
          <Meta
            label={t('keys.drawer.expiration')}
            value={keyData?.expires_at ? formatDateTime(keyData.expires_at) : t('keys.neverExpires')}
          />
          <Meta
            label={t('keys.drawer.currentLimits')}
            value={
              <span className="wg-num">
                {compactNumber(rpm ?? 0)} RPM · {compactNumber(tpm ?? 0)} TPM
              </span>
            }
          />
        </div>
        <p className="wg-field-hint">{t('keys.drawer.readonlyTip')}</p>
      </div>
    </div>
  );

  return (
    <SideSheet
      title={isCreate ? t('keys.drawer.createTitle') : t('keys.drawer.editTitle')}
      visible={visible}
      onCancel={onClose}
      width="min(600px, 90vw)"
      bodyStyle={{ padding: '12px 24px 24px' }}
      footer={
        created ? (
          <div className="wg-drawer-footer wg-drawer-footer-single">
            <Button
              type="primary"
              theme="solid"
              onClick={() => {
                onSaved();
                onClose();
              }}
            >
              {t('keys.created.done')}
            </Button>
          </div>
        ) : (
          <div className="wg-drawer-footer">
            <Button theme="light" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="primary" theme="solid" loading={submitting} onClick={submit}>
              {isCreate ? t('keys.create') : t('common.save')}
            </Button>
          </div>
        )
      }
    >
      {created ? (
        <div className="wg-key-created">
          <Banner type="warning" fullMode={false} description={t('keys.created.warning')} />
          <div className="wg-key-created-box">
            <code className="wg-key-created-value">{created.key}</code>
            <Button theme="solid" type="primary" icon={<IconCopyStroked />} onClick={copyCreated}>
              {t('common.copy')}
            </Button>
          </div>
          <p className="wg-key-created-tip">{t('keys.created.tip')}</p>
        </div>
      ) : (
        <>
          {isCreate ? createMain : editMain}
          <div className="wg-key-folded">
            <DrawerSections sections={[limitsSection, modelsSection, ipSection]} activeKey={openSections} onChange={setOpenSections} />
          </div>
        </>
      )}
    </SideSheet>
  );
}
