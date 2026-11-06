import { useEffect, useRef, useState } from 'react';
import { Toast, Tooltip } from '@douyinfe/semi-ui';
import { IconCopy, IconTick } from '@douyinfe/semi-icons';
import { copyText } from '../utils/format';
import { useI18n } from '../i18n';

interface CopyButtonProps {
  text: string;
  /** show a toast in addition to the inline check icon */
  toast?: boolean;
  className?: string;
  ariaLabel?: string;
  stop?: boolean;
}

/** Copy icon button with inline copied ✓ feedback (1.5s). */
export default function CopyButton({ text, toast = false, className = '', ariaLabel, stop = true }: CopyButtonProps) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const timer = useRef<number>();

  useEffect(() => () => window.clearTimeout(timer.current), []);

  const onClick = async (e: React.MouseEvent) => {
    if (stop) e.stopPropagation();
    const ok = await copyText(text);
    if (!ok) {
      Toast.warning(t('common.copyFailed'));
      return;
    }
    setCopied(true);
    if (toast) Toast.success(t('common.copied'));
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopied(false), 1500);
  };

  return (
    <Tooltip content={copied ? t('common.copied') : t('common.copy')}>
      <button
        type="button"
        className={`wg-icon-btn ${className} ${copied ? 'wg-copied' : ''}`}
        onClick={onClick}
        aria-label={ariaLabel ?? t('common.copy')}
      >
        {copied ? <IconTick /> : <IconCopy />}
      </button>
    </Tooltip>
  );
}
