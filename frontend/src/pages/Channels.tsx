import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type ChannelHealth } from '../api/client'
import { useAction, useResource } from '../lib/data'
import { Feedback, LoadState } from '../components/Panel'
import { time } from '../lib/format'
import { solveCaptcha, UU_CAPTCHA_APP_ID } from '../lib/tcaptcha'

type SavedCredential = { status: string; fingerprint?: string }
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
const healthLabel = (value?: string) =>
  !value
    ? '状态未知'
    : value === 'ok' || value.startsWith('ok:')
      ? '连接正常'
      : value === 'not_configured'
        ? '尚未配置'
        : '连接异常'

export default function Channels() {
  const health = useResource<ChannelHealth>('/channels', 30000)
  return (
    <div>
      <div className="page-heading">
        <h2>渠道账号</h2>
        <button
          className="ghost"
          disabled={health.loading}
          onClick={health.reload}
        >
          刷新健康状态
        </button>
      </div>
      <LoadState {...health} />
      <p className="muted">
        最近检查：{time(health.updatedAt)} · 展开渠道配置或更新凭证
      </p>
      {(['uu', 'eco', 'steam'] as const).map((ch) => (
        <details className="section channel-card" key={ch}>
          <summary>
            <strong>
              {ch === 'uu'
                ? '悠悠有品 · UU'
                : ch === 'eco'
                  ? 'ECOSteam · ECO'
                  : 'Steam'}
            </strong>
            <span
              className={`badge ${!health.err && health.data?.[ch]?.startsWith('ok') ? 'ok' : 'warn'}`}
            >
              {healthLabel(health.err ? undefined : health.data?.[ch])}
            </span>
          </summary>
          {health.data?.[ch] && !health.err && (
            <p className="hint">{health.data[ch]}</p>
          )}
          {ch === 'uu' ? (
            <UUForm reload={health.reload} />
          ) : ch === 'eco' ? (
            <EcoForm reload={health.reload} />
          ) : (
            <SteamForm reload={health.reload} />
          )}
        </details>
      ))}
      <p className="hint">
        成功保存后清空敏感输入，失败时保留输入供修正。凭证只提交到当前服务，不存为浏览器草稿。
      </p>
      <Link to="/audit">查看凭证变更审计</Link>
    </div>
  )
}

function UUForm({ reload }: { reload: () => void }) {
  const [token, setToken] = useState(''),
    action = useAction(),
    [fingerprint, setFingerprint] = useState('')
  const [phone, setPhone] = useState(''),
    [code, setCode] = useState(''),
    [session, setSession] = useState(''),
    [sms, setSms] = useState<SmsResp | null>(null),
    [loginTicket, setLoginTicket] = useState(''),
    [cooldown, setCooldown] = useState(0)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  useEffect(() => {
    if (cooldown <= 0) return
    const timer = setTimeout(() => setCooldown((c) => c - 1), 1000)
    return () => clearTimeout(timer)
  }, [cooldown])
  const sendSms = async (
    captcha?: { ticket: string; randstr: string; req_ticket: string },
    smsSessionId?: string,
  ): Promise<void> => {
    if (!alive.current) return
    const body: Record<string, unknown> = { phone }
    const sid = smsSessionId ?? session
    if (sid) body.session_id = sid
    if (captcha) body.captcha = captcha
    const r = await api.post<SmsResp>('/channels/uu/sms', body)
    if (!alive.current) return
    setSession(r.session_id)
    if (r.secs && r.secs > 0) setCooldown(r.secs)
    if (r.mode === 'captcha') {
      setSms(null)
      action.setMsg('平台风控要求图形验证，请在弹窗中手动完成')
      const c = await solveCaptcha(UU_CAPTCHA_APP_ID)
      // Preserve the upstream session and required cooldown across retries.
      for (let left = r.secs ?? 0; left > 0; left--) {
        if (!alive.current) return
        action.setMsg(`图形验证通过，平台冷却中，${left}s 后自动重发…`)
        await new Promise((resolve) => setTimeout(resolve, 1000))
      }
      if (alive.current)
        await sendSms(
          {
            ticket: c.ticket,
            randstr: c.randstr,
            req_ticket: r.req_ticket || '',
          },
          r.session_id,
        )
      return
    }
    setSms(r)
    if (r.login_req_ticket) setLoginTicket(r.login_req_ticket)
    action.setMsg(
      r.mode === 'up' ? '请按下方说明完成短信上行' : '验证码已发送，请查收短信',
    )
  }
  return (
    <>
      <h3>手动导入 Token（推荐）</h3>
      <p className="hint">
        在浏览器登录 youpin898.com 后，从开发者工具的应用存储或网络请求中复制
        JWT Token。导入时会验证并加密保存。
      </p>
      <form
        onSubmit={async (e) => {
          e.preventDefault()
          await action.run(async () => {
            const saved = await api.put<SavedCredential>('/channels/uu', {
              token: token.trim(),
            })
            setToken('')
            setFingerprint(saved.fingerprint || '')
            reload()
          }, 'UU Token 已验证并加密入库')
        }}
      >
        <label className="field">
          UU Token
          <textarea
            aria-label="UU Token"
            rows={3}
            required
            autoComplete="off"
            spellCheck={false}
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="粘贴 JWT Token"
          />
        </label>
        <button disabled={action.busy || !token.trim()}>
          {action.busy ? '验证中…' : '验证并保存 Token'}
        </button>
      </form>
      <Feedback {...action} />
      {fingerprint && <p className="hint">已保存 Token 尾号：{fingerprint}</p>}
      <details className="secondary-flow">
        <summary>短信登录（当前被平台限制）</summary>
        <p className="hint">
          平台已限制第三方短信登录。此入口保留用于平台恢复后的验证，可能仍被拒绝。
        </p>
        <div className="toolbar">
          <label className="field">
            手机号
            <input
              placeholder="+86 手机号"
              type="tel"
              autoComplete="tel"
              value={phone}
              onChange={(e) => {
                setPhone(e.target.value)
                setSession('')
                setLoginTicket('')
                setSms(null)
              }}
              disabled={action.busy}
            />
          </label>
          <button
            type="button"
            onClick={() => action.run(() => sendSms(), '')}
            disabled={action.busy || !phone || cooldown > 0}
          >
            {action.busy
              ? '请求处理中…'
              : cooldown > 0
                ? `发送验证码(${cooldown}s)`
                : '发送验证码'}
          </button>
        </div>
        {sms?.mode === 'up' && (
          <p>
            请用本机编辑短信 <b>{sms.sms_up_content || '获取失败，请重试'}</b>{' '}
            发送至 <b>{sms.sms_up_number || '未提供'}</b>
            ，完成后验证码留空，点击登录。
          </p>
        )}
        {session && (
          <form
            onSubmit={async (e) => {
              e.preventDefault()
              await action.run(async () => {
                await api.post('/channels/uu/sms-verify', {
                  phone,
                  code,
                  session_id: session,
                  login_req_ticket: loginTicket || undefined,
                })
                setCode('')
                setSession('')
                setLoginTicket('')
                setSms(null)
                reload()
              }, 'UU 登录成功，Token 已加密保存')
            }}
          >
            <label className="field">
              验证码（短信上行时留空）
              <input
                placeholder="6位验证码（留空=短信上行）"
                inputMode="numeric"
                autoComplete="one-time-code"
                value={code}
                onChange={(e) => setCode(e.target.value)}
              />
            </label>
            <button disabled={action.busy}>
              {action.busy ? '登录中…' : '登录'}
            </button>
          </form>
        )}
      </details>
    </>
  )
}
function EcoForm({ reload }: { reload: () => void }) {
  const [partnerId, setPartnerId] = useState(''),
    [privateKey, setPrivateKey] = useState(''),
    [steamId, setSteamId] = useState(''),
    [fingerprint, setFingerprint] = useState(''),
    action = useAction()
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault()
        await action.run(async () => {
          const r = await api.put<SavedCredential>('/channels/eco', {
            partner_id: partnerId,
            private_key_pem: privateKey,
            steam_id: steamId,
          })
          setPrivateKey('')
          setFingerprint(r.fingerprint || '')
          reload()
        }, 'ECO 凭证已加密保存并验证')
      }}
    >
      <h3>开放平台凭证</h3>
      <p className="hint">
        私钥接受 PKCS8 PEM 或裸 base64。保存成功后仅保留指纹用于核对。
      </p>
      <fieldset className="form-body" disabled={action.busy}>
        <div className="toolbar">
          <label className="field">
            PartnerId
            <input
              placeholder="PartnerId"
              required
              value={partnerId}
              onChange={(e) => setPartnerId(e.target.value)}
            />
          </label>
          <label className="field">
            SteamID（可空）
            <input
              placeholder="SteamID（可空）"
              value={steamId}
              onChange={(e) => setSteamId(e.target.value)}
            />
          </label>
        </div>
        <label className="field">
          ECO 私钥
          <textarea
            rows={5}
            required
            spellCheck={false}
            autoComplete="off"
            placeholder="-----BEGIN PRIVATE KEY-----"
            value={privateKey}
            onChange={(e) => setPrivateKey(e.target.value)}
          />
        </label>
      </fieldset>
      <Feedback {...action} />
      {fingerprint && <p className="hint">已保存私钥指纹：{fingerprint}</p>}
      <button disabled={action.busy || !partnerId || !privateKey}>
        {action.busy ? '验证中…' : '保存并验证'}
      </button>
    </form>
  )
}
function SteamForm({ reload }: { reload: () => void }) {
  const [username, setUser] = useState(''),
    [password, setPass] = useState(''),
    [shared, setShared] = useState(''),
    [identity, setIdentity] = useState(''),
    [fingerprint, setFingerprint] = useState(''),
    action = useAction()
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault()
        await action.run(async () => {
          const r = await api.put<SavedCredential>('/channels/steam', {
            username,
            password,
            shared_secret: shared,
            identity_secret: identity,
          })
          setPass('')
          setShared('')
          setIdentity('')
          setFingerprint(r.fingerprint || '')
          reload()
        }, 'Steam 登录成功，会话已加密保存')
      }}
    >
      <h3>自动收报价凭证</h3>
      <p className="hint">
        从 Steam Guard 工具导出 shared_secret 与
        identity_secret。系统只自动接受不付出物品的报价，其他报价仅记录。
      </p>
      <fieldset className="form-body" disabled={action.busy}>
        <div className="form-grid">
          <label className="field">
            Steam 用户名
            <input
              placeholder="Steam 用户名"
              required
              autoComplete="username"
              value={username}
              onChange={(e) => setUser(e.target.value)}
            />
          </label>
          <label className="field">
            Steam 密码
            <input
              placeholder="密码"
              required
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPass(e.target.value)}
            />
          </label>
          <label className="field">
            shared_secret
            <input
              placeholder="shared_secret (base64)"
              required
              autoComplete="off"
              type="password"
              value={shared}
              onChange={(e) => setShared(e.target.value)}
            />
          </label>
          <label className="field">
            identity_secret
            <input
              placeholder="identity_secret (base64)"
              required
              autoComplete="off"
              type="password"
              value={identity}
              onChange={(e) => setIdentity(e.target.value)}
            />
          </label>
        </div>
      </fieldset>
      <Feedback {...action} />
      {fingerprint && <p className="hint">已保存令牌指纹：{fingerprint}</p>}
      <button
        disabled={action.busy || !username || !password || !shared || !identity}
      >
        {action.busy ? '登录中…' : '登录 Steam 并保存'}
      </button>
    </form>
  )
}
