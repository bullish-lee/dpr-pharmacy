import { ArrowLeft, Barcode, CalendarRange, ClipboardCheck, Plus, ScanLine, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api, obj, useAction, useProduct, useSuppliers, type service } from '../../app/api'
import { basisLabel, dateDot, dateTime, detailLabel, pct, price, qty, todayISO, won, wonUnit } from '../../app/format'
import { Link, navigate, paths } from '../../app/router'
import { ChangeLogTable } from '../../components/ChangeLog'
import { DataTable } from '../../components/DataTable'
import { FileDrop, fileToBase64 } from '../../components/FileDrop'
import { Badge, Button, Callout, Card, Dialog, Empty, ErrorBox, Field, Input, PageHeader, Segmented, Select, Skeleton, Stat, cx } from '../../components/ui'
import { FlagBadges, MethodBadge, NeedsCheck, RecBadge, decisionSentence } from './shared'

const DAY = (n: number) => (n <= 0 || n >= 31 ? '말일' : `${n}일`)
function ruleShort(r: { rateBp: number; windowMode: string; lookback: number; cutoffDay: number; settlementDay: number; netReturns: boolean }) {
  const period =
    r.windowMode === 'rolling_day' ? `직전 ${r.lookback}일` : r.windowMode === 'rolling_month' ? `직전 ${r.lookback}개월` : r.cutoffDay > 0 && r.cutoffDay < 31 ? `${r.lookback}개월(${r.cutoffDay}일 마감)` : `${r.lookback}개월(달력 월)`
  return `${r.rateBp / 100}% · ${period}${r.netReturns ? ' · 반품 차감' : ''} · 결제 ${DAY(r.settlementDay)}`
}

type Tab = 'settle' | 'purchase' | 'lots' | 'scans' | 'anchors' | 'history' | 'unit' | 'changes'

export function ProductPage({ date, code }: { date: string; code: string }) {
  const { data, isLoading, error, refetch } = useProduct(date, code)
  const [tab, setTab] = useState<Tab>('settle')
  const [override, setOverride] = useState<service.PositionView | null>(null)
  const confirm = useAction((d: string, c: string) => api.ConfirmDecision(d, c), {
    success: (n) => (n > 0 ? `도매 ${n}곳의 처리 예정을 저장했습니다` : '저장할 보상 항목이 없습니다 (이미 완료된 건은 유지)'),
  })

  if (isLoading) return <Skeleton rows={12} />
  if (error || !data) return <ErrorBox error={error} onRetry={() => refetch()} />
  const r = data.row
  const d = data.decision
  const physical = (data.positions ?? []).filter((p) => p.method === 'physical')

  return (
    <div className="space-y-5">
      <PageHeader
        back={
          <Link to={paths.cut(date)} className="inline-flex items-center gap-1 text-[0.8125rem] font-semibold text-gray-600 hover:text-gray-900">
            <ArrowLeft className="size-3.5" /> {dateDot(date)} 시행 순위로
          </Link>
        }
        title={r.name}
        sub={
          <span className="flex flex-wrap items-center gap-2">
            <span className="code">{r.code}</span>·<span>{r.company}</span>
            {r.noticeNo && <span>· 고시 제{r.noticeNo}호</span>}
            <FlagBadges row={r} />
          </span>
        }
        actions={
          <>
            <Button icon={<ScanLine className="size-4" />} variant={r.recommendation === 'physical_review' ? 'primary' : 'secondary'} onClick={() => navigate(paths.scan(date, code))}>
              바코드 스캔
            </Button>
            <Button icon={<Barcode className="size-4" />} disabled={physical.length === 0} onClick={() => navigate(paths.register(date, code))} title={physical.length === 0 ? '실물보상이 유리한 도매가 없습니다' : undefined}>
              도매 앱 등록
            </Button>
            <Button icon={<ClipboardCheck className="size-4" />} loading={confirm.isPending} onClick={() => confirm.mutate([date, code])}>
              판정 확정
            </Button>
          </>
        }
      />

      {r.flexible && (
        <Callout tone="warn">
          약가유연계약 품목입니다. 고시에는 표시가만 있어 실제 상한금액(별도합의)은 요양기관업무포털 → 약가파일에서 확인하세요. {r.note}
        </Callout>
      )}

      <div className="grid grid-cols-[1fr_1fr_1fr_1.4fr] gap-3 max-[1180px]:grid-cols-3 max-[760px]:grid-cols-1 max-[1180px]:[&>*:last-child]:col-span-3 max-[760px]:[&>*:last-child]:col-span-1">
        <Card>
          <div className="text-[0.8125rem] font-semibold text-gray-600">상한금액 (고시 단위 {r.baseLabel || r.unit || r.noticeUnit || '1'})</div>
          <div className="num mt-1.5 text-xl font-semibold">
            {price(r.priceBefore)} → {price(r.priceAfter)}
            <span className="ml-1.5 text-sm font-medium text-gray-600">({pct(r.rate)} 인하)</span>
          </div>
          <div className="mt-1 text-[0.8125rem] text-gray-600">
            현재 매입가 {price(r.currentUnitPrice)}원 (
            {r.priceSource === 'purchase'
              ? '최근 사입'
              : r.priceSource === 'initial'
                ? '초기재고 단가'
                : r.priceSource === 'ceiling'
                  ? '이번 고시 변경 전 가격 — 최근 매입가가 더 높음 (이전 인하 전에 샀다면 그 차이는 이전 회차 손실)'
                  : '고시 변경전가'}
            )
            {r.unitStatus === 'check' && ' · 단위 확인 필요'}
          </div>
        </Card>
        <Card>
          <div className="text-[0.8125rem] font-semibold text-gray-600">재고</div>
          <div className="num mt-1.5 text-xl font-semibold">
            {qty(r.stock)}
            <span className="ml-1 text-sm font-medium text-gray-600">{r.unit}</span>
          </div>
          <div className="mt-1 text-[0.8125rem] text-gray-600">
            {r.countQty != null ? `실계수 ${dateDot(r.countDate)} · 추정 ${qty(r.estimate)}` : `추정재고 (${basisLabel[r.basis] ?? r.basis} 기준)`}
          </div>
        </Card>
        <Card>
          <div className="text-[0.8125rem] font-semibold text-gray-600">예상 손실</div>
          <div className="num mt-1.5 text-xl font-semibold">{wonUnit(r.grossLoss)}</div>
          <div className="mt-1 text-[0.8125rem] text-gray-600">
            1단위 {price(r.delta)}원 × {qty(r.stock)}
          </div>
        </Card>
        <Card>
          <div className="flex items-center justify-between">
            <span className="text-[0.8125rem] font-semibold text-gray-600">판정</span>
            <RecBadge rec={r.recommendation} settle={r.settleStatus} upside={d.physicalUpside} />
          </div>
          <p className="mt-2 text-sm leading-relaxed text-gray-800">{decisionSentence({ ...d, recommendation: r.recommendation, detail: r.detail })}</p>
          <div className="mt-2 flex gap-5">
            <Stat label="예상 보상" value={wonUnit(d.recovery)} />
            <Stat label="보상 후 순손실" value={d.netLoss < 0 ? `${wonUnit(-d.netLoss)} 이득` : wonUnit(d.netLoss)} />
          </div>
        </Card>
      </div>

      <Card
        title="도매별 보상 판정"
        actions={<span className="text-xs text-gray-600">{d.scanned ? '바코드로 확인한 보유량 기준' : '보유량은 최근 사입분부터 남아 있다고 가정한 추정치(FIFO)'}</span>}
        pad={false}
      >
        {(data.positions ?? []).length === 0 ? (
          <Empty title="관련 도매 기록이 없습니다">사입현황에 이 약품의 거래처 기록이 없습니다. 사입현황을 올리거나 바코드를 스캔하세요.</Empty>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[980px] text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-[0.8125rem] whitespace-nowrap text-gray-700">
                <tr>
                  <th className="px-4 py-2.5 text-left font-semibold">도매</th>
                  <th className="px-3 py-2.5 text-left font-semibold">보상 기준</th>
                  <th className="px-3 py-2.5 text-left font-semibold">대상기간</th>
                  <th className="px-3 py-2.5 text-right font-semibold">기간 사입</th>
                  <th className="px-3 py-2.5 text-right font-semibold">반품</th>
                  <th className="px-3 py-2.5 text-right font-semibold">서류보상</th>
                  <th className="px-3 py-2.5 text-right font-semibold">보유 ({d.scanned ? '스캔' : '추정'})</th>
                  <th className="px-3 py-2.5 text-left font-semibold">방식</th>
                  <th className="px-4 py-2.5 text-right font-semibold">예상 보상(원)</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {data.positions.map((p) => (
                  <tr key={p.supplierId} className={cx(p.method === 'physical' && 'bg-blue-50/40')}>
                    <td className="px-4 py-2.5 font-semibold whitespace-nowrap">{p.supplierName}</td>
                    <td className="px-3 py-2.5 text-[0.8125rem] text-gray-700">
                      {p.rule ? (
                        <span className="flex flex-col gap-1" title={p.ruleText}>
                          <span className="whitespace-nowrap">{ruleShort(p.rule)}</span>
                          <span className="flex gap-1">
                            {p.isDefaultRule && <Badge tone="outline">기본 규칙</Badge>}
                            {p.needsReview && <NeedsCheck title="실제 도매 기준을 아직 입력하지 않았습니다">기준 확인</NeedsCheck>}
                          </span>
                        </span>
                      ) : (
                        <span className="text-gray-500">보상 대상 아님 (기타 거래처)</span>
                      )}
                    </td>
                    <td className="px-3 py-2.5">
                      {p.windowFrom ? (
                        <button className="num inline-flex items-center gap-1 rounded px-1 text-[0.8125rem] whitespace-nowrap hover:bg-gray-100" onClick={() => setOverride(p)} title="이번 회차만 기간 직접 지정">
                          {dateDot(p.windowFrom)} ~ {dateDot(p.windowTo)}
                          {p.overridden && <Badge tone="amber">직접 지정</Badge>}
                          <CalendarRange className="size-3.5 text-gray-400" />
                        </button>
                      ) : (
                        <span className="text-gray-400">—</span>
                      )}
                    </td>
                    <td className="num px-3 py-2.5 text-right">{qty(p.purchased)}</td>
                    <td className="num px-3 py-2.5 text-right text-gray-600">{p.returned ? qty(p.returned) : ''}</td>
                    <td className="num px-3 py-2.5 text-right font-semibold">{qty(p.docQty)}</td>
                    <td className="num px-3 py-2.5 text-right">
                      {qty(p.heldUsed)}
                      {d.scanned && <span className="block text-xs text-gray-500">추정 {qty(p.estimatedHeld)}</span>}
                    </td>
                    <td className="px-3 py-2.5">
                      <MethodBadge method={p.method} />
                      {p.method === 'physical' && p.scannedUnits > 0 && <span className="ml-1 text-xs text-gray-600">등록 {p.registeredUnits}/{p.scannedUnits}</span>}
                    </td>
                    <td className="num px-4 py-2.5 text-right">{won(p.recovery)}</td>
                  </tr>
                ))}
                {data.unattributed > 0 && (
                  <tr>
                    <td className="px-4 py-2.5 text-gray-600" colSpan={6}>
                      사입 기록보다 오래된 재고 (초기재고 등) — 도매 보상 대상으로 확인되지 않음
                    </td>
                    <td className="num px-3 py-2.5 text-right text-gray-600">{qty(data.unattributed)}</td>
                    <td colSpan={2} />
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Segmented<Tab>
        value={tab}
        onChange={setTab}
        items={[
          { value: 'settle', label: '처리 결과', count: data.settlements?.length ?? 0 },
          { value: 'purchase', label: '사입 이력', count: data.purchases?.length ?? 0 },
          { value: 'lots', label: '제조번호 근거', count: data.lots?.length ?? 0 },
          { value: 'scans', label: '스캔 내역', count: data.scans?.length ?? 0 },
          { value: 'anchors', label: '재고 기준점', count: data.anchors?.length ?? 0 },
          { value: 'history', label: '고시 이력', count: data.history?.length ?? 0 },
          { value: 'unit', label: '단위 설정' },
          { value: 'changes', label: '수정 기록' },
        ]}
      />
      {tab === 'settle' && <SettlementsTab rows={data.settlements ?? []} />}
      {tab === 'purchase' && <PurchasesTab rows={data.purchases ?? []} />}
      {tab === 'lots' && <LotsTab code={code} rows={data.lots ?? []} />}
      {tab === 'scans' && <ScansTab rows={data.scans ?? []} />}
      {tab === 'anchors' && <AnchorsTab code={code} asOf={date < todayISO() ? todayISO() : data.asOf} rows={data.anchors ?? []} />}
      {tab === 'history' && <HistoryTab rows={data.history ?? []} />}
      {tab === 'unit' && <UnitTab code={code} row={r} packSize={data.packSize} />}
      {tab === 'changes' && <ChangeLogTable productCode={code} />}

      <WindowOverrideDialog date={date} pos={override} onClose={() => setOverride(null)} />
    </div>
  )
}

function WindowOverrideDialog({ date, pos, onClose }: { date: string; pos: service.PositionView | null; onClose: () => void }) {
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [memo, setMemo] = useState('')
  const save = useAction((f: string, t: string, m: string) => api.SetWindowOverride(date, pos!.supplierId, f, t, m), { success: '대상기간을 지정했습니다' })
  const clear = useAction(() => api.ClearWindowOverride(date, pos!.supplierId), { success: '규칙 기간으로 되돌렸습니다' })
  if (!pos) return null
  return (
    <Dialog
      open={!!pos}
      onClose={onClose}
      title={`${pos.supplierName} — 이번 회차 대상기간`}
      footer={
        <>
          {pos.overridden && (
            <Button variant="ghost" onClick={() => clear.mutate([], { onSuccess: onClose })}>
              규칙대로 되돌리기
            </Button>
          )}
          <Button onClick={onClose}>취소</Button>
          <Button variant="primary" loading={save.isPending} onClick={() => save.mutate([from || pos.windowFrom, to || pos.windowTo, memo], { onSuccess: onClose })}>
            저장
          </Button>
        </>
      }
    >
      <p className="mb-4 text-sm text-gray-600">도매에서 이번 인하에 한해 다른 기간을 안내한 경우에만 지정하세요. 기본 규칙: {pos.ruleText || '없음'}</p>
      <div className="grid grid-cols-2 gap-3">
        <Field label="시작일">
          <Input type="date" defaultValue={pos.windowFrom} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field label="종료일">
          <Input type="date" defaultValue={pos.windowTo} onChange={(e) => setTo(e.target.value)} />
        </Field>
        <Field label="메모" className="col-span-2">
          <Input placeholder="예: 11월 인하분 특별 기준 안내 (담당자 ○○)" onChange={(e) => setMemo(e.target.value)} />
        </Field>
      </div>
    </Dialog>
  )
}

function SettlementsTab({ rows }: { rows: service.SettlementView[] }) {
  const setStatus = useAction((id: number, st: string) => api.SetSettlementStatus(id, st))
  const del = useAction((id: number) => api.DeleteSettlement(id), { success: '삭제했습니다' })
  return (
    <DataTable
      rows={rows}
      rowKey={(r) => r.id}
      empty={<Empty title="아직 처리 결과가 없습니다">판정을 확인한 뒤 '판정 확정'을 누르면 도매별 처리 예정이 저장됩니다. 도매 처리가 끝나면 완료로 바꾸세요.</Empty>}
      columns={[
        { key: 'sup', header: '도매', width: 'minmax(160px,1fr)', render: (r) => <span className="font-semibold">{r.supplierName}</span> },
        { key: 'method', header: '방식', width: '120px', render: (r) => <MethodBadge method={r.method} /> },
        { key: 'qty', header: '수량', width: '100px', align: 'right', render: (r) => qty(r.qty) },
        { key: 'delta', header: '1단위 인하폭', width: '110px', align: 'right', render: (r) => price(r.unitDelta) },
        { key: 'amount', header: '보상액(원)', width: '120px', align: 'right', render: (r) => won(r.amount) },
        {
          key: 'status',
          header: '상태',
          width: '200px',
          render: (r) => (
            <Segmented
              size="sm"
              value={r.status}
              onChange={(v) => setStatus.mutate([r.id, v])}
              items={[
                { value: 'planned', label: '예정' },
                { value: 'done', label: '완료' },
              ]}
            />
          ),
        },
        { key: 'done', header: '완료 시각', width: '150px', render: (r) => <span className="num text-gray-600">{dateTime(r.doneAt)}</span> },
        {
          key: 'x',
          header: '',
          width: '48px',
          render: (r) => (
            <button className="text-gray-400 hover:text-red-600" onClick={() => del.mutate([r.id])} title="삭제">
              <Trash2 className="size-4" />
            </button>
          ),
        },
      ]}
    />
  )
}

function PurchasesTab({ rows }: { rows: service.PurchaseView[] }) {
  return (
    <DataTable
      rows={rows}
      rowKey={(r) => r.id}
      maxHeight="520px"
      rowClassName={(r) => (r.inWindow ? 'bg-blue-50/40' : undefined)}
      empty={<Empty title="사입 기록이 없습니다" />}
      columns={[
        { key: 'date', header: '일자', width: '110px', sort: (r) => r.date, render: (r) => <span className="num">{dateDot(r.date)}</span> },
        { key: 'sup', header: '거래처', width: 'minmax(140px,1fr)', sort: (r) => r.supplierName, render: (r) => r.supplierName },
        { key: 'kind', header: '구분', width: '90px', render: (r) => ({ purchase: '사입', return: '반품', disposal: '폐기', adjust: '재고조정', borrow_in: '차용입고', borrow_out: '차용출고' } as Record<string, string>)[r.kind] ?? r.kind },
        { key: 'qty', header: '수량(고시 단위)', width: '120px', align: 'right', sort: (r) => r.qty, render: (r) => qty(r.qty) },
        {
          key: 'raw',
          header: '이팜 원본',
          width: '130px',
          align: 'right',
          render: (r) =>
            r.factor !== 1 ? (
              <span className="text-gray-600" title={`이팜 원본 수량 × ${+r.factor.toFixed(4)} = 고시 단위`}>
                {qty(r.rawQty)} {r.rawUnit}
              </span>
            ) : (
              <span className="text-gray-400">{r.packSize ? `${qty(r.packSize)}입` : ''}</span>
            ),
        },
        { key: 'price', header: '단가', width: '90px', align: 'right', render: (r) => price(r.unitPrice) },
        { key: 'lot', header: '제조번호', width: '120px', render: (r) => <span className="code">{r.lot}</span> },
        { key: 'exp', header: '유효기간', width: '110px', render: (r) => <span className="num">{dateDot(r.expiry)}</span> },
        { key: 'win', header: '', width: '110px', render: (r) => (r.inWindow ? <Badge tone="blue">대상기간</Badge> : null) },
      ]}
      defaultSort={{ key: 'date', dir: 'desc' }}
    />
  )
}

function LotsTab({ code, rows }: { code: string; rows: service.LotView[] }) {
  const sups = useSuppliers(false)
  const [form, setForm] = useState({ supplierId: 0, lot: '', expiry: '', qty: '', date: '' })
  const add = useAction(() => api.AddLot(obj<service.LotInput>({ code, supplierId: Number(form.supplierId), lot: form.lot, expiry: form.expiry, qty: Number(form.qty) || 0, date: form.date, source: 'manual' })), {
    success: '제조번호 근거를 추가했습니다',
  })
  const del = useAction((id: number) => api.DeleteLot(id), { success: '삭제했습니다' })
  const upload = useAction(async (f: File) => {
    const res = await api.Import(obj<service.ImportRequest>({ kind: 'lot', fileName: f.name, dataBase64: await fileToBase64(f), productCode: code }))
    if (res.needsMapping) throw new Error('열을 인식하지 못했습니다. 데이터 가져오기 화면에서 열 매핑을 지정해 주세요.')
    return res
  }, { success: (r) => r.message })
  return (
    <div className="grid grid-cols-[minmax(0,2fr)_minmax(0,1fr)] gap-4 max-[1180px]:grid-cols-1">
      <DataTable
        rows={rows}
        rowKey={(r) => `${r.source}-${r.id}-${r.lot}-${r.date}-${r.supplierId}`}
        empty={<Empty title="제조번호 근거가 없습니다">이팜에서 제조번호·유효기간이 포함된 사입 내역을 엑셀로 내보내 올리면 바코드 → 도매 매칭이 자동으로 됩니다.</Empty>}
        columns={[
          { key: 'sup', header: '도매', width: 'minmax(130px,1fr)', render: (r) => r.supplierName },
          { key: 'lot', header: '제조번호', width: '130px', render: (r) => <span className="code font-semibold">{r.lot}</span> },
          { key: 'exp', header: '유효기간', width: '110px', render: (r) => <span className="num">{dateDot(r.expiry)}</span> },
          { key: 'date', header: '사입일', width: '110px', render: (r) => <span className="num">{dateDot(r.date)}</span> },
          { key: 'qty', header: '수량', width: '80px', align: 'right', render: (r) => (r.qty ? qty(r.qty) : '') },
          { key: 'src', header: '출처', width: '90px', render: (r) => <Badge tone="outline">{{ purchase: '사입현황', import: '엑셀', manual: '직접 입력', photo: '사진' }[r.source] ?? r.source}</Badge> },
          {
            key: 'x',
            header: '',
            width: '44px',
            render: (r) =>
              r.id ? (
                <button className="text-gray-400 hover:text-red-600" onClick={() => del.mutate([r.id])} title="삭제">
                  <Trash2 className="size-4" />
                </button>
              ) : null,
          },
        ]}
      />
      <div className="space-y-4">
        <Card title="직접 추가">
          <div className="grid grid-cols-2 gap-3">
            <Field label="도매" className="col-span-2">
              <Select value={form.supplierId} onChange={(e) => setForm({ ...form, supplierId: Number(e.target.value) })}>
                <option value={0}>선택</option>
                {(sups.data ?? []).map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="제조번호">
              <Input value={form.lot} onChange={(e) => setForm({ ...form, lot: e.target.value })} placeholder="31007-2" />
            </Field>
            <Field label="유효기간">
              <Input value={form.expiry} onChange={(e) => setForm({ ...form, expiry: e.target.value })} placeholder="20300615" />
            </Field>
            <Field label="사입일">
              <Input type="date" value={form.date} onChange={(e) => setForm({ ...form, date: e.target.value })} />
            </Field>
            <Field label="수량">
              <Input value={form.qty} onChange={(e) => setForm({ ...form, qty: e.target.value })} inputMode="decimal" />
            </Field>
          </div>
          <Button className="mt-4 w-full" variant="primary" icon={<Plus className="size-4" />} loading={add.isPending} disabled={!form.supplierId || !form.lot} onClick={() => add.mutate([])}>
            추가
          </Button>
        </Card>
        <FileDrop compact title="제조번호 포함 사입 엑셀 올리기" hint="이 약품 기준으로 저장됩니다 (약품코드 열이 없어도 됨)" onFiles={(f) => upload.mutate([f[0]])} />
      </div>
    </div>
  )
}

function ScansTab({ rows }: { rows: service.ScanView[] }) {
  return (
    <DataTable
      rows={rows}
      rowKey={(r) => r.id}
      maxHeight="520px"
      empty={<Empty title="스캔한 바코드가 없습니다">'바코드 스캔' 화면에서 보유 중인 포장의 바코드를 찍으세요.</Empty>}
      columns={[
        { key: 'lot', header: '제조번호', width: '120px', render: (r) => <span className="code">{r.lot}</span> },
        { key: 'exp', header: '유효기간', width: '110px', render: (r) => <span className="num">{dateDot(r.expiry)}</span> },
        { key: 'serial', header: '일련번호', width: 'minmax(150px,1fr)', render: (r) => <span className="code">{r.serial}</span> },
        { key: 'qty', header: '수량', width: '70px', align: 'right', render: (r) => qty(r.qty) },
        { key: 'sup', header: '도매', width: '160px', render: (r) => (r.supplierName ? r.supplierName : r.matchStatus === 'ambiguous' ? <NeedsCheck>도매 확인</NeedsCheck> : <Badge tone="outline">매칭 없음</Badge>) },
        { key: 'reg', header: '도매 앱 등록', width: '110px', render: (r) => (r.registered ? <Badge tone="outline">등록 완료</Badge> : '') },
        { key: 'time', header: '스캔 시각', width: '140px', render: (r) => <span className="num text-gray-600">{dateTime(r.createdAt)}</span> },
      ]}
    />
  )
}

function AnchorsTab({ code, asOf, rows }: { code: string; asOf: string; rows: service.AnchorView[] }) {
  const [qtyV, setQty] = useState('')
  const [date, setDate] = useState(asOf)
  const save = useAction(() => api.SaveCount(obj<service.CountInput>({ code, qty: Number(qtyV), asOf: date, timing: 'end', note: '상세 화면' })), { success: '실계수를 저장했습니다' })
  const del = useAction((id: number) => api.DeleteAnchor(id), { success: '삭제했습니다' })
  return (
    <div className="grid grid-cols-[minmax(0,2fr)_minmax(0,1fr)] gap-4 max-[1180px]:grid-cols-1">
      <DataTable
        rows={rows}
        rowKey={(r) => r.id}
        empty={<Empty title="재고 기준점이 없습니다" />}
        columns={[
          { key: 'date', header: '기준일', width: '110px', render: (r) => <span className="num">{dateDot(r.asOf)}</span> },
          { key: 'timing', header: '시점', width: '100px', render: (r) => (r.timing === 'end' ? '영업 후' : '영업 전') },
          { key: 'src', header: '종류', width: '100px', render: (r) => <Badge tone={r.source === 'count' ? 'blue' : 'gray'}>{r.source === 'count' ? '실계수' : '초기재고'}</Badge> },
          { key: 'qty', header: '수량', width: '100px', align: 'right', render: (r) => qty(r.qty) },
          { key: 'note', header: '메모', width: 'minmax(120px,1fr)', render: (r) => <span className="text-gray-600">{r.note}</span> },
          {
            key: 'x',
            header: '',
            width: '44px',
            render: (r) =>
              r.source === 'count' ? (
                <button className="text-gray-400 hover:text-red-600" onClick={() => del.mutate([r.id])} title="삭제">
                  <Trash2 className="size-4" />
                </button>
              ) : null,
          },
        ]}
      />
      <Card title="실계수 입력">
        <div className="grid grid-cols-2 gap-3">
          <Field label="센 수량">
            <Input value={qtyV} onChange={(e) => setQty(e.target.value)} inputMode="decimal" />
          </Field>
          <Field label="실사일 (영업 후 기준)">
            <Input type="date" value={date} onChange={(e) => setDate(e.target.value)} />
          </Field>
        </div>
        <Button className="mt-4 w-full" variant="primary" disabled={qtyV === ''} loading={save.isPending} onClick={() => save.mutate([])}>
          저장
        </Button>
        <p className="mt-3 text-xs text-gray-600">실계수가 있으면 추정재고 대신 '실계수 + 이후 사입 − 이후 조제'로 계산합니다.</p>
      </Card>
    </div>
  )
}

function HistoryTab({ rows }: { rows: service.ChangeRow[] }) {
  return (
    <DataTable
      rows={rows}
      rowKey={(r) => r.id}
      columns={[
        { key: 'eff', header: '시행일', width: '110px', render: (r) => <span className="num">{dateDot(r.effectiveDate)}</span> },
        { key: 'detail', header: '구분', width: '160px', render: (r) => detailLabel[r.detail] ?? r.detail },
        { key: 'price', header: '상한금액', width: '160px', align: 'right', render: (r) => `${price(r.priceBefore)} → ${price(r.priceAfter)}` },
        { key: 'grace', header: '유예', width: '110px', render: (r) => <span className="num">{dateDot(r.graceUntil)}</span> },
        { key: 'note', header: '비고', width: 'minmax(200px,2fr)', render: (r) => <span className="text-gray-600">{r.note}</span> },
      ]}
    />
  )
}

function UnitTab({ code, row, packSize }: { code: string; row: service.RankRow; packSize: number }) {
  const [factor, setFactor] = useState(String(row.unitStatus === 'manual' ? row.unitFactor : ''))
  const [pack, setPack] = useState(packSize ? String(packSize) : '')
  const saveFactor = useAction(() => api.SetUnitFactor(code, Number(factor) || 0, '', '품목 판정'), { success: '사입 환산 배율을 저장했습니다' })
  const savePack = useAction(() => api.SetPackSize(code, Number(pack) || 0, '', '품목 판정'), { success: '바코드 수량을 저장했습니다' })
  const statusText: Record<string, string> = {
    ok: '자동 — 사입 단위가 고시 단위와 같음',
    auto_unit: '자동 환산 — 고시 규격으로 판단',
    auto_pack: '자동 보정 — 단가 비율로 포장 수량 보정',
    check: '확인 필요 — 단가 비율로 설명되지 않음',
    no_reference: '판단 불가 — 비교할 고시 단위·가격이 없음 (급여목록표를 올리면 다시 판단)',
    manual: '직접 지정',
  }
  return (
    <div className="grid grid-cols-2 gap-4 max-[1180px]:grid-cols-1">
      <Card title="고시 단위">
        <p className="mb-2 text-sm text-gray-700">
          이 약의 재고·손실·서류보상 수량은 모두 <b>{row.baseLabel || row.unit || '고시 단위'}</b> 기준입니다 (이팜 조제 총수량과 같은 단위).
        </p>
        <p className="mb-3 text-sm text-gray-600">
          사입 기록 환산: {statusText[row.unitStatus] ?? '—'}
          {row.unitNote && <span className="mt-1 block text-[0.8125rem] text-gray-500">{row.unitNote}</span>}
        </p>
        <UnitOptionsList code={code} />
        <Field label="사입 기록 1단위 = 고시 단위 몇 개 (비우면 자동)">
          <div className="flex gap-2">
            <Input value={factor} onChange={(e) => setFactor(e.target.value)} placeholder="예: 100 (100정 1통을 1로 기록한 경우)" inputMode="decimal" />
            <Button variant="primary" loading={saveFactor.isPending} onClick={() => saveFactor.mutate([])}>
              저장
            </Button>
          </div>
        </Field>
      </Card>
      <Card title="바코드 1개 = 고시 단위 몇 개">
        <p className="mb-3 text-sm text-gray-600">
          스캔한 포장 1개를 몇 {row.unit || '개'}로 셀지입니다. 비우면 표준코드 제품총수량을 고시 단위로 환산하고(예: 28정 PTP → 1팩), 없으면 사입 포장으로 정합니다.
        </p>
        <div className="flex gap-2">
          <Input value={pack} onChange={(e) => setPack(e.target.value)} placeholder="비우면 자동" inputMode="decimal" />
          <Button variant="primary" loading={savePack.isPending} onClick={() => savePack.mutate([])}>
            저장
          </Button>
        </div>
      </Card>
    </div>
  )
}

function UnitOptionsList({ code }: { code: string }) {
  const [opts, setOpts] = useState<service.UnitOption[]>([])
  useEffect(() => {
    void api.UnitOptions(code).then((o) => setOpts(o ?? []))
  }, [code])
  if (opts.length <= 1) return null
  return (
    <div className="mb-3 rounded-lg bg-gray-100 px-3 py-2 text-[0.8125rem] text-gray-700">
      <div className="mb-1 font-semibold">실계수는 이 단위로도 넣을 수 있어요</div>
      {opts
        .filter((o) => o.key !== 'base')
        .map((o) => (
          <div key={o.key}>
            · {o.label}: {o.hint}
          </div>
        ))}
    </div>
  )
}
