// Minimal typed API client: JWT in localStorage, JSON everywhere.
const BASE = '/api/v1'

export function getToken(): string {
  return localStorage.getItem('ra_token') ?? ''
}

export function setToken(t: string): void {
  localStorage.setItem('ra_token', t)
}

export function clearToken(): void {
  localStorage.removeItem('ra_token')
}

export class ApiError extends Error {
  code: string
  status: number
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const tok = getToken()
  if (tok) headers['Authorization'] = `Bearer ${tok}`
  const res = await fetch(BASE + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 401 && path !== '/auth/login') {
    clearToken()
    window.location.hash = '#/login'
  }
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const e = data as { code?: string; message?: string }
    throw new ApiError(res.status, e.code ?? 'error', e.message ?? res.statusText)
  }
  return data as T
}

export const api = {
  get: <T>(p: string) => request<T>('GET', p),
  post: <T>(p: string, b?: unknown) => request<T>('POST', p, b),
  put: <T>(p: string, b?: unknown) => request<T>('PUT', p, b),
}

export interface Paged<T> { items: T[]; total: number }

export interface InventoryRow {
  id: number; channel: string; asset_id: string; hash_name: string
  market_hash_name: string; template_id: number | null
  mark_price: number; tradable: boolean; status: string; cost_basis: number
}

export interface ListingRow {
  id: number; channel: string; asset_id: string; hash_name: string; goods_ref: string
  desired_state: string; actual_state: string
  rent_price: number; long_rent_price: number; max_days: number; deposit: number
  listed_at: string | null; last_reprice_at: string | null
}

export interface OrderRow {
  id: number; channel: string; order_ref: string; hash_name: string
  order_type: string; status: string; rent_days: number
  rent_price: number; order_amount: number; deposits: number
  started_at: string | null; due_at: string | null; finished_at: string | null
}

export interface DashboardData {
  assets: { total: number; inventory: number; deposits: Record<string, number>; wallets: Record<string, number> }
  income: { total: number; today: number; by_channel: { channel: string; income: number; orders: number }[] }
  leased_out: number
  annualized_roi: number
  categories: { category: string; cost: number; income: number; yield: number }[]
  series_30d: { date: string; income: number }[]
}

export interface StrategyRow {
  id: number; name: string; scope: string; channel_route: string
  params: Record<string, unknown>; real_execution_enabled: boolean
  priority: number; updated_at: string
}

export interface AuditRow {
  ts: string; actor: string; action: string; channel?: string; target?: string
  detail?: Record<string, unknown>
}
