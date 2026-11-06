import { useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, Collapse, Spin, Toast } from '@douyinfe/semi-ui';
import {
  IconBolt,
  IconChevronRight,
  IconCopy,
  IconSafe,
  IconShield,
  IconTick,
} from '@douyinfe/semi-icons';
import { listPublicModels, BASE_URL, type PublicModel } from '../api/models';
import { copyText } from '../utils/format';
import ProviderIcon from '../components/ProviderIcon';
import { useI18n } from '../i18n';

export default function Landing() {
  const { t } = useI18n();
  const [models, setModels] = useState<PublicModel[] | null>(null);
  const [copied, setCopied] = useState(false);
  const copyTimer = useRef<number>();

  useEffect(() => {
    listPublicModels()
      .then(setModels)
      .catch(() => setModels([]));
    return () => window.clearTimeout(copyTimer.current);
  }, []);

  const accessModels = useMemo(() => (models ?? []).slice(0, 6), [models]);

  const copyBaseUrl = async () => {
    const ok = await copyText(BASE_URL);
    if (!ok) {
      Toast.warning(t('common.copyFailed'));
      return;
    }
    setCopied(true);
    Toast.success(t('common.copied'));
    window.clearTimeout(copyTimer.current);
    copyTimer.current = window.setTimeout(() => setCopied(false), 1600);
  };

  return (
    <div className="wg-landing">
      {/* hero — left copy, right live model card */}
      <section className="wg-hero">
        <div className="wg-container wg-hero-grid">
          <div>
            <p className="wg-hero-pill">{t('landing.hero.badge')}</p>
            <h1 className="wg-hero-title">
              {t('landing.hero.title1')}
              <br />
              {t('landing.hero.title2')}
            </h1>
            <p className="wg-hero-sub">
              {t('landing.hero.tagline')} — {t('landing.hero.switchBaseUrl')}
            </p>
            <div className="wg-copybar">
              <span className="wg-dot" aria-hidden />
              <span className="wg-copybar-url">{BASE_URL}</span>
              <button
                type="button"
                className={`wg-copybar-btn ${copied ? 'wg-copied' : ''}`}
                onClick={copyBaseUrl}
              >
                {copied ? <IconTick size="small" /> : <IconCopy size="small" />}
                {copied ? t('common.copied') : t('common.copy')}
              </button>
            </div>
            <p className="wg-hero-policy">
              <IconShield size="small" aria-hidden />
              <a href="#privacy" onClick={(e) => e.preventDefault()}>{t('landing.policy.zeroRetention')}</a>
              <span aria-hidden>·</span>
              <a href="#security" onClick={(e) => e.preventDefault()}>{t('landing.policy.encryption')}</a>
            </p>
            <div className="wg-hero-cta">
              <Button theme="solid" type="primary" size="large">
                {t('nav.getKey')}
              </Button>
              <Button theme="light" size="large">
                {t('landing.hero.readDocs')}
              </Button>
            </div>
            <ul className="wg-hero-trust">
              <li>{t('landing.trust.uptime')}</li>
              <li>{t('landing.trust.caching')}</li>
              <li>{t('landing.trust.routing')}</li>
            </ul>
          </div>

          <aside className="wg-access-card" aria-label={t('landing.access.title')}>
            <div className="wg-access-head">
              <span className="wg-access-title">{t('landing.access.title')}</span>
              <Link to="/models" className="wg-access-link">
                {t('landing.access.viewAll')}
                <IconChevronRight size="small" />
              </Link>
            </div>
            <div className="wg-access-rows">
              {models === null ? (
                <div className="wg-center"><Spin size="large" /></div>
              ) : accessModels.length === 0 ? (
                <div className="wg-center" style={{ color: 'var(--wg-text-3)', fontSize: 13 }}>
                  {t('landing.access.empty')}
                </div>
              ) : (
                accessModels.map((m) => (
                  <AccessRow key={m.api_model_name} model={m} />
                ))
              )}
            </div>
            <div className="wg-access-foot">{t('landing.access.foot')}</div>
          </aside>
        </div>
      </section>

      {/* features */}
      <section className="wg-section">
        <div className="wg-container">
          <h2 className="wg-features-title">{t('landing.features.title')}</h2>
          <div className="wg-features">
            <div className="wg-feature-card">
              <span className="wg-feature-icon wg-tint-primary" aria-hidden>
                <IconBolt />
              </span>
              <span className="wg-feature-title">{t('landing.features.f1t')}</span>
              <p className="wg-feature-desc">{t('landing.features.f1d')}</p>
            </div>
            <div className="wg-feature-card">
              <span className="wg-feature-icon" aria-hidden>
                <IconTick />
              </span>
              <span className="wg-feature-title">{t('landing.features.f2t')}</span>
              <p className="wg-feature-desc">{t('landing.features.f2d')}</p>
            </div>
            <div className="wg-feature-card">
              <span className="wg-feature-icon" aria-hidden>
                <IconSafe />
              </span>
              <span className="wg-feature-title">{t('landing.features.f3t')}</span>
              <p className="wg-feature-desc">{t('landing.features.f3d')}</p>
            </div>
          </div>
        </div>
      </section>

      {/* FAQ */}
      <section className="wg-section" style={{ paddingTop: 8 }}>
        <div className="wg-container">
          <h2 className="wg-features-title">{t('landing.faq.title')}</h2>
          <div className="wg-faq-wrap">
            <Collapse accordion defaultActiveKey="q1">
              {(['q1', 'q2', 'q3', 'q4', 'q5'] as const).map((k) => (
                <Collapse.Panel itemKey={k} key={k} header={t(`landing.faq.${k}`)}>
                  {t(`landing.faq.a${k.slice(1)}`)}
                </Collapse.Panel>
              ))}
            </Collapse>
          </div>
        </div>
      </section>

      {/* CTA band */}
      <section className="wg-section" style={{ paddingTop: 8 }}>
        <div className="wg-container">
          <div className="wg-cta">
            <h2 className="wg-cta-title">{t('landing.cta.title')}</h2>
            <p className="wg-cta-sub">{t('landing.cta.subtitle')}</p>
            <div className="wg-cta-actions">
              <Button theme="solid" type="primary" size="large">{t('nav.getKey')}</Button>
              <Link to="/models">
                <Button theme="light" size="large">{t('nav.models')}</Button>
              </Link>
            </div>
          </div>
        </div>
      </section>

      <Footer />
    </div>
  );
}

function AccessRow({ model }: { model: PublicModel }) {
  const { t } = useI18n();
  const typeLabel = t(`models.type.${model.model_type ?? 'other'}`);
  return (
    <Link to="/models" className="wg-access-row">
      <ProviderIcon provider={model.provider} manufacturer={model.manufacturer} size={28} />
      <span className="wg-access-name">{model.api_model_name}</span>
      <span className="wg-access-meta">
        {model.provider} · {typeLabel}
      </span>
    </Link>
  );
}

function Footer() {
  const { t } = useI18n();
  return (
    <footer className="wg-footer">
      <div className="wg-container">
        <div className="wg-footer-grid">
          <div>
            <Link to="/" className="wg-logo">
              <span className="wg-logo-dot">W</span>
              WOODGIT
            </Link>
            <p className="wg-footer-desc">{t('landing.footer.tagline')}</p>
          </div>
          <div className="wg-footer-col">
            <h3 className="wg-footer-h">{t('landing.footer.product')}</h3>
            <Link to="/models">{t('nav.models')}</Link>
            <a href="#enterprise" onClick={(e) => e.preventDefault()}>{t('nav.enterprise')}</a>
            <a href="#pricing" onClick={(e) => e.preventDefault()}>{t('models.sheet.price.title')}</a>
          </div>
          <div className="wg-footer-col">
            <h3 className="wg-footer-h">{t('landing.footer.resources')}</h3>
            <a href="#docs" onClick={(e) => e.preventDefault()}>{t('nav.docs')}</a>
            <a href="#api" onClick={(e) => e.preventDefault()}>{t('landing.footer.apiRef')}</a>
            <a href="#faq" onClick={(e) => e.preventDefault()}>{t('landing.footer.faq')}</a>
          </div>
          <div className="wg-footer-col">
            <h3 className="wg-footer-h">{t('landing.footer.company')}</h3>
            <a href="#about" onClick={(e) => e.preventDefault()}>{t('landing.footer.about')}</a>
            <a href="#contact" onClick={(e) => e.preventDefault()}>{t('landing.footer.contact')}</a>
            <a href="#status" onClick={(e) => e.preventDefault()}>{t('landing.footer.status')}</a>
          </div>
        </div>
        <div className="wg-footer-bottom">
          <span>{t('landing.footer.rights')}</span>
          <span>{BASE_URL}</span>
        </div>
      </div>
    </footer>
  );
}
