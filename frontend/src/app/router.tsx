import { useEffect, useState, type AnchorHTMLAttributes, type ReactNode } from 'react'

// 해시 라우터: Wails 자산 서버는 SPA 폴백이 없으므로 #/경로 를 쓴다 (새로고침에도 안전).
// 경로 예) #/cuts/2026-11-01/p/987654320/scan

export type Route = { path: string; parts: string[]; query: URLSearchParams }

function read(): Route {
  const raw = window.location.hash.replace(/^#/, '') || '/'
  const [path, q = ''] = raw.split('?')
  return { path, parts: path.split('/').filter(Boolean).map(decodeURIComponent), query: new URLSearchParams(q) }
}

export function useRoute(): Route {
  const [route, setRoute] = useState(read)
  useEffect(() => {
    const on = () => setRoute(read())
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  return route
}

export function navigate(to: string) {
  window.location.hash = to.startsWith('#') ? to : `#${to}`
}

export function Link({ to, children, ...rest }: { to: string; children: ReactNode } & AnchorHTMLAttributes<HTMLAnchorElement>) {
  return (
    <a href={`#${to}`} {...rest}>
      {children}
    </a>
  )
}

export const paths = {
  dashboard: () => '/',
  cuts: () => '/cuts',
  cut: (date: string) => `/cuts/${date}`,
  product: (date: string, code: string) => `/cuts/${date}/p/${code}`,
  scan: (date: string, code: string) => `/cuts/${date}/p/${code}/scan`,
  register: (date: string, code: string) => `/cuts/${date}/p/${code}/register`,
  notices: () => '/notices',
  notice: (id: number) => `/notices/${id}`,
  inventory: () => '/inventory',
  lookup: () => '/lookup',
  imports: () => '/import',
  suppliers: () => '/suppliers',
  reports: () => '/reports',
  settings: () => '/settings',
}
