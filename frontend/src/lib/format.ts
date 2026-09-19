export const money = (n: number) =>
  `¥${n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
export const percent = (n: number) => `${(n * 100).toFixed(2)}%`
export const time = (value?: string | null) =>
  value && Number.isFinite(Date.parse(value))
    ? new Date(value).toLocaleString('zh-CN', { hour12: false })
    : '未记录'
export const timezone = () => Intl.DateTimeFormat().resolvedOptions().timeZone
export const toISO = (value: string) =>
  value && !Number.isNaN(Date.parse(value)) ? new Date(value).toISOString() : ''
export const toLocalInput = (value: Date) =>
  new Date(value.getTime() - value.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16)
export const statusLabels: Record<string, string> = {
  in_stock: '在库',
  listed: '已上架',
  leased: '已租出',
  locked: '锁定',
  sold: '已售出',
  missing: '同步未见',
  active: '在架',
  none: '未在架',
  delisted: '期望下架',
  stale: '状态过期',
  unknown: '未知',
  pending_payment: '待支付',
  delivering: '待发货',
  leasing: '租赁中',
  returning: '归还中',
  done: '已完成',
  bought_out: '已买断',
  cancelled: '已取消',
  breach: '违约',
  arbitrating: '仲裁中',
}
export const statusLabel = (status: string) =>
  statusLabels[status] ?? `未知（${status || '未提供'}）`
export const orderTypes: Record<string, string> = {
  short: '短租',
  long: '长租',
  buyout: '买断',
}
export const jobLabels: Record<string, string> = {
  reprice: '全局重定价',
  inventory_sync: '库存同步',
  shelf_sync: '货架同步',
  orders_sync: '订单同步',
  market_snapshot: '行情采集',
  value_anchor: '估值更新',
  factor_events: '反馈因子更新',
  reconcile: '货架对账',
  uu_delivery: 'UU 发货',
  eco_delivery: 'ECO 发货',
  steam_offers: 'Steam 报价处理',
  zero_cd: 'UU 0CD',
  channel_recovery: '渠道恢复',
}
export const reasons: Record<string, string> = {
  guardrail_conflict: '价格护栏冲突',
  no_value_anchor: '缺少价值锚点',
  no_baseline: '缺少行情基线',
  cooldown: '改价冷却中',
  noise: '变化未达阈值',
  noise_floor: '变化未达阈值',
  deposit_cap: '押金超过护栏',
  environment_dry_run: '环境设置为模拟执行',
  global_disabled: '全局真实执行未开启',
  template_disabled: '模板真实执行未开启',
}
export const reasonLabel = (reason: string) => reasons[reason] ?? reason

export function relativeDue(
  due: string | null,
  status: string,
  now = Date.now(),
) {
  if (!due || ['done', 'bought_out', 'cancelled'].includes(status)) return ''
  const hours = (Date.parse(due) - now) / 3600000
  if (!Number.isFinite(hours)) return ''
  const span =
    Math.abs(hours) >= 24
      ? `${Math.floor(Math.abs(hours) / 24)}天`
      : `${Math.max(1, Math.ceil(Math.abs(hours)))}小时`
  return hours < 0 ? `已到期 ${span}` : `剩余 ${span}`
}
