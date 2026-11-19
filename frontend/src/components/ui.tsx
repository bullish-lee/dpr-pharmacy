import clsx from 'clsx'
import { X, Loader2, AlertTriangle, Info, CircleAlert, CheckCircle2 } from 'lucide-react'
import {
  forwardRef,
  useEffect,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
} from 'react'
import { createPortal } from 'react-dom'
import type { Tone } from '../app/format'

export const cx = clsx

// ── 버튼 ────────────────────────────────────────────────────────────
type BtnVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'subtle'
type BtnSize = 'sm' | 'md' | 'lg'

const btnBase =
  'inline-flex items-center justify-center gap-1.5 rounded-lg font-semibold transition-colors disabled:opacity-50 disabled:pointer-events-none select-none whitespace-nowrap'
const btnVariant: Record<BtnVariant, string> = {
  primary: 'bg-blue-600 text-white hover:brightness-110 active:brightness-95',
  secondary: 'bg-gray-0 text-gray-900 border border-gray-300 hover:bg-gray-50 active:bg-gray-100',
  subtle: 'bg-gray-100 text-gray-800 hover:bg-gray-200',
  ghost: 'text-gray-700 hover:bg-gray-100',
  danger: 'bg-gray-0 text-red-700 border border-red-600/40 hover:bg-red-50',
}
const btnSize: Record<BtnSize, string> = {
  sm: 'h-8 px-2.5 text-[0.8125rem]',
  md: 'h-9 px-3.5 text-sm',
  lg: 'h-11 px-5 text-[0.9375rem]',
}

export const Button = forwardRef<
  HTMLButtonElement,
  ButtonHTMLAttributes<HTMLButtonElement> & { variant?: BtnVariant; size?: BtnSize; loading?: boolean; icon?: ReactNode }
>(function Button({ variant = 'secondary', size = 'md', loading, icon, className, children, disabled, ...rest }, ref) {
  return (
    <button ref={ref} className={cx(btnBase, btnVariant[variant], btnSize[size], className)} disabled={disabled || loading} {...rest}>
      {loading ? <Loader2 className="size-4 animate-spin" /> : icon}
      {children}
    </button>
  )
})

// ── 배지 (색 = 필요한 조치의 정도) ─────────────────────────────────────
const toneCls: Record<Tone, string> = {
  gray: 'bg-gray-100 text-gray-700',
  blue: 'bg-blue-50 text-blue-700',
  amber: 'bg-amber-50 text-amber-700',
  green: 'bg-green-50 text-green-700',
  red: 'bg-red-50 text-red-700',
  outline: 'border border-gray-200 text-gray-600 bg-gray-0',
}

export function Badge({ tone = 'gray', icon, children, className, title }: { tone?: Tone; icon?: ReactNode; children: ReactNode; className?: string; title?: string }) {
  return (
    <span title={title} className={cx('inline-flex h-[22px] items-center gap-1 rounded-md px-2 text-xs font-semibold whitespace-nowrap', toneCls[tone], className)}>
      {icon}
      {children}
    </span>
  )
}

// ── 카드 · KPI ──────────────────────────────────────────────────────
export function Card({ children, className, title, actions, pad = true }: { children: ReactNode; className?: string; title?: ReactNode; actions?: ReactNode; pad?: boolean }) {
  return (
    <section className={cx('rounded-xl border border-gray-200 bg-gray-0', className)}>
      {(title || actions) && (
        <header className="flex min-h-12 items-center justify-between gap-3 border-b border-gray-100 px-5 py-2.5">
          <h3 className="text-[0.9375rem] font-semibold text-gray-900">{title}</h3>
          <div className="flex items-center gap-2">{actions}</div>
        </header>
      )}
      <div className={cx(pad && 'p-5')}>{children}</div>
    </section>
  )
}

export function Kpi({ label, value, unit, sub, onClick, tone }: { label: string; value: ReactNode; unit?: string; sub?: ReactNode; onClick?: () => void; tone?: 'amber' | 'default' }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={!onClick}
      className={cx(
        'flex min-w-0 flex-col items-start rounded-xl border bg-gray-0 px-5 py-4 text-left transition-colors',
        tone === 'amber' ? 'border-amber-600/40' : 'border-gray-200',
        onClick && 'hover:border-gray-300 hover:bg-gray-50',
      )}
    >
      <span className="text-[0.8125rem] font-semibold text-gray-600">{label}</span>
      <span className="mt-1.5 flex items-baseline gap-1">
        <span className="num text-[1.75rem] leading-9 font-semibold tracking-[-0.02em] text-gray-900">{value}</span>
        {unit && <span className="text-base font-medium text-gray-700">{unit}</span>}
      </span>
      {sub && <span className="mt-1 text-[0.8125rem] text-gray-600">{sub}</span>}
    </button>
  )
}

// ── 입력 ────────────────────────────────────────────────────────────
export const inputCls =
  'h-9 w-full rounded-md border border-gray-400/70 bg-gray-0 px-2.5 text-sm text-gray-900 placeholder:text-gray-400 focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/25 disabled:bg-gray-50 disabled:text-gray-500'

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input({ className, ...rest }, ref) {
  return <input ref={ref} className={cx(inputCls, className)} {...rest} />
})

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={cx(inputCls, 'pr-8', className)} {...rest}>
      {children}
    </select>
  )
}

export function Field({ label, help, children, className }: { label: ReactNode; help?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <label className={cx('flex flex-col gap-1.5', className)}>
      <span className="text-[0.8125rem] font-semibold text-gray-700">{label}</span>
      {children}
      {help && <span className="text-xs text-gray-600">{help}</span>}
    </label>
  )
}

export function Switch({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label?: ReactNode; disabled?: boolean }) {
  return (
    <label className={cx('inline-flex cursor-pointer items-center gap-2 text-sm', disabled && 'opacity-50')}>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={cx('relative h-5 w-9 rounded-full transition-colors', checked ? 'bg-blue-600' : 'bg-gray-300')}
      >
        <span className={cx('absolute top-0.5 size-4 rounded-full bg-gray-0 shadow transition-all', checked ? 'left-[18px]' : 'left-0.5')} />
      </button>
      {label && <span className="text-gray-800">{label}</span>}
    </label>
  )
}

/** 탭/세그먼트: 상태 필터·보기 전환 */
export function Segmented<T extends string>({ value, onChange, items, size = 'md' }: { value: T; onChange: (v: T) => void; items: { value: T; label: ReactNode; count?: number }[]; size?: 'sm' | 'md' }) {
  return (
    <div className="inline-flex rounded-lg bg-gray-100 p-0.5">
      {items.map((it) => (
        <button
          key={it.value}
          type="button"
          onClick={() => onChange(it.value)}
          className={cx(
            'inline-flex items-center gap-1.5 rounded-md font-semibold transition-colors',
            size === 'sm' ? 'h-7 px-2.5 text-xs' : 'h-8 px-3 text-[0.8125rem]',
            value === it.value ? 'bg-gray-0 text-gray-900 shadow-sm' : 'text-gray-600 hover:text-gray-900',
          )}
        >
          {it.label}
          {it.count != null && <span className={cx('num text-xs', value === it.value ? 'text-gray-600' : 'text-gray-500')}>{it.count}</span>}
        </button>
      ))}
    </div>
  )
}

// ── 대화상자 · 드로어 ────────────────────────────────────────────────
function useEsc(open: boolean, onClose: () => void) {
  useEffect(() => {
    if (!open) return
    const on = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [open, onClose])
}

export function Dialog({ open, onClose, title, children, footer, width = 560 }: { open: boolean; onClose: () => void; title: ReactNode; children: ReactNode; footer?: ReactNode; width?: number }) {
  useEsc(open, onClose)
  if (!open) return null
  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/30 p-6" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="flex max-h-full flex-col rounded-2xl bg-gray-0 shadow-[var(--shadow-dialog)]" style={{ width }}>
        <header className="flex items-center justify-between border-b border-gray-100 px-6 py-4">
          <h2 className="text-lg font-semibold">{title}</h2>
          <button className="rounded-md p-1 text-gray-500 hover:bg-gray-100" onClick={onClose} aria-label="닫기">
            <X className="size-5" />
          </button>
        </header>
        <div className="min-h-0 flex-1 overflow-auto px-6 py-5">{children}</div>
        {footer && <footer className="flex justify-end gap-2 border-t border-gray-100 px-6 py-3.5">{footer}</footer>}
      </div>
    </div>,
    document.body,
  )
}

export function Drawer({ open, onClose, title, children, width = 520 }: { open: boolean; onClose: () => void; title: ReactNode; children: ReactNode; width?: number }) {
  useEsc(open, onClose)
  if (!open) return null
  return createPortal(
    <div className="fixed inset-0 z-40 flex justify-end bg-gray-900/20" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <aside className="flex h-full flex-col border-l border-gray-200 bg-gray-0 shadow-[var(--shadow-dialog)]" style={{ width }}>
        <header className="flex items-center justify-between border-b border-gray-100 px-5 py-3.5">
          <h2 className="text-base font-semibold">{title}</h2>
          <button className="rounded-md p-1 text-gray-500 hover:bg-gray-100" onClick={onClose} aria-label="닫기">
            <X className="size-5" />
          </button>
        </header>
        <div className="min-h-0 flex-1 overflow-auto p-5">{children}</div>
      </aside>
    </div>,
    document.body,
  )
}

// ── 상태 표시 ───────────────────────────────────────────────────────
export function Empty({ title, children, action }: { title: ReactNode; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-6 py-14 text-center">
      <p className="text-[0.9375rem] font-semibold text-gray-800">{title}</p>
      {children && <p className="max-w-md text-sm text-gray-600">{children}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  )
}

export function Spinner({ label = '불러오는 중…' }: { label?: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-16 text-sm text-gray-600">
      <Loader2 className="size-4 animate-spin" />
      {label}
    </div>
  )
}

export function Skeleton({ rows = 6 }: { rows?: number }) {
  return (
    <div className="space-y-2 p-4">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="h-8 animate-pulse rounded-md bg-gray-100" />
      ))}
    </div>
  )
}

const calloutStyle = {
  info: { cls: 'bg-blue-50 text-blue-700 border-blue-100', icon: <Info className="size-4 shrink-0" /> },
  warn: { cls: 'bg-amber-50 text-amber-700 border-amber-600/20', icon: <AlertTriangle className="size-4 shrink-0" /> },
  danger: { cls: 'bg-red-50 text-red-700 border-red-600/20', icon: <CircleAlert className="size-4 shrink-0" /> },
  success: { cls: 'bg-green-50 text-green-700 border-green-600/20', icon: <CheckCircle2 className="size-4 shrink-0" /> },
}

export function Callout({ tone = 'info', children, action, className }: { tone?: keyof typeof calloutStyle; children: ReactNode; action?: ReactNode; className?: string }) {
  const s = calloutStyle[tone]
  return (
    <div className={cx('flex items-start gap-2.5 rounded-lg border px-3.5 py-2.5 text-sm', s.cls, className)}>
      <span className="mt-0.5">{s.icon}</span>
      <div className="min-w-0 flex-1">{children}</div>
      {action}
    </div>
  )
}

export function ErrorBox({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <Callout tone="danger" action={onRetry && <Button size="sm" variant="danger" onClick={onRetry}>다시 시도</Button>}>
      {typeof error === 'string' ? error : error instanceof Error ? error.message : String(error)}
    </Callout>
  )
}

/** 페이지 제목 줄 */
export function PageHeader({ title, sub, actions, back }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode; back?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3 pb-5">
      <div className="min-w-0">
        {back && <div className="mb-1.5">{back}</div>}
        <h1 className="text-xl font-bold tracking-[-0.01em] text-gray-900">{title}</h1>
        {sub && <div className="mt-1 text-sm text-gray-600">{sub}</div>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

export function Stat({ label, value, className }: { label: ReactNode; value: ReactNode; className?: string }) {
  return (
    <div className={cx('flex flex-col gap-0.5', className)}>
      <span className="text-xs font-semibold text-gray-600">{label}</span>
      <span className="num text-[0.9375rem] font-semibold text-gray-900">{value}</span>
    </div>
  )
}

export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="rounded border border-gray-300 bg-gray-50 px-1.5 py-0.5 font-sans text-[0.6875rem] font-semibold text-gray-700">{children}</kbd>
}
