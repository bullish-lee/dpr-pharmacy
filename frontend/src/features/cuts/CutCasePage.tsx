import { ArrowLeft, ArrowRight, Check, CircleDashed, CircleDot, Search, TriangleAlert, Upload } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { api, errorText, queryClient, keys, useDeletions, useRanking, type service } from '../../app/api'
import { dateDot, dateLong, dday, parseDate, pct, price, qty, todayISO, won, wonUnit } from '../../app/format'
import { Link, navigate, paths } from '../../app/router'
import { DataTable } from '../../components/DataTable'
import { Badge, Button, Callout, Card, Empty, ErrorBox, Input, Kbd, Kpi, PageHeader, Segmented, Skeleton, cx } from '../../components/ui'
import { useLotEvidence } from './LotPhotoPage'
import { FlagBadges, RecBadge } from './shared'

type Tab = 'rank' | 'delete'

export function CutCasePage({ date }: { date: string }) {
  const [held, setHeld] = useState(true)
  const [tab, setTab] = useState<Tab>('rank')
  const [q, setQ] = useState('')
  const rk = useRanking(date, held)
  const del = useDeletions(date)
  const daysLeft = useMemo(() => {
    const t = parseDate(date)
    const n = parseDate(todayISO())
    return t && n ? Math.round((t.getTime() - n.getTime()) / 86400000) : 0
  }, [date])

  if (rk.isLoading) return <Skeleton rows={12} />
  if (rk.error || !rk.data) return <ErrorBox error={rk.error} onRetry={() => rk.refetch()} />
  const data = rk.data
  const rows = (data.rows ?? []).filter((r) => !q || r.name.includes(q) || r.code.startsWith(q))
  const heldRows = (data.rows ?? []).filter((r) => r.stock > 0)

  return (
    <div className="space-y-5">
      <PageHeader
        back={
          <Link to={paths.cuts()} className="inline-flex items-center gap-1 text-[0.8125rem] font-semibold text-gray-600 hover:text-gray-900">
            <ArrowLeft className="size-3.5" /> 약가인하 대응
          </Link>
        }
        title={
          <span className="flex items-center gap-3">
            {dateLong(date)} 시행 약가인하
            <Badge tone={daysLeft >= 0 ? 'blue' : 'outline'}>{dday(daysLeft)}</Badge>
          </span>
        }
        sub={`고시 ${(data.notices ?? []).map((n) => `제${n}호`).join(', ') || '번호 미상'} · 재고 기준 ${dateDot(data.asOf)}${data.isPast ? ' (시행 전날)' : ''}`}
        actions={
          <>
            <Button icon={<Upload className="size-4" />} onClick={() => navigate(paths.imports())}>
              최신 자료 올리기
            </Button>
            <Button onClick={() => navigate(paths.reports() + `?date=${date}`)}>처리 내역</Button>
          </>
        }
      />

      {(data.warnings?.length ?? 0) > 0 && (
        <Callout tone="warn">
          <ul className="space-y-0.5">
            {data.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </Callout>
      )}

      {/* 좁은 화면(1366×768 · 125%)에서도 손실 순위가 첫 화면에 보이게 요약·단계를 한 줄로 유지한다 */}
      <div className="grid grid-cols-4 gap-3 max-[860px]:grid-cols-2">
        <Kpi label="보유 중인 인하 품목" value={data.totals.held} unit="개" sub={`인하 고시 ${data.totals.items}개 중`} />
        <Kpi label="예상 손실 (보상 전)" value={won(data.totals.grossLoss)} unit="원" />
        <Kpi label="예상 보상" value={won(data.totals.recovery)} unit="원" sub={`서류 ${data.totals.document} · 실물 검토 ${data.totals.physical}${data.totals.check ? ` · 실제가 확인 ${data.totals.check}` : ''}`} />
        <Kpi
          label="보상 후 순손실"
          value={won(Math.max(0, data.totals.netLoss))}
          unit="원"
          sub={data.totals.netLoss < 0 ? `${wonUnit(-data.totals.netLoss)} 이득 예상` : '실계수·바코드로 줄일 수 있어요'}
        />
      </div>

      <TaskList data={data} heldRows={heldRows} />

      <div className="flex flex-wrap items-center justify-between gap-3">
        <Segmented<Tab>
          value={tab}
          onChange={setTab}
          items={[
            { value: 'rank', label: '손실 순위', count: heldRows.length },
            { value: 'delete', label: '보유 중 급여삭제', count: del.data?.length ?? 0 },
          ]}
        />
        {tab === 'rank' && (
          <div className="flex items-center gap-2">
            <div className="relative">
              <Search className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-gray-400" />
              <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="약품명·코드 검색" className="w-56 pl-8" />
            </div>
            <Segmented
              size="sm"
              value={held ? 'held' : 'all'}
              onChange={(v) => setHeld(v === 'held')}
              items={[
                { value: 'held', label: '보유 품목' },
                { value: 'all', label: '전체 인하 품목' },
              ]}
            />
          </div>
        )}
      </div>

      {tab === 'rank' && date < todayISO() && (
        <Callout tone="info">
          시행일이 지난 회차입니다. 여기 넣는 실계수는 <b>오늘({dateDot(todayISO())})</b> 날짜로 저장되어 재고 화면에 바로 반영됩니다. 이 회차의 손실·판정은 시행 전날({dateDot(data.asOf)}) 재고로
          계산합니다 — 오늘 센 수량은 다음 회차부터 판정에 씁니다.
        </Callout>
      )}
      {tab === 'rank' ? (
        <RankingTable date={date} asOf={data.asOf} rows={rows} totals={data.totals} />
      ) : (
        <DataTable
          rows={del.data ?? []}
          rowKey={(r) => r.code}
          empty={<Empty title="보유 중인 급여 삭제 품목이 없습니다" />}
          columns={[
            { key: 'name', header: '약품명', width: 'minmax(260px,2fr)', render: (r) => <span className="font-medium">{r.name}</span>, sort: (r) => r.name },
            { key: 'stock', header: '재고', width: '100px', align: 'right', render: (r) => qty(r.stock), sort: (r) => r.stock },
            { key: 'price', header: '상한금액', width: '110px', align: 'right', render: (r) => price(r.price) },
            { key: 'value', header: '재고 금액(원)', width: '130px', align: 'right', render: (r) => won(r.value), sort: (r) => r.value },
            { key: 'grace', header: '급여 유지', width: '150px', render: (r) => (r.graceUntil ? <span className="num">{dateDot(r.graceUntil)}까지</span> : <Badge tone="red">유예 없음</Badge>) },
          ]}
          defaultSort={{ key: 'value', dir: 'desc' }}
        />
      )}
    </div>
  )
}

// ── 작업 목록 (회차 허브) ─────────────────────────────────────────────
function TaskList({ data, heldRows }: { data: service.Ranking; heldRows: service.RankRow[] }) {
  const late = data.effectiveDate < todayISO()
  const countedCodes = heldRows.filter((r) => (late ? r.laterCountQty : r.countQty) != null).map((r) => r.code)
  const counted = countedCodes.length
  const evidence = useLotEvidence(countedCodes)
  const withPhoto = new Set((evidence.data ?? []).map((e) => e.code)).size
  const physical = heldRows.filter((r) => r.recommendation === 'physical_review' || r.physicalUpside > 0)
  const scanned = physical.filter((r) => r.scan.units > 0).length
  const ambiguous = heldRows.reduce((s, r) => s + (r.scan?.ambiguous ?? 0), 0)
  const done = heldRows.filter((r) => r.settleStatus === 'done').length
  const confirmed = heldRows.filter((r) => r.settleStatus !== '').length
  const stale = (data.warnings ?? []).some((w) => w.includes('까지만 반영') || w.includes('데이터가 없습니다'))
  type St = 'done' | 'doing' | 'todo' | 'attention' | 'na'
  const tasks: { title: string; detail: string; st: St; to?: string }[] = [
    { title: '최신 사입·조제 자료 반영', detail: stale ? '자료가 오래되었습니다 — 이팜에서 내려받아 올려주세요' : '최신 자료 기준', st: stale ? 'attention' : 'done', to: paths.imports() },
    { title: '실계수 입력 (선택)', detail: `${counted} / ${heldRows.length}개 입력 · 손실이 큰 품목부터 세어보세요`, st: heldRows.length === 0 ? 'na' : counted === 0 ? 'todo' : counted < heldRows.length ? 'doing' : 'done' },
    {
      title: '판정 확인·확정',
      detail: `서류 ${data.totals.document} · 실물 검토 ${data.totals.physical}${data.totals.check ? ` · 실제가 확인 ${data.totals.check}` : ''} · 확정 ${confirmed}/${heldRows.length}`,
      st: heldRows.length === 0 ? 'na' : data.totals.check > 0 ? 'attention' : confirmed === 0 ? 'todo' : confirmed < heldRows.length ? 'doing' : 'done',
    },
    {
      title: '사입정보 사진 확인',
      detail: counted === 0 ? '실계수를 넣은 품목이 없습니다' : `${withPhoto} / ${counted}개 품목 · 유효기간·제조번호가 보이는 사입 내역을 사진으로 올리세요`,
      st: counted === 0 ? 'na' : withPhoto === 0 ? 'todo' : withPhoto < counted ? 'doing' : 'done',
      to: paths.lotPhotos(data.effectiveDate),
    },
    {
      title: '바코드로 도매 확인',
      detail: physical.length === 0 ? '실물보상이 유리할 수 있는 품목이 없습니다' : `${scanned} / ${physical.length}개 품목 스캔 (실물 유리 가능)${ambiguous ? ` · 도매 확인 필요 ${ambiguous}개` : ''}`,
      st: physical.length === 0 ? 'na' : ambiguous > 0 ? 'attention' : scanned === 0 ? 'todo' : scanned < physical.length ? 'doing' : 'done',
    },
    { title: '처리 결과 기록', detail: `${done} / ${heldRows.length}개 품목 완료`, st: heldRows.length === 0 ? 'na' : done === 0 ? 'todo' : done < heldRows.length ? 'doing' : 'done' },
  ]
  const label: Record<St, { text: string; cls: string; icon: React.ReactNode }> = {
    done: { text: '완료', cls: 'text-gray-600', icon: <Check className="size-4 text-green-600" /> },
    doing: { text: '진행 중', cls: 'text-blue-700', icon: <CircleDot className="size-4 text-blue-600" /> },
    todo: { text: '시작 전', cls: 'text-gray-600', icon: <CircleDashed className="size-4 text-gray-400" /> },
    attention: { text: '확인 필요', cls: 'text-amber-700', icon: <TriangleAlert className="size-4 text-amber-600" /> },
    na: { text: '해당 없음', cls: 'text-gray-400', icon: <CircleDashed className="size-4 text-gray-300" /> },
  }
  return (
    <Card pad={false}>
      <ol className="grid grid-cols-6 divide-x divide-gray-100 max-[860px]:grid-cols-1 max-[860px]:divide-x-0 max-[860px]:divide-y">
        {tasks.map((t, i) => {
          const l = label[t.st]
          const inner = (
            <>
              <div className="flex items-center gap-2">
                {l.icon}
                <span className="text-xs font-semibold text-gray-500">{i + 1}단계</span>
                <span className={cx('ml-auto text-xs font-semibold', l.cls)}>{l.text}</span>
              </div>
              <div className="mt-1.5 text-sm font-semibold text-gray-900 max-[1180px]:text-[0.8125rem]">{t.title}</div>
              <div className="mt-0.5 text-xs text-gray-600 max-[1180px]:line-clamp-2">{t.detail}</div>
            </>
          )
          return (
            <li key={t.title} className="min-w-0 px-4 py-3.5 max-[1180px]:px-2.5 max-[1180px]:py-2.5" title={t.detail}>
              {t.to ? (
                <a href={`#${t.to}`} className="block hover:opacity-80">
                  {inner}
                </a>
              ) : (
                inner
              )}
            </li>
          )
        })}
      </ol>
    </Card>
  )
}

// ── 손실 순위 + 실계수 입력 ──────────────────────────────────────────
function RankingTable({ date, asOf, rows, totals }: { date: string; asOf: string; rows: service.RankRow[]; totals: service.RankTotals }) {
  // 입력 중에는 행이 움직이지 않도록 순서를 고정하고, 실계수 열을 벗어나면 다시 정렬
  const [frozen, setFrozen] = useState<string[] | null>(null)
  const [moved, setMoved] = useState<Set<string>>(new Set())
  const inputs = useRef(new Map<number, HTMLInputElement>())
  const byCode = useMemo(() => new Map(rows.map((r) => [r.code, r])), [rows])
  const natural = rows.map((r) => r.code)
  const order = frozen ? [...frozen.filter((c) => byCode.has(c)), ...natural.filter((c) => !frozen.includes(c))] : natural
  const view = order.map((c) => byCode.get(c)!).filter(Boolean)
  const prevOrder = useRef<string[]>(natural)

  useEffect(() => {
    if (frozen) return
    const before = prevOrder.current
    const changed = new Set(natural.filter((c, i) => before.indexOf(c) !== -1 && before.indexOf(c) !== i))
    prevOrder.current = natural
    if (changed.size) {
      setMoved(changed)
      const t = setTimeout(() => setMoved(new Set()), 1300)
      return () => clearTimeout(t)
    }
  }, [frozen, natural.join('|')]) // eslint-disable-line react-hooks/exhaustive-deps

  const onFocus = () => setFrozen((f) => f ?? order)
  const onBlur = () =>
    setTimeout(() => {
      const el = document.activeElement as HTMLElement | null
      if (!el?.dataset?.countInput) setFrozen(null)
    }, 0)

  const grid = 'grid-cols-[56px_minmax(240px,2.4fr)_150px_76px_96px_120px_120px_96px_190px_28px]'
  // 시행일이 지난 뒤 세는 수량은 시행 전날 재고가 아니다 → 오늘 날짜로 저장 (예전에는 시행 전날로 저장해 재고 화면에서 그 뒤 조제를 한 번 더 뺐다)
  const late = date < todayISO()
  const countDate = late ? todayISO() : asOf

  if (rows.length === 0) {
    return (
      <Card>
        <Empty title="보유 중인 인하 품목이 없습니다">이 시행일의 인하 품목 중 추정재고가 있는 품목이 없습니다. '전체 인하 품목'으로 바꿔 확인할 수 있어요.</Empty>
      </Card>
    )
  }

  return (
    <div className="overflow-hidden rounded-xl border border-gray-200 bg-gray-0">
      <div className="overflow-x-auto">
        <div className={cx('grid min-w-fit border-b border-gray-200 bg-gray-50 text-[0.8125rem] font-semibold text-gray-700', grid)}>
          <div className="px-3 py-2.5 text-right whitespace-nowrap">순위</div>
          <div className="px-3 py-2.5">약품</div>
          <div className="px-3 py-2.5 text-right" title="고시 단위(정·관 등) 1개당 상한금액">상한금액 전 → 후</div>
          <div className="px-3 py-2.5 text-right">인하율</div>
          <div className="px-3 py-2.5 text-right">추정재고</div>
          <div
            className="px-3 py-2.5 text-right"
            title={
              late
                ? '오늘 직접 센 수량 (고시 단위) — 오늘 날짜로 저장되어 재고 화면에 반영됩니다. 이 회차 계산은 시행 전날 재고로 합니다. Enter 로 저장하고 다음 품목으로'
                : '실계수 = 약을 직접 세어 본 수량 (고시 단위). 넣으면 추정재고 대신 이 수량으로 계산합니다. 영업을 마친 뒤 센 수량으로 저장됩니다. Enter 로 저장하고 다음 품목으로'
            }
          >
            {late ? '오늘 실계수' : '실계수'} <span className="font-normal text-gray-500">(Enter)</span>
          </div>
          <div className="px-3 py-2.5 text-right">예상 손실(원)</div>
          <div className="px-3 py-2.5 text-right" title="도매별 서류보상 수량 합계">서류보상</div>
          <div className="px-3 py-2.5">판정</div>
          <div />
        </div>
        {view.map((r, i) => (
          <div
            key={r.code}
            className={cx('grid min-w-fit items-center border-b border-gray-200 text-sm hover:bg-gray-50', grid, moved.has(r.code) && 'row-moved')}
          >
            <div className="num px-3 text-right text-gray-500">{i + 1}</div>
            <button className="min-w-0 px-3 py-2 text-left" onClick={() => navigate(paths.product(date, r.code))}>
              <div className="truncate font-medium text-gray-900" title={r.name}>
                {r.name}
              </div>
              <div className="mt-0.5 flex items-center gap-2 text-xs text-gray-500">
                <span className="code">{r.code}</span>
                <FlagBadges row={r} />
              </div>
            </button>
            <div className="num px-3 text-right">
              <span className="text-gray-600">{price(r.priceBefore)}</span>
              <span className="px-1 text-gray-400">→</span>
              <span className="font-semibold">{price(r.priceAfter)}</span>
            </div>
            <div className="num px-3 text-right">{pct(r.rate)}</div>
            <div className={cx('num px-3 text-right', r.countQty != null && 'text-gray-500')} title={`기준: ${r.basis}`}>
              {qty(r.estimate)}
              {r.unit && <span className="ml-0.5 text-[0.75rem] font-normal text-gray-500">{r.unit}</span>}
            </div>
            <div className="px-2">
              <CountInput row={r} index={i} asOf={countDate} late={late} inputs={inputs.current} onFocus={onFocus} onBlur={onBlur} />
            </div>
            <div className={cx('num px-3 text-right', i < 20 && r.grossLoss > 0 && 'font-semibold')}>{won(r.grossLoss)}</div>
            <div className="num px-3 text-right">{qty(r.totalDocQty)}</div>
            <div className="px-3">
              <RecBadge rec={r.recommendation} settle={r.settleStatus} upside={r.physicalUpside} />
            </div>
            <button className="text-gray-400 hover:text-gray-700" onClick={() => navigate(paths.product(date, r.code))} title="상세">
              <ArrowRight className="size-4" />
            </button>
          </div>
        ))}
      </div>
      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-gray-200 bg-gray-50 px-4 py-2.5 text-[0.8125rem] text-gray-700">
        <span>
          보유 <b className="num">{totals.held}</b>개 품목 · 예상 손실 합계 <b className="num">{wonUnit(totals.grossLoss)}</b> · 예상 보상 <b className="num">{wonUnit(totals.recovery)}</b>
        </span>
        <span className="flex items-center gap-1.5 text-gray-600">
          실계수(직접 센 수량): <Kbd>Enter</Kbd> 저장·다음 <Kbd>Esc</Kbd> 되돌리기 · 빈칸으로 저장하면 삭제
        </span>
      </div>
    </div>
  )
}

function CountInput({
  row,
  index,
  asOf,
  late,
  inputs,
  onFocus,
  onBlur,
}: {
  row: service.RankRow
  index: number
  asOf: string
  late: boolean // 지난 회차: 오늘 날짜 실계수를 보여 주고 고친다 (회차 계산에는 쓰지 않음)
  inputs: Map<number, HTMLInputElement>
  onFocus: () => void
  onBlur: () => void
}) {
  const cq = late ? row.laterCountQty : row.countQty
  const anchorId = late ? row.laterCountAnchorId : row.countAnchorId
  const cdate = late ? row.laterCountDate : row.countDate
  const initial = cq != null ? String(cq) : ''
  const [value, setValue] = useState(initial)
  const [state, setState] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle')
  const [err, setErr] = useState('')
  useEffect(() => setValue(initial), [initial])

  const invalid = value.trim() !== '' && (!/^\d+(\.\d{1,2})?$/.test(value.trim()) || Number(value) < 0)

  const commit = async () => {
    const v = value.trim()
    if (v === initial || invalid) return true
    setState('saving')
    try {
      if (v === '') {
        if (anchorId) await api.DeleteAnchor(anchorId)
      } else {
        await api.SaveCount({ code: row.code, qty: Number(v), unit: 'base', asOf, timing: 'end', note: '', source: '손실 순위' })
      }
      setState('saved')
      setTimeout(() => setState('idle'), 1000)
      queryClient.invalidateQueries({ queryKey: keys.all })
      return true
    } catch (e) {
      setErr(errorText(e))
      setState('error')
      return false
    }
  }

  return (
    <div className="relative">
      <input
        ref={(el) => {
          if (el) inputs.set(index, el)
          else inputs.delete(index)
        }}
        data-count-input="1"
        inputMode="decimal"
        value={value}
        placeholder="—"
        title={invalid ? '0 이상의 숫자(소수 2자리까지)를 입력하세요' : err || (cdate ? `${dateDot(cdate)} 실계수` : '직접 센 수량')}
        onChange={(e) => {
          setValue(e.target.value)
          setState('idle')
        }}
        onFocus={(e) => {
          e.currentTarget.select()
          onFocus()
        }}
        onBlur={() => {
          void commit()
          onBlur()
        }}
        onKeyDown={async (e) => {
          if (e.nativeEvent.isComposing || e.keyCode === 229) return
          if (e.key === 'Enter' || e.key === 'ArrowDown') {
            e.preventDefault()
            if (await commit()) inputs.get(index + 1)?.focus()
          } else if (e.key === 'ArrowUp') {
            e.preventDefault()
            inputs.get(index - 1)?.focus()
          } else if (e.key === 'Escape') {
            setValue(initial)
            setState('idle')
          }
        }}
        className={cx(
          'num h-8 w-full rounded-md border bg-gray-0 px-2 text-right text-sm focus:ring-2 focus:outline-none',
          invalid || state === 'error' ? 'border-red-600 focus:ring-red-600/20' : 'border-gray-400/70 focus:border-blue-500 focus:ring-blue-500/25',
          cq != null && 'font-semibold',
        )}
      />
      {state === 'saved' && <Check className="absolute top-2 -left-4 size-3.5 text-green-600" />}
    </div>
  )
}
