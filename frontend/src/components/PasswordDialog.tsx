import { useEffect, useRef, useState } from 'react'
import { api } from '../api/client'

export default function PasswordDialog({ onClose, onChanged }: { onClose: () => void; onChanged: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    const element = dialog.current
    element?.showModal()
    return () => element?.close()
  }, [])

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErr('')
    const bytes = new TextEncoder().encode(next).length
    if (bytes < 12 || bytes > 72) { setErr('新密码必须为 12–72 字节'); return }
    if (next !== confirmation) { setErr('两次输入的新密码不一致'); return }
    setBusy(true)
    try {
      await api.put('/auth/password', { current_password: current, new_password: next })
      setCurrent(''); setNext(''); setConfirmation('')
      onChanged()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <dialog ref={dialog} className="account-dialog" aria-labelledby="password-title"
      onCancel={(e) => { e.preventDefault(); if (!busy) onClose() }}>
      <form onSubmit={submit}>
        <h2 id="password-title">修改密码</h2>
        {err && <div className="error" role="alert">{err}</div>}
        <label>当前密码<input type="password" autoComplete="current-password" autoFocus required
          value={current} onChange={(e) => setCurrent(e.target.value)} /></label>
        <label>新密码<input type="password" autoComplete="new-password" required
          value={next} onChange={(e) => setNext(e.target.value)} /></label>
        <label>确认新密码<input type="password" autoComplete="new-password" required
          value={confirmation} onChange={(e) => setConfirmation(e.target.value)} /></label>
        <div className="toolbar">
          <button type="submit" disabled={busy}>{busy ? '保存中…' : '保存新密码'}</button>
          <button type="button" className="ghost" onClick={onClose} disabled={busy}>取消</button>
        </div>
      </form>
    </dialog>
  )
}
