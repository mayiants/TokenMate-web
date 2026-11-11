import type { CSSProperties } from 'react';

import anthropic from '../assets/providers/provider-anthropic.svg';
import approach from '../assets/providers/provider-approach.svg';
import bytedance from '../assets/providers/provider-bytedance.svg';
import deepseek from '../assets/providers/provider-deepseek.svg';
import google from '../assets/providers/provider-google.svg';
import meta from '../assets/providers/provider-meta.svg';
import minimax from '../assets/providers/provider-minimax.svg';
import mistral from '../assets/providers/provider-mistral.svg';
import moonshot from '../assets/providers/provider-moonshot.svg';
import openai from '../assets/providers/provider-openai.svg';
import qwen from '../assets/providers/provider-qwen.svg';
import tokenrouter from '../assets/providers/provider-tokenrouter.svg';
import xai from '../assets/providers/provider-xai.svg';

export interface ProviderIconProps {
  provider?: string | null;
  manufacturer?: string | null;
  size?: number;
  style?: CSSProperties;
}

const iconByName: Record<string, string> = {
  openai,
  anthropic,
  google,
  deepseek,
  meta,
  mistral,
  xai,
  'x-ai': xai,
  moonshot,
  qwen,
  minimax,
  bytedance,
  approach,
  tokenrouter,
};

/**
 * Brand icon for a model provider. Falls back to a neutral grey
 * initial-letter Avatar (no random colors) when no local SVG matches.
 */
export default function ProviderIcon({ provider, manufacturer, size = 36, style }: ProviderIconProps) {
  const name = (provider ?? '').trim();
  const icon = iconByName[name.toLowerCase()];
  if (icon) {
    return (
      <img
        src={icon}
        alt={name || 'provider'}
        width={size}
        height={size}
        style={{ borderRadius: size / 4, display: 'block', ...style }}
      />
    );
  }
  const label = (name || manufacturer || '?').trim();
  const initial = label[0]?.toUpperCase() ?? '?';
  // Plain span instead of Semi's Avatar: Avatar always renders role="listitem",
  // which fails axe's aria-required-parent when used as a standalone icon.
  return (
    <span
      role="img"
      aria-label={label}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: size,
        height: size,
        borderRadius: size / 4,
        background: 'var(--wg-text-3)',
        color: '#fff',
        fontSize: Math.round(size * 0.42),
        fontWeight: 600,
        lineHeight: 1,
        ...style,
      }}
    >
      {initial}
    </span>
  );
}
