import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { Paged } from '../api/client'
import { useResource } from './data'

export function usePagedList<T>(
  path: string,
  filters: Record<string, string> = {},
  pageSize = 50,
  interval = 0,
  enabled = true,
) {
  const [params, setParams] = useSearchParams()
  const page = Math.max(
    1,
    Math.min(1000000, Number.parseInt(params.get('page') ?? '1') || 1),
  )
  const filterKey = JSON.stringify(filters)
  const query = useMemo(() => {
    const q = new URLSearchParams({
      page: String(page),
      page_size: String(pageSize),
    })
    for (const [k, v] of Object.entries(
      JSON.parse(filterKey) as Record<string, string>,
    ))
      if (v) q.set(k, v)
    return q
  }, [filterKey, page, pageSize])
  const resource = useResource<Paged<T>>(
    enabled ? `${path}?${query}` : '',
    interval,
  )
  const [localError, setErr] = useState('')
  const setPage = (nextPage: number) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous)
      if (nextPage <= 1) next.delete('page')
      else next.set('page', String(nextPage))
      return next
    })
  return { ...resource, page, setPage, err: localError || resource.err, setErr }
}

export function Pager({
  page,
  total,
  pageSize,
  onPage,
  meta,
  loading = false,
}: {
  page: number
  total?: number
  pageSize: number
  onPage: (p: number) => void
  meta?: ReactNode
  loading?: boolean
}) {
  return (
    <div className="toolbar pager" aria-label="分页">
      <button
        type="button"
        className="ghost small"
        disabled={loading || page <= 1}
        onClick={() => onPage(page - 1)}
      >
        上一页
      </button>
      <span className="muted">{meta ?? `第 ${page} 页`}</span>
      {total !== undefined && !meta && (
        <span className="muted">
          共 {Math.max(1, Math.ceil(total / pageSize))} 页 · 每页 {pageSize} 条
        </span>
      )}
      <button
        type="button"
        className="ghost small"
        disabled={loading || total === undefined || page * pageSize >= total}
        onClick={() => onPage(page + 1)}
      >
        下一页
      </button>
    </div>
  )
}
