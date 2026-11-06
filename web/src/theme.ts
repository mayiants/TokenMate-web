// Brand direction: light theme + #0086ff brand blue, hover #1890ff.
// Semi 2.x has no public ConfigProvider theme prop — design tokens are CSS
// variables, so the palette lives in styles/global.css (--wg-* overrides of
// --semi-color-primary*). This file only keeps the JS-side scale.
export const brandPrimary = '#0086ff';

export const semiTheme = {
  primary: brandPrimary,
  primaryHover: '#1890ff',
  primaryActive: '#006ad6',
  link: brandPrimary,
} as const;
