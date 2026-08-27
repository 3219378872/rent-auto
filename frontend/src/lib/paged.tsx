import { useCallback, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { api, Paged } from '../api/client'

// usePagedList owns the state machine shared by the inventory / listings /
// orders / audit pages: paged fetch with an alive-guard (no stale races),
// page counter, error string and reload handle. Filters are page-owned state
// passed as a plain record; changing them does NOT reset the page
// automatically — pages call setPage(1) in their own onChange handlers so
// that filter edits stay deliberate.
export function usePagedList<T>(path: string, filters: Record<string, string> = {}, pageSize = 50) {
  const [data, setData] = useState<Paged<T> | null>(null)
  const [page, setPage] = useState(1)
  const [err, setErr] = useState('')

  // Objects are recreated per render; depend on the serialized form so the
  // query only rebuilds when a filter value actually changed.
  const filterKey = JSON.stringify(filters)
  const query = useMemo(() => {
    const q = new URLSearchParams({ page: String(page), page_size: String(pageSize) })
    for (const [k, v] of Object.entries(JSON.parse(filterKey) as Record<string, string>)) {
      if (v) q.set(k, v)
    }
    return q
  }, [filterKey, page, pageSize])

  const load = useCallback(() => {
    let alive = true
    api.get<Paged<T>>(`${path}?${query}`)
      .then((d) => { if (alive) { setData(d); setErr('') } })
      .catch((e) => { if (alive) setErr(e instanceof Error ? e.message : String(e)) })
    return () => { alive = false }
  }, [path, query])

  useEffect(load, [load])

  return { data, page, setPage, err, setErr, reload: load }
}

// Pager renders the shared 上一页/下一页 footer; `meta` replaces the middle
// label (pages that show totals pass their own node).
export function Pager(props: {
  page: number; total?: number; pageSize: number
  onPage: (p: number) => void; meta?: ReactNode
}) {
  const { page, total, pageSize, onPage, meta } = props
  const noNext = total === undefined ? true : page * pageSize >= total
  return (
    <div className="toolbar" style={{ marginTop: 12 }}>
      <button className="ghost small" disabled={page <= 1} onClick={() => onPage(page - 1)}>上一页</button>
      <span className="muted">{meta ?? `第 ${page} 页`}</span>
      <button className="ghost small" disabled={noNext} onClick={() => onPage(page + 1)}>下一页</button>
    </div>
  )
}
