import { Search } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api, looksLikeEDICode, obj, useAction, useCodeCheck, useInventory, useProductSearch, useUnresolved, type service } from '../../app/api'
import { basisLabel, dateDot, price, qty, todayISO, won } from '../../app/format'
import { useRoute } from '../../app/router'
import { ChangeLogTable } from '../../components/ChangeLog'
import { DataTable } from '../../components/DataTable'
import { Badge, Button, Callout, Card, Dialog, Empty, ErrorBox, Field, Input, PageHeader, Segmented, Select, Switch } from '../../components/ui'

type Tab = 'stock' | 'unresolved' | 'baseline' | 'history'

function useDebounced<T>(v: T, ms = 250) {
  const [d, setD] = useState(v)
  useEffect(() => {
    const t = setTimeout(() => setD(v), ms)
    return () => clearTimeout(t)
  }, [v, ms])
  return d
}

export function InventoryPage() {
  const route = useRoute()
  const [tab, setTab] = useState<Tab>((route.query.get('tab') as Tab) || 'stock')
  const un = useUnresolved()
  return (
    <div className="space-y-5">
      <PageHeader title="재고" sub="추정재고 = 기준점(초기재고·실계수) + 이후 사입 − 이후 조제. 실계수가 있으면 실계수가 우선합니다." />
      <Segmented<Tab>
        value={tab}
        onChange={setTab}
        items={[
          { value: 'stock', label: '재고 목록' },
          { value: 'unresolved', label: '초기재고 확인 필요', count: un.data?.length ?? 0 },
          { value: 'baseline', label: '초기재고 기준일' },
          { value: 'history', label: '수정 기록' },
        ]}
      />
      {tab === 'stock' && <StockTab />}
      {tab === 'history' && <ChangeLogTable maxHeight="calc(100vh - 300px)" />}
      {tab === 'unresolved' && <UnresolvedTab rows={un.data ?? []} />}
      {tab === 'baseline' && <BaselineTab />}
    </div>
  )
}

const FLAG_LABEL: Record<string, string> = { negative: '음수', unit_check: '단위 확인', overstock: '과다 재고', unit_auto: '자동 환산' }

function StockTab() {
  const route = useRoute()
  const [q, setQ] = useState(route.query.get('q') ?? '') // 약품 조회 화면에서 넘어온 검색어
  const [flag, setFlag] = useState(route.query.get('f') ?? '')
  const [nonzero, setNonzero] = useState(!route.query.get('f') && !route.query.get('q'))
  const dq = useDebounced(q)
  const inv = useInventory(dq, nonzero)
  const [counting, setCounting] = useState<service.InventoryRow | null>(null)
  const [factoring, setFactoring] = useState<service.InventoryRow | null>(null)
  const [acking, setAcking] = useState<{ row: service.InventoryRow; flag: string } | null>(null)
  const [history, setHistory] = useState<service.InventoryRow | null>(null)
  const [hideAcked, setHideAcked] = useState(true)
  const unack = useAction((code: string, f: string) => api.UnackHealth(code, f, '데이터 점검'), { success: '다시 점검 목록에 올렸습니다' })
  useEffect(() => {
    const f = route.query.get('f') ?? ''
    setFlag(f)
    if (f) setNonzero(false)
  }, [route.query])
  if (inv.error) return <ErrorBox error={inv.error} onRetry={() => inv.refetch()} />
  const v = inv.data
  const counts: Record<string, number> = {}
  for (const r of v?.rows ?? []) for (const f of r.flags ?? []) if (!r.acked?.includes(f)) counts[f] = (counts[f] ?? 0) + 1
  const rows = flag ? (v?.rows ?? []).filter((r) => r.flags?.includes(flag) && !(hideAcked && r.acked?.includes(flag))) : (v?.rows ?? [])
  const source = flag ? '데이터 점검' : '재고'
  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative">
          <Search className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-gray-400" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="약품명 또는 코드" className="w-72 pl-8" />
        </div>
        <Switch checked={nonzero} onChange={setNonzero} label="재고 0 숨기기" />
        <Segmented
          value={flag}
          onChange={setFlag}
          items={[{ value: '', label: '전체' }, ...Object.entries(FLAG_LABEL).map(([k, l]) => ({ value: k, label: `${l} ${counts[k] ?? 0}` }))]}
        />
        {flag && <Switch checked={hideAcked} onChange={setHideAcked} label="'확인함' 숨기기" />}
        {v && (
          <span className="ml-auto text-sm text-gray-600">
            기준 {dateDot(v.asOf)} · 사입 {dateDot(v.lastPurchase) || '없음'} · 조제 {dateDot(v.lastDispense) || '없음'}까지 반영 · 재고금액 합계 <b className="num text-gray-900">{won(v.totalValue)}원</b>
          </span>
        )}
      </div>
      {v && v.negative > 0 && (
        <Callout tone="warn">추정재고가 음수인 품목이 {v.negative}개 있습니다. 초기재고 누락이나 사입 자료 누락일 수 있어요 — 실계수를 입력하거나 자료를 확인하세요.</Callout>
      )}
      <DataTable
        rows={rows}
        rowKey={(r) => r.code}
        virtual
        maxHeight="calc(100vh - 290px)"
        onRowClick={(r) => setCounting(r)}
        empty={<Empty title={inv.isLoading ? '불러오는 중…' : '재고 데이터가 없습니다'}>'데이터 가져오기'에서 초기재고·사입·조제 자료를 올려주세요.</Empty>}
        rowClassName={(r) => (r.qty < 0 ? 'text-red-700' : undefined)}
        columns={[
          {
            key: 'name',
            header: '약품명',
            width: 'minmax(220px,3fr)',
            sort: (r) => r.name,
            render: (r) => (
              <div className="min-w-0">
                <div className="truncate font-medium">{r.name || '(이름 없음)'}</div>
                <div className="flex min-w-0 items-center gap-1.5 text-xs text-gray-500">
                  <span className="code">{r.code}</span>
                  {r.flags?.includes('unit_check') && (
                    <Badge tone="amber" title={r.unitNote}>
                      단위 확인
                    </Badge>
                  )}
                  {r.flags?.includes('unit_auto') && (
                    <Badge tone="outline" title={r.unitNote}>
                      자동 환산
                    </Badge>
                  )}
                  {r.flags?.includes('overstock') && <Badge tone="gray" title={`최근 6개월 월평균 ${qty(r.monthlyUse)}${r.unit}`}>과다 재고</Badge>}
                  {r.qty < 0 && r.hint && <span className="truncate text-red-700">{r.hint}</span>}
                </div>
              </div>
            ),
          },
          {
            key: 'qty',
            header: '재고',
            width: '120px',
            align: 'right',
            sort: (r) => r.qty,
            // 재고가 무엇으로 이루어졌는지 바로 보이게 (기준 수량 + 사입 − 조제, 고시 단위)
            render: (r) => (
              <div title={`${basisLabel[r.basis] ?? '기준'} ${qty(r.anchorQty)}${r.anchorDate ? ` (${dateDot(r.anchorDate)})` : ''} + 사입 ${qty(r.inflow)} − 조제 ${qty(r.outflow)} = ${qty(r.qty)} ${r.unit}${r.unitNote ? ` · ${r.unitNote}` : ''}`}>
                <b>{qty(r.qty)}</b>
                <span className="ml-1 text-xs font-normal text-gray-500">{r.unit}</span>
                {r.countQty != null && r.estimate !== r.qty && <div className="text-xs font-normal text-gray-500">추정 {qty(r.estimate)}</div>}
              </div>
            ),
          },
          {
            key: 'basis',
            header: '기준 수량',
            width: '200px',
            render: (r) => (
              <span className="flex items-center gap-1.5" title="재고 = 기준 수량 + 이후 사입 − 이후 조제">
                <Badge tone={r.basis === 'count' ? 'blue' : 'gray'}>{basisLabel[r.basis] ?? r.basis}</Badge>
                <span className="num text-sm text-gray-800">{qty(r.anchorQty)}</span>
                <span className="num text-xs text-gray-500">{dateDot(r.anchorDate)}</span>
              </span>
            ),
          },
          { key: 'in', header: '+ 사입', width: '80px', align: 'right', render: (r) => qty(r.inflow) },
          { key: 'out', header: '− 조제', width: '80px', align: 'right', render: (r) => qty(r.outflow) },
          ...(flag
            ? []
            : [
                {
                  key: 'value',
                  header: '금액(원)',
                  width: '100px',
                  align: 'right' as const,
                  sort: (r: service.InventoryRow) => r.value,
                  render: (r: service.InventoryRow) => <span title={`1${r.unit || '단위'} ${price(r.unitPrice)}원 × ${qty(r.qty)}`}>{won(r.value)}</span>,
                },
              ]),
          {
            key: 'act',
            header: '바로 고치기',
            width: flag ? '300px' : '190px',
            render: (r) => {
              const f = flag || r.flags?.find((x) => !r.acked?.includes(x)) || ''
              return (
                <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
                  <Button size="sm" variant="ghost" onClick={() => setCounting(r)} title="직접 센 수량으로 재고를 바로잡기">
                    실계수
                  </Button>
                  {(r.flags?.includes('unit_check') || r.flags?.includes('unit_auto') || r.flags?.includes('overstock')) && (
                    <Button size="sm" variant="ghost" onClick={() => setFactoring(r)} title="사입 기록의 단위 배율 지정">
                      배율
                    </Button>
                  )}
                  {f &&
                    (r.acked?.includes(f) ? (
                      <Button size="sm" variant="ghost" onClick={() => unack.mutate([r.code, f])} title="다시 점검 목록에 올리기">
                        확인 취소
                      </Button>
                    ) : (
                      <Button size="sm" variant="ghost" onClick={() => setAcking({ row: r, flag: f })} title="이상 없음으로 표시 (값이 바뀌면 다시 나타남)">
                        확인함
                      </Button>
                    ))}
                  <Button size="sm" variant="ghost" onClick={() => setHistory(r)} title="이 약의 수정 기록">
                    기록
                  </Button>
                </div>
              )
            },
          },
        ]}
        defaultSort={{ key: 'name', dir: 'asc' }}
      />
      <CountDialog row={counting} source={source} onClose={() => setCounting(null)} />
      <FactorDialog row={factoring} source={source} onClose={() => setFactoring(null)} />
      <AckDialog target={acking} onClose={() => setAcking(null)} />
      <Dialog open={!!history} onClose={() => setHistory(null)} title={`수정 기록 — ${history?.name ?? ''}`} width={980}>
        {history && <ChangeLogTable productCode={history.code} maxHeight="60vh" />}
      </Dialog>
    </>
  )
}

function CountDialog({ row, source = '재고', onClose }: { row: service.InventoryRow | null; source?: string; onClose: () => void }) {
  const [v, setV] = useState('')
  const [note, setNote] = useState('')
  const [date, setDate] = useState(todayISO()) // toISOString 은 UTC 라 오전 9시 전에는 어제 날짜가 된다
  const [timing, setTiming] = useState('end')
  const [unit, setUnit] = useState('base')
  const [opts, setOpts] = useState<service.UnitOption[]>([])
  const save = useAction(() => api.SaveCount(obj<service.CountInput>({ code: row!.code, qty: Number(v), unit, asOf: date, timing, note, source })), { success: '실계수를 저장했습니다' })
  useEffect(() => {
    setV(row?.countQty != null ? String(row.countQty) : '')
    setUnit('base')
    if (row) void api.UnitOptions(row.code).then((o) => setOpts(o ?? []))
  }, [row])
  if (!row) return null
  const opt = opts.find((o) => o.key === unit)
  const converted = opt && v !== '' ? Number(v) * opt.factor : null
  return (
    <Dialog
      open
      onClose={onClose}
      title={`실계수(직접 센 수량) — ${row.name}`}
      footer={
        <>
          <Button onClick={onClose}>취소</Button>
          <Button variant="primary" disabled={v === '' || Number(v) < 0} loading={save.isPending} onClick={() => save.mutate([], { onSuccess: onClose })}>
            저장
          </Button>
        </>
      }
    >
      <div className="grid grid-cols-3 gap-3">
        <Field label="센 수량" help={opt && opt.key !== 'base' ? opt.hint : '개봉한 통·소분은 낱알, 미개봉은 포장으로 세어도 됩니다'}>
          <div className="flex gap-1.5">
            <Input autoFocus value={v} onChange={(e) => setV(e.target.value)} inputMode="decimal" />
            <Select value={unit} onChange={(e) => setUnit(e.target.value)} className="w-24 shrink-0">
              {(opts.length ? opts : [{ key: 'base', label: row.unit || '개', factor: 1, hint: '' }]).map((o) => (
                <option key={o.key} value={o.key}>
                  {o.label}
                </option>
              ))}
            </Select>
          </div>
        </Field>
        <Field label="실사일">
          <Input type="date" value={date} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field label="센 때" help="영업 후 = 그날 조제까지 끝난 뒤 센 수량 · 영업 전 = 그날 조제 전에 센 수량">
          <Select value={timing} onChange={(e) => setTiming(e.target.value)}>
            <option value="end">영업 후</option>
            <option value="start">영업 전</option>
          </Select>
        </Field>
      </div>
      <Field label="메모 (선택)" className="mt-3">
        <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="예: 다시 셈, 개봉한 통 포함" />
      </Field>
      <p className="mt-4 text-sm text-gray-600">
        {converted != null && unit !== 'base' && (
          <b className="mr-2 text-gray-900">
            = {qty(converted)} {row.unit} 로 저장
          </b>
        )}
        현재 추정 {qty(row.estimate)} {row.unit} · 기준 {basisLabel[row.basis]} {dateDot(row.anchorDate)}
      </p>
    </Dialog>
  )
}

function UnresolvedTab({ rows }: { rows: service.UnresolvedRow[] }) {
  const resolve = useAction((id: number, code: string) => api.ResolveUnresolved(id, code))
  const resolveCode = useAction((id: number, code: string) => api.ResolveUnresolvedByCode(id, code), { success: '입력한 보험코드로 지정해 초기재고에 더했습니다' })
  const [search, setSearch] = useState<service.UnresolvedRow | null>(null)
  if (rows.length === 0) {
    return (
      <Card>
        <Empty title="확인이 필요한 초기재고 행이 없습니다">초기재고 파일의 모든 행이 약품과 연결되었습니다.</Empty>
      </Card>
    )
  }
  return (
    <>
      <Callout tone="info">
        초기재고 파일에서 약품코드 없이 이름만 있어 자동으로 연결하지 못한 행입니다. 후보를 고르거나 검색해 지정하세요. 검색에도 없으면 '검색…'에 보험코드(9자리)를 입력해 직접 지정할 수 있습니다. 재고와 무관한 행(합계·메모 등)은 '제외'하세요. 지정하면 바로 초기재고에 더해집니다.
      </Callout>
      <DataTable
        rows={rows}
        rowKey={(r) => r.id}
        maxHeight="calc(100vh - 300px)"
        columns={[
          { key: 'sheet', header: '시트 / 행', width: '170px', render: (r) => <span className="text-xs text-gray-600">{r.sheet} · {r.row}행</span> },
          { key: 'name', header: '파일의 약품명', width: 'minmax(200px,1.5fr)', render: (r) => <span className="font-medium">{r.name}</span> },
          { key: 'qty', header: '수량', width: '80px', align: 'right', render: (r) => qty(r.qty) },
          { key: 'price', header: '단가', width: '80px', align: 'right', render: (r) => price(r.unitPrice) },
          {
            key: 'cand',
            header: '후보 (클릭해서 지정)',
            width: 'minmax(320px,3fr)',
            render: (r) => (
              <span className="flex flex-wrap items-center gap-1.5">
                {(r.candidates ?? []).slice(0, 3).map((c) => (
                  <button key={c.code} onClick={() => resolve.mutate([r.id, c.code])} className="h-7 max-w-[260px] truncate rounded-md border border-gray-300 bg-gray-0 px-2 text-[0.8125rem] hover:border-blue-500 hover:bg-blue-50" title={`${c.name} (${c.code}) 유사도 ${Math.round(c.score * 100)}% · 단가 ${price(c.unitPrice)}`}>
                    {c.name}
                  </button>
                ))}
                <Button size="sm" variant="ghost" onClick={() => setSearch(r)}>
                  검색…
                </Button>
              </span>
            ),
          },
          {
            key: 'x',
            header: '',
            width: '70px',
            render: (r) => (
              <Button size="sm" variant="ghost" onClick={() => resolve.mutate([r.id, ''])}>
                제외
              </Button>
            ),
          },
        ]}
      />
      <ProductPickDialog
        row={search}
        onClose={() => setSearch(null)}
        onPick={(code) => search && resolve.mutate([search.id, code], { onSuccess: () => setSearch(null) })}
        onPickCode={(code) => search && resolveCode.mutate([search.id, code], { onSuccess: () => setSearch(null) })}
        pending={resolve.isPending || resolveCode.isPending}
      />
    </>
  )
}

function ProductPickDialog({
  row,
  onClose,
  onPick,
  onPickCode,
  pending,
}: {
  row: service.UnresolvedRow | null
  onClose: () => void
  onPick: (code: string) => void
  onPickCode: (code: string) => void
  pending: boolean
}) {
  const [q, setQ] = useState('')
  const dq = useDebounced(q)
  const res = useProductSearch(dq)
  const check = useCodeCheck(dq)
  useEffect(() => setQ(row?.name.split(/[\s(]/)[0] ?? ''), [row])
  if (!row) return null
  const info = looksLikeEDICode(dq) ? check.data : undefined
  return (
    <Dialog open onClose={onClose} title={`약품 지정 — ${row.name}`}>
      <Input autoFocus value={q} onChange={(e) => setQ(e.target.value)} placeholder="약품명 또는 보험코드(9자리)" />
      <p className="mt-1.5 text-xs text-gray-600">찾는 약이 없으면 보험코드(9자리)를 입력하세요. 목록에 없는 코드도 직접 지정할 수 있습니다.</p>
      {info?.code && (
        <div className="mt-3 space-y-2">
          {info.source ? (
            <Callout
              tone="info"
              action={
                <Button size="sm" variant="primary" loading={pending} onClick={() => onPickCode(info.code)}>
                  이 코드로 지정
                </Button>
              }
            >
              <span className="code">{info.code}</span> · {info.name} ({info.source === 'pharmacy' ? '약국 품목' : '급여목록'})
            </Callout>
          ) : (
            <Callout
              tone="warn"
              action={
                <Button size="sm" variant="primary" loading={pending} onClick={() => onPickCode(info.code)}>
                  그래도 이 코드로 지정
                </Button>
              }
            >
              <span className="code">{info.code}</span>는 약국 품목·급여목록에 없는 코드입니다. 숫자를 다시 확인하세요. 지정하면 파일의 약품명 '{row.name}'(으)로 재고에 추가되고, 같은 코드의 고시가 나오면 약가인하 대상에 포함됩니다. 나중에 올린 급여목록에 이 코드가 있으면 이름이 바로잡힙니다.
            </Callout>
          )}
        </div>
      )}
      <div className="mt-3 max-h-80 space-y-1 overflow-auto">
        {(res.data ?? []).map((c) => (
          <button key={c.code} onClick={() => onPick(c.code)} className="flex w-full items-center justify-between gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-gray-50">
            <span className="truncate">{c.name}</span>
            <span className="code shrink-0 text-gray-500">{c.code}</span>
          </button>
        ))}
        {dq && res.data?.length === 0 && !info?.code && (
          <p className="py-6 text-center text-sm text-gray-600">검색 결과가 없습니다. 보험코드(9자리)를 알면 입력해 직접 지정하세요.</p>
        )}
      </div>
    </Dialog>
  )
}

function BaselineTab() {
  const inv = useInventory('', true)
  const [date, setDate] = useState('')
  const [timing, setTiming] = useState('start')
  useEffect(() => {
    if (inv.data) {
      setDate(inv.data.baseline)
      setTiming(inv.data.baselineTiming || 'start')
    }
  }, [inv.data])
  const save = useAction(() => api.SetBaseline(date, timing), { success: '초기재고 기준일을 바꿨습니다 — 모든 추정재고가 다시 계산됩니다' })
  return (
    <Card title="초기재고 기준일" className="max-w-2xl">
      <p className="mb-4 text-sm text-gray-600">
        초기재고(기준일 시점 재고)를 센 날짜입니다. 이 날 이후의 사입·조제가 더해지고 빠집니다. 언제든 바꿀 수 있으며, 바꾸면 모든 추정재고가 즉시 다시 계산됩니다. 초기재고 파일에 없는 약품은 이 날 0개로 봅니다.
      </p>
      <div className="grid grid-cols-2 gap-3">
        <Field label="기준일">
          <Input type="date" value={date} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field label="시점" help="영업 전: 기준일 당일 거래부터 반영 / 영업 후: 다음날부터 반영">
          <Select value={timing} onChange={(e) => setTiming(e.target.value)}>
            <option value="start">영업 전 (당일 거래 반영)</option>
            <option value="end">영업 후 (다음날부터 반영)</option>
          </Select>
        </Field>
      </div>
      <Button className="mt-4" variant="primary" disabled={!date} loading={save.isPending} onClick={() => save.mutate([])}>
        저장
      </Button>
    </Card>
  )
}

/** 사입 기록의 단위 배율 지정 (데이터 점검에서 바로) */
function FactorDialog({ row, source, onClose }: { row: service.InventoryRow | null; source: string; onClose: () => void }) {
  const [v, setV] = useState('')
  const [note, setNote] = useState('')
  useEffect(() => {
    setV('')
    setNote('')
  }, [row])
  const save = useAction(() => api.SetUnitFactor(row!.code, Number(v) || 0, note, source), { success: '단위 배율을 저장하고 사입 수량을 다시 계산했습니다' })
  if (!row) return null
  return (
    <Dialog
      open
      onClose={onClose}
      title={`사입 단위 배율 — ${row.name}`}
      footer={
        <>
          <Button onClick={onClose}>취소</Button>
          <Button variant="primary" disabled={v !== '' && !(Number(v) >= 0)} loading={save.isPending} onClick={() => save.mutate([], { onSuccess: onClose })}>
            {v === '' || Number(v) === 0 ? '자동으로 되돌리기' : '저장'}
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-sm">
        <p className="text-gray-700">
          이 약의 고시 단위는 <b>{row.unit || '—'}</b> 입니다. 이팜 사입 기록 1단위가 고시 단위 몇 개인지 정하면 사입 수량을 다시 계산합니다. 단가가 없는 같은 단위의 초기재고·실사 행도 이 배율을 따릅니다.
        </p>
        {row.unitNote && <Callout tone={row.unitStatus === 'check' ? 'warn' : 'info'}>{row.unitNote}</Callout>}
        <Field label="사입 기록 1단위 = 고시 단위 몇 개" help="예: 100정 1통을 '1'로 적었다면 100 · 28정 1팩 약을 '정'으로 샀다면 0.0357(=1/28). 비우면 자동">
          <Input autoFocus value={v} onChange={(e) => setV(e.target.value)} inputMode="decimal" placeholder="비우면 자동" />
        </Field>
        <Field label="메모 (선택)">
          <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="예: 도매 명세서로 확인" />
        </Field>
      </div>
    </Dialog>
  )
}

/** 데이터 점검 항목을 '확인함(이상 없음)'으로 표시 */
function AckDialog({ target, onClose }: { target: { row: service.InventoryRow; flag: string } | null; onClose: () => void }) {
  const [note, setNote] = useState('')
  useEffect(() => setNote(''), [target])
  const save = useAction(() => api.AckHealth(target!.row.code, target!.flag, note, '데이터 점검'), { success: '확인함으로 표시했습니다 (값이 바뀌면 다시 나타납니다)' })
  if (!target) return null
  return (
    <Dialog
      open
      onClose={onClose}
      title={`확인함 — ${target.row.name}`}
      footer={
        <>
          <Button onClick={onClose}>취소</Button>
          <Button variant="primary" loading={save.isPending} onClick={() => save.mutate([], { onSuccess: onClose })}>
            확인함으로 표시
          </Button>
        </>
      }
    >
      <p className="mb-3 text-sm text-gray-700">
        '{FLAG_LABEL[target.flag] ?? target.flag}' 항목을 이상 없음으로 표시합니다. 재고나 단위 상태가 바뀌면 다시 점검 목록에 나타나고, 표시한 내역은 수정 기록에 남습니다.
      </p>
      <Field label="이유 (권장)">
        <Input autoFocus value={note} onChange={(e) => setNote(e.target.value)} placeholder="예: 비급여 약이라 사입은 다른 코드, 실제로 재고 많음" />
      </Field>
    </Dialog>
  )
}
