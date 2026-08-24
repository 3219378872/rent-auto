import { describe, expect, it } from 'vitest'
import { csvCell } from './ui'

// csvCell is the only CSV formula-injection defense for the export path.
describe('csvCell', () => {
  it('prefixes dangerous leading characters with an apostrophe', () => {
    for (const dangerous of ['=SUM(A1)', '+1', '-1', '@cmd', '\tTab', '\rCR']) {
      const cell = csvCell(dangerous)
      // wrapped in quotes, with the neutralising apostrophe right inside
      expect(cell.startsWith('"\'')).toBe(true)
      expect(cell.slice(2, -1)).toBe(dangerous)
    }
  })

  it('escapes and wraps embedded quotes/newlines/commas', () => {
    expect(csvCell('say "hi"')).toBe('"say ""hi"""')
    expect(csvCell('a,b')).toBe('"a,b"')
    expect(csvCell('line\nbreak')).toBe('"line\nbreak"')
  })

  it('passes safe text through wrapped but unescaped', () => {
    expect(csvCell('AK-47 | Redline')).toBe('"AK-47 | Redline"')
    expect(csvCell('')).toBe('""')
  })
})
