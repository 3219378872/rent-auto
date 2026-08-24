// Pure helpers extracted from Dashboard for unit testing.
export const scaleY = (v: number, min: number, max: number, h: number): number =>
  h - ((v - min) / (max - min || 1)) * (h - 8) - 4

// channelIssues extracts non-ok entries from the /channels health map —
// rendered as the dashboard alert bar (US-DASH-03).
export function channelIssues(health: Record<string, string>): string[] {
  return Object.entries(health)
    .filter(([, v]) => v !== 'ok')
    .map(([ch, v]) => `${ch.toUpperCase()}：${v}`)
}
