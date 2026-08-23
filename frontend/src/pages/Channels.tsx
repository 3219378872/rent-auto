import { useCallback, useEffect, useState } from 'react'
import { api } from '../api/client'
import { solveCaptcha, UU_CAPTCHA_APP_ID } from '../lib/tcaptcha'

type Health = Record<string, string>

type SmsResp = {
  session_id: string
  mode: 'down' | 'up' | 'captcha'
  msg?: string
  sms_up_content?: string
  sms_up_number?: string
  req_ticket?: string
  secs?: number
  login_req_ticket?: string
}

export default function Channels() {
  const [health, setHealth] = useState<Health | null>(null)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const [phone, setPhone] = useState('')
  const [code, setCode] = useState('')
  const [session, setSession] = useState('')
  const [sms, setSms] = useState<SmsResp | null>(null)
  const [loginTicket, setLoginTicket] = useState('')
  const [cooldown, setCooldown] = useState(0)
  const [partnerId, setPartnerId] = useState('')
  const [privateKey, setPrivateKey] = useState('')
  const [steamId, setSteamId] = useState('')
  const [steamUser, setSteamUser] = useState('')
  const [steamPass, setSteamPass] = useState('')
  const [sharedSecret, setSharedSecret] = useState('')
  const [identitySecret, setIdentitySecret] = useState('')

  const load = useCallback(() => {
    api.get<Health>('/channels').then(setHealth).catch((e) => setErr(e.message))
  }, [])
  useEffect(load, [load])

  useEffect(() => {
    if (cooldown <= 0) return undefined
    const t = setTimeout(() => setCooldown((s) => s - 1), 1000)
    return () => clearTimeout(t)
  }, [cooldown])

  const sendSms = async (captcha?: { ticket: string; randstr: string; req_ticket: string }) => {
    setErr(''); setMsg('')
    try {
      const body: Record<string, unknown> = { phone }
      if (session) body.session_id = session
      if (captcha) body.captcha = captcha
      const r = await api.post<SmsResp>('/channels/uu/sms', body)
      setSession(r.session_id)
      if (r.secs && r.secs > 0) setCooldown(r.secs)
      if (r.mode === 'captcha') {
        setSms(null)
        setMsg('平台风控要求图形验证，请在弹窗中手动完成')
        try {
          const c = await solveCaptcha(UU_CAPTCHA_APP_ID)
          setMsg('图形验证通过，正在重新发送短信…')
          await sendSms({ ticket: c.ticket, randstr: c.randstr, req_ticket: r.req_ticket || '' })
        } catch (e2) {
          setMsg('')
          setErr(e2 instanceof Error ? e2.message : String(e2))
        }
        return
      }
      setSms(r)
      if (r.login_req_ticket) setLoginTicket(r.login_req_ticket)
      setMsg(r.mode === 'up' ? '' : '验证码已发送，请查收短信')
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  const verifySms = async () => {
    setErr(''); setMsg('')
    try {
      await api.post('/channels/uu/sms-verify', {
        phone, code, session_id: session, login_req_ticket: loginTicket || undefined,
      })
      setMsg('UU 登录成功，token 已入库')
      load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  const saveSteam = async () => {
    setErr(''); setMsg('')
    try {
      await api.put('/channels/steam', {
        username: steamUser, password: steamPass,
        shared_secret: sharedSecret, identity_secret: identitySecret,
      })
      setMsg('Steam 登录成功，会话已加密保存')
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

  const badge = (s: string) =>
    s === 'ok' || s.startsWith('ok:') ? 'ok' : s.startsWith('error') ? 'bad' : ''

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
          <button onClick={() => sendSms()} disabled={!phone || cooldown > 0}>
            {cooldown > 0 ? `发送验证码(${cooldown}s)` : '发送验证码'}
          </button>
          {session && (
            <>
              <input placeholder="6位验证码（留空=短信上行）" value={code} onChange={(e) => setCode(e.target.value)} />
              <button onClick={verifySms}>登录</button>
            </>
          )}
        </div>
        {sms?.mode === 'captcha' && (
          <div className="muted" style={{ marginTop: 8 }}>
            平台风控要求图形验证。若弹窗未出现或加载失败，请到 youpin898.com
            官网登录页完成一次图形验证后回来重试。
          </div>
        )}
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
          placeholder={'-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----'}
          value={privateKey}
          onChange={(e) => setPrivateKey(e.target.value)}
          style={{ width: '100%', fontFamily: 'monospace', fontSize: 12 }}
        />
        <div className="toolbar" style={{ marginTop: 10 }}>
          <button onClick={saveEco} disabled={!partnerId || !privateKey}>保存并验证</button>
        </div>
      </div>

      <div className="section">
        <h3 style={{ marginTop: 0 }}>Steam · 自动收报价（礼物 / 租赁归还）</h3>
        <div className="muted" style={{ marginBottom: 8 }}>
          需要 Steam Guard 令牌的 shared_secret 与 identity_secret（SDA/ Watt Toolkit 导出）。
          系统自动接受「我们不付出任何物品」的报价，其余报价仅记录不动。
        </div>
        <div className="toolbar">
          <input placeholder="Steam 用户名" value={steamUser} onChange={(e) => setSteamUser(e.target.value)} />
          <input placeholder="密码" type="password" value={steamPass} onChange={(e) => setSteamPass(e.target.value)} />
        </div>
        <div className="toolbar">
          <input placeholder="shared_secret (base64)" type="password" autoComplete="off"
            value={sharedSecret}
            onChange={(e) => setSharedSecret(e.target.value)} style={{ minWidth: 260 }} />
          <input placeholder="identity_secret (base64)" type="password" autoComplete="off"
            value={identitySecret}
            onChange={(e) => setIdentitySecret(e.target.value)} style={{ minWidth: 260 }} />
        </div>
        <div className="toolbar">
          <button onClick={saveSteam}
            disabled={!steamUser || !steamPass || !sharedSecret || !identitySecret}>
            登录 Steam 并保存
          </button>
        </div>
      </div>
    </div>
  )
}
