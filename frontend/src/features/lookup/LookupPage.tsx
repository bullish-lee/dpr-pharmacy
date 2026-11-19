import { useQuery } from '@tanstack/react-query'
import { Copy, Search } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import { ClipboardSetText } from '../../../wailsjs/runtime/runtime'
import { api, errorText, keys, type service } from '../../app/api'
import { dateDot, detailLabel, qty } from '../../app/format'
import { navigate, useRoute } from '../../app/router'
import { normalizeTyped } from '../cuts/scanner'
import { Badge, Callout, Card, Empty, Input, PageHeader } from '../../components/ui'

const HOW: Record<string, string> = { code: '제품코드', barcode: '바코드', ingredient: '같은 성분', name: '이름', similar: '비슷한 이름' }
const won = (n: number) => (n ? `${n.toLocaleString('ko-KR', { maximumFractionDigits: 2 })}원` : '—')

/** 받침이 있으면 '을', 없으면 '를' */
const eul = (w: string) => {
  const c = w.charCodeAt(w.length - 1) - 0xac00
  return c >= 0 && c < 11172 && c % 28 ? `${w}을` : `${w}를`
}

/** 한글 입력 상태로 친 코드 되살리기 — 주성분코드(643601ATB → 643601ㅁㅆㅠ)·GS1 바코드만. 약 이름은 그대로 둔다 */
function fixTypedCode(raw: string): string {
  const t = raw.trim()
  if (!/[ㄱ-ㅎㅏ-ㅣ가-힣]/.test(t)) return t
  if (/^\d{6}[A-Za-zㄱ-ㅎㅏ-ㅣ가-힣]{1,3}$/.test(t) || /^(\(01\)|01)\d{14}/.test(t)) return normalizeTyped(t)
  return t
}

function copy(text: string, what: string) {
  ClipboardSetText(text)
    .then((ok) => (ok ? toast.success(`${eul(what)} 복사했습니다: ${text}`) : toast.error('복사하지 못했습니다')))
    .catch(() => toast.error('복사하지 못했습니다'))
}

function CopyBtn({ text, what }: { text: string; what: string }) {
  return (
    <button className="ml-1 inline-flex rounded p-0.5 text-gray-400 hover:bg-gray-100 hover:text-gray-700" title={`${what} 복사`} aria-label={`${what} 복사`} onClick={() => copy(text, what)}>
      <Copy className="size-3.5" />
    </button>
  )
}

/** 약품 조회 — 제품코드·약품명·바코드·주성분코드로 서로 찾기 */
export function LookupPage() {
  const { query } = useRoute()
  const qp = query.get('q') ?? ''
  const cp = query.get('code') ?? ''
  const [q, setQ] = useState(qp)
  const [dq, setDq] = useState(q)
  const [code, setCode] = useState(cp)
  // 주소가 바뀌면(다른 화면의 링크·뒤로 가기) 따라간다 — 이미 이 화면에 있어도
  useEffect(() => {
    setQ(qp)
    setCode(cp)
  }, [qp, cp])
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => {
    const t = setTimeout(() => setDq(fixTypedCode(q)), 250)
    return () => clearTimeout(t)
  }, [q])
  useEffect(() => input.current?.focus(), [])
  const hits = useQuery({ queryKey: [...keys.all, 'lookup', dq], queryFn: () => api.LookupDrugs(dq), enabled: dq !== '' })
  const list = hits.data ?? []
  useEffect(() => {
    // 결과가 하나면(코드·바코드) 바로 자세히 — 이미 고른 약이 있으면 그대로
    if (list.length === 1) setCode((c) => c || list[0].code)
  }, [list])
  const open = (c: string) => {
    setCode(c)
    navigate(`/lookup?q=${encodeURIComponent(q)}&code=${c}`)
  }
  const search = (text: string) => {
    setQ(text)
    setCode('')
  }

  return (
    <div className="space-y-5">
      <PageHeader title="약품 조회" sub="제품코드(9자리)·약품명·바코드(스캐너로 찍어도 됨)·주성분코드를 넣으면 서로 찾아 줍니다. 급여목록·고시·표준코드·약국 기록을 함께 봅니다." />
      <div className="relative max-w-2xl">
        <Search className="pointer-events-none absolute top-3 left-3 size-5 text-gray-400" />
        <Input
          ref={input}
          value={q}
          onChange={(e) => search(e.target.value)}
          onKeyDown={(e) => {
            if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
            const fixed = fixTypedCode(q)
            if (fixed !== q.trim()) search(fixed)
            e.currentTarget.select()
          }}
          placeholder="제품코드(9자리) · 약품명 · 바코드(스캐너) · 주성분코드"
          className="h-11 pl-10 text-base"
        />
      </div>
      <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.25fr)] items-start gap-5 max-[1180px]:grid-cols-1">
        <Card title={dq ? `찾은 약 ${list.length}개` : '찾은 약'} pad={false}>
          {!dq ? (
            <Empty title="찾을 약을 입력하세요">코드·이름·바코드 어느 것이든 됩니다.</Empty>
          ) : hits.isLoading ? (
            <Empty title="찾는 중…" />
          ) : hits.isError ? (
            <Empty title="찾다가 오류가 났습니다">{errorText(hits.error)}</Empty>
          ) : list.length === 0 ? (
            <Empty title="찾지 못했습니다">이름은 일부만 넣어도 됩니다. 바코드는 표준코드 매핑에 있는 것만 찾습니다.</Empty>
          ) : (
            <ul className="max-h-[640px] divide-y divide-gray-100 overflow-y-auto">
              {list.length >= 60 && <li className="bg-gray-50 px-4 py-2 text-xs text-gray-600">앞의 60개만 보여 줍니다 — 더 자세히 넣으면 좁혀집니다.</li>}
              {list.map((h) => (
                <li key={h.code}>
                  <button className={`flex w-full items-start gap-3 px-4 py-2.5 text-left hover:bg-gray-50 ${h.code === code ? 'bg-blue-50' : ''}`} onClick={() => open(h.code)}>
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-medium text-gray-900">{h.name || '(이름 없음)'}</div>
                      <div className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-gray-500">
                        <span className="code">{h.code}</span>
                        {h.company && <span>{h.company}</span>}
                        {h.baseLabel && <span>· {h.baseLabel}</span>}
                      </div>
                    </div>
                    <div className="shrink-0 text-right text-xs">
                      <div className="num text-gray-800">{won(h.price)}</div>
                      <div className="mt-0.5 flex justify-end gap-1">
                        {h.pharmacy && <Badge tone="blue">재고 {qty(h.stock)}</Badge>}
                        {!h.inMaster && <Badge tone="gray">급여목록 없음</Badge>}
                        {h.matchedBy !== 'name' && <Badge tone="outline">{HOW[h.matchedBy] ?? h.matchedBy}</Badge>}
                      </div>
                    </div>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </Card>
        {code ? (
          <DrugCard code={code} onSearch={search} onOpen={open} />
        ) : (
          <Card title="자세히">
            <Empty title="왼쪽에서 약을 고르세요" />
          </Card>
        )}
      </div>
    </div>
  )
}

function DrugCard({ code, onSearch, onOpen }: { code: string; onSearch: (q: string) => void; onOpen: (code: string) => void }) {
  const info = useQuery({ queryKey: [...keys.all, 'drugInfo', code], queryFn: () => api.DrugInfo(code) })
  const d = info.data
  if (info.isLoading)
    return (
      <Card title="자세히">
        <Empty title="불러오는 중…" />
      </Card>
    )
  if (!d)
    return (
      <Card title="자세히">
        <Empty title="정보를 찾지 못했습니다" />
      </Card>
    )
  const facts: [string, React.ReactNode][] = [
    [
      '제품코드',
      <span key="c" className="code">
        {d.code}
        <CopyBtn text={d.code} what="제품코드" />
      </span>,
    ],
    [
      '규격·단위',
      <span key="s">
        {d.spec || '—'}
        {d.baseLabel && <span className="ml-2 text-gray-500">(고시 단위: {d.baseLabel})</span>}
      </span>,
    ],
    [
      '상한금액',
      <span key="p" className="num">
        {won(d.price)}
        {d.listDate && <span className="ml-2 text-xs text-gray-500">급여목록 적용일 {dateDot(d.listDate)}</span>}
      </span>,
    ],
    ['업체', d.company || '—'],
    ['투여·구분', [d.route, d.otc, d.payNote].filter(Boolean).join(' · ') || '—'],
    [
      '주성분코드',
      d.ingredientCode ? (
        <span key="i">
          <button className="code font-semibold text-blue-700 underline" onClick={() => onSearch(d.ingredientCode)} title="같은 성분 제품 모두 찾기">
            {d.ingredientCode}
          </button>
          <CopyBtn text={d.ingredientCode} what="주성분코드" />
        </span>
      ) : (
        '—'
      ),
    ],
  ]
  return (
    <div className="space-y-4">
      <Card
        title={
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate">{d.name}</span>
            <CopyBtn text={d.name} what="약품명" />
          </span>
        }
      >
        {!d.inMaster && (
          <Callout tone="warn" className="mb-3">
            {d.listDate
              ? `지금 급여목록에는 없습니다 — 약국 기록이 있어 ${dateDot(d.listDate)} 목록의 규격·단위를 그대로 씁니다 (삭제·코드 변경 여부는 고시에서 확인).`
              : '지금 급여목록에 없는 코드입니다 (삭제·변경되었거나 급여목록을 아직 받지 않음).'}
          </Callout>
        )}
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
          {facts.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-gray-600">{k}</dt>
              <dd className="min-w-0 break-all">{v}</dd>
            </div>
          ))}
        </dl>
      </Card>

      {d.pharmacy && (
        <Card title="이 약국 기록">
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
            <dt className="text-gray-600">지금 재고</dt>
            <dd className="num">
              {qty(d.stock)} {d.unit}
            </dd>
            <dt className="text-gray-600">월 사용량</dt>
            <dd className="num">{d.monthlyUse ? `${qty(d.monthlyUse)} ${d.unit} (최근 6개월 평균)` : '—'}</dd>
            <dt className="text-gray-600">마지막 사입</dt>
            <dd>{d.lastPurchase ? `${dateDot(d.lastPurchase)} · ${d.lastSupplier || '도매 미상'} · ${won(d.lastPriceUnit)}/${d.unit}` : '—'}</dd>
            <dt className="text-gray-600">마지막 조제</dt>
            <dd>{d.lastDispense ? dateDot(d.lastDispense) : '—'}</dd>
            {d.unitNote && (
              <>
                <dt className="text-gray-600">단위 환산</dt>
                <dd className="text-gray-700">{d.unitNote}</dd>
              </>
            )}
          </dl>
          <a href={`#/inventory?q=${encodeURIComponent(d.code)}`} className="mt-3 inline-block text-[0.8125rem] font-semibold text-blue-700 underline">
            재고 화면에서 보기
          </a>
        </Card>
      )}

      <Card title={`바코드 ${d.barcodes?.length ?? 0}개`} pad={false}>
        {d.barcodes?.length ? (
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-xs text-gray-500">
              <tr>
                <th className="px-4 py-2 text-left font-medium">바코드(GTIN)</th>
                <th className="px-4 py-2 text-right font-medium">포장 1개</th>
                <th className="px-4 py-2 text-left font-medium">출처</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {d.barcodes.map((b) => (
                <tr key={b.gtin}>
                  <td className="code px-4 py-1.5">
                    {b.gtin}
                    <CopyBtn text={b.gtin} what="바코드" />
                  </td>
                  <td className="num px-4 py-1.5 text-right">
                    {b.packQty ? `${qty(b.packQty)}개` : '—'}
                    {b.perBarcode > 0 && (
                      <span className="ml-1 text-xs text-gray-500">
                        = {qty(b.perBarcode)} {d.unit}
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-1.5 text-xs text-gray-600">{b.source === 'official' ? '표준코드' : b.source === 'user' ? '직접 확인' : '추정'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <Empty title="바코드 정보가 없습니다">표준코드 매핑에 없거나 아직 받지 않았습니다.</Empty>
        )}
      </Card>

      <Card title="가격 변화 (고시)" pad={false}>
        {d.prices?.length ? (
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-xs text-gray-500">
              <tr>
                <th className="px-4 py-2 text-left font-medium">시행일</th>
                <th className="px-4 py-2 text-left font-medium">내용</th>
                <th className="px-4 py-2 text-right font-medium">변경 전 → 후</th>
                <th className="px-4 py-2 text-left font-medium">고시</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {d.prices.map((p, i) => (
                <tr key={i}>
                  <td className="num px-4 py-1.5">{dateDot(p.effectiveDate)}</td>
                  <td className="px-4 py-1.5">
                    <Badge tone={p.detail === 'price_down' ? 'red' : p.detail === 'delete' ? 'gray' : 'outline'}>{detailLabel[p.detail] ?? p.detail}</Badge>
                  </td>
                  <td className="num px-4 py-1.5 text-right">{p.before || p.after ? `${won(p.before)} → ${won(p.after)}` : '—'}</td>
                  <td className="px-4 py-1.5 text-xs text-gray-600">{p.noticeNo || '번호 미상'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <Empty title="받은 고시에 이 약의 변경이 없습니다" />
        )}
      </Card>

      {!!d.sameIngredient?.length && (
        <Card title={`같은 성분 다른 제품 ${d.sameIngredient.length}개 (주성분코드 ${d.ingredientCode})`} pad={false}>
          <ul className="max-h-80 divide-y divide-gray-100 overflow-y-auto">
            {d.sameIngredient.map((h: service.DrugHit) => (
              <li key={h.code}>
                <button className="flex w-full items-center gap-3 px-4 py-2 text-left hover:bg-gray-50" onClick={() => onOpen(h.code)}>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm">{h.name}</div>
                    <div className="text-xs text-gray-500">
                      <span className="code">{h.code}</span> {h.company}
                    </div>
                  </div>
                  <div className="num shrink-0 text-sm">{won(h.price)}</div>
                  {h.pharmacy && <Badge tone="blue">약국 품목</Badge>}
                </button>
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  )
}
