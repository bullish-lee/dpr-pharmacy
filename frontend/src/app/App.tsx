import { lazy, Suspense, useEffect } from 'react'
import { Empty, Spinner } from '../components/ui'
import { DashboardPage } from '../features/dashboard/DashboardPage'
import { CutsPage } from '../features/cuts/CutsPage'
import { CutCasePage } from '../features/cuts/CutCasePage'
import { ProductPage } from '../features/cuts/ProductPage'
import { LotPhotoPage } from '../features/cuts/LotPhotoPage'
import { NoticesPage, NoticeDetailPage } from '../features/notices/NoticesPage'
import { InventoryPage } from '../features/inventory/InventoryPage'
import { LookupPage } from '../features/lookup/LookupPage'
import { ImportPage } from '../features/imports/ImportPage'
import { SuppliersPage } from '../features/suppliers/SuppliersPage'
import { ReportsPage } from '../features/reports/ReportsPage'
import { SettingsPage } from '../features/settings/SettingsPage'
import { hasBackend, useAppInfo } from './api'
import { ErrorBoundary } from './ErrorBoundary'
import { Shell } from './Shell'
import { navigate, useRoute } from './router'

// 바코드 화면은 WASM 판독기·바코드 렌더러를 쓰므로 필요할 때만 불러온다
const ScanPage = lazy(() => import('../features/cuts/ScanPage').then((m) => ({ default: m.ScanPage })))
const RegisterPage = lazy(() => import('../features/cuts/RegisterPage').then((m) => ({ default: m.RegisterPage })))

function Routes() {
  const { parts } = useRoute()
  const [a, b, c, d, e] = parts
  switch (a) {
    case undefined:
      return <DashboardPage />
    case 'cuts':
      if (!b) return <CutsPage />
      if (c === 'p' && d && e === 'scan') return <ScanPage date={b} code={d} />
      if (c === 'p' && d && e === 'register') return <RegisterPage date={b} code={d} />
      if (c === 'p' && d) return <ProductPage date={b} code={d} />
      if (c === 'lots') return <LotPhotoPage date={b} />
      return <CutCasePage date={b} />
    case 'notices':
      return b ? <NoticeDetailPage id={Number(b)} /> : <NoticesPage />
    case 'inventory':
      return <InventoryPage />
    case 'lookup':
      return <LookupPage />
    case 'import':
      return <ImportPage />
    case 'suppliers':
      return <SuppliersPage />
    case 'reports':
      return <ReportsPage />
    case 'settings':
      return <SettingsPage />
  }
  return <Empty title="화면을 찾을 수 없습니다" />
}

function StartRoute() {
  const info = useAppInfo()
  useEffect(() => {
    if (info.data?.startRoute && (!window.location.hash || window.location.hash === '#/')) navigate(info.data.startRoute)
  }, [info.data?.startRoute])
  return null
}

export function App() {
  if (!hasBackend()) {
    return (
      <div className="flex h-screen items-center justify-center">
        <Empty title="DPR 앱 안에서 실행해 주세요">
          이 화면은 데스크탑 앱(Wails)의 Go 백엔드와 함께 동작합니다. 개발 중이라면 <code>wails dev</code> 로 실행하세요.
        </Empty>
      </div>
    )
  }
  return (
    <Shell>
      <StartRoute />
      <RouteBoundary />
    </Shell>
  )
}

function RouteBoundary() {
  const { parts } = useRoute()
  return (
    <ErrorBoundary resetKey={parts.join('/')}>
      <Suspense fallback={<Spinner />}>
        <Routes />
      </Suspense>
    </ErrorBoundary>
  )
}
