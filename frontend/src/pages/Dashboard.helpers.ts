// Pure helpers extracted from Dashboard for unit testing.
export const scaleY = (v: number, min: number, max: number, h: number): number =>
  h - ((v - min) / (max - min || 1)) * (h - 8) - 4
