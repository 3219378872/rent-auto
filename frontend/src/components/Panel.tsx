import { useContext, useEffect, useId, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Link, useBlocker, UNSAFE_DataRouterContext } from 'react-router-dom'
import type { ExecutionStatus, JobStatus, LastDecision } from '../api/client'
import { useResource } from '../lib/data'
import { jobLabels, reasonLabel, statusLabel, time } from '../lib/format'

export function Feedback({ err, msg }: { err?: string; msg?: string }) {
  return (
    <>
      {err && (
        <div className="error" role="alert">
          {err}
        </div>
      )}
      {msg && (
        <div className="ok-msg" role="status">
          {msg}
        </div>
      )}
    </>
  )
}

export function LoadState({
  data,
  err,
  loading,
  reload,
  empty,
  filtered,
  reset,
  emptyText = '暂无数据，完成渠道配置并等待同步后可在此查看。',
}: {
  data?: unknown
  err: string
  loading: boolean
  reload: () => void
  empty?: boolean
  filtered?: boolean
  reset?: () => void
  emptyText?: string
}) {
  return (
    <>
      {err && (
        <div className="error" role="alert">
          {err}{' '}
          <button
            type="button"
            className="ghost small"
            onClick={reload}
            disabled={loading}
          >
            重试
          </button>
          {data !== undefined && <span> · 以下为上次成功获取的数据</span>}
        </div>
      )}
      {loading && (
        <div className="muted loading-state" role="status">
          {data === undefined ? '正在加载…' : '正在更新，以下为上次结果…'}
        </div>
      )}
      {!loading && !err && empty && (
        <div className="empty-state">
          {filtered ? '没有符合当前筛选的结果。' : emptyText}
          {filtered && reset && (
            <button className="ghost" onClick={reset}>
              清除筛选
            </button>
          )}
        </div>
      )}
    </>
  )
}

export function StatusBadge({ value }: { value: string }) {
  const style = ['in_stock', 'active', 'leasing', 'done'].includes(value)
    ? 'ok'
    : ['locked', 'breach', 'arbitrating', 'unknown', 'stale'].includes(value)
      ? 'bad'
      : 'warn'
  return <span className={`badge ${style}`}>{statusLabel(value)}</span>
}

export function CopyButton({
  value,
  label = '复制',
}: {
  value: string
  label?: string
}) {
  const [message, setMessage] = useState('')
  return (
    <span className="copy-control">
      <button
        className="ghost small"
        onClick={async () => {
          try {
            await navigator.clipboard.writeText(value)
            setMessage('已复制')
          } catch {
            setMessage('复制失败，请手动选择文本')
          }
        }}
      >
        {label}
      </button>
      <span role="status" className="muted">
        {message}
      </span>
    </span>
  )
}

export function Modal({
  title,
  children,
  onClose,
  actions,
}: {
  title: string
  children: ReactNode
  onClose: () => void
  actions?: ReactNode
}) {
  const ref = useRef<HTMLDialogElement>(null),
    id = useId()
  useEffect(() => {
    const trigger = document.activeElement as HTMLElement | null
    const el = ref.current
    el?.showModal()
    return () => {
      el?.close()
      trigger?.focus()
    }
  }, [])
  return (
    <dialog
      ref={ref}
      className="detail-dialog"
      aria-labelledby={id}
      onCancel={(e) => {
        e.preventDefault()
        onClose()
      }}
    >
      <header className="page-heading">
        <h2 id={id}>{title}</h2>
        <button className="ghost small" onClick={onClose} aria-label="关闭详情">
          关闭
        </button>
      </header>
      {children}
      {actions && <div className="toolbar dialog-actions">{actions}</div>}
    </dialog>
  )
}

export function RecordDetail({ detail }: { detail: unknown }) {
  const text = JSON.stringify(detail, null, 2)
  return (
    <>
      <CopyButton value={text} label="复制详情" />
      <pre className="json-detail" tabIndex={0}>
        {text}
      </pre>
    </>
  )
}

export function ItemLinks({ hash, asset }: { hash: string; asset?: string }) {
  const q = new URLSearchParams({ hash_name: hash })
  if (asset) q.set('asset_id', asset)
  return (
    <nav className="context-links" aria-label="相关记录">
      <Link to={`/inventory?${q}`}>库存</Link>
      <Link to={`/listings?state=&${q}`}>货架</Link>
      <Link to={`/orders?${q}`}>订单</Link>
      <Link to={`/audit?tab=pricing&${q}`}>定价记录</Link>
    </nav>
  )
}

export function DecisionBadge({
  decision,
}: {
  decision: Pick<LastDecision, 'success' | 'dry_run' | 'action'>
}) {
  return (
    <span
      className={`badge ${decision.success === false ? 'bad' : decision.dry_run || decision.action === 'skip' ? 'warn' : 'ok'}`}
    >
      {decision.action === 'skip'
        ? '跳过'
        : decision.success === false
          ? '执行失败'
          : decision.dry_run
            ? '模拟建议'
            : decision.success === true
              ? '执行成功'
              : '结果未记录'}
    </span>
  )
}

export function ExecutionMode({ hash = '' }: { hash?: string }) {
  const state = useResource<ExecutionStatus>(
    `/execution/status${hash ? `?hash_name=${encodeURIComponent(hash)}` : ''}`,
    30000,
  )
  useEffect(() => {
    window.addEventListener('ra-execution-changed', state.reload)
    return () =>
      window.removeEventListener('ra-execution-changed', state.reload)
  }, [state.reload])
  if (!state.data || typeof state.data.dry_run !== 'boolean' || state.err)
    return (
      <span className="execution-state">
        <span className="badge warn">
          {state.loading ? '执行模式加载中' : '执行模式未知'}
        </span>
        {state.err && (
          <button type="button" className="ghost small" onClick={state.reload}>
            重试状态
          </button>
        )}
      </span>
    )
  return (
    <span className="execution-state">
      <span className={`badge ${state.data.dry_run ? 'warn' : 'ok'}`}>
        {state.data.dry_run ? '模拟执行' : '允许真实执行'}
      </span>
      <span className="muted">
        {state.data.reasons.map(reasonLabel).join('；') ||
          '任务仍受模板许可、渠道状态与护栏约束'}
      </span>
    </span>
  )
}

export function JobList({
  only,
  onRefresh,
}: {
  only?: string
  onRefresh?: () => void
}) {
  const state = useResource<JobStatus[]>('/jobs', 15000)
  const items = Array.isArray(state.data)
    ? state.data.filter((job) => !only || job.name === only)
    : []
  return (
    <section className="section">
      <div className="page-heading">
        <h3>最近任务状态</h3>
        <button
          className="ghost small"
          onClick={() => {
            state.reload()
            onRefresh?.()
          }}
          disabled={state.loading}
        >
          刷新任务
        </button>
      </div>
      <LoadState
        {...state}
        empty={items.length === 0}
        emptyText="暂无已注册任务。"
      />
      {items.map((job) => (
        <div className="job-row" key={job.name}>
          <div>
            <strong>{jobLabels[job.name] || job.name}</strong>
            <div className="muted">
              最近运行：{time(job.last_run)} · 下次计划：{time(job.next_run)}
            </div>
            {job.last_error && <div className="error">{job.last_error}</div>}
          </div>
          <span
            className={`badge ${job.running ? 'warn' : job.last_ok ? 'ok' : job.last_run ? 'bad' : ''}`}
          >
            {job.running
              ? '运行中'
              : !job.last_run
                ? '尚未运行'
                : job.last_ok
                  ? '最近一次成功'
                  : '最近一次失败'}
          </span>
        </div>
      ))}
      <p className="hint">
        显示任务的最近状态；“已受理”不代表完成，也不保证这里的最近记录对应某一次点击。
      </p>
    </section>
  )
}

function RouterDraftGuard({ dirty }: { dirty: boolean }) {
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty && currentLocation.pathname !== nextLocation.pathname,
  )
  useEffect(() => {
    if (blocker.state === 'blocked') {
      if (window.confirm('有未保存更改。放弃更改并离开？')) blocker.proceed()
      else blocker.reset()
    }
  }, [blocker])
  return null
}

export function DraftGuard({ dirty }: { dirty: boolean }) {
  const inRouter = useContext(UNSAFE_DataRouterContext)
  useEffect(() => {
    const handler = (e: BeforeUnloadEvent) => {
      if (dirty) {
        e.preventDefault()
        e.returnValue = ''
      }
    }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [dirty])
  return inRouter ? <RouterDraftGuard dirty={dirty} /> : null
}
