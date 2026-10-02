export const WCAG_TARGET = 'WCAG 2.2 level AA';
export const WCAG_TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'];
export const AXE_OPTIONS = {
  runOnly: { type: 'tag', values: [...WCAG_TAGS, 'best-practice'] },
  resultTypes: ['violations', 'incomplete'],
};

const gatingImpacts = new Set(['serious', 'critical']);

export const gatesAccessibility = (finding) => gatingImpacts.has(finding.impact)
  && finding.tags.some((tag) => WCAG_TAGS.includes(tag));
