import { useCallback, useEffect, useState } from 'react'
import { api } from '../api/client'

type Health = Record<string, string>

type SmsResp = {
  session_id: string
  mode: 'down' | 'up'
  msg?: string
  sms_up_content?: string
  sms_up_number?: string
}

export default function Channels() {
  const [health, setHealth] = useState<Health | null>(null)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const [phone, setPhone] = useState('')
  const [code, setCode] = useState('')
  const [session, setSession] = useState('')
  const [sms, setSms] = useState<SmsResp | null>(null)
  const [partnerId, setPartnerId] = useState('')
  const [privateKey, setPrivateKey] = useState('')
  const [steamId, setSteamId] = useState('')

  const load = useCallback(() => {
    api.get<Health>('/channels').then(setHealth).catch((e) => setErr(e.message))
  }, [])
  useEffect(load, [load])

  const sendSms = async () => {
    setErr(''); setMsg('')
    try {
      const r = await api.post<SmsResp>('/channels/uu/sms', { phone })
      setSession(r.session_id)
      setSms(r)
      setMsg(r.mode === 'up' ? '' : '验证码已发送，请查收短信')
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  const verifySms = async () => {
    setErr(''); setMsg('')
    try {
      await api.post('/channels/uu/sms-verify', { phone, code, session_id: session })
      setMsg('UU 登录成功，token 已入库')
      load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  const saveEco = async () => {
    setErr(''); setMsg('')
    try {
      await api.put('/channels/eco', { partner_id: partnerId, private_key_pem: privateKey, steam_id: steamId })
      setMsg('ECO 凭证已加密保存并验证')
      load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  const badge = (s: string) => (s === 'ok' ? 'ok' : s.startsWith('error') ? 'bad' : '')

  return (
    <div>
      <h2>渠道账号</h2>
      {err && <div className="error">{err}</div>}
      {msg && <div className="ok-msg">{msg}</div>}

      <div className="section">
        <h3 style={{ marginTop: 0 }}>健康状态</h3>
        {!health && <div className="muted">加载中…</div>}
        {health && Object.entries(health).map(([ch, st]) => (
          <p key={ch} style={{ margin: '4px 0' }}>
            <b>{ch.toUpperCase()}</b>
            {' '}<span className={`badge ${badge(st)}`}>{st}</span>
          </p>
        ))}
      </div>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>悠悠有品 · 短信登录</h3>
        <div className="toolbar">
          <input placeholder="+86 手机号" value={phone} onChange={(e) => setPhone(e.target.value)} />
          <button onClick={sendSms} disabled={!phone}>发送验证码</button>
          {session && (
            <>
              <input placeholder="6位验证码（留空=短信上行）" value={code} onChange={(e) => setCode(e.target.value)} />
              <button onClick={verifySms}>登录</button>
            </>
          )}
        </div>
        {sms?.mode === 'up' && (
          <div className="muted" style={{ marginTop: 8 }}>
            平台未下发验证码，该手机号需短信上行验证：请用本机编辑短信{' '}
            <b>{sms.sms_up_content || '（获取失败，请重试发送）'}</b> 发送至 <b>{sms.sms_up_number || '?'}</b>
            ，发送完成后验证码留空，直接点击登录
          </div>
        )}
        {session && <div className="muted">session: {session.slice(0, 8)}…</div>}
      </div>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>ECOSteam · 开放平台凭证</h3>
        <div className="muted" style={{ marginBottom: 8 }}>
          私钥为 PKCS8 PEM 或裸 base64；保存后以 AES-256-GCM 加密存储，仅显示指纹
        </div>
        <div className="toolbar">
          <input placeholder="PartnerId" value={partnerId} onChange={(e) => setPartnerId(e.target.value)} />
          <input placeholder="SteamID（可空）" value={steamId} onChange={(e) => setSteamId(e.target.value)} />
        </div>
        <textarea
          rows={5}
          placeholder={'-----BEGIN PRIVATE KEY-----\\n...\\n-----END PRIVATE KEY-----'}
          value={privateKey}
          onChange={(e) => setPrivateKey(e.target.value)}
          style={{ width: '100%', fontFamily: 'monospace', fontSize: 12 }}
        />
        <div className="toolbar" style={{ marginTop: 10 }}>
          <button onClick={saveEco} disabled={!partnerId || !privateKey}>保存并验证</button>
        </div>
      </div>
    </div>
  )
}
