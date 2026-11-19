import { useQuery } from '@tanstack/react-query'
import { History, Plus, RotateCcw, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { api, obj, useAction, useCutDates, useRules, useSuppliers, type domain, type service } from '../../app/api'
import { dateDot, todayISO } from '../../app/format'
import { Badge, Button, Callout, Card, Dialog, Drawer, Field, Input, PageHeader, Select, Switch, cx } from '../../components/ui'

type RuleDraft = {
  rate: string // %
  windowMode: string
  lookback: string
  cutoffDay: number
  extendToAnchor: boolean
  anchor: string
  rounding: string
  netReturns: boolean
  settlementDay: number
  needsReview: boolean
  memo: string
}

type Row = { key: string; supplierId: number; name: string; kind: string; active: boolean; deleted: boolean; isDefault: boolean; ruleId: number; validFrom: string; draft: RuleDraft; lines: number; last: string }

const toDraft = (r: domain.CompRule | undefined, needsReview: boolean, memo: string): RuleDraft => ({
  rate: r ? String(r.rateBp / 100) : '30',
  windowMode: r?.windowMode ?? 'cycle',
  lookback: String(r?.lookback ?? 3),
  cutoffDay: r?.cutoffDay ?? 0,
  extendToAnchor: r?.extendToAnchor ?? false,
  anchor: r?.anchor ?? 'effective_date',
  rounding: r?.rounding ?? 'floor',
  netReturns: r?.netReturns ?? true,
  settlementDay: r?.settlementDay ?? 0,
  needsReview,
  memo,
})

const toRule = (d: RuleDraft) =>
  obj<domain.CompRule>({
    rateBp: Math.round(Number(d.rate) * 100),
    windowMode: d.windowMode,
    lookback: Number(d.lookback),
    cutoffDay: Number(d.cutoffDay),
    extendToAnchor: d.extendToAnchor,
    anchor: d.anchor,
    rounding: d.rounding,
    netReturns: d.netReturns,
    settlementDay: Number(d.settlementDay),
  })

const draftError = (d: RuleDraft) => {
  const rate = Number(d.rate)
  if (!(rate >= 0 && rate <= 100)) return '비율은 0~100'
  const n = Number(d.lookback)
  if (!(Number.isInteger(n) && n > 0)) return '기간은 1 이상 정수'
  return ''
}

const DAYS = [{ v: 0, l: '말일' }, ...Array.from({ length: 31 }, (_, i) => ({ v: i + 1, l: `${i + 1}일` }))]

export function SuppliersPage() {
  const [showDeleted, setShowDeleted] = useState(false)
  const sups = useSuppliers(showDeleted)
  const def = useRules(0)
  const dates = useCutDates()
  const nextEff = useMemo(() => (dates.data ?? []).filter((d) => d.daysLeft >= 0).sort((a, b) => a.daysLeft - b.daysLeft)[0]?.effectiveDate ?? todayISO(), [dates.data])
  const [eff, setEff] = useState('')
  const previewDate = eff || nextEff

  const base: Row[] = useMemo(() => {
    const out: Row[] = []
    const d0 = def.data?.[0]
    if (d0) out.push({ key: 'default', supplierId: 0, name: '기본 규칙', kind: 'wholesaler', active: true, deleted: false, isDefault: false, ruleId: d0.id, validFrom: d0.validFrom, draft: toDraft(d0.rule, d0.needsReview, d0.memo), lines: 0, last: '' })
    for (const s of sups.data ?? []) {
      const r = s.rule
      out.push({
        key: `s${s.id}`,
        supplierId: s.id,
        name: s.name,
        kind: s.kind,
        active: s.active,
        deleted: s.deleted,
        isDefault: !!r?.isDefault,
        ruleId: r && !r.isDefault ? r.id : 0,
        validFrom: r && !r.isDefault ? r.validFrom : '2000-01-01',
        draft: toDraft(r?.rule, r?.needsReview ?? true, r && !r.isDefault ? r.memo : ''),
        lines: s.purchaseLines,
        last: s.lastPurchase,
      })
    }
    return out
  }, [sups.data, def.data])

  const [edits, setEdits] = useState<Record<string, Partial<RuleDraft> & { kind?: string; active?: boolean }>>({})
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [bulk, setBulk] = useState(false)
  const [adding, setAdding] = useState(false)
  const [history, setHistory] = useState<Row | null>(null)
  useEffect(() => setEdits({}), [sups.dataUpdatedAt, def.dataUpdatedAt])

  const rows = base.map((r) => ({ ...r, kind: edits[r.key]?.kind ?? r.kind, active: edits[r.key]?.active ?? r.active, draft: { ...r.draft, ...edits[r.key] } }))
  const dirtyKeys = Object.keys(edits).filter((k) => Object.keys(edits[k]).length > 0)
  const errors = rows.filter((r) => dirtyKeys.includes(r.key) && draftError(r.draft))

  const edit = (key: string, patch: Partial<RuleDraft> & { kind?: string; active?: boolean }) => setEdits((e) => ({ ...e, [key]: { ...e[key], ...patch } }))

  const saveAll = useAction(
    async () => {
      const ruleInputs: service.RuleInput[] = []
      for (const r of rows.filter((x) => dirtyKeys.includes(x.key))) {
        const e = edits[r.key]
        const supplierChanged = e.kind !== undefined || e.active !== undefined
        if (supplierChanged && r.supplierId) {
          await api.SaveSupplier(obj<service.SupplierInput>({ id: r.supplierId, name: r.name, kind: r.kind, active: r.active, contact: '', memo: '' }))
        }
        const ruleChanged = Object.keys(e).some((k) => k !== 'kind' && k !== 'active')
        if (ruleChanged) {
          ruleInputs.push(obj<service.RuleInput>({ id: r.ruleId, supplierId: r.supplierId, validFrom: r.validFrom, rule: toRule(r.draft), needsReview: r.draft.needsReview, memo: r.draft.memo }))
        }
      }
      if (ruleInputs.length) await api.SaveRules(ruleInputs)
    },
    { success: '보상 기준을 저장했습니다' },
  )
  const del = useAction((id: number) => api.DeleteSupplier(id), { success: '삭제했습니다 (사용 이력이 있으면 숨김 처리)' })
  const restore = useAction((id: number) => api.RestoreSupplier(id), { success: '복원했습니다' })

  const toggleSel = (key: string) =>
    setSelected((s) => {
      const n = new Set(s)
      if (n.has(key)) n.delete(key)
      else n.add(key)
      return n
    })

  const cellCls = (key: string, field: string) => cx(edits[key] && field in edits[key] && 'bg-[var(--color-edit)] font-semibold')

  return (
    <div className="space-y-5 pb-20">
      <PageHeader
        title="도매처·보상기준"
        sub="도매마다 서류보상 기준(비율·기간·마감일·결제일)이 다릅니다. 표에서 바로 고치고 한 번에 저장하세요. 도매 전용 기준이 없으면 '기본 규칙'을 씁니다."
        actions={
          <>
            <Switch checked={showDeleted} onChange={setShowDeleted} label="삭제된 거래처 보기" />
            <Button icon={<Plus className="size-4" />} onClick={() => setAdding(true)}>
              거래처 추가
            </Button>
          </>
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <Field label="미리보기 시행일" className="w-48">
          <Input type="date" value={previewDate} onChange={(e) => setEff(e.target.value)} />
        </Field>
        <p className="text-[0.8125rem] text-gray-600">각 행의 '대상기간'은 이 시행일에 적용되는 실제 날짜입니다. 15일 마감 도매라면 방식을 '마감일 주기'로, 마감일을 15일로 두세요 (16일 ~ 다음달 15일).</p>
      </div>

      {rows.some((r) => r.draft.needsReview) && (
        <Callout tone="warn">'확인 필요'로 표시된 기준은 아직 도매에서 실제 기준을 확인하지 않은 기본값입니다. 확인 후 체크를 해제하세요.</Callout>
      )}

      {selected.size > 0 && (
        <div className="sticky top-0 z-20 flex items-center gap-3 rounded-xl border border-blue-100 bg-blue-50 px-4 py-2.5 text-sm">
          <b className="text-blue-700">{selected.size}개 선택됨</b>
          <Button size="sm" variant="primary" onClick={() => setBulk(true)}>
            선택 항목에 일괄 적용
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
            선택 해제
          </Button>
        </div>
      )}

      <Card pad={false}>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[1360px] text-sm">
            <thead className="border-b border-gray-200 bg-gray-50 text-[0.8125rem] whitespace-nowrap text-gray-700">
              <tr>
                <th className="w-10 px-3 py-2.5" />
                <th className="px-3 py-2.5 text-left font-semibold">도매처</th>
                <th className="px-2 py-2.5 text-left font-semibold">구분</th>
                <th className="px-2 py-2.5 text-right font-semibold">비율(%)</th>
                <th className="px-2 py-2.5 text-left font-semibold">기간 방식</th>
                <th className="px-2 py-2.5 text-right font-semibold">기간</th>
                <th className="px-2 py-2.5 text-left font-semibold">마감일</th>
                <th className="px-2 py-2.5 text-left font-semibold">기준일</th>
                <th className="px-2 py-2.5 text-left font-semibold">반올림</th>
                <th className="px-2 py-2.5 text-center font-semibold">반품차감</th>
                <th className="px-2 py-2.5 text-left font-semibold">결제일</th>
                <th className="px-2 py-2.5 text-center font-semibold">확인필요</th>
                <th className="px-2 py-2.5 text-left font-semibold">대상기간 ({dateDot(previewDate)} 시행)</th>
                <th className="px-2 py-2.5 text-left font-semibold">메모</th>
                <th className="w-20" />
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {rows.map((r) => {
                const d = r.draft
                const isDirty = dirtyKeys.includes(r.key)
                const other = r.kind === 'other'
                return (
                  <tr key={r.key} className={cx(isDirty && 'shadow-[inset_3px_0_0_var(--color-amber-600)]', r.key === 'default' && 'bg-gray-50/70', r.deleted && 'opacity-50')}>
                    <td className="px-3 py-2">
                      {r.key !== 'default' && !r.deleted && <input type="checkbox" checked={selected.has(r.key)} onChange={() => toggleSel(r.key)} className="size-4 accent-blue-600" />}
                    </td>
                    <td className="min-w-[210px] px-3 py-2">
                      <div className="font-semibold">{r.name}</div>
                      <div className="text-xs whitespace-nowrap text-gray-500">
                        {r.key === 'default' ? '도매 전용 기준이 없을 때 적용' : `사입 ${r.lines.toLocaleString()}건 · 최근 ${dateDot(r.last) || '-'}`}
                        {r.isDefault && <Badge tone="outline" className="ml-1.5">기본 규칙 사용 중</Badge>}
                      </div>
                    </td>
                    <td className="px-2 py-2">
                      {r.key !== 'default' && (
                        <select className={cx('h-8 rounded-md border border-gray-300 bg-gray-0 px-1.5', cellCls(r.key, 'kind'))} value={r.kind} onChange={(e) => edit(r.key, { kind: e.target.value })}>
                          <option value="wholesaler">도매</option>
                          <option value="other">기타(보상 없음)</option>
                        </select>
                      )}
                    </td>
                    <td className="px-2 py-2 text-right">
                      <input disabled={other} className={cx('num h-8 w-16 rounded-md border border-gray-300 bg-gray-0 px-2 text-right disabled:opacity-40', cellCls(r.key, 'rate'), draftError(d).includes('비율') && 'border-red-600')} value={d.rate} onChange={(e) => edit(r.key, { rate: e.target.value })} inputMode="decimal" />
                    </td>
                    <td className="px-2 py-2">
                      <select disabled={other} className={cx('h-8 rounded-md border border-gray-300 bg-gray-0 px-1.5 disabled:opacity-40', cellCls(r.key, 'windowMode'))} value={d.windowMode} onChange={(e) => edit(r.key, { windowMode: e.target.value })}>
                        <option value="cycle">마감일 주기(개월)</option>
                        <option value="rolling_month">직전 N개월</option>
                        <option value="rolling_day">직전 N일</option>
                      </select>
                    </td>
                    <td className="px-2 py-2 text-right">
                      <input disabled={other} className={cx('num h-8 w-14 rounded-md border border-gray-300 bg-gray-0 px-2 text-right disabled:opacity-40', cellCls(r.key, 'lookback'), draftError(d).includes('기간') && 'border-red-600')} value={d.lookback} onChange={(e) => edit(r.key, { lookback: e.target.value })} inputMode="numeric" />
                    </td>
                    <td className="px-2 py-2">
                      <select disabled={other || d.windowMode !== 'cycle'} className={cx('h-8 rounded-md border border-gray-300 bg-gray-0 px-1.5 disabled:opacity-40', cellCls(r.key, 'cutoffDay'))} value={d.cutoffDay} onChange={(e) => edit(r.key, { cutoffDay: Number(e.target.value) })}>
                        {DAYS.map((x) => (
                          <option key={x.v} value={x.v}>
                            {x.l}
                          </option>
                        ))}
                      </select>
                    </td>
                    <td className="px-2 py-2">
                      <select disabled={other} className={cx('h-8 rounded-md border border-gray-300 bg-gray-0 px-1.5 disabled:opacity-40', cellCls(r.key, 'anchor'))} value={d.anchor} onChange={(e) => edit(r.key, { anchor: e.target.value })}>
                        <option value="effective_date">시행일</option>
                        <option value="notice_date">고시일</option>
                      </select>
                    </td>
                    <td className="px-2 py-2">
                      <select disabled={other} className={cx('h-8 rounded-md border border-gray-300 bg-gray-0 px-1.5 disabled:opacity-40', cellCls(r.key, 'rounding'))} value={d.rounding} onChange={(e) => edit(r.key, { rounding: e.target.value })}>
                        <option value="floor">버림</option>
                        <option value="round">반올림</option>
                        <option value="ceil">올림</option>
                      </select>
                    </td>
                    <td className={cx('px-2 py-2 text-center', cellCls(r.key, 'netReturns'))}>
                      <input disabled={other} type="checkbox" className="size-4 accent-blue-600" checked={d.netReturns} onChange={(e) => edit(r.key, { netReturns: e.target.checked })} />
                    </td>
                    <td className="px-2 py-2">
                      <select disabled={other} className={cx('h-8 rounded-md border border-gray-300 bg-gray-0 px-1.5 disabled:opacity-40', cellCls(r.key, 'settlementDay'))} value={d.settlementDay} onChange={(e) => edit(r.key, { settlementDay: Number(e.target.value) })}>
                        {DAYS.map((x) => (
                          <option key={x.v} value={x.v}>
                            {x.l}
                          </option>
                        ))}
                      </select>
                    </td>
                    <td className={cx('px-2 py-2 text-center', cellCls(r.key, 'needsReview'))}>
                      <input type="checkbox" className="size-4 accent-amber-600" checked={d.needsReview} onChange={(e) => edit(r.key, { needsReview: e.target.checked })} />
                    </td>
                    <td className="px-2 py-2">{!other && <WindowPreview draft={d} eff={previewDate} />}</td>
                    <td className="px-2 py-2">
                      <input className={cx('h-8 w-40 rounded-md border border-gray-300 bg-gray-0 px-2', cellCls(r.key, 'memo'))} value={d.memo} onChange={(e) => edit(r.key, { memo: e.target.value })} placeholder="담당자·안내 내용" />
                    </td>
                    <td className="px-2 py-2 text-right whitespace-nowrap">
                      <button className="rounded p-1 text-gray-500 hover:bg-gray-100" title="기준 변경 이력·버전" onClick={() => setHistory(r)}>
                        <History className="size-4" />
                      </button>
                      {r.key !== 'default' &&
                        (r.deleted ? (
                          <button className="rounded p-1 text-gray-500 hover:bg-gray-100" title="복원" onClick={() => restore.mutate([r.supplierId])}>
                            <RotateCcw className="size-4" />
                          </button>
                        ) : (
                          <button
                            className="rounded p-1 text-gray-500 hover:bg-red-50 hover:text-red-600"
                            title="삭제"
                            onClick={() => confirm(`'${r.name}'을(를) 삭제할까요? 사입 이력이 있으면 숨김 처리되고 보상 기준만 지워집니다.`) && del.mutate([r.supplierId])}
                          >
                            <Trash2 className="size-4" />
                          </button>
                        ))}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </Card>

      {dirtyKeys.length > 0 && (
        <div className="fixed right-6 bottom-6 left-[calc(232px+24px)] z-30 flex items-center gap-3 rounded-xl border border-gray-200 bg-gray-0 px-5 py-3 shadow-[var(--shadow-float)] max-[1100px]:left-[80px]">
          <span className="text-sm">
            저장하지 않은 변경 <b className="num">{dirtyKeys.length}</b>건{errors.length > 0 && <span className="ml-2 font-semibold text-red-700">오류 {errors.length}건</span>}
          </span>
          <span className="flex-1" />
          <Button onClick={() => setEdits({})}>되돌리기</Button>
          <Button variant="primary" disabled={errors.length > 0} loading={saveAll.isPending} onClick={() => saveAll.mutate([])}>
            저장
          </Button>
        </div>
      )}

      {bulk && (
        <BulkDialog
          ids={rows.filter((r) => selected.has(r.key)).map((r) => r.supplierId)}
          names={rows.filter((r) => selected.has(r.key)).map((r) => r.name)}
          onClose={() => setBulk(false)}
          onDone={() => {
            setBulk(false)
            setSelected(new Set())
          }}
        />
      )}
      {adding && <AddDialog onClose={() => setAdding(false)} />}
      {history && <HistoryDrawer row={history} suppliers={(sups.data ?? []).filter((s) => s.id !== history.supplierId)} onClose={() => setHistory(null)} />}
    </div>
  )
}

function WindowPreview({ draft, eff }: { draft: RuleDraft; eff: string }) {
  const err = draftError(draft)
  const rule = toRule(draft)
  const q = useQuery({ queryKey: ['dpr', 'rule-preview', rule, eff], queryFn: () => api.PreviewRule(rule, eff), enabled: !err, staleTime: Infinity })
  if (err) return <span className="text-xs font-semibold text-red-700">{err}</span>
  if (!q.data) return null
  if (q.data.error) return <span className="text-xs text-red-700">{q.data.error}</span>
  return (
    <span className="num text-[0.8125rem] whitespace-nowrap text-gray-800" title={q.data.description}>
      {dateDot(q.data.windowFrom)} ~ {dateDot(q.data.windowTo)}
      <span className="block text-xs text-gray-500">결제 예정 {dateDot(q.data.settlementDate)}</span>
    </span>
  )
}

function RuleForm({ draft, onChange }: { draft: RuleDraft; onChange: (d: RuleDraft) => void }) {
  const set = (p: Partial<RuleDraft>) => onChange({ ...draft, ...p })
  return (
    <div className="grid grid-cols-3 gap-3">
      <Field label="서류보상 비율(%)">
        <Input value={draft.rate} onChange={(e) => set({ rate: e.target.value })} inputMode="decimal" />
      </Field>
      <Field label="기간 방식">
        <Select value={draft.windowMode} onChange={(e) => set({ windowMode: e.target.value })}>
          <option value="cycle">마감일 주기(개월)</option>
          <option value="rolling_month">직전 N개월</option>
          <option value="rolling_day">직전 N일</option>
        </Select>
      </Field>
      <Field label="기간 (개월/일)">
        <Input value={draft.lookback} onChange={(e) => set({ lookback: e.target.value })} inputMode="numeric" />
      </Field>
      <Field label="마감일 (마감일 주기)">
        <Select value={draft.cutoffDay} disabled={draft.windowMode !== 'cycle'} onChange={(e) => set({ cutoffDay: Number(e.target.value) })}>
          {DAYS.map((x) => (
            <option key={x.v} value={x.v}>
              {x.l}
            </option>
          ))}
        </Select>
      </Field>
      <Field label="기준일" help="고시일 = 복지부 게시일 (자동 수집한 고시). 고시 파일을 직접 올렸다면 올린 날로 잡힙니다.">
        <Select value={draft.anchor} onChange={(e) => set({ anchor: e.target.value })}>
          <option value="effective_date">시행일</option>
          <option value="notice_date">고시일</option>
        </Select>
      </Field>
      <Field label="반올림">
        <Select value={draft.rounding} onChange={(e) => set({ rounding: e.target.value })}>
          <option value="floor">버림</option>
          <option value="round">반올림</option>
          <option value="ceil">올림</option>
        </Select>
      </Field>
      <Field label="결제일">
        <Select value={draft.settlementDay} onChange={(e) => set({ settlementDay: Number(e.target.value) })}>
          {DAYS.map((x) => (
            <option key={x.v} value={x.v}>
              {x.l}
            </option>
          ))}
        </Select>
      </Field>
      <div className="col-span-2 flex flex-wrap items-end gap-4 pb-1.5">
        <Switch checked={draft.netReturns} onChange={(v) => set({ netReturns: v })} label="기간 내 반품 차감" />
        <Switch checked={draft.extendToAnchor} onChange={(v) => set({ extendToAnchor: v })} label="마감 후 ~ 기준일 전날 포함" />
        <Switch checked={draft.needsReview} onChange={(v) => set({ needsReview: v })} label="확인 필요" />
      </div>
      <Field label="메모" className="col-span-3">
        <Input value={draft.memo} onChange={(e) => set({ memo: e.target.value })} />
      </Field>
    </div>
  )
}

function BulkDialog({ ids, names, onClose, onDone }: { ids: number[]; names: string[]; onClose: () => void; onDone: () => void }) {
  const [draft, setDraft] = useState<RuleDraft>(toDraft(undefined, false, ''))
  const [validFrom, setValidFrom] = useState('2000-01-01')
  const apply = useAction(() => api.ApplyRuleToSuppliers(obj<service.RuleInput>({ id: 0, supplierId: 0, validFrom, rule: toRule(draft), needsReview: draft.needsReview, memo: draft.memo }), ids), {
    success: `${ids.length}개 거래처에 기준을 적용했습니다`,
  })
  return (
    <Dialog
      open
      onClose={onClose}
      width={720}
      title={`선택한 ${ids.length}개 거래처에 일괄 적용`}
      footer={
        <>
          <Button onClick={onClose}>취소</Button>
          <Button variant="primary" disabled={!!draftError(draft)} loading={apply.isPending} onClick={() => apply.mutate([], { onSuccess: onDone })}>
            적용
          </Button>
        </>
      }
    >
      <p className="mb-4 text-sm text-gray-600">{names.join(', ')}</p>
      <RuleForm draft={draft} onChange={setDraft} />
      <Field label="적용 시작일" help="이 날짜 이후 시행되는 약가인하부터 적용 (과거 처리 결과는 그대로)" className="mt-3 w-60">
        <Input type="date" value={validFrom} onChange={(e) => setValidFrom(e.target.value)} />
      </Field>
    </Dialog>
  )
}

function AddDialog({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState('')
  const [kind, setKind] = useState('wholesaler')
  const add = useAction(() => api.SaveSupplier(obj<service.SupplierInput>({ id: 0, name, kind, active: true, contact: '', memo: '' })), { success: '거래처를 추가했습니다' })
  return (
    <Dialog
      open
      onClose={onClose}
      title="거래처 추가"
      footer={
        <>
          <Button onClick={onClose}>취소</Button>
          <Button variant="primary" disabled={!name.trim()} loading={add.isPending} onClick={() => add.mutate([], { onSuccess: onClose })}>
            추가
          </Button>
        </>
      }
    >
      <div className="grid grid-cols-2 gap-3">
        <Field label="이름">
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="예: 가나약품" />
        </Field>
        <Field label="구분">
          <Select value={kind} onChange={(e) => setKind(e.target.value)}>
            <option value="wholesaler">도매</option>
            <option value="other">기타(보상 없음)</option>
          </Select>
        </Field>
      </div>
      <p className="mt-3 text-xs text-gray-600">사입현황을 가져오면 거래처는 자동으로 추가됩니다. 이름의 (주)·주식회사 등은 자동으로 정리됩니다.</p>
    </Dialog>
  )
}

function HistoryDrawer({ row, suppliers, onClose }: { row: Row; suppliers: service.SupplierView[]; onClose: () => void }) {
  const rules = useRules(row.supplierId)
  const [draft, setDraft] = useState<RuleDraft>(row.draft)
  const [validFrom, setValidFrom] = useState(todayISO())
  const [mergeInto, setMergeInto] = useState(0)
  const addVersion = useAction(() => api.SaveRules([obj<service.RuleInput>({ id: 0, supplierId: row.supplierId, validFrom, rule: toRule(draft), needsReview: draft.needsReview, memo: draft.memo })]), {
    success: '새 기준(버전)을 저장했습니다',
  })
  const delRule = useAction((id: number) => api.DeleteRule(id), { success: '삭제했습니다' })
  const merge = useAction(() => api.MergeSupplier(row.supplierId, mergeInto), { success: '거래처를 합쳤습니다' })
  return (
    <Drawer open onClose={onClose} title={`${row.name} — 기준 이력`} width={640}>
      <div className="space-y-5">
        <div>
          <h4 className="mb-2 text-sm font-semibold">적용 중인 기준 버전</h4>
          {(rules.data ?? []).length === 0 ? (
            <p className="text-sm text-gray-600">전용 기준이 없어 기본 규칙을 사용합니다.</p>
          ) : (
            <ul className="divide-y divide-gray-100 rounded-lg border border-gray-200">
              {(rules.data ?? []).map((r) => (
                <li key={r.id} className="flex items-start gap-3 px-3 py-2.5 text-sm">
                  <span className="num w-24 shrink-0 font-semibold">{r.validFrom === '2000-01-01' ? '처음부터' : `${dateDot(r.validFrom)}~`}</span>
                  <span className="min-w-0 flex-1">
                    {r.description}
                    {r.memo && <span className="block text-xs text-gray-600">{r.memo}</span>}
                  </span>
                  {r.needsReview && <Badge tone="amber">확인 필요</Badge>}
                  <button className="text-gray-400 hover:text-red-600" onClick={() => delRule.mutate([r.id])} title="이 버전 삭제">
                    <Trash2 className="size-4" />
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
        <div>
          <h4 className="mb-2 text-sm font-semibold">기준이 바뀌었을 때: 새 버전 추가</h4>
          <p className="mb-3 text-xs text-gray-600">적용 시작일 이후 시행되는 인하부터 새 기준을 쓰고, 이전 회차는 이전 기준으로 계산합니다.</p>
          <RuleForm draft={draft} onChange={setDraft} />
          <div className="mt-3 flex items-end gap-3">
            <Field label="적용 시작일" className="w-48">
              <Input type="date" value={validFrom} onChange={(e) => setValidFrom(e.target.value)} />
            </Field>
            <Button variant="primary" disabled={!!draftError(draft)} loading={addVersion.isPending} onClick={() => addVersion.mutate([])}>
              새 버전 저장
            </Button>
          </div>
        </div>
        {row.supplierId > 0 && (
          <div>
            <h4 className="mb-2 text-sm font-semibold">다른 거래처로 합치기</h4>
            <p className="mb-2 text-xs text-gray-600">같은 도매가 다른 이름으로 들어온 경우 사입 기록·근거·스캔을 한 거래처로 합칩니다.</p>
            <div className="flex gap-2">
              <Select value={mergeInto} onChange={(e) => setMergeInto(Number(e.target.value))}>
                <option value={0}>합칠 대상 선택</option>
                {suppliers.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
              <Button disabled={!mergeInto} loading={merge.isPending} onClick={() => confirm(`'${row.name}'을(를) 선택한 거래처로 합칠까요? 되돌릴 수 없습니다.`) && merge.mutate([], { onSuccess: onClose })}>
                합치기
              </Button>
            </div>
          </div>
        )}
      </div>
    </Drawer>
  )
}
