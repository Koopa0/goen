export const WCAG_LEVEL = 'WCAG 2.2 level AA';
export const WCAG_TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'];
export const AXE_OPTIONS = {
  runOnly: { type: 'tag', values: [...WCAG_TAGS, 'best-practice'] },
  resultTypes: ['violations', 'incomplete'],
};

// Minor and moderate findings remain annotations: a gate failing all four
// impacts would be turned off within a week.
const gatingImpacts = new Set(['serious', 'critical']);

// Best-practice rules include opinions about landmarks and heading order;
// making those block the build is how a gate gets disabled.
export const gatesAccessibility = (finding) => gatingImpacts.has(finding.impact)
  && finding.tags.some((tag) => WCAG_TAGS.includes(tag));

// Tag selection enables target-size even though it is disabled by default;
// axe still excludes experimental and deprecated tags unless explicitly selected.
export const wcagRuleExclusion = (rule) => rule.tags.find((tag) =>
  tag === 'experimental' || tag === 'deprecated') ?? null;
