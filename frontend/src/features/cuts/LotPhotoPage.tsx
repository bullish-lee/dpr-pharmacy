import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, ArrowLeftRight, ArrowRight, ArrowUpToLine, Camera, Check, ChevronDown, ImageIcon, Pencil, Plus, Save, Trash2, X } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import { api, errorText, keys, obj, useAction, useRanking, useSuppliers, type service } from '../../app/api'
import { dateDot, qty, todayISO } from '../../app/format'
import { Link, navigate, paths } from '../../app/router'
import { FileDrop, fileToBase64 } from '../../components/FileDrop'
import { PhoneConnectButton } from '../../components/PhoneConnect'
import { Badge, Button, Callout, Card, Dialog, Empty, ErrorBox, Input, Kbd, PageHeader, Select, Skeleton, Spinner, cx } from '../../components/ui'

// 회차 3단계: 실계수를 넣은 약품의 사입 이력(유효기간·제조번호 포함)을 사진으로 올려
// Windows 기본 OCR(무료·오프라인)로 읽고 → 사용자가 사진과 비교·수정 → 저장 → 4단계 판정 확인으로 이어진다.
// 손실 순위 표에서 약품 오른쪽 화살표(→)로 들어오면 그 약품이 골라진 채로 열린다 (#/cuts/{시행일}/lots/{약품코드}).
// 저장한 근거는 5단계 바코드 → 도매 매칭에도 쓰인다.

/** 판독 결과 한 줄 (화면에서 고치는 중) */
type Draft = {
  key: number
  date: string
  supplierId: number
  supplierRaw: string
  qty: string
  expiry: string
  lot: string
  matched: string
  unsure: string[]
  /** 두 글자 인식 엔진이 다르게 읽은 칸의 다른 쪽 값 (칸 → 값, 도매는 supplierId 도) */
  alt: Record<string, string>
}
type AltField = 'date' | 'supplier' | 'qty' | 'expiry' | 'lot'
type Photo = {
  id: number
  name: string
  url: string
  code: string
  status: 'reading' | 'done' | 'error'
  error?: string
  result?: service.LotPhotoResult
  rows: Draft[]
  checked: boolean
  phoneId?: number // 휴대폰에서 온 사진 (phone.go) — 닫거나 저장하면 PC 의 받은 목록에서 뺀다
}

let seq = 1
const nextKey = () => seq++
/** 글자 인식이 안 될 때 직접 입력할 빈 줄 */
const blankDraft = (): Draft => ({ key: nextKey(), date: '', supplierId: 0, supplierRaw: '', qty: '', expiry: '', lot: '', matched: '', unsure: [], alt: {} })

const toDrafts = (res?: service.LotPhotoResult | null): Draft[] =>
  (res?.rows ?? []).map((r) => ({
    key: nextKey(),
    date: r.date,
    supplierId: r.supplierId,
    supplierRaw: r.supplierRaw,
    qty: r.qty ? String(r.qty) : '',
    expiry: r.expiry,
    lot: r.lot,
    matched: r.matched,
    unsure: r.unsure ?? [],
    alt: r.alt ?? {},
  }))

export const useLotEvidence = (codes: string[]) =>
  useQuery({ queryKey: [...keys.all, 'lot-evidence', codes], queryFn: () => api.ListLotEvidence(codes), enabled: codes.length > 0 })

/** 실계수를 넣은 약품 (지난 회차는 오늘 날짜 실계수) */
export function countedRows(data: service.Ranking | undefined) {
  if (!data) return []
  const late = data.effectiveDate < todayISO()
  return (data.rows ?? []).filter((r) => r.stock > 0 && (late ? r.laterCountQty : r.countQty) != null)
}

const dateInput = (iso: string) => (/^\d{4}-\d{2}-\d{2}$/.test(iso) ? iso : '')

export function LotPhotoPage({ date, initialCode }: { date: string; initialCode?: string }) {
  const rk = useRanking(date, true)
  const sups = useSuppliers(false)
  // 휴대폰에서 온 사입정보 사진 (아직 확인하지 않은 것)
  const inbox = useQuery({ queryKey: [...keys.all, 'phone-inbox', date], queryFn: () => api.PhoneInbox(date) })
  // 실계수를 넣은 약품 + 화살표로 들어온 약품·휴대폰 사진의 약품 (실계수 전이어도 보여 준다)
  const counted = useMemo(() => {
    const list = countedRows(rk.data)
    const want = [initialCode, ...(inbox.data ?? []).map((i) => i.code)].filter((c): c is string => !!c)
    const extra: service.RankRow[] = []
    for (const c of want) {
      if (list.some((r) => r.code === c) || extra.some((r) => r.code === c)) continue
      const row = (rk.data?.rows ?? []).find((r) => r.code === c)
      if (row) extra.push(row)
    }
    return [...extra, ...list]
  }, [rk.data, initialCode, inbox.data])
  const codes = useMemo(() => counted.map((r) => r.code), [counted])
  const ev = useLotEvidence(codes)
  const [photos, setPhotos] = useState<Photo[]>([])
  const [zoom, setZoom] = useState<Photo | null>(null)
  const [code, setCode] = useState(initialCode ?? '')
  const [saved, setSaved] = useState('') // 방금 근거를 저장한 약품 → 4단계 안내
  const evidence = ev.data ?? []
  const evCount = (c: string) => evidence.filter((e) => e.code === c).length

  // 화살표로 들어온 약품을 고른다. 주소 없이 들어오면 아직 근거가 없는 첫 약품
  useEffect(() => {
    if (initialCode) setCode(initialCode)
  }, [initialCode])
  useEffect(() => {
    if (!code && counted.length > 0 && ev.isFetched) setCode((counted.find((r) => evCount(r.code) === 0) ?? counted[0]).code)
  }, [counted, ev.isFetched]) // eslint-disable-line react-hooks/exhaustive-deps
  const pick = (c: string) => {
    setSaved('')
    navigate(paths.lotPhotos(date, c)) // 주소도 바꿔 뒤로 가기·새로 고침에도 같은 약품
  }

  const patchPhoto = (id: number, p: Partial<Photo>) => setPhotos((ps) => ps.map((x) => (x.id === id ? { ...x, ...p } : x)))

  // 휴대폰에서 새 사진이 오면 확인 카드로 올린다 (한 번만)
  const seenPhone = useRef(new Set<number>())
  useEffect(() => {
    const fresh = (inbox.data ?? []).filter((it) => !seenPhone.current.has(it.id))
    if (fresh.length === 0) return
    for (const it of fresh) seenPhone.current.add(it.id)
    setPhotos((ps) => [
      ...fresh.map(
        (it): Photo => ({
          id: nextKey(),
          phoneId: it.id,
          name: `휴대폰 사진 · ${new Date(it.receivedAt).toLocaleTimeString('ko-KR', { hour: '2-digit', minute: '2-digit' })}`,
          url: `data:${it.mediaType || 'image/jpeg'};base64,${it.imageBase64}`,
          code: it.code,
          status: it.error ? 'error' : 'done',
          error: it.error || undefined,
          result: it.result ?? undefined,
          rows: it.error && !(it.result?.rows ?? []).length ? [blankDraft()] : toDrafts(it.result), // 읽지 못했으면 직접 입력할 빈 줄
          checked: false,
        }),
      ),
      ...ps,
    ])
  }, [inbox.data])

  const readFiles = async (files: File[]) => {
    if (!code) {
      toast.warning('어느 약품의 사입 이력인지 먼저 고르세요')
      return
    }
    for (const f of files) {
      const id = nextKey()
      setPhotos((ps) => [{ id, name: f.name || '붙여 넣은 화면', url: URL.createObjectURL(f), code, status: 'reading', rows: [], checked: false }, ...ps])
      try {
        const res = await api.ReadLotPhoto(obj<service.LotPhotoRequest>({ imageBase64: await fileToBase64(f), code }))
        patchPhoto(id, { status: 'done', rows: toDrafts(res), result: res })
      } catch (e) {
        // 직접 입력할 빈 줄 하나를 열어 둔다
        patchPhoto(id, { status: 'error', error: errorText(e), rows: [blankDraft()] })
      }
    }
  }

  // Ctrl+V: 화면 캡처(Win+Shift+S)를 바로 붙여 넣기
  const readRef = useRef(readFiles)
  readRef.current = readFiles
  useEffect(() => {
    const onPaste = (e: ClipboardEvent) => {
      const files = Array.from(e.clipboardData?.items ?? [])
        .filter((i) => i.type.startsWith('image/'))
        .map((i) => i.getAsFile())
        .filter((f): f is File => !!f)
      if (files.length) {
        e.preventDefault()
        void readRef.current(files)
      }
    }
    window.addEventListener('paste', onPaste)
    return () => window.removeEventListener('paste', onPaste)
  }, [])

  if (rk.isLoading) return <Skeleton rows={10} />
  if (rk.error || !rk.data) return <ErrorBox error={rk.error} onRetry={() => rk.refetch()} />
  const late = rk.data.effectiveDate < todayISO()
  const sel = counted.find((r) => r.code === code)
  const selName = sel?.name ?? ''
  const nameOf = (c: string) => counted.find((r) => r.code === c)?.name ?? c
  const nextTodo = counted.find((r) => r.code !== saved && evCount(r.code) === 0)
  const selCounted = !sel || (late ? sel.laterCountQty : sel.countQty) != null

  return (
    <div className="space-y-5">
      <PageHeader
        back={
          <Link to={paths.cut(date)} className="inline-flex items-center gap-1 text-[0.8125rem] font-semibold text-gray-600 hover:text-gray-900">
            <ArrowLeft className="size-3.5" /> 회차로 돌아가기 (2단계 실계수 입력)
          </Link>
        }
        title={
          <span className="flex items-center gap-2">
            <Badge tone="blue">3단계</Badge> 사입정보 사진 확인
          </span>
        }
        sub="실계수를 넣은 약품의 사입 이력(이팜 사입현황 화면 등)을 올리면 사입일자·사입도매·수량·유효기간·제조번호를 읽어 줍니다. 이 PC의 Windows 글자 인식을 써서 비용이 들지 않고 인터넷도 쓰지 않습니다. 읽은 결과는 사진과 비교해 확인한 뒤 저장하면 4단계 판정 확인으로 이어집니다."
        actions={
          <>
            <PhoneConnectButton />
            {code && (
              <Button variant="primary" icon={<ArrowRight className="size-4" />} onClick={() => navigate(paths.product(date, code))}>
                4단계: 판정 확인
              </Button>
            )}
          </>
        }
      />

      {saved && (
        <Callout
          tone="success"
          action={
            <div className="flex flex-wrap gap-2">
              <Button variant="primary" icon={<ArrowRight className="size-4" />} onClick={() => navigate(paths.product(date, saved))}>
                4단계: 이 약품 판정 확인
              </Button>
              {nextTodo && <Button onClick={() => pick(nextTodo.code)}>다음 약품 사진 올리기</Button>}
            </div>
          }
        >
          「{nameOf(saved)}」 사입 근거를 저장했습니다. 이어서 <b>4단계 판정 확인</b>에서 서류보상·실물보상 판정을 확인하세요.
        </Callout>
      )}

      {code && !selCounted && (
        <Callout tone="warn">
          「{selName}」은(는) 아직 실계수를 넣지 않았습니다. 사입정보는 지금 올려도 되지만, 회차 화면 2단계에서 실계수도 넣어 주세요.
        </Callout>
      )}

      <Card title={`실계수를 넣은 약품 ${counted.length}개`} pad={false}>
        {counted.length === 0 ? (
          <Empty title="실계수를 넣은 약품이 없습니다" action={<Button onClick={() => navigate(paths.cut(date))}>2단계에서 실계수 넣기</Button>}>
            회차 화면의 손실 순위 표에서 실계수를 먼저 넣으세요.
          </Empty>
        ) : (
          <ul className="divide-y divide-gray-100">
            {counted.map((r) => {
              const n = evCount(r.code)
              const cq = late ? r.laterCountQty : r.countQty
              const sel = r.code === code
              return (
                <li key={r.code}>
                  <button
                    className={cx('flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm hover:bg-gray-50', sel && 'bg-blue-50 hover:bg-blue-50')}
                    onClick={() => pick(r.code)}
                    title="이 약품의 사입 이력 사진을 올리려면 누르세요"
                  >
                    {n > 0 ? <Check className="size-4 shrink-0 text-green-600" /> : <span className="size-4 shrink-0 rounded-full border-2 border-gray-300" />}
                    <span className={cx('min-w-0 flex-1 truncate font-medium', sel ? 'text-blue-700' : 'text-gray-900')}>{r.name}</span>
                    <span className="code text-xs text-gray-500">{r.code}</span>
                    <span className="num w-28 text-right text-gray-700">
                      실계수 <b>{qty(cq ?? 0)}</b>
                      {r.unit && <span className="ml-0.5 text-xs text-gray-500">{r.unit}</span>}
                    </span>
                    <span className="w-24 text-right">{n > 0 ? <Badge tone="green">근거 {n}건</Badge> : <Badge tone="outline">사진 전</Badge>}</span>
                    {sel && <Badge tone="blue">선택됨</Badge>}
                  </button>
                </li>
              )
            })}
          </ul>
        )}
      </Card>

      {counted.length > 0 && (
        <div className="space-y-2">
          <Callout tone="info">
            <b>가장 정확한 방법:</b> 이팜에서 약품의 <b>사입현황</b> 탭을 열고 <Kbd>Win</Kbd>+<Kbd>Shift</Kbd>+<Kbd>S</Kbd>로 표 부분을 캡처한 뒤, 이 화면에서 <Kbd>Ctrl</Kbd>+<Kbd>V</Kbd> 로 붙여 넣으세요. 휴대폰으로 모니터를 찍은 사진도 되지만 화면 무늬 때문에
            제조번호·유효기간을 자주 틀리게 읽습니다. 거래명세서처럼 종이로 된 사입 내역은 위쪽 <b>[휴대폰으로 찍기]</b>로 휴대폰에서 찍어 바로 보낼 수 있습니다.
          </Callout>
          <FileDrop
            accept="image/*"
            multiple
            compact
            disabled={!code}
            icon={<Camera className="size-6" />}
            title={code ? `「${selName}」 사입 이력 사진을 끌어다 놓거나 클릭해서 선택 (또는 Ctrl+V)` : '위 목록에서 약품을 먼저 고르세요'}
            hint="이팜 사입현황 화면 캡처 · 거래명세서 사진 · JPG/PNG · 여러 장 가능"
            onFiles={(f) => void readFiles(f)}
          />
        </div>
      )}

      {photos.map((p) => (
        <PhotoCard
          key={p.id}
          photo={p}
          counted={counted}
          suppliers={sups.data ?? []}
          onZoom={() => setZoom(p)}
          onChange={(patch) => patchPhoto(p.id, patch)}
          onSaved={() => {
            setSaved(p.code)
            window.scrollTo({ top: 0, behavior: 'smooth' })
          }}
          onClose={() => {
            if (p.url.startsWith('blob:')) URL.revokeObjectURL(p.url)
            if (p.phoneId) void api.DismissPhoneItem(p.phoneId) // 휴대폰에서 온 사진은 PC 의 받은 목록에서도 뺀다
            setPhotos((ps) => ps.filter((x) => x.id !== p.id))
          }}
        />
      ))}

      {counted.length > 0 && <SavedEvidence counted={counted} evidence={evidence} suppliers={sups.data ?? []} loading={ev.isLoading} />}

      <Dialog open={!!zoom} onClose={() => setZoom(null)} title={zoom?.name ?? ''} width={1100}>
        {zoom && <img src={zoom.url} alt={zoom.name} className="max-h-[78vh] w-full object-contain" />}
      </Dialog>
    </div>
  )
}

// ── 판독 결과 (사진 1장) ─────────────────────────────────────────────
const unsureCls = 'border-amber-500 bg-amber-50'

function PhotoCard({
  photo,
  counted,
  suppliers,
  onZoom,
  onChange,
  onSaved,
  onClose,
}: {
  photo: Photo
  counted: service.RankRow[]
  suppliers: service.SupplierView[]
  onZoom: () => void
  onChange: (p: Partial<Photo>) => void
  onSaved: () => void
  onClose: () => void
}) {
  const save = useAction(
    () =>
      api.SaveLotPhotoRows(
        photo.rows.map((r) => obj<service.LotInput>({ code: photo.code, supplierId: r.supplierId, date: r.date, qty: Number(r.qty) || 0, expiry: r.expiry, lot: r.lot.trim(), source: 'photo' })),
      ),
    { success: (n) => (n > 0 ? `사입 근거 ${n}건을 저장했습니다` : '이미 저장된 근거라 새로 저장한 줄이 없습니다') },
  )
  const rows = photo.rows
  // 표를 고치면 '확인했습니다' 체크를 다시 받는다
  const setRows = (r: Draft[]) => onChange({ rows: r, checked: false })
  // 사용자가 고친 칸은 '확인 필요' 표시를 지운다
  const set = (key: number, field: string, p: Partial<Draft>) => setRows(rows.map((r) => (r.key === key ? { ...r, ...p, unsure: r.unsure.filter((u) => u !== field) } : r)))
  const missing = (r: Draft) => !r.supplierId || !r.lot.trim()
  const blocked = rows.filter(missing).length
  const unsureCount = rows.reduce((s, r) => s + r.unsure.length, 0)
  const res = photo.result
  const isU = (r: Draft, f: string) => r.unsure.includes(f)
  // 다른 엔진이 읽은 값으로 바꾼다 — 지금 값은 후보로 남겨 다시 누르면 되돌린다
  const pickAlt = (r: Draft, f: AltField) => {
    const v = r.alt[f]
    if (v == null) return
    const alt = { ...r.alt }
    if (f === 'supplier') {
      alt.supplier = suppliers.find((x) => x.id === r.supplierId)?.name ?? r.supplierRaw
      alt.supplierId = r.supplierId ? String(r.supplierId) : ''
      set(r.key, f, { supplierId: Number(r.alt.supplierId || 0), supplierRaw: v, alt })
      return
    }
    alt[f] = r[f]
    set(r.key, f, { [f]: v, alt })
  }
  const altChip = (r: Draft, f: AltField, show: (v: string) => string = (v) => v) =>
    r.alt[f] ? (
      <button
        type="button"
        className="mt-0.5 flex max-w-full items-center gap-1 truncate rounded bg-violet-50 px-1.5 py-0.5 text-xs font-medium text-violet-800 hover:bg-violet-100"
        title="두 글자 인식(Windows 기본 · PaddleOCR)이 다르게 읽었습니다. 사진과 비교해 맞는 쪽을 고르세요 — 누르면 이 값으로 바뀌고, 다시 누르면 되돌립니다"
        onClick={() => pickAlt(r, f)}
      >
        <ArrowLeftRight className="size-3 shrink-0" /> {show(r.alt[f])}
      </button>
    ) : null

  return (
    <Card
      title={
        <span className="flex flex-wrap items-center gap-2">
          <ImageIcon className="size-4 text-gray-500" />
          {photo.name}
          {photo.status === 'reading' && <Badge tone="blue">읽는 중</Badge>}
          {photo.status === 'done' && <Badge tone="green">{rows.length}줄 읽음</Badge>}
          {photo.status === 'done' && res?.mode === 'photo' && <Badge tone="amber">휴대폰 사진 — 꼼꼼히 확인</Badge>}
          {photo.status === 'error' && <Badge tone="red">읽기 실패</Badge>}
        </span>
      }
      actions={
        <button className="text-gray-400 hover:text-gray-700" onClick={onClose} title="닫기 (저장하지 않은 줄은 버림)">
          <X className="size-4" />
        </button>
      }
    >
      <div className="space-y-3">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-gray-600">약품</span>
          <Select value={photo.code} onChange={(e) => onChange({ code: e.target.value, checked: false })} className="max-w-[420px]">
            {counted.map((c) => (
              <option key={c.code} value={c.code}>
                {c.name}
              </option>
            ))}
          </Select>
          {res && res.skipped > 0 && <span className="text-xs text-gray-500">반품·재고조정 등 사입이 아닌 줄 {res.skipped}개는 뺐습니다</span>}
        </div>

        <div className="grid grid-cols-[minmax(0,1fr)] gap-4 min-[1300px]:grid-cols-[420px_minmax(0,1fr)]">
          <button onClick={onZoom} className="group relative self-start overflow-hidden rounded-lg border border-gray-200 bg-gray-50" title="크게 보기">
            <img src={photo.url} alt={photo.name} className="max-h-[340px] w-full object-contain" />
            <span className="absolute right-1 bottom-1 rounded bg-black/60 px-1.5 py-0.5 text-[0.6875rem] text-white opacity-80 group-hover:opacity-100">크게 보기</span>
          </button>

          <div className="min-w-0 space-y-3">
            {photo.status === 'reading' && <Spinner label="글자를 읽는 중… (몇 초 걸립니다)" />}
            {photo.status === 'error' && (
              <Callout tone="danger">
                {photo.error}
                <div className="mt-1 text-gray-700">왼쪽 사진을 보면서 아래 표에 직접 넣어도 됩니다 — 사진을 누르면 크게 보입니다.</div>
              </Callout>
            )}
            {(photo.status === 'done' || photo.status === 'error') && (
              <>
                {res?.note && <Callout tone="warn">{res.note}</Callout>}
                <div className="overflow-x-auto">
                  <table className="w-full min-w-[780px] text-sm">
                    <thead>
                      <tr className="border-b border-gray-200 text-left text-[0.8125rem] font-semibold text-gray-700">
                        <th className="py-2 pr-2">사입일자</th>
                        <th className="px-2 py-2">사입도매</th>
                        <th className="px-2 py-2 text-right">수량</th>
                        <th className="px-2 py-2">유효기간</th>
                        <th className="px-2 py-2">제조번호</th>
                        <th className="px-2 py-2">사입현황</th>
                        <th className="w-14" />
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map((r, i) => (
                        <tr key={r.key} className="border-b border-gray-100 align-top">
                          <td className="py-1.5 pr-2">
                            <Input type="date" value={dateInput(r.date)} onChange={(e) => set(r.key, 'date', { date: e.target.value })} className={cx('w-[140px]', isU(r, 'date') && unsureCls)} />
                            {altChip(r, 'date', dateDot)}
                          </td>
                          <td className="px-2 py-1.5">
                            <SupplierSelect value={r.supplierId} suppliers={suppliers} unsure={isU(r, 'supplier')} onChange={(id) => set(r.key, 'supplier', { supplierId: id })} />
                            {r.supplierRaw && !r.supplierId && <div className="mt-0.5 truncate text-xs text-red-700">읽은 글자: {r.supplierRaw}</div>}
                            {altChip(r, 'supplier')}
                          </td>
                          <td className="px-2 py-1.5">
                            <Input value={r.qty} inputMode="decimal" onChange={(e) => set(r.key, 'qty', { qty: e.target.value })} className={cx('num w-20 text-right', isU(r, 'qty') && unsureCls)} />
                            {altChip(r, 'qty')}
                          </td>
                          <td className="px-2 py-1.5">
                            <Input type="date" value={dateInput(r.expiry)} onChange={(e) => set(r.key, 'expiry', { expiry: e.target.value })} className={cx('w-[140px]', isU(r, 'expiry') && unsureCls)} />
                            {altChip(r, 'expiry', dateDot)}
                          </td>
                          <td className="px-2 py-1.5">
                            <Input
                              value={r.lot}
                              onChange={(e) => set(r.key, 'lot', { lot: e.target.value.toUpperCase() })}
                              className={cx('code w-32 font-semibold', !r.lot.trim() ? 'border-red-600' : isU(r, 'lot') && unsureCls)}
                            />
                            {altChip(r, 'lot')}
                          </td>
                          <td className="px-2 py-1.5 pt-3.5 text-xs whitespace-nowrap">
                            {r.matched === 'date' ? (
                              <span className="text-green-700" title="같은 날짜의 사입 기록과 맞췄습니다 (날짜·도매·수량은 사입현황 값)">
                                ✓ 일치
                              </span>
                            ) : r.matched === 'order' ? (
                              <span className="text-amber-700" title="날짜를 정확히 못 읽어 도매·순서로 사입 기록을 짐작했습니다. 노란 칸(날짜, 사진에서 못 읽은 도매)을 사진과 비교하세요">
                                추정
                              </span>
                            ) : (
                              <span className="text-gray-400" title="사입현황에서 같은 사입을 찾지 못했습니다">
                                —
                              </span>
                            )}
                          </td>
                          <td className="py-1.5 pl-1 whitespace-nowrap">
                            {i > 0 && (
                              <button
                                className="mt-2 mr-2 text-gray-400 hover:text-blue-600"
                                title="윗줄의 유효기간·제조번호를 그대로 쓰기 (같은 제조번호를 여러 번 사입한 경우)"
                                onClick={() =>
                                  setRows(rows.map((x) => (x.key === r.key ? { ...x, expiry: rows[i - 1].expiry, lot: rows[i - 1].lot, unsure: x.unsure.filter((u) => u !== 'lot' && u !== 'expiry'), alt: Object.fromEntries(Object.entries(x.alt).filter(([k]) => k !== 'lot' && k !== 'expiry')) } : x)))
                                }
                              >
                                <ArrowUpToLine className="size-4" />
                              </button>
                            )}
                            <button className="mt-2 text-gray-400 hover:text-red-600" title="이 줄 빼기" onClick={() => setRows(rows.filter((x) => x.key !== r.key))}>
                              <Trash2 className="size-4" />
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {rows.length === 0 && photo.status === 'done' && <Empty title="사입 줄을 찾지 못했습니다">사입일자·거래처·유효기간·제조번호 열이 보이게 다시 올리거나, 아래 '줄 추가'로 직접 넣으세요.</Empty>}

                <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-gray-600">
                  <span className="inline-flex items-center gap-1">
                    <span className="inline-block size-3 rounded-sm border border-amber-500 bg-amber-50" /> 노란 칸 = 글자가 흐리거나 순서로 짐작해 확신이 낮음 ({unsureCount}칸)
                  </span>
                  <span>
                    <span className="text-green-700">✓ 일치</span> = 날짜·도매·수량을 이미 가져온 사입현황 값으로 채움
                  </span>
                  <span className="inline-flex items-center gap-1">
                    <ArrowUpToLine className="size-3.5" /> = 윗줄 제조번호·유효기간 복사
                  </span>
                  <span className="inline-flex items-center gap-1">
                    <span className="inline-flex items-center gap-0.5 rounded bg-violet-50 px-1 text-violet-800">
                      <ArrowLeftRight className="size-3" /> 값
                    </span>
                    = 다른 글자 인식이 읽은 값 (누르면 바꿈)
                  </span>
                </div>

                {res?.rawText && (
                  <details className="text-xs">
                    <summary className="flex cursor-pointer items-center gap-1 text-gray-600">
                      <ChevronDown className="size-3.5" /> 인식된 글자 그대로 보기
                    </summary>
                    <pre className="mt-1 max-h-48 overflow-auto rounded bg-gray-50 p-2 whitespace-pre-wrap text-gray-700">{res.rawText}</pre>
                  </details>
                )}

                <div className="flex flex-wrap items-center gap-3 border-t border-gray-100 pt-3">
                  <Button
                    size="sm"
                    icon={<Plus className="size-3.5" />}
                    onClick={() => setRows([...rows, { key: nextKey(), date: '', supplierId: 0, supplierRaw: '', qty: '', expiry: '', lot: '', matched: '', unsure: [], alt: {} }])}
                  >
                    줄 추가
                  </Button>
                  {blocked > 0 && <span className="text-xs text-red-700">사입도매·제조번호가 빈 줄 {blocked}개를 채우거나 빼야 저장할 수 있습니다</span>}
                  <label className="ml-auto inline-flex cursor-pointer items-center gap-2 text-sm font-medium text-gray-800">
                    <input type="checkbox" className="size-4 accent-blue-600" checked={photo.checked} onChange={(e) => onChange({ checked: e.target.checked })} />
                    사진과 한 줄씩 비교해 확인했습니다
                  </label>
                  <Button
                    variant="primary"
                    icon={<Save className="size-4" />}
                    loading={save.isPending}
                    disabled={rows.length === 0 || blocked > 0 || !photo.checked}
                    title={!photo.checked ? '사진과 비교해 확인한 뒤 체크하세요' : undefined}
                    onClick={() =>
                      save.mutate([], {
                        onSuccess: () => {
                          onSaved()
                          onClose()
                        },
                      })
                    }
                  >
                    {rows.length}줄 저장
                  </Button>
                </div>
              </>
            )}
          </div>
        </div>
      </div>
    </Card>
  )
}

function SupplierSelect({ value, suppliers, unsure, onChange }: { value: number; suppliers: service.SupplierView[]; unsure?: boolean; onChange: (id: number) => void }) {
  return (
    <Select value={value} onChange={(e) => onChange(Number(e.target.value))} className={cx('min-w-[150px]', !value ? 'border-red-600' : unsure && unsureCls)}>
      <option value={0}>도매 선택</option>
      {suppliers.map((s) => (
        <option key={s.id} value={s.id}>
          {s.name}
        </option>
      ))}
    </Select>
  )
}

// ── 저장한 근거 (고치기·지우기) ──────────────────────────────────────
function SavedEvidence({ counted, evidence, suppliers, loading }: { counted: service.RankRow[]; evidence: service.LotEvidenceView[]; suppliers: service.SupplierView[]; loading: boolean }) {
  const [edit, setEdit] = useState<number | null>(null)
  const names = new Map(counted.map((r) => [r.code, r.name]))
  const del = useAction((id: number) => api.DeleteLot(id), { success: '삭제했습니다' })
  return (
    <Card title={`저장한 사입 근거 ${evidence.length}건`} pad={false}>
      {loading ? (
        <Skeleton rows={3} />
      ) : evidence.length === 0 ? (
        <Empty title="아직 저장한 근거가 없습니다">사진을 올려 읽은 결과를 확인한 뒤 저장하세요.</Empty>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[900px] text-sm">
            <thead>
              <tr className="border-b border-gray-200 bg-gray-50 text-left text-[0.8125rem] font-semibold text-gray-700">
                <th className="px-4 py-2">약품</th>
                <th className="px-2 py-2">사입일자</th>
                <th className="px-2 py-2">사입도매</th>
                <th className="px-2 py-2 text-right">수량</th>
                <th className="px-2 py-2">유효기간</th>
                <th className="px-2 py-2">제조번호</th>
                <th className="px-2 py-2">출처</th>
                <th className="w-20" />
              </tr>
            </thead>
            <tbody>
              {evidence.map((e) =>
                edit === e.id ? (
                  <EditRow key={e.id} row={e} counted={counted} suppliers={suppliers} onDone={() => setEdit(null)} />
                ) : (
                  <tr key={e.id} className="border-b border-gray-100 hover:bg-gray-50">
                    <td className="max-w-[280px] truncate px-4 py-2 font-medium" title={names.get(e.code)}>
                      {names.get(e.code) ?? e.code}
                    </td>
                    <td className="num px-2 py-2">{dateDot(e.date)}</td>
                    <td className="px-2 py-2">{e.supplierName}</td>
                    <td className="num px-2 py-2 text-right">{e.qty ? qty(e.qty) : ''}</td>
                    <td className="num px-2 py-2">{dateDot(e.expiry)}</td>
                    <td className="code px-2 py-2 font-semibold">{e.lot}</td>
                    <td className="px-2 py-2">
                      <Badge tone="outline">{{ import: '엑셀', manual: '직접 입력', photo: '사진' }[e.source] ?? e.source}</Badge>
                    </td>
                    <td className="px-2 py-2 text-right whitespace-nowrap">
                      <button className="mr-2 text-gray-400 hover:text-blue-600" title="고치기" onClick={() => setEdit(e.id)}>
                        <Pencil className="size-4" />
                      </button>
                      <button className="text-gray-400 hover:text-red-600" title="삭제" onClick={() => del.mutate([e.id])}>
                        <Trash2 className="size-4" />
                      </button>
                    </td>
                  </tr>
                ),
              )}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  )
}

function EditRow({ row, counted, suppliers, onDone }: { row: service.LotEvidenceView; counted: service.RankRow[]; suppliers: service.SupplierView[]; onDone: () => void }) {
  const [f, setF] = useState({ code: row.code, date: row.date, supplierId: row.supplierId, qty: row.qty ? String(row.qty) : '', expiry: row.expiry, lot: row.lot })
  const save = useAction(
    () => api.UpdateLot(row.id, obj<service.LotInput>({ code: f.code, supplierId: f.supplierId, date: f.date, qty: Number(f.qty) || 0, expiry: f.expiry, lot: f.lot.trim(), source: row.source })),
    { success: '고쳤습니다' },
  )
  return (
    <tr className="border-b border-gray-100 bg-blue-50/50 align-top">
      <td className="px-4 py-1.5">
        <Select value={f.code} onChange={(e) => setF({ ...f, code: e.target.value })} className="min-w-[200px]">
          {!counted.some((c) => c.code === f.code) && <option value={f.code}>{f.code}</option>}
          {counted.map((c) => (
            <option key={c.code} value={c.code}>
              {c.name}
            </option>
          ))}
        </Select>
      </td>
      <td className="px-2 py-1.5">
        <Input type="date" value={dateInput(f.date)} onChange={(e) => setF({ ...f, date: e.target.value })} className="w-[140px]" />
      </td>
      <td className="px-2 py-1.5">
        <SupplierSelect value={f.supplierId} suppliers={suppliers} onChange={(id) => setF({ ...f, supplierId: id })} />
      </td>
      <td className="px-2 py-1.5">
        <Input value={f.qty} inputMode="decimal" onChange={(e) => setF({ ...f, qty: e.target.value })} className="num w-20 text-right" />
      </td>
      <td className="px-2 py-1.5">
        <Input type="date" value={dateInput(f.expiry)} onChange={(e) => setF({ ...f, expiry: e.target.value })} className="w-[140px]" />
      </td>
      <td className="px-2 py-1.5">
        <Input value={f.lot} onChange={(e) => setF({ ...f, lot: e.target.value.toUpperCase() })} className="code w-32 font-semibold" />
      </td>
      <td />
      <td className="px-2 py-1.5 text-right whitespace-nowrap">
        <Button size="sm" variant="primary" loading={save.isPending} disabled={!f.code || !f.supplierId || !f.lot.trim()} onClick={() => save.mutate([], { onSuccess: onDone })}>
          저장
        </Button>
        <button className="ml-1 text-gray-400 hover:text-gray-700" title="취소" onClick={onDone}>
          <X className="size-4" />
        </button>
      </td>
    </tr>
  )
}
