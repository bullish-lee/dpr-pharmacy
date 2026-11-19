import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowDown, ArrowUp } from 'lucide-react'
import { useMemo, useRef, useState, type ReactNode } from 'react'
import { cx } from './ui'

// 가볍고 고치기 쉬운 표: 정렬 + 가상 스크롤(수천 행도 화면에 보이는 30여 행만 그림).
// 숫자는 오른쪽 정렬·tabular-nums, 헤더 고정, 줄무늬 없음, 행 높이 40px.

export type Column<T> = {
  key: string
  header: ReactNode
  width?: string // CSS grid 트랙 (예: '120px', 'minmax(240px,2fr)')
  align?: 'left' | 'right' | 'center'
  sort?: (row: T) => number | string
  render: (row: T, index: number) => ReactNode
  title?: string
  className?: string
}

export type SortState = { key: string; dir: 'asc' | 'desc' } | null

export function DataTable<T>({
  rows,
  columns,
  rowKey,
  onRowClick,
  defaultSort = null,
  virtual = rows.length > 150,
  rowHeight = 40,
  maxHeight,
  empty,
  footer,
  rowClassName,
  selectedKey,
}: {
  rows: T[]
  columns: Column<T>[]
  rowKey: (row: T) => string | number
  onRowClick?: (row: T) => void
  defaultSort?: SortState
  virtual?: boolean
  rowHeight?: number
  maxHeight?: string
  empty?: ReactNode
  footer?: ReactNode
  rowClassName?: (row: T) => string | undefined
  selectedKey?: string | number
}) {
  const [sort, setSort] = useState<SortState>(defaultSort)
  const sorted = useMemo(() => {
    if (!sort) return rows
    const col = columns.find((c) => c.key === sort.key)
    if (!col?.sort) return rows
    const f = col.sort
    const out = [...rows].sort((a, b) => {
      const x = f(a)
      const y = f(b)
      const c = typeof x === 'number' && typeof y === 'number' ? x - y : String(x).localeCompare(String(y), 'ko')
      return sort.dir === 'asc' ? c : -c
    })
    return out
  }, [rows, sort, columns])

  const template = columns.map((c) => c.width ?? 'minmax(80px,1fr)').join(' ')
  const parentRef = useRef<HTMLDivElement>(null)
  const virtualizer = useVirtualizer({
    count: virtual ? sorted.length : 0,
    getScrollElement: () => parentRef.current,
    estimateSize: () => rowHeight,
    overscan: 12,
  })

  const toggleSort = (c: Column<T>) => {
    if (!c.sort) return
    setSort((s) => {
      if (!s || s.key !== c.key) return { key: c.key, dir: c.align === 'right' ? 'desc' : 'asc' } // 숫자는 큰 값부터
      return { key: c.key, dir: s.dir === 'asc' ? 'desc' : 'asc' }
    })
  }

  const renderRow = (row: T, i: number, style?: React.CSSProperties) => {
    const k = rowKey(row)
    return (
      <div
        key={k}
        role="row"
        style={{ ...style, gridTemplateColumns: template, minHeight: rowHeight }}
        onClick={onRowClick ? () => onRowClick(row) : undefined}
        className={cx(
          'grid items-center border-b border-gray-200 text-sm',
          onRowClick && 'cursor-pointer hover:bg-gray-100/70',
          selectedKey === k && 'bg-blue-50',
          rowClassName?.(row),
        )}
      >
        {columns.map((c) => (
          <div
            key={c.key}
            role="cell"
            className={cx(
              'min-w-0 px-3 py-1.5',
              c.align === 'right' && 'num text-right',
              c.align === 'center' && 'text-center',
              c.className,
            )}
          >
            {c.render(row, i)}
          </div>
        ))}
      </div>
    )
  }

  return (
    <div className="flex min-h-0 flex-col overflow-hidden rounded-xl border border-gray-200 bg-gray-0" style={{ maxHeight }}>
      <div ref={parentRef} className="min-h-0 flex-1 overflow-auto" role="table">
        <div role="row" className="sticky top-0 z-10 grid border-b border-gray-200 bg-gray-50" style={{ gridTemplateColumns: template, minWidth: 'fit-content' }}>
          {columns.map((c) => (
            <div
              key={c.key}
              role="columnheader"
              title={c.title}
              onClick={() => toggleSort(c)}
              className={cx(
                'group flex h-10 items-center gap-1 px-3 text-[0.8125rem] font-semibold whitespace-nowrap text-gray-700 select-none',
                c.align === 'right' && 'justify-end text-right',
                c.align === 'center' && 'justify-center',
                c.sort && 'cursor-pointer hover:text-gray-900',
              )}
            >
              {c.header}
              {c.sort && (
                <span className={cx('text-gray-500', sort?.key === c.key ? 'opacity-100' : 'opacity-0 group-hover:opacity-60')}>
                  {sort?.key === c.key && sort.dir === 'asc' ? <ArrowUp className="size-3.5" /> : <ArrowDown className="size-3.5" />}
                </span>
              )}
            </div>
          ))}
        </div>
        {sorted.length === 0 ? (
          <div className="px-4">{empty}</div>
        ) : virtual ? (
          <div style={{ height: virtualizer.getTotalSize(), position: 'relative', minWidth: 'fit-content' }}>
            {virtualizer.getVirtualItems().map((v) =>
              renderRow(sorted[v.index], v.index, { position: 'absolute', top: 0, left: 0, right: 0, height: rowHeight, transform: `translateY(${v.start}px)` }),
            )}
          </div>
        ) : (
          <div style={{ minWidth: 'fit-content' }}>{sorted.map((r, i) => renderRow(r, i))}</div>
        )}
      </div>
      {footer && <div className="border-t border-gray-200 bg-gray-50 px-4 py-2.5 text-[0.8125rem] text-gray-700">{footer}</div>}
    </div>
  )
}
