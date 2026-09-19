import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api } from '../api/client'

export const errorText = (error: unknown): string => {
  const message = error instanceof Error ? error.message : String(error)
  const known: Record<string, string> = {
    'internal error': '服务暂不可用，请稍后重试',
    'trigger failed': '任务未能受理，请检查任务状态后重试',
    'Failed to fetch': '网络连接失败，请检查网络后重试',
    'too many attempts, retry later': '操作过于频繁，请稍后重试',
  }
  return known[message] ?? message
}

export function useResource<T>(path: string, interval = 0) {
  const [revision, setRevision] = useState(0)
  const signature = `${path}:${revision}`
  const [state, setState] = useState<{
    path: string
    signature: string
    data?: T
    err: string
    updatedAt?: string
    pending: boolean
  }>({ path: '', signature: '', err: '', pending: false })
  const reload = useCallback(() => setRevision((v) => v + 1), [])
  useEffect(() => {
    if (!path) return
    let active = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const controller = new AbortController()
    const load = async () => {
      if (!active) return
      setState((s) => ({ ...s, pending: true }))
      try {
        const data = await api.get<T>(path, controller.signal)
        if (active)
          setState({
            path,
            signature,
            data,
            err: '',
            updatedAt: new Date().toISOString(),
            pending: false,
          })
      } catch (error) {
        if (active)
          setState((s) => ({
            path,
            signature,
            data: s.path === path ? s.data : undefined,
            updatedAt: s.path === path ? s.updatedAt : undefined,
            pending: false,
            err: errorText(error),
          }))
      } finally {
        if (active && interval > 0) timer = setTimeout(poll, interval)
      }
    }
    const poll = () => {
      if (document.visibilityState === 'hidden')
        timer = setTimeout(poll, interval)
      else void load()
    }
    void load()
    return () => {
      active = false
      controller.abort()
      clearTimeout(timer)
    }
  }, [path, signature, interval])
  const samePath = state.path === path
  return {
    data: samePath ? state.data : undefined,
    err: samePath ? state.err : '',
    loading: !!path && (state.signature !== signature || state.pending),
    updatedAt: samePath ? state.updatedAt : undefined,
    reload,
  }
}

export function useFilters<T extends Record<string, string>>(defaults: T) {
  const [params, setParams] = useSearchParams()
  const values = Object.fromEntries(
    Object.entries(defaults).map(([key, fallback]) => [
      key,
      params.get(key) ?? fallback,
    ]),
  ) as T
  const set = (changes: Partial<T>) =>
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous)
        for (const [key, value] of Object.entries(changes)) {
          if (value === defaults[key]) next.delete(key)
          else if (value !== undefined) next.set(key, value)
        }
        next.delete('page')
        return next
      },
      { replace: true },
    )
  const reset = () =>
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous)
        for (const key of Object.keys(defaults)) next.delete(key)
        next.delete('page')
        return next
      },
      { replace: true },
    )
  return { values, set, reset }
}

export function useAction() {
  const lock = useRef(false)
  const alive = useRef(true)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  const run = async (action: () => Promise<unknown>, message = '已保存') => {
    if (lock.current) return false
    lock.current = true
    setBusy(true)
    setErr('')
    setMsg('')
    try {
      await action()
      if (alive.current && message) setMsg(message)
      return true
    } catch (e) {
      if (alive.current) setErr(errorText(e))
      return false
    } finally {
      lock.current = false
      if (alive.current) setBusy(false)
    }
  }
  return { busy, err, msg, run, setErr, setMsg }
}
