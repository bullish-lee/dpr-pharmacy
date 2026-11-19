import { Download } from 'lucide-react'
import { useState } from 'react'
import { api, obj, useAction, useReport, useSuppliers, type service } from '../../app/api'
import { dateDot, dateTime, price, qty, won } from '../../app/format'
import { navigate, paths, useRoute } from '../../app/router'
import { DataTable } from '../../components/DataTable'
import { Button, Empty, ErrorBox, Field, Input, Kpi, PageHeader, Segmented, Select, Skeleton } from '../../components/ui'
import { MethodBadge } from '../cuts/shared'

type View = 'date' | 'supplier' | 'product' | 'rows'

export function ReportsPage() {
  const route = useRoute()
  const [f, setF] = useState({ from: '', to: '', supplierId: 0, status: '', effectiveDate: route.query.get('date') ?? '' })
  const [view, setView] = useState<View>('rows')
  const rep = useReport(f)
  const sups = useSuppliers(true)
  const setStatus = useAction((id: number, st: string) => api.SetSettlementStatus(id, st))
  const exp = useAction(() => api.ExportReportCSV(obj<service.SettlementFilter>(f)), { success: (p) => (p ? `저장했습니다: ${p}` : '') })

  return (
    <div className="space-y-5">
      <PageHeader
        title="처리 내역"
        sub="어느 약을 어느 도매에서 몇 개, 얼마를 보상받았는지 모아 봅니다."
        actions={
          <Button icon={<Download className="size-4" />} loading={exp.isPending} onClick={() => exp.mutate([])}>
            엑셀(CSV)로 저장
          </Button>
        }
      />
      <div className="flex flex-wrap items-end gap-3">
        <Field label="시행일" className="w-44">
          <Input type="date" value={f.effectiveDate} onChange={(e) => setF({ ...f, effectiveDate: e.target.value })} />
        </Field>
        <Field label="기간 시작" className="w-44">
          <Input type="date" value={f.from} onChange={(e) => setF({ ...f, from: e.target.value })} />
        </Field>
        <Field label="기간 끝" className="w-44">
          <Input type="date" value={f.to} onChange={(e) => setF({ ...f, to: e.target.value })} />
        </Field>
        <Field label="도매" className="w-52">
          <Select value={f.supplierId} onChange={(e) => setF({ ...f, supplierId: Number(e.target.value) })}>
            <option value={0}>전체</option>
            {(sups.data ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="상태" className="w-36">
          <Select value={f.status} onChange={(e) => setF({ ...f, status: e.target.value })}>
            <option value="">전체</option>
            <option value="planned">예정</option>
            <option value="done">완료</option>
          </Select>
        </Field>
        <Button variant="ghost" onClick={() => setF({ from: '', to: '', supplierId: 0, status: '', effectiveDate: '' })}>
          초기화
        </Button>
      </div>

      {rep.isLoading ? (
        <Skeleton rows={8} />
      ) : rep.error || !rep.data ? (
        <ErrorBox error={rep.error} onRetry={() => rep.refetch()} />
      ) : (
        <>
          <div className="grid grid-cols-4 gap-3 max-[1180px]:grid-cols-2">
            <Kpi label="보상액 합계" value={won(rep.data.total.amount)} unit="원" sub={`${rep.data.total.count}건`} />
            <Kpi label="서류보상" value={won(rep.data.total.document)} unit="원" />
            <Kpi label="실물보상" value={won(rep.data.total.physical)} unit="원" />
            <Kpi label="처리 상태" value={`${rep.data.total.done} / ${rep.data.total.count}`} sub={`완료 ${rep.data.total.done} · 예정 ${rep.data.total.planned}`} />
          </div>
          <Segmented<View>
            value={view}
            onChange={setView}
            items={[
              { value: 'rows', label: '상세 내역', count: rep.data.rows?.length ?? 0 },
              { value: 'date', label: '시행일별' },
              { value: 'supplier', label: '도매별' },
              { value: 'product', label: '약품별' },
            ]}
          />
          {view === 'rows' ? (
            <DataTable
              rows={rep.data.rows ?? []}
              rowKey={(r) => r.id}
              maxHeight="calc(100vh - 420px)"
              empty={<Empty title="처리 내역이 없습니다">약가인하 품목 화면에서 '판정 확정'을 누르면 도매별 처리 예정이 저장됩니다.</Empty>}
              onRowClick={(r) => navigate(paths.product(r.effectiveDate, r.code))}
              columns={[
                { key: 'eff', header: '시행일', width: '110px', sort: (r) => r.effectiveDate, render: (r) => <span className="num">{dateDot(r.effectiveDate)}</span> },
                { key: 'name', header: '약품', width: 'minmax(220px,2fr)', sort: (r) => r.name, render: (r) => <span className="truncate font-medium">{r.name}</span> },
                { key: 'sup', header: '도매', width: '150px', sort: (r) => r.supplierName, render: (r) => r.supplierName },
                { key: 'method', header: '방식', width: '120px', render: (r) => <MethodBadge method={r.method} /> },
                { key: 'qty', header: '수량', width: '90px', align: 'right', sort: (r) => r.qty, render: (r) => qty(r.qty) },
                { key: 'delta', header: '1단위 인하폭', width: '110px', align: 'right', render: (r) => price(r.unitDelta) },
                { key: 'amount', header: '보상액(원)', width: '120px', align: 'right', sort: (r) => r.amount, render: (r) => won(r.amount) },
                {
                  key: 'st',
                  header: '상태',
                  width: '150px',
                  render: (r) => (
                    <span onClick={(e) => e.stopPropagation()}>
                      <Segmented
                        size="sm"
                        value={r.status}
                        onChange={(v) => setStatus.mutate([r.id, v])}
                        items={[
                          { value: 'planned', label: '예정' },
                          { value: 'done', label: '완료' },
                        ]}
                      />
                    </span>
                  ),
                },
                { key: 'done', header: '완료 시각', width: '140px', render: (r) => <span className="num text-gray-600">{dateTime(r.doneAt)}</span> },
              ]}
              defaultSort={{ key: 'eff', dir: 'desc' }}
            />
          ) : (
            <DataTable
              rows={view === 'date' ? rep.data.byDate ?? [] : view === 'supplier' ? rep.data.bySupplier ?? [] : rep.data.byProduct ?? []}
              rowKey={(r) => r.key}
              columns={[
                { key: 'label', header: view === 'date' ? '시행일' : view === 'supplier' ? '도매' : '약품', width: 'minmax(220px,2fr)', sort: (r) => r.label, render: (r) => <span className="font-medium">{r.label}</span> },
                { key: 'count', header: '건수', width: '80px', align: 'right', sort: (r) => r.count, render: (r) => r.count },
                { key: 'qty', header: '수량', width: '100px', align: 'right', sort: (r) => r.qty, render: (r) => qty(r.qty) },
                { key: 'doc', header: '서류보상(원)', width: '130px', align: 'right', sort: (r) => r.document, render: (r) => won(r.document) },
                { key: 'phy', header: '실물보상(원)', width: '130px', align: 'right', sort: (r) => r.physical, render: (r) => won(r.physical) },
                { key: 'amount', header: '합계(원)', width: '130px', align: 'right', sort: (r) => r.amount, render: (r) => <b>{won(r.amount)}</b> },
                { key: 'st', header: '완료 / 예정', width: '120px', align: 'right', render: (r) => `${r.done} / ${r.planned}` },
              ]}
              defaultSort={{ key: 'amount', dir: 'desc' }}
            />
          )}
        </>
      )}
    </div>
  )
}
