import {
  Bell,
  Boxes,
  Building2,
  ClipboardCheck,
  FileText,
  LayoutDashboard,
  Monitor,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Search,
  Settings,
  Sun,
  TrendingDown,
  Upload,
} from 'lucide-react'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { cx } from '../components/ui'
import { api, keys, queryClient, useDashboard, useSettings } from './api'
import { dateTime } from './format'
import { navigate, paths, useRoute } from './router'
import { UpdateBanner } from './UpdateBanner'

const NAV = [
  { to: paths.dashboard(), match: '', label: '대시보드', icon: LayoutDashboard },
  { to: paths.cuts(), match: 'cuts', label: '약가인하 대응', icon: TrendingDown },
  { to: paths.notices(), match: 'notices', label: '고시', icon: FileText },
  { to: paths.inventory(), match: 'inventory', label: '재고', icon: Boxes },
  { to: paths.lookup(), match: 'lookup', label: '약품 조회', icon: Search },
  { to: paths.imports(), match: 'import', label: '데이터 가져오기', icon: Upload },
  { to: paths.suppliers(), match: 'suppliers', label: '도매처·보상기준', icon: Building2 },
  { to: paths.reports(), match: 'reports', label: '처리 내역', icon: ClipboardCheck },
  { to: paths.settings(), match: 'settings', label: '설정', icon: Settings },
]

function readCollapsed() {
  try {
    return localStorage.getItem('dpr.sidebar') === 'rail'
  } catch {
    return false
  }
}

const ZOOM_STEPS = ['87.5', '100', '112.5', '125', '137.5', '150']

export function Shell({ children }: { children: ReactNode }) {
  const route = useRoute()
  const [rail, setRail] = useState(readCollapsed)
  const [narrow, setNarrow] = useState(() => window.innerWidth < 1100)
  const settings = useSettings()
  const dash = useDashboard()

  // 화면 배율(글자 크기)·테마 — 사이드바 버튼과 Ctrl +/−/0 으로 바로 바꾸고 저장한다
  const [zoom, setZoom] = useState<string | null>(null)
  const [theme, setTheme] = useState<string | null>(null)
  useEffect(() => {
    setZoom(null) // 설정 화면에서 바꾼 값이 오면 그 값을 따른다
    setTheme(null)
  }, [settings.data?.zoom, settings.data?.theme])
  const curZoom = zoom ?? settings.data?.zoom ?? '100'
  const curTheme = theme ?? settings.data?.theme ?? 'light'
  useEffect(() => {
    const z = parseFloat(curZoom) || 100
    document.documentElement.style.fontSize = `${(16 * z) / 100}px`
  }, [curZoom])
  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const apply = () => {
      document.documentElement.dataset.theme = curTheme === 'system' ? (mq.matches ? 'dark' : 'light') : curTheme
    }
    apply()
    mq.addEventListener('change', apply)
    return () => mq.removeEventListener('change', apply)
  }, [curTheme])
  const savePrefs = (z: string, t: string) => {
    setZoom(z)
    setTheme(t)
    void api.SetUIPrefs(z, t).then(() => queryClient.invalidateQueries({ queryKey: keys.settings }))
  }
  const stepZoom = (dir: number) => {
    const i = ZOOM_STEPS.indexOf(curZoom)
    const next = ZOOM_STEPS[Math.min(ZOOM_STEPS.length - 1, Math.max(0, (i < 0 ? 1 : i) + dir))]
    savePrefs(next, curTheme)
  }
  const stepRef = useRef(stepZoom)
  stepRef.current = stepZoom
  const resetRef = useRef(() => savePrefs('100', curTheme))
  resetRef.current = () => savePrefs('100', curTheme)

  useEffect(() => {
    const onResize = () => setNarrow(window.innerWidth < 1100)
    const onKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey)) return
      if (e.key === '=' || e.key === '+') {
        e.preventDefault()
        stepRef.current(1)
        return
      }
      if (e.key === '-' || e.key === '_') {
        e.preventDefault()
        stepRef.current(-1)
        return
      }
      if (e.key === '0') {
        e.preventDefault()
        resetRef.current()
        return
      }
      if (e.key === 'b' || e.key === 'B') {
        e.preventDefault()
        setRail((r) => {
          try {
            localStorage.setItem('dpr.sidebar', r ? 'full' : 'rail')
          } catch {
            /* ignore */
          }
          return !r
        })
      }
      const n = Number(e.key)
      if (n >= 1 && n <= NAV.length) {
        e.preventDefault()
        navigate(NAV[n - 1].to)
      }
    }
    window.addEventListener('resize', onResize)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('resize', onResize)
      window.removeEventListener('keydown', onKey)
    }
  }, [])

  const collapsed = rail || narrow
  const active = route.parts[0] ?? ''
  const unread = dash.data?.pending?.unread ?? 0
  const crawl = dash.data?.crawl

  return (
    <div className="flex h-screen w-screen overflow-hidden">
      <aside className={cx('flex shrink-0 flex-col border-r border-gray-200 bg-gray-0 transition-[width]', collapsed ? 'w-14' : 'w-[232px]')}>
        <div className={cx('flex h-14 items-center border-b border-gray-100', collapsed ? 'justify-center' : 'justify-between px-4')}>
          {!collapsed && (
            <div className="leading-tight">
              <div className="text-[0.9375rem] font-bold tracking-[-0.01em]">급여약품 관리</div>
              <div className="text-[0.6875rem] font-medium text-gray-500">DPR</div>
            </div>
          )}
          <button
            className="rounded-md p-1.5 text-gray-500 hover:bg-gray-100"
            title={collapsed ? '메뉴 펼치기 (Ctrl+B)' : '메뉴 접기 (Ctrl+B)'}
            onClick={() => setRail((r) => !r)}
          >
            {collapsed ? <PanelLeftOpen className="size-[18px]" /> : <PanelLeftClose className="size-[18px]" />}
          </button>
        </div>
        <nav className="flex flex-1 flex-col gap-0.5 p-2">
          {NAV.map((n, i) => {
            const on = n.match === active
            const Icon = n.icon
            return (
              <a
                key={n.to}
                href={`#${n.to}`}
                title={collapsed ? `${n.label} (Ctrl+${i + 1})` : undefined}
                className={cx(
                  'flex h-9 items-center gap-2.5 rounded-lg text-sm font-medium transition-colors',
                  collapsed ? 'justify-center' : 'px-2.5',
                  on ? 'bg-blue-50 text-blue-700' : 'text-gray-700 hover:bg-gray-100',
                  n.match === 'settings' && 'mt-auto',
                )}
              >
                <Icon className="size-[18px] shrink-0" />
                {!collapsed && <span className="truncate">{n.label}</span>}
                {!collapsed && n.match === '' && unread > 0 && (
                  <span className="ml-auto inline-flex items-center gap-1 rounded-full bg-amber-50 px-1.5 text-[0.6875rem] font-bold text-amber-700">
                    <Bell className="size-3" />
                    {unread}
                  </span>
                )}
              </a>
            )
          })}
        </nav>
        <div className={cx('flex items-center gap-1 border-t border-gray-100 px-2 py-2', collapsed ? 'flex-col' : 'justify-between')}>
          <div className={cx('flex items-center gap-0.5', collapsed && 'flex-col')}>
            <button className="rounded-md px-1.5 py-1 text-[0.75rem] font-bold text-gray-600 hover:bg-gray-100" title="글자 작게 (Ctrl −)" onClick={() => stepZoom(-1)}>
              A−
            </button>
            {!collapsed && (
              <button className="num rounded-md px-1 py-1 text-[0.6875rem] text-gray-500 hover:bg-gray-100" title="기본 크기 (Ctrl 0)" onClick={() => savePrefs('100', curTheme)}>
                {curZoom}%
              </button>
            )}
            <button className="rounded-md px-1.5 py-1 text-[0.875rem] font-bold text-gray-600 hover:bg-gray-100" title="글자 크게 (Ctrl +)" onClick={() => stepZoom(1)}>
              A+
            </button>
          </div>
          <button
            className="rounded-md p-1.5 text-gray-600 hover:bg-gray-100"
            title={curTheme === 'dark' ? '밝은 화면으로' : curTheme === 'system' ? '시스템 설정 따름 — 누르면 밝은 화면' : '어두운 화면으로'}
            onClick={() => savePrefs(curZoom, curTheme === 'light' ? 'dark' : curTheme === 'dark' ? 'system' : 'light')}
          >
            {curTheme === 'dark' ? <Moon className="size-4" /> : curTheme === 'system' ? <Monitor className="size-4" /> : <Sun className="size-4" />}
          </button>
        </div>
        {!collapsed && crawl && (
          <div className="border-t border-gray-100 px-4 py-3 text-[0.6875rem] leading-relaxed text-gray-600">
            <div className="flex items-center gap-1.5">
              <span className={cx('size-1.5 rounded-full', crawl.stale ? 'bg-amber-600' : crawl.enabled ? 'bg-green-600' : 'bg-gray-400')} />
              고시 자동 확인 {crawl.enabled ? `(${crawl.times})` : '꺼짐'}
            </div>
            <div className="mt-0.5">마지막 성공 {crawl.lastOk ? dateTime(crawl.lastOk) : '없음'}</div>
          </div>
        )}
      </aside>
      <main className="min-w-0 flex-1 overflow-auto">
        <UpdateBanner />
        <div className="mx-auto max-w-[1680px] px-6 py-6 max-[1280px]:px-4">{children}</div>
      </main>
    </div>
  )
}
