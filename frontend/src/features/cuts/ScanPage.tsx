import { ArrowLeft, Barcode, Camera, CheckCircle2, CircleAlert, Copy, Keyboard, ScanLine, Trash2, Volume2, VolumeX, XCircle } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import { api, errorText, keys, obj, queryClient, useProduct, useProductSearch, useScans, useSuppliers, type service } from '../../app/api'
import { dateDot, dateTime, qty } from '../../app/format'
import { Link, navigate, paths } from '../../app/router'
import { Badge, Button, Card, Dialog, Empty, ErrorBox, Input, Kbd, Skeleton, cx } from '../../components/ui'
import { decodeImage } from './barcode'
import { normalizeTyped, useWedgeScanner } from './scanner'
import { sounds } from './sound'

type Last = { at: number; res?: service.ScanResult; error?: string; raw: string }

function readSound() {
  try {
    return localStorage.getItem('dpr.sound') !== 'off'
  } catch {
    return true
  }
}

export function ScanPage({ date, code }: { date: string; code: string }) {
  const product = useProduct(date, code)
  const scans = useScans(date, code)
  const [last, setLast] = useState<Last | null>(null)
  const [pending, setPending] = useState(0)
  const [sound, setSound] = useState(readSound)
  const [manual, setManual] = useState(false)
  const [unknown, setUnknown] = useState<{ raw: string; res: service.ScanResult } | null>(null)
  const [photoBusy, setPhotoBusy] = useState(false)
  const queue = useRef(Promise.resolve())
  const fileInput = useRef<HTMLInputElement>(null)

  const play = useCallback((k: keyof typeof sounds) => sound && sounds[k](), [sound])

  const submit = useCallback(
    (raw: string) => {
      setPending((p) => p + 1)
      queue.current = queue.current.then(async () => {
        try {
          const res = await api.RecordScan(obj<service.ScanInput>({ effectiveDate: date, productCode: code, raw, qty: 0 }))
          setLast({ at: Date.now(), res, raw })
          if (res.status === 'added') play(res.unit?.matchStatus === 'unique' ? 'ok' : 'attention')
          else if (res.status === 'duplicate') play('duplicate')
          else play('error')
          if (res.status === 'unknown_product') setUnknown({ raw, res })
          queryClient.invalidateQueries({ queryKey: keys.all })
        } catch (e) {
          setLast({ at: Date.now(), error: errorText(e), raw })
          play('error')
        } finally {
          setPending((p) => p - 1)
        }
      })
    },
    [date, code, play],
  )

  useWedgeScanner(submit, { enabled: !manual && !unknown })

  // Ctrl+Z: 마지막 스캔 되돌리기
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'z' && !(e.target as HTMLElement)?.closest?.('input,textarea')) {
        const latest = scans.data?.[0]
        if (latest) {
          e.preventDefault()
          void api.DeleteScan(latest.id).then(() => {
            toast.info('마지막 스캔을 되돌렸습니다')
            queryClient.invalidateQueries({ queryKey: keys.all })
          })
        }
      }
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [scans.data])

  const onPhotos = async (files: File[]) => {
    setPhotoBusy(true)
    let found = 0
    let failed = 0
    try {
      for (const f of files) {
        try {
          const codes = await decodeImage(f)
          if (codes.length === 0) failed++
          for (const c of codes) {
            found++
            submit(c.text)
          }
        } catch {
          failed++
        }
      }
      toast[found ? 'success' : 'warning'](`사진 ${files.length}장: 바코드 ${found}개 인식${failed ? ` · ${failed}장 인식 실패` : ''}`, {
        description: failed ? '흔들림·반사가 없도록 가까이에서 다시 찍어주세요 (아이폰은 JPG로 전송)' : undefined,
      })
    } finally {
      setPhotoBusy(false)
    }
  }

  if (product.isLoading) return <Skeleton rows={10} />
  if (product.error || !product.data) return <ErrorBox error={product.error} onRetry={() => product.refetch()} />
  const p = product.data
  const list = scans.data ?? []
  const scannedQty = list.reduce((s, u) => s + u.qty, 0)
  const ambiguous = list.filter((u) => !u.supplierId && u.matchStatus === 'ambiguous').length
  const unmatched = list.filter((u) => !u.supplierId && u.matchStatus !== 'ambiguous').length

  return (
    <div className="space-y-4 text-[1.0625rem]">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <Link to={paths.product(date, code)} className="inline-flex items-center gap-1 text-[0.8125rem] font-semibold text-gray-600 hover:text-gray-900">
            <ArrowLeft className="size-3.5" /> 판정 화면으로
          </Link>
          <h1 className="mt-1 truncate text-2xl font-bold tracking-[-0.01em]">{p.row.name}</h1>
          <div className="mt-1 flex items-center gap-3 text-sm text-gray-600">
            <span className="code">{code}</span>
            <span>
              보유 <b className="num text-gray-900">{qty(p.row.stock)}</b> 중 스캔 <b className="num text-gray-900">{qty(scannedQty)}</b> ({list.length}포장)
            </span>
            {pending > 0 && <Badge tone="blue">처리 중 {pending}</Badge>}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <span className="inline-flex h-9 items-center gap-2 rounded-full bg-green-50 px-3 text-sm font-semibold text-green-700">
            <span className="size-2 animate-pulse rounded-full bg-green-600" /> 스캔 대기 중
          </span>
          <Button
            variant="ghost"
            icon={sound ? <Volume2 className="size-4" /> : <VolumeX className="size-4" />}
            onClick={() => {
              setSound((s) => {
                try {
                  localStorage.setItem('dpr.sound', s ? 'off' : 'on')
                } catch {
                  /* ignore */
                }
                return !s
              })
            }}
          >
            소리
          </Button>
          <Button icon={<Camera className="size-4" />} loading={photoBusy} onClick={() => fileInput.current?.click()}>
            사진에서 읽기
          </Button>
          <Button icon={<Keyboard className="size-4" />} onClick={() => setManual(true)}>
            직접 입력
          </Button>
          <Button variant="primary" icon={<Barcode className="size-4" />} onClick={() => navigate(paths.register(date, code))}>
            도매 앱 등록 →
          </Button>
          <input
            ref={fileInput}
            type="file"
            accept="image/*"
            multiple
            hidden
            onChange={(e) => {
              const files = Array.from(e.target.files ?? [])
              e.target.value = ''
              if (files.length) void onPhotos(files)
            }}
          />
        </div>
      </div>

      <div className="grid grid-cols-[minmax(0,3fr)_minmax(0,2fr)] gap-4 max-[1180px]:grid-cols-1">
        <LastScanCard last={last} />
        <Card title="도매별 집계" pad={false}>
          <div className="divide-y divide-gray-100">
            {(p.positions ?? []).map((pos) => {
              const held = pos.scannedHeld ?? 0
              const diff = held - pos.docQty
              const scanning = scannedQty < p.row.stock // 아직 다 안 찍음 → 판정은 안 찍은 몫을 사입 기록으로 추정해 낸다
              return (
                <div key={pos.supplierId} className="flex items-center gap-3 px-5 py-3">
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-semibold">{pos.supplierName}</div>
                    <div className="text-xs text-gray-600">서류보상 {qty(pos.docQty)}</div>
                  </div>
                  <div className="text-right">
                    <div className="num text-[1.75rem] leading-8 font-semibold">{qty(held)}</div>
                    <div className={cx('text-xs font-semibold', pos.rule ? (diff > 0 ? 'text-blue-700' : 'text-gray-500') : 'text-gray-400')}>
                      {!pos.rule
                        ? '보상 대상 아님'
                        : scanning
                          ? `${scannedQty > 0 ? '찍는 중' : '찍기 전'} · 추정 ${qty(pos.heldUsed)} → ${pos.method === 'physical' ? '실물' : '서류'} 예상`
                          : diff > 0
                            ? `실물 우세 +${qty(diff)}`
                            : `서류가 ${qty(-diff)} 많음`}
                    </div>
                  </div>
                </div>
              )
            })}
            <div className="flex gap-4 px-5 py-3 text-sm">
              <span className={cx(ambiguous ? 'font-semibold text-amber-700' : 'text-gray-500')}>도매 확인 필요 {ambiguous}</span>
              <span className={cx(unmatched ? 'font-semibold text-gray-800' : 'text-gray-500')}>매칭 없음 {unmatched}</span>
            </div>
          </div>
        </Card>
      </div>

      <ScanLog rows={list} />

      <ManualDialog open={manual} onClose={() => setManual(false)} onSubmit={(raw) => submit(raw)} />
      <UnknownProductDialog state={unknown} code={code} onClose={() => setUnknown(null)} onResolved={(raw) => submit(raw)} />
      <p className="flex items-center gap-2 text-sm text-gray-600">
        <ScanLine className="size-4" /> 스캐너로 포장의 2D 바코드를 연속으로 찍으세요. <Kbd>Ctrl</Kbd>+<Kbd>Z</Kbd> 마지막 스캔 되돌리기 · 한/영 상태와 무관하게 인식합니다.
      </p>
    </div>
  )
}

function LastScanCard({ last }: { last: Last | null }) {
  if (!last) {
    return (
      <div className="flex min-h-[220px] flex-col items-center justify-center rounded-xl border-2 border-dashed border-gray-300 bg-gray-0 text-center">
        <ScanLine className="size-10 text-gray-400" />
        <p className="mt-3 text-lg font-semibold text-gray-800">바코드를 스캔하세요</p>
        <p className="mt-1 text-sm text-gray-600">포장의 2D 바코드(제조번호·유효기간·일련번호)를 읽어 사입 도매를 찾습니다.</p>
      </div>
    )
  }
  const r = last.res
  const u = r?.unit
  let tone = 'border-gray-300 bg-gray-0'
  let icon = <CheckCircle2 className="size-9 text-green-600" />
  let title = ''
  let sub = r?.message ?? last.error ?? ''
  if (last.error || !r) {
    tone = 'border-red-600/40 bg-red-50'
    icon = <XCircle className="size-9 text-red-600" />
    title = '처리 실패'
  } else if (r.status === 'added' && u?.matchStatus === 'unique') {
    tone = 'border-green-600/40 bg-green-50'
    title = u.supplierName
    sub = '사입 도매 확인'
  } else if (r.status === 'added' && u?.matchStatus === 'ambiguous') {
    tone = 'border-amber-600/50 bg-amber-50'
    icon = <CircleAlert className="size-9 text-amber-600" />
    title = '도매 확인 필요'
    sub = `후보: ${(u.candidates ?? []).map((c) => c.name).join(', ')} — 아래 기록에서 지정하세요`
  } else if (r.status === 'added') {
    tone = 'border-gray-400 bg-gray-50'
    icon = <CircleAlert className="size-9 text-gray-500" />
    title = '매칭 없음'
  } else if (r.status === 'duplicate') {
    tone = 'border-blue-600/40 bg-blue-50'
    icon = <Copy className="size-9 text-blue-600" />
    title = '이미 스캔한 포장'
    sub = u ? `${dateTime(u.createdAt)}에 스캔됨 — 집계에 다시 넣지 않았습니다` : sub
  } else if (r.status === 'other_product') {
    tone = 'border-red-600/40 bg-red-50'
    icon = <XCircle className="size-9 text-red-600" />
    title = '다른 약품의 바코드'
  } else if (r.status === 'unknown_product') {
    tone = 'border-amber-600/50 bg-amber-50'
    icon = <CircleAlert className="size-9 text-amber-600" />
    title = '처음 보는 바코드'
  } else {
    tone = 'border-red-600/40 bg-red-50'
    icon = <XCircle className="size-9 text-red-600" />
    title = '바코드 형식 오류'
  }
  const g = r?.parsed
  return (
    <div key={last.at} className={cx('flash flex min-h-[220px] flex-col justify-between rounded-xl border-2 p-6', tone)}>
      <div className="flex items-start gap-4">
        {icon}
        <div className="min-w-0">
          <div className="text-[2rem] leading-10 font-bold tracking-[-0.02em]">{title}</div>
          <div className="mt-1 text-[0.9375rem] text-gray-700">{sub}</div>
        </div>
      </div>
      {g && (
        <dl className="mt-5 grid grid-cols-3 gap-4 text-sm">
          <div>
            <dt className="text-xs font-semibold text-gray-600">제조번호</dt>
            <dd className="code mt-0.5 text-lg font-semibold">{g.lot || '—'}</dd>
          </div>
          <div>
            <dt className="text-xs font-semibold text-gray-600">유효기간</dt>
            <dd className="num mt-0.5 text-lg font-semibold">{dateDot(g.expiry) || '—'}</dd>
          </div>
          <div>
            <dt className="text-xs font-semibold text-gray-600">일련번호 (끝 6자리)</dt>
            <dd className="code mt-0.5 text-lg font-semibold">
              <span className="text-gray-500">{(g.serial || '').slice(0, -6)}</span>
              {(g.serial || '').slice(-6) || '—'}
            </dd>
          </div>
        </dl>
      )}
    </div>
  )
}

function ScanLog({ rows }: { rows: service.ScanView[] }) {
  const sups = useSuppliers(false)
  const resolve = async (id: number, sid: number) => {
    try {
      await api.ResolveScan(id, sid)
      queryClient.invalidateQueries({ queryKey: keys.all })
    } catch (e) {
      toast.error(errorText(e))
    }
  }
  const del = async (id: number) => {
    await api.DeleteScan(id)
    queryClient.invalidateQueries({ queryKey: keys.all })
  }
  if (rows.length === 0) {
    return (
      <Card>
        <Empty title="아직 스캔 기록이 없습니다" />
      </Card>
    )
  }
  return (
    <Card title={`스캔 기록 ${rows.length}`} pad={false}>
      <div className="max-h-[420px] overflow-auto">
        <table className="w-full text-sm">
          <thead className="sticky top-0 border-b border-gray-200 bg-gray-50 text-[0.8125rem] text-gray-700">
            <tr>
              <th className="px-4 py-2 text-left font-semibold">시각</th>
              <th className="px-3 py-2 text-left font-semibold">제조번호</th>
              <th className="px-3 py-2 text-left font-semibold">유효기간</th>
              <th className="px-3 py-2 text-left font-semibold">일련번호</th>
              <th className="px-3 py-2 text-right font-semibold">수량</th>
              <th className="px-3 py-2 text-left font-semibold">도매</th>
              <th className="w-10" />
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {rows.map((u) => (
              <tr key={u.id} className={cx(!u.supplierId && u.matchStatus === 'ambiguous' && 'bg-amber-50/60')}>
                <td className="num px-4 py-2 text-gray-600">{dateTime(u.createdAt).slice(11)}</td>
                <td className="code px-3 py-2 font-semibold">{u.lot}</td>
                <td className="num px-3 py-2">{dateDot(u.expiry)}</td>
                <td className="code px-3 py-2">{u.serial}</td>
                <td className="num px-3 py-2 text-right">{qty(u.qty)}</td>
                <td className="px-3 py-2">
                  {u.matchStatus === 'ambiguous' || u.resolvedBy === 'user' ? (
                    <span className="flex flex-wrap items-center gap-1.5">
                      {(u.candidates ?? []).map((c, i) => (
                        <button
                          key={c.id}
                          onClick={() => resolve(u.id, c.id)}
                          className={cx(
                            'h-7 rounded-md border px-2 text-[0.8125rem] font-semibold',
                            u.supplierId === c.id ? 'border-blue-600 bg-blue-50 text-blue-700' : 'border-gray-300 bg-gray-0 hover:bg-gray-50',
                          )}
                          title="도매 앱에서 확인한 뒤 선택"
                        >
                          {i + 1} {c.name}
                        </button>
                      ))}
                      <select
                        className="h-7 rounded-md border border-gray-300 bg-gray-0 px-1 text-[0.8125rem]"
                        value={u.candidates?.some((c) => c.id === u.supplierId) ? '' : u.supplierId || ''}
                        onChange={(e) => resolve(u.id, Number(e.target.value))}
                      >
                        <option value="">다른 도매…</option>
                        {(sups.data ?? []).map((s) => (
                          <option key={s.id} value={s.id}>
                            {s.name}
                          </option>
                        ))}
                      </select>
                    </span>
                  ) : u.supplierId ? (
                    <span className="font-semibold">{u.supplierName}</span>
                  ) : (
                    <select className="h-7 rounded-md border border-gray-300 bg-gray-0 px-1 text-[0.8125rem]" value="" onChange={(e) => resolve(u.id, Number(e.target.value))}>
                      <option value="">매칭 없음 — 도매 지정…</option>
                      {(sups.data ?? []).map((s) => (
                        <option key={s.id} value={s.id}>
                          {s.name}
                        </option>
                      ))}
                    </select>
                  )}
                </td>
                <td className="px-2">
                  <button className="text-gray-400 hover:text-red-600" onClick={() => del(u.id)} title="삭제">
                    <Trash2 className="size-4" />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  )
}

function ManualDialog({ open, onClose, onSubmit }: { open: boolean; onClose: () => void; onSubmit: (raw: string) => void }) {
  const [text, setText] = useState('')
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="바코드 직접 입력"
      footer={
        <>
          <Button onClick={onClose}>닫기</Button>
          <Button
            variant="primary"
            disabled={!text.trim()}
            onClick={() => {
              onSubmit(normalizeTyped(text))
              setText('')
            }}
          >
            입력
          </Button>
        </>
      }
    >
      <div data-scan-ignore>
        <p className="mb-3 text-sm text-gray-600">포장에 인쇄된 (01)(17)(10)(21) 숫자를 그대로 입력하거나, 메모장에 스캔한 내용을 붙여넣으세요.</p>
        <Input
          autoFocus
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="(01)08809000000117(17)300615(10)31007-2(21)31007-2000123"
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing) return
            if (e.key === 'Enter' && text.trim()) {
              onSubmit(normalizeTyped(text))
              setText('')
            }
          }}
          className="code"
        />
      </div>
    </Dialog>
  )
}

function UnknownProductDialog({ state, code, onClose, onResolved }: { state: { raw: string; res: service.ScanResult } | null; code: string; onClose: () => void; onResolved: (raw: string) => void }) {
  const [q, setQ] = useState('')
  const search = useProductSearch(q)
  if (!state) return null
  const gtin = state.res.parsed?.gtin ?? ''
  const pick = async (c: string) => {
    try {
      await api.ConfirmGTIN(gtin, c)
      onClose()
      onResolved(state.raw)
    } catch (e) {
      toast.error(errorText(e))
    }
  }
  const cands = [...(state.res.candidates ?? []), ...(search.data ?? [])]
  return (
    <Dialog open onClose={onClose} title="이 바코드는 어떤 약품인가요?" footer={<Button onClick={onClose}>건너뛰기</Button>}>
      <div data-scan-ignore className="space-y-3">
        <p className="text-sm text-gray-600">
          처음 보는 표준코드 <span className="code font-semibold">{gtin}</span> 입니다. 한 번 지정하면 다음부터 자동으로 인식합니다. (데이터 가져오기 → '표준코드 매핑'을 올리면 대부분 자동입니다)
        </p>
        <Button variant="primary" className="w-full" onClick={() => pick(code)}>
          지금 작업 중인 약품으로 지정 ({code})
        </Button>
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="약품명 또는 코드로 검색" />
        <div className="max-h-64 space-y-1 overflow-auto">
          {cands.map((c) => (
            <button key={c.code} onClick={() => pick(c.code)} className="flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm hover:bg-gray-50">
              <span className="truncate">{c.name}</span>
              <span className="code text-gray-500">{c.code}</span>
            </button>
          ))}
        </div>
      </div>
    </Dialog>
  )
}
