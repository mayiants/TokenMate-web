import { useState, type ReactNode } from 'react';
import { Collapse, Tooltip } from '@douyinfe/semi-ui';
import { IconInfoCircle } from '@douyinfe/semi-icons';

export interface DrawerSection {
  key: string;
  /** 15px / 600 */
  title: string;
  /** one-liner under the title, ≤20 chars */
  desc?: string;
  /** longer explanation, folded into an ⓘ tooltip next to the description */
  tip?: string;
  children: ReactNode;
}

interface Props {
  sections: DrawerSection[];
  /** controlled open section (accordion keeps a single one); omit for uncontrolled */
  activeKey?: string[];
  onChange?: (keys: string[]) => void;
}

function SectionHead({ title, desc, tip }: Pick<DrawerSection, 'title' | 'desc' | 'tip'>) {
  return (
    <div className="wg-section-head">
      <span className="wg-section-title">{title}</span>
      {desc &&
        (tip ? (
          <Tooltip content={tip} position="topLeft">
            <span className="wg-section-desc wg-section-desc-tip">
              {desc}
              <IconInfoCircle size="small" className="wg-info-icon" />
            </span>
          </Tooltip>
        ) : (
          <span className="wg-section-desc">{desc}</span>
        ))}
    </div>
  );
}

/**
 * Drawer body groups. >2 sections fold into an accordion with only the first
 * one open; ≤2 sections stay flat (nothing worth folding).
 */
export default function DrawerSections({ sections, activeKey, onChange }: Props) {
  const [inner, setInner] = useState<string[]>(() => (sections[0] ? [sections[0].key] : []));
  const active = activeKey ?? inner;

  const update = (keys: string[]) => {
    if (activeKey === undefined) setInner(keys);
    onChange?.(keys);
  };

  if (sections.length <= 2) {
    return (
      <div className="wg-sections wg-sections-plain">
        {sections.map((s) => (
          <section key={s.key} className="wg-section-plain">
            <SectionHead title={s.title} desc={s.desc} tip={s.tip} />
            <div className="wg-section-body">{s.children}</div>
          </section>
        ))}
      </div>
    );
  }

  return (
    <Collapse
      className="wg-sections"
      accordion
      activeKey={active[0] ?? ''}
      onChange={(k) => update(typeof k === 'string' ? (k ? [k] : []) : (k as string[]))}
    >
      {sections.map((s) => (
        <Collapse.Panel
          key={s.key}
          itemKey={s.key}
          header={<SectionHead title={s.title} desc={s.desc} tip={s.tip} />}
        >
          <div className="wg-section-body">{s.children}</div>
        </Collapse.Panel>
      ))}
    </Collapse>
  );
}
