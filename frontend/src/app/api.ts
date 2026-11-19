// Go(Wails 바인딩) 호출을 한 곳에 모은 계층. 화면은 이 파일의 훅/함수만 사용한다.
// → 바인딩이 바뀌어도(예: Wails v3 이전) 이 파일만 고치면 된다.
import { QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import * as App from '../../wailsjs/go/main/App'
import type { domain, ingest, main, service } from '../../wailsjs/go/models'
import { EventsOn } from '../../wailsjs/runtime/runtime'

export type { domain, ingest, main, service }

// 로컬 앱: 네트워크 상태와 무관하게 항상 실행, 창 포커스 시 재조회 안 함 (변경 시 명시적으로 무효화)
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { networkMode: 'always', retry: false, refetchOnWindowFocus: false, staleTime: 30_000 },
    mutations: { networkMode: 'always', retry: false },
  },
})

export function errorText(e: unknown): string {
  if (typeof e === 'string') return e
  if (e instanceof Error) return e.message
  return String(e)
}

/** Wails 클래스 타입에 일반 객체를 넘길 때 사용 */
export const obj = <T,>(o: unknown) => o as T

export const keys = {
  all: ['dpr'] as const,
  dashboard: ['dpr', 'dashboard'] as const,
  cutDates: ['dpr', 'cut-dates'] as const,
  ranking: (date: string, held: boolean) => ['dpr', 'ranking', date, held] as const,
  product: (date: string, code: string) => ['dpr', 'product', date, code] as const,
  deletions: (date: string) => ['dpr', 'deletions', date] as const,
  notices: ['dpr', 'notices'] as const,
  noticeChanges: (id: number, group: string, held: boolean) => ['dpr', 'notice-changes', id, group, held] as const,
  inventory: (q: string, nonzero: boolean) => ['dpr', 'inventory', q, nonzero] as const,
  unresolved: ['dpr', 'unresolved'] as const,
  imports: ['dpr', 'imports'] as const,
  suppliers: (deleted: boolean) => ['dpr', 'suppliers', deleted] as const,
  rules: (sid: number) => ['dpr', 'rules', sid] as const,
  settings: ['dpr', 'settings'] as const,
  appInfo: ['dpr', 'app-info'] as const,
  jobs: ['dpr', 'jobs'] as const,
  notifications: ['dpr', 'notifications'] as const,
  report: (f: unknown) => ['dpr', 'report', f] as const,
  scans: (date: string, code: string) => ['dpr', 'scans', date, code] as const,
  anchors: (code: string) => ['dpr', 'anchors', code] as const,
  search: (q: string) => ['dpr', 'search', q] as const,
}

/** Go 에서 데이터가 바뀌었다고 알리면 전체 캐시를 무효화 (로컬 DB라 재조회 비용이 작다) */
export function wireEvents() {
  try {
    EventsOn('notify', (n: service.Notification) => {
      const fn = n.level === 'error' ? toast.error : n.level === 'warn' ? toast.warning : toast.info
      fn(n.title, { description: n.body, duration: 8000 })
      queryClient.invalidateQueries({ queryKey: keys.all })
    })
    EventsOn('data-changed', () => queryClient.invalidateQueries({ queryKey: keys.all }))
  } catch {
    /* 브라우저 단독 실행(바인딩 없음) */
  }
  // 화면 오류도 기록(로그)에 남겨 '오류 보고'로 확인할 수 있게 한다
  // 개발 중 일반 브라우저로 열었을 때만 나는 Wails 내부 스크립트(ipc.js) 오류는 앱과 무관하므로 기록하지 않는다
  const inWebView = typeof (window as unknown as { chrome?: { webview?: unknown } }).chrome?.webview !== 'undefined'
  window.addEventListener('error', (e) => {
    if (!inWebView && /\/wails\/(ipc|runtime)\.js/.test(`${e.filename} ${e.error?.stack ?? ''}`)) return
    logClientError('window', e.message, e.error?.stack ?? `${e.filename}:${e.lineno}`)
  })
  window.addEventListener('unhandledrejection', (e) => logClientError('promise', errorText(e.reason), (e.reason as Error | undefined)?.stack ?? ''))
}

export function logClientError(kind: string, message: string, detail = '') {
  if (!hasBackend()) return
  try {
    void App.LogClientError(kind, message, detail, window.location.hash).catch(() => undefined)
  } catch {
    /* ignore */
  }
}

export const hasBackend = () => typeof (window as unknown as { go?: unknown }).go !== 'undefined'

// ── 조회 훅 ─────────────────────────────────────────────────────────

export const useAppInfo = () => useQuery({ queryKey: keys.appInfo, queryFn: () => App.AppInfo(), staleTime: Infinity })
export const useDashboard = () => useQuery({ queryKey: keys.dashboard, queryFn: () => App.Dashboard() })
export const useCutDates = () => useQuery({ queryKey: keys.cutDates, queryFn: () => App.PriceCutDates() })
export const useRanking = (date: string, held: boolean) =>
  useQuery({ queryKey: keys.ranking(date, held), queryFn: () => App.Ranking(date, held), enabled: !!date })
export const useProduct = (date: string, code: string) =>
  useQuery({ queryKey: keys.product(date, code), queryFn: () => App.ProductDetail(date, code), enabled: !!date && !!code })
export const useDeletions = (date: string) => useQuery({ queryKey: keys.deletions(date), queryFn: () => App.HeldDeletions(date) })
export const useNotices = () => useQuery({ queryKey: keys.notices, queryFn: () => App.ListNotices() })
export const useNoticeChanges = (id: number, group: string, held: boolean) =>
  useQuery({ queryKey: keys.noticeChanges(id, group, held), queryFn: () => App.NoticeChanges(id, group, held), enabled: id > 0 })
export const useInventory = (q: string, nonzero: boolean) =>
  useQuery({ queryKey: keys.inventory(q, nonzero), queryFn: () => App.Inventory(q, nonzero), placeholderData: (prev) => prev })
export const useUnresolved = () => useQuery({ queryKey: keys.unresolved, queryFn: () => App.ListUnresolved() })
export const useImports = () => useQuery({ queryKey: keys.imports, queryFn: () => App.ListImports() })
export const useImportKinds = () => useQuery({ queryKey: ['dpr', 'import-kinds'], queryFn: () => App.ImportKinds(), staleTime: Infinity })
export const useSuppliers = (deleted = false) => useQuery({ queryKey: keys.suppliers(deleted), queryFn: () => App.ListSuppliers(deleted) })
export const useRules = (sid: number) => useQuery({ queryKey: keys.rules(sid), queryFn: () => App.ListRules(sid) })
export const useSettings = () => useQuery({ queryKey: keys.settings, queryFn: () => App.GetSettings() })
export const useJobs = () => useQuery({ queryKey: keys.jobs, queryFn: () => App.JobRuns() })
export const useNotifications = () => useQuery({ queryKey: keys.notifications, queryFn: () => App.Notifications(50) })
export const useReport = (f: Partial<service.SettlementFilter>) =>
  useQuery({ queryKey: keys.report(f), queryFn: () => App.Report(obj<service.SettlementFilter>(f)) })
export const useScans = (date: string, code: string) =>
  useQuery({ queryKey: keys.scans(date, code), queryFn: () => App.ListScans(date, code), enabled: !!date })
export const useAnchors = (code: string) => useQuery({ queryKey: keys.anchors(code), queryFn: () => App.ProductAnchors(code), enabled: !!code })
export const useProductSearch = (q: string) =>
  useQuery({ queryKey: keys.search(q), queryFn: () => App.SearchProducts(q), enabled: q.trim().length >= 1, placeholderData: (p) => p })

// ── 변경 (성공 시 관련 화면 전부 갱신) ───────────────────────────────

export function useAction<A extends unknown[], R>(fn: (...args: A) => Promise<R>, opts?: { success?: string | ((r: R) => string); silent?: boolean }) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (args: A) => fn(...args),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: keys.all })
      const msg = typeof opts?.success === 'function' ? opts.success(r) : opts?.success
      if (msg) toast.success(msg)
    },
    onError: (e) => {
      logClientError('action', errorText(e))
      if (!opts?.silent) toast.error(errorText(e))
    },
  })
}

export const api = App
