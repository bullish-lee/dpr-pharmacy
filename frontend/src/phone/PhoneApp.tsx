import { Camera, Check, ChevronLeft, CircleAlert, ImageIcon, Loader2, RefreshCw, ScanLine, Search, Wifi, WifiOff } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

// 휴대폰 화면: 회차·약품을 고르고 ① 사입정보 사진 ② 포장 바코드 사진을 찍어 PC 의 DPR 로 바로 보낸다.
// 사입정보는 PC 에서 글자를 읽고, PC 의 3단계 화면에서 확인·저장한다. 바코드는 휴대폰에서 읽어 5단계 기록에 바로 넣는다.

type CaseItem = { date: string; held: number }
// PC 는 꼭 필요한 것만 보낸다: 약품 이름·코드, 실계수를 넣었는지, 사입근거·바코드 건수 (재고·수량·가격·읽은 내용은 보내지 않음)
type Row = { code: string; name: string; counted: boolean; lots: number; scanned: number }
type ScanRes = { status: string; message: string; matched: boolean; unsure: boolean }
type Feed =
  | { id: number; kind: 'lot'; name: string; at: Date; state: 'sending' | 'done' | 'error'; rows?: number; error?: string; mode?: string }
  | { id: number; kind: 'barcode'; name: string; at: Date; state: 'sending' | 'done' | 'error'; found?: number; results?: ScanRes[]; error?: string }

// ── PC 와 연결 ──────────────────────────────────────────────────────
// 연결 비밀번호는 주소의 # 뒤에 온다 (#t=…). # 뒤는 서버·중계로 전송되지 않으므로 중계 기록에 남지 않는다.
const TOKEN_KEY = 'dpr-phone-token'
function readToken(): string {
  const t = new URLSearchParams(location.hash.replace(/^#/, '')).get('t') ?? new URLSearchParams(location.search).get('t') ?? ''
  try {
    if (t) {
      localStorage.setItem(TOKEN_KEY, t)
      history.replaceState(null, '', location.pathname) // 주소창에서 비밀번호를 지운다 (화면 공유·캡처 대비)
    }
    return t || localStorage.getItem(TOKEN_KEY) || ''
  } catch {
    return t
  }
}
const token = readToken()

async function call<T>(path: string, body?: unknown): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method: body ? 'POST' : 'GET',
      headers: { 'X-DPR-Token': token, ...(body ? { 'Content-Type': 'application/json' } : {}) },
      body: body ? JSON.stringify(body) : undefined,
    })
  } catch {
    throw new Error('PC 의 DPR 에 연결하지 못했습니다. PC 에서 DPR 이 켜져 있는지, 휴대폰이 약국 와이파이에 연결되어 있는지 확인하세요.')
  }
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error((data as { error?: string }).error || `연결 오류 (${res.status})`)
  return data as T
}

async function fileToBase64(f: Blob): Promise<string> {
  const buf = new Uint8Array(await f.arrayBuffer())
  let bin = ''
  for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode(...buf.subarray(i, i + 0x8000))
  return btoa(bin)
}

/** 큰 사진은 긴 변 3200px JPEG 로 줄여 빨리 보낸다 (글자 인식에는 충분) */
async function shrinkForUpload(f: File): Promise<Blob> {
  if (f.size < 3_500_000) return f
  try {
    const bmp = await createImageBitmap(f)
    const k = Math.min(1, 3200 / Math.max(bmp.width, bmp.height))
    const c = document.createElement('canvas')
    c.width = Math.round(bmp.width * k)
    c.height = Math.round(bmp.height * k)
    c.getContext('2d')!.drawImage(bmp, 0, 0, c.width, c.height)
    return await new Promise<Blob>((ok) => c.toBlob((b) => ok(b ?? f), 'image/jpeg', 0.92))
  } catch {
    return f
  }
}

const dot = (iso: string) => (iso ? iso.replaceAll('-', '.') : '')
const SCAN_LABEL: Record<string, string> = {
  added: '추가됨',
  duplicate: '이미 찍은 포장',
  other_product: '다른 약품의 바코드',
  unknown_product: '처음 보는 바코드 (PC에서 약품 지정)',
  invalid: '바코드를 읽지 못함',
  error: '오류',
}

let feedSeq = 1

export function PhoneApp() {
  const [online, setOnline] = useState<boolean | null>(null)
  const [connErr, setConnErr] = useState('')
  const [cases, setCases] = useState<CaseItem[]>([])
  const [date, setDate] = useState(() => localStorage.getItem('dpr-phone-date') ?? '')
  const [rows, setRows] = useState<Row[]>([])
  const [loading, setLoading] = useState(false)
  const [code, setCode] = useState('')
  const [q, setQ] = useState('')
  const [feed, setFeed] = useState<Feed[]>([])
  const lotInput = useRef<HTMLInputElement>(null)
  const lotGallery = useRef<HTMLInputElement>(null)
  const bcInput = useRef<HTMLInputElement>(null)

  // 회차 목록
  const loadCases = useCallback(async () => {
    try {
      const r = await call<{ cases: CaseItem[]; today: string }>('/api/cases')
      setCases(r.cases ?? [])
      setOnline(true)
      setConnErr('')
      // 고른 회차가 없으면 오늘 이후 가장 가까운 회차, 없으면 가장 최근
      setDate((d) => {
        if (d && r.cases.some((c) => c.date === d)) return d
        const up = [...r.cases].filter((c) => c.date >= r.today).sort((a, b) => a.date.localeCompare(b.date))[0]
        return (up ?? r.cases[0])?.date ?? ''
      })
    } catch (e) {
      setOnline(false)
      setConnErr((e as Error).message)
    }
  }, [])
  useEffect(() => {
    void loadCases()
  }, [loadCases])

  // 회차의 약품
  const loadRows = useCallback(async () => {
    if (!date) return
    setLoading(true)
    try {
      const r = await call<{ rows: Row[] }>(`/api/case?date=${encodeURIComponent(date)}`)
      setRows(r.rows ?? [])
      setOnline(true)
    } catch (e) {
      setConnErr((e as Error).message)
      setOnline(false)
    } finally {
      setLoading(false)
    }
  }, [date])
  useEffect(() => {
    try {
      localStorage.setItem('dpr-phone-date', date)
    } catch {
      /* 저장 못 해도 됨 */
    }
    void loadRows()
  }, [date, loadRows])

  const sel = rows.find((r) => r.code === code)
  const shown = useMemo(() => rows.filter((r) => !q || r.name.includes(q) || r.code.startsWith(q)), [rows, q])
  const patch = (id: number, p: Partial<Feed>) => setFeed((fs) => fs.map((f) => (f.id === id ? ({ ...f, ...p } as Feed) : f)))

  // ① 사입정보 사진 → PC
  const sendLot = async (files: File[]) => {
    if (!sel) return
    for (const f of files) {
      const id = feedSeq++
      setFeed((fs) => [{ id, kind: 'lot', name: sel.name, at: new Date(), state: 'sending' }, ...fs])
      try {
        const blob = await shrinkForUpload(f)
        const r = await call<{ rows: number; mode: string; error?: string }>('/api/lot-photo', {
          date,
          code: sel.code,
          name: sel.name,
          imageBase64: await fileToBase64(blob),
          mediaType: blob.type || 'image/jpeg',
        })
        patch(id, r.error ? { state: 'error', error: r.error } : { state: 'done', rows: r.rows, mode: r.mode })
      } catch (e) {
        patch(id, { state: 'error', error: (e as Error).message })
      }
    }
  }

  // ② 포장 바코드 사진 → 휴대폰에서 읽기 → PC 의 스캔 기록
  const sendBarcodes = async (files: File[]) => {
    if (!sel) return
    for (const f of files) {
      const id = feedSeq++
      setFeed((fs) => [{ id, kind: 'barcode', name: sel.name, at: new Date(), state: 'sending' }, ...fs])
      try {
        const { decodeImage } = await import('../features/cuts/barcode') // 바코드 판독기(WASM)는 필요할 때만 불러온다
        const codes = (await decodeImage(f)).filter((c) => c.isGS1).map((c) => c.text)
        if (codes.length === 0) {
          patch(id, { state: 'error', found: 0, error: '사진에서 2D 바코드를 찾지 못했습니다. 흔들리지 않게, 바코드가 화면 가운데 크게 나오도록 가까이 찍어 주세요.' })
          continue
        }
        const r = await call<{ results: ScanRes[] }>('/api/barcodes', { date, code: sel.code, raws: codes })
        patch(id, { state: 'done', found: codes.length, results: r.results })
      } catch (e) {
        patch(id, { state: 'error', error: (e as Error).message })
      }
    }
    void loadRows()
  }

  if (!token) {
    return (
      <Shell online={false}>
        <Notice tone="error" title="연결 정보가 없습니다">
          PC 의 DPR 에서 <b>설정 → 휴대폰 연결</b>을 켜고, 화면에 나온 <b>QR 코드</b>를 이 휴대폰 카메라로 찍어 들어오세요.
        </Notice>
      </Shell>
    )
  }

  return (
    <Shell online={online} onRetry={() => void loadCases()}>
      {connErr && (
        <Notice tone="error" title="PC 와 연결되지 않았습니다">
          {connErr}
        </Notice>
      )}

      {!sel ? (
        <>
          <label className="block">
            <span className="mb-1 block text-sm font-semibold text-gray-700">약가인하 회차 (시행일)</span>
            <select value={date} onChange={(e) => setDate(e.target.value)} className="h-12 w-full rounded-xl border border-gray-300 bg-white px-3 text-base">
              {cases.length === 0 && <option value="">보유 품목이 있는 회차가 없습니다</option>}
              {cases.map((c) => (
                <option key={c.date} value={c.date}>
                  {dot(c.date)} 시행 · 보유 인하 {c.held}품목
                </option>
              ))}
            </select>
          </label>

          <div className="relative">
            <Search className="pointer-events-none absolute top-3.5 left-3 size-5 text-gray-400" />
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="약품명 검색" className="h-12 w-full rounded-xl border border-gray-300 bg-white pr-3 pl-10 text-base" />
          </div>

          <p className="text-sm text-gray-600">사진을 찍을 약품을 누르세요. 실계수를 넣은 약품이 위에 있습니다.</p>
          {loading && <Loading />}
          <ul className="divide-y divide-gray-100 overflow-hidden rounded-xl border border-gray-200 bg-white">
            {shown.map((r) => (
              <li key={r.code}>
                <button className="flex w-full items-start gap-3 px-4 py-3 text-left active:bg-blue-50" onClick={() => setCode(r.code)}>
                  <div className="min-w-0 flex-1">
                    <div className="text-[0.9375rem] leading-snug font-semibold text-gray-900">{r.name}</div>
                    <div className="mt-1 flex flex-wrap gap-1.5 text-xs">
                      {r.counted ? <Chip tone="blue">실계수 입력함</Chip> : <Chip>실계수 전</Chip>}
                      {r.lots > 0 ? <Chip tone="green">사입근거 {r.lots}건</Chip> : <Chip>사입근거 없음</Chip>}
                      {r.scanned > 0 && <Chip tone="green">바코드 {r.scanned}개</Chip>}
                    </div>
                  </div>
                  <ChevronLeft className="mt-1 size-5 rotate-180 text-gray-400" />
                </button>
              </li>
            ))}
            {!loading && shown.length === 0 && <li className="px-4 py-6 text-center text-sm text-gray-500">약품이 없습니다</li>}
          </ul>
        </>
      ) : (
        <>
          <button className="inline-flex items-center gap-1 text-sm font-semibold text-gray-600" onClick={() => setCode('')}>
            <ChevronLeft className="size-4" /> 약품 목록
          </button>
          <div className="rounded-xl border border-gray-200 bg-white p-4">
            <div className="text-xs text-gray-500">{dot(date)} 시행 회차</div>
            <div className="mt-0.5 text-lg leading-snug font-bold text-gray-900">{sel.name}</div>
            <div className="mt-2 flex flex-wrap gap-1.5 text-xs">
              {sel.counted ? <Chip tone="blue">실계수 입력함</Chip> : <Chip>실계수 전 (PC에서 입력)</Chip>}
              <Chip tone={sel.lots > 0 ? 'green' : undefined}>사입근거 {sel.lots}건</Chip>
              <Chip tone={sel.scanned > 0 ? 'green' : undefined}>바코드 {sel.scanned}개</Chip>
            </div>
          </div>

          <BigButton icon={<Camera className="size-7" />} title="① 사입정보 사진 찍기" sub="거래명세서 같은 종이 사입 내역 (유효기간·제조번호가 보이게). 이팜 화면은 PC 에서 캡처(Win+Shift+S)해 붙여 넣는 쪽이 정확합니다" onClick={() => lotInput.current?.click()} />
          <button className="-mt-1 w-full text-center text-sm font-medium text-blue-700" onClick={() => lotGallery.current?.click()}>
            <ImageIcon className="mr-1 inline size-4" />
            이미 찍은 사진(갤러리)에서 고르기
          </button>
          <BigButton icon={<ScanLine className="size-7" />} title="② 포장 바코드 찍기" sub="포장의 2D 바코드 — 여러 포장을 한 장에 같이 찍어도 됩니다" onClick={() => bcInput.current?.click()} />

          <input ref={lotInput} type="file" accept="image/*" capture="environment" hidden onChange={(e) => (void sendLot(Array.from(e.target.files ?? [])), (e.target.value = ''))} />
          <input ref={lotGallery} type="file" accept="image/*" multiple hidden onChange={(e) => (void sendLot(Array.from(e.target.files ?? [])), (e.target.value = ''))} />
          <input ref={bcInput} type="file" accept="image/*" capture="environment" hidden onChange={(e) => (void sendBarcodes(Array.from(e.target.files ?? [])), (e.target.value = ''))} />
        </>
      )}

      {feed.length > 0 && (
        <section className="space-y-2">
          <h2 className="text-sm font-bold text-gray-700">보낸 기록</h2>
          {feed.map((f) => (
            <FeedCard key={f.id} f={f} />
          ))}
        </section>
      )}
    </Shell>
  )
}

// ── 작은 부품들 ──────────────────────────────────────────────────────
function Shell({ online, onRetry, children }: { online: boolean | null; onRetry?: () => void; children: React.ReactNode }) {
  return (
    <div className="min-h-dvh bg-gray-50 pb-10">
      <header className="sticky top-0 z-10 flex items-center gap-2 bg-blue-600 px-4 py-3 text-white">
        <span className="text-base font-bold">DPR 휴대폰</span>
        <span className="text-xs opacity-80">약국 PC로 바로 보내기</span>
        <button className="ml-auto inline-flex items-center gap-1 rounded-full bg-white/15 px-2.5 py-1 text-xs" onClick={onRetry}>
          {online === false ? <WifiOff className="size-3.5" /> : <Wifi className="size-3.5" />}
          {online === null ? '연결 중' : online ? 'PC 연결됨' : '연결 안 됨'}
          {onRetry && <RefreshCw className="size-3" />}
        </button>
      </header>
      <main className="mx-auto max-w-xl space-y-4 px-4 pt-4">{children}</main>
    </div>
  )
}

function Notice({ tone, title, children }: { tone: 'error' | 'info'; title: string; children: React.ReactNode }) {
  return (
    <div className={tone === 'error' ? 'rounded-xl border border-red-200 bg-red-50 p-4 text-red-800' : 'rounded-xl border border-blue-100 bg-blue-50 p-4 text-blue-800'}>
      <div className="flex items-center gap-1.5 font-semibold">
        <CircleAlert className="size-4" /> {title}
      </div>
      <div className="mt-1 text-sm leading-relaxed">{children}</div>
    </div>
  )
}

function Chip({ tone, children }: { tone?: 'blue' | 'green'; children: React.ReactNode }) {
  const cls = tone === 'blue' ? 'bg-blue-50 text-blue-700' : tone === 'green' ? 'bg-green-50 text-green-700' : 'bg-gray-100 text-gray-600'
  return <span className={`rounded-full px-2 py-0.5 font-medium ${cls}`}>{children}</span>
}

function BigButton({ icon, title, sub, onClick }: { icon: React.ReactNode; title: string; sub: string; onClick: () => void }) {
  return (
    <button onClick={onClick} className="flex w-full items-center gap-4 rounded-2xl bg-blue-600 px-5 py-5 text-left text-white shadow-sm active:bg-blue-700">
      {icon}
      <span>
        <span className="block text-lg font-bold">{title}</span>
        <span className="mt-0.5 block text-[0.8125rem] opacity-90">{sub}</span>
      </span>
    </button>
  )
}

function Loading({ label = '불러오는 중…' }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 text-sm text-gray-600">
      <Loader2 className="size-4 animate-spin" /> {label}
    </div>
  )
}

function FeedCard({ f }: { f: Feed }) {
  const time = f.at.toLocaleTimeString('ko-KR', { hour: '2-digit', minute: '2-digit' })
  return (
    <div className="rounded-xl border border-gray-200 bg-white p-3 text-sm">
      <div className="flex items-center gap-2">
        <span className="font-semibold">{f.kind === 'lot' ? '사입정보 사진' : '포장 바코드'}</span>
        <span className="truncate text-xs text-gray-500">{f.name}</span>
        <span className="ml-auto text-xs text-gray-400">{time}</span>
      </div>
      {f.state === 'sending' && <Loading label={f.kind === 'lot' ? 'PC로 보내고 글자를 읽는 중… (몇 초)' : '바코드 읽는 중…'} />}
      {f.state === 'error' && <div className="mt-1 text-red-700">{f.error}</div>}
      {f.state === 'done' && f.kind === 'lot' && (
        <div className="mt-1 space-y-1.5">
          <div className="flex items-center gap-1 text-green-700">
            <Check className="size-4" /> PC로 보냈습니다 · {f.rows ?? 0}줄 읽음
          </div>
          {f.mode === 'photo' && <div className="text-xs text-amber-700">휴대폰으로 모니터를 찍은 사진은 제조번호를 자주 틀리게 읽습니다.</div>}
          <div className="text-xs font-medium text-blue-700">읽은 내용은 PC 의 DPR → 3단계 '사입정보 사진 확인' 화면에서 사진과 비교해 확인한 뒤 저장하세요.</div>
        </div>
      )}
      {f.state === 'done' && f.kind === 'barcode' && (
        <div className="mt-1 space-y-1">
          <div className="text-gray-700">바코드 {f.found}개를 읽어 PC 스캔 기록에 보냈습니다</div>
          {(f.results ?? []).map((r, i) => (
            <div key={i} className={`rounded-lg px-2 py-1 text-xs ${r.status === 'added' ? 'bg-green-50 text-green-800' : 'bg-amber-50 text-amber-800'}`}>
              {SCAN_LABEL[r.status] ?? r.status}
              {r.status === 'added' && (r.matched ? ' · 도매 확인됨' : r.unsure ? ' · 도매 확인 필요 (PC에서 지정)' : ' · 도매 못 찾음 (PC에서 확인)')}
              {r.status !== 'added' && r.message && <div className="opacity-80">{r.message}</div>}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
