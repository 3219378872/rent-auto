// Tencent TCaptcha loader used to satisfy UU risk-control 图形校验.
// The slider/click challenge must be solved manually by the operator in this
// browser; ticket/randstr are then echoed by the backend on SMS retry.
// Behavioral spec: docs/knowledge/design/platform-uu-api-notes.md (认证节).
export type TCaptchaResult = { ret: number; ticket: string; randstr: string }

type CaptchaInstance = { show: () => void }
type CaptchaCtor = new (appid: string, cb: (res: TCaptchaResult) => void) => CaptchaInstance

declare global {
  interface Window {
    TencentCaptcha?: CaptchaCtor
  }
}

// UU's captcha app id, captured from the official web login flow (2026-08-23).
// If the platform rotates it or enables a domain whitelist, adjust here.
export const UU_CAPTCHA_APP_ID = '191004049'

let loader: Promise<CaptchaCtor> | null = null

function loadTCaptcha(): Promise<CaptchaCtor> {
  if (window.TencentCaptcha) return Promise.resolve(window.TencentCaptcha)
  if (loader) return loader
  loader = new Promise<CaptchaCtor>((resolve, reject) => {
    const s = document.createElement('script')
    s.src = 'https://turing.captcha.qcloud.com/TCaptcha.js'
    s.onload = () => {
      if (window.TencentCaptcha) resolve(window.TencentCaptcha)
      else reject(new Error('TCaptcha 加载异常：脚本已加载但入口缺失'))
    }
    s.onerror = () => {
      loader = null
      reject(new Error('TCaptcha 脚本加载失败，请检查网络后重试，或到 youpin898.com 官网完成一次图形验证后再试'))
    }
    document.head.appendChild(s)
  })
  return loader
}

export async function solveCaptcha(appid: string): Promise<TCaptchaResult> {
  const Ctor = await loadTCaptcha()
  return new Promise<TCaptchaResult>((resolve, reject) => {
    const cap = new Ctor(appid, (res) => {
      if (res && res.ret === 0 && res.ticket) resolve(res)
      else reject(new Error('图形验证未完成（已取消或失败），可重新点击发送验证码'))
    })
    cap.show()
  })
}
