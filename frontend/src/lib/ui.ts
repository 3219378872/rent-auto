import { useEffect, useState } from 'react'

// useDebounced delays mirroring of a fast-changing value (e.g. search input)
// so consumers fire requests at most once per delay window.
export function useDebounced<T>(value: T, delay = 300): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setV(value), delay)
    return () => clearTimeout(t)
  }, [value, delay])
  return v
}

// csvCell escapes one field: quotes doubled, wrapped in quotes, and values
// that spreadsheet software may interpret as formulas are neutralised.
export function csvCell(v: string | number): string {
  let s = String(v)
  if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`
  return `"${s.replace(/"/g, '""')}"`
}
