import { ArrowLeft, ArrowRight, Check, ChevronLeft, Smartphone } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { api, errorText, keys, obj, queryClient, useProduct, useScans, type service } from '../../app/api'
import { dateDot } from '../../app/format'
import { Link, navigate, paths } from '../../app/router'
import { Badge, Button, Callout, Card, Empty, ErrorBox, Kbd, Segmented, Skeleton, cx } from '../../components/ui'
import { gs1DataMatrixSVG } from './barcode'
import { useWedgeScanner } from './scanner'
import { sounds } from './sound'

export function RegisterPage({ date, code }: { date: string; code: string }) {
  const product = useProduct(date, code)
  const scans = useScans(date, code)
  const positions = product.data?.positions ?? []
  const physical = positions.filter((p) => p.method === 'physical')
  const choices = physical.length ? physical : positions.filter((p) => p.scannedUnits > 0)
  const [sid, setSid] = useState<number>(0)
  const supplierId = sid || choices[0]?.supplierId || 0
  const units = useMemo(
    () => (scans.data ?? []).filter((u) => u.supplierId === supplierId).sort((a, b) => Number(a.registered) - Number(b.registered) || a.id - b.id),
    [scans.data, supplierId],
  )
  const [idx, setIdx] = useState(0)
  useEffect(() => setIdx(0), [supplierId])
  const cur = units[Math.min(idx, Math.max(0, units.length - 1))]
  const done = units.filter((u) => u.registered).length
  const supplierName = choices.find((c) => c.supplierId === supplierId)?.supplierName ?? ''

  const mark = async (u: service.ScanView, v: boolean) => {
    try {
      await api.SetScanRegistered(u.id, v)
      await queryClient.invalidateQueries({ queryKey: keys.scans(date, code) })
    } catch (e) {
      toast.error(errorText(e))
    }
  }
  const next = async () => {
    if (!cur) return
    if (!cur.registered) await mark(cur, true)
    sounds.ok()
    setIdx((i) => Math.min(i + 1, units.length - 1))
  }

  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement)?.closest?.('input,textarea,select')) return
      if (e.key === ' ' || e.key === 'Enter') {
        e.preventDefault()
        void next()
      } else if (e.key === 'Backspace' || e.key === 'ArrowLeft') {
        e.preventDefault()
        setIdx((i) => Math.max(0, i - 1))
      } else if (e.key === 'ArrowRight') {
        e.preventDefault()
        setIdx((i) => Math.min(units.length - 1, i + 1))
      }
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  })

  // 화면 앞에서 실물 스캐너로 찍으면(버스트) 다음으로 넘기지 않고, 표시 중인 코드와 같은지 확인만 한다
  useWedgeScanner((raw) => {
    if (cur && raw.includes(cur.serial)) toast.success('표시된 바코드와 일치합니다 (스캐너 확인)')
    else toast.warning('표시된 바코드와 다른 코드가 스캔되었습니다')
  })

  const finish = async () => {
    try {
      await api.ConfirmDecision(date, code)
      const sts = await api.ListSettlements(obj<service.SettlementFilter>({ effectiveDate: date, code }))
      const st = sts.find((s) => s.supplierId === supplierId)
      if (st) await api.SetSettlementStatus(st.id, 'done')
      queryClient.invalidateQueries({ queryKey: keys.all })
      toast.success(`${supplierName} 실물보상 처리 완료로 기록했습니다`)
    } catch (e) {
      toast.error(errorText(e))
    }
  }

  if (product.isLoading || scans.isLoading) return <Skeleton rows={10} />
  if (product.error) return <ErrorBox error={product.error} />

  return (
    <div className="space-y-4 text-[1.0625rem]">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <Link to={paths.scan(date, code)} className="inline-flex items-center gap-1 text-[0.8125rem] font-semibold text-gray-600 hover:text-gray-900">
            <ArrowLeft className="size-3.5" /> 스캔 화면으로
          </Link>
          <h1 className="mt-1 text-2xl font-bold tracking-[-0.01em]">도매 앱 등록 · {product.data?.row.name}</h1>
        </div>
        {choices.length > 1 && (
          <Segmented
            value={String(supplierId)}
            onChange={(v) => setSid(Number(v))}
            items={choices.map((c) => ({ value: String(c.supplierId), label: c.supplierName, count: c.scannedUnits }))}
          />
        )}
      </div>

      {choices.length === 0 ? (
        <Card>
          <Empty title="등록할 실물보상 도매가 없습니다" action={<Button onClick={() => navigate(paths.scan(date, code))}>바코드 스캔하기</Button>}>
            바코드를 스캔해 도매가 확인되고, 보유량이 서류보상보다 많은 도매가 있을 때 여기서 등록합니다.
          </Empty>
        </Card>
      ) : (
        <>
          <Callout tone="info">
            <span className="flex items-center gap-2 font-semibold">
              <Smartphone className="size-4" /> 이제 휴대폰에서 <b className="text-blue-700">{supplierName}</b> 앱의 실물보상(반품) 스캔 화면을 여세요.
            </span>
            <span className="text-[0.8125rem]">화면의 바코드를 한 개씩 찍고, 앱에 등록되면 <Kbd>Space</Kbd> 또는 <Kbd>Enter</Kbd> 를 눌러 다음으로 넘어가세요.</span>
          </Callout>

          {units.length === 0 ? (
            <Card>
              <Empty title={`${supplierName}로 확인된 스캔이 없습니다`} />
            </Card>
          ) : (
            <div className="grid grid-cols-[minmax(0,1fr)_320px] gap-4 max-[1180px]:grid-cols-1">
              <Card>
                <div className="flex items-center justify-between">
                  <span className="num text-xl font-bold">
                    {Math.min(idx + 1, units.length)} / {units.length}
                  </span>
                  <span className="text-sm text-gray-600">
                    등록 완료 <b className="num text-gray-900">{done}</b>
                  </span>
                </div>
                <div className="mt-2 flex gap-0.5">
                  {units.map((u, i) => (
                    <button
                      key={u.id}
                      title={`${i + 1}번 ${u.registered ? '완료' : '대기'}`}
                      onClick={() => setIdx(i)}
                      className={cx('h-2 flex-1 rounded-sm', u.registered ? 'bg-green-600' : i === idx ? 'bg-blue-600' : 'bg-gray-200')}
                    />
                  ))}
                </div>
                {cur && <BigCode unit={cur} />}
                <div className="mt-4 flex items-center justify-center gap-3">
                  <Button size="lg" icon={<ChevronLeft className="size-5" />} onClick={() => setIdx((i) => Math.max(0, i - 1))}>
                    이전
                  </Button>
                  <Button size="lg" variant="primary" icon={<Check className="size-5" />} onClick={() => void next()}>
                    {cur?.registered ? '다음' : '스캔완료 → 다음'}
                  </Button>
                  {cur?.registered && (
                    <Button size="lg" variant="ghost" onClick={() => mark(cur, false)}>
                      등록 취소
                    </Button>
                  )}
                </div>
              </Card>
              <Card title="포장 목록" pad={false}>
                <div className="max-h-[560px] divide-y divide-gray-100 overflow-auto">
                  {units.map((u, i) => (
                    <button key={u.id} onClick={() => setIdx(i)} className={cx('flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm', i === idx ? 'bg-blue-50' : 'hover:bg-gray-50')}>
                      <span className="num w-6 text-right text-gray-500">{i + 1}</span>
                      <span className="code min-w-0 flex-1 truncate">…{u.serial.slice(-8)}</span>
                      {u.registered ? <Badge tone="outline" icon={<Check className="size-3.5" />}>완료</Badge> : <Badge tone="gray">대기</Badge>}
                    </button>
                  ))}
                </div>
                <div className="border-t border-gray-100 p-4">
                  <Button className="w-full" variant={done === units.length ? 'primary' : 'secondary'} onClick={finish} icon={<ArrowRight className="size-4" />}>
                    {supplierName} 처리 완료로 기록
                  </Button>
                  <p className="mt-2 text-xs text-gray-600">판정을 확정하고 이 도매의 처리 결과를 '완료'로 저장합니다.</p>
                </div>
              </Card>
            </div>
          )}
        </>
      )}
    </div>
  )
}

function BigCode({ unit }: { unit: service.ScanView }) {
  const svg = useMemo(() => {
    try {
      return { ok: true, html: gs1DataMatrixSVG(unit.human, 10) }
    } catch (e) {
      return { ok: false, html: errorText(e) }
    }
  }, [unit.human])
  return (
    <div className="mt-5 flex flex-col items-center">
      <div className="rounded-2xl border border-gray-200 bg-[#ffffff] p-4">
        {svg.ok ? (
          <div className="size-[260px] [&>svg]:h-full [&>svg]:w-full" dangerouslySetInnerHTML={{ __html: svg.html }} />
        ) : (
          <div className="flex size-[260px] items-center justify-center p-4 text-center text-sm text-red-700">바코드를 만들 수 없습니다: {svg.html}</div>
        )}
      </div>
      <div className="code mt-3 text-center text-[0.9375rem] leading-relaxed text-gray-800">
        <div>(01){unit.gtin}</div>
        <div>
          (17){unit.expiry ? unit.expiry.slice(2).replaceAll('-', '') : ''} · 유효 {dateDot(unit.expiry)} · (10){unit.lot}
        </div>
        <div className="text-lg font-semibold">(21){unit.serial}</div>
      </div>
      {unit.registered && <Badge tone="green" className="mt-2" icon={<Check className="size-3.5" />}>도매 앱 등록 완료</Badge>}
    </div>
  )
}
