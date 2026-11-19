// 숫자·날짜·라벨 표기 규칙 (docs/UI_GUIDE.md)
// - 금액: 천 단위 쉼표, 소수 없음. 표 안에는 단위 생략(헤더에 '(원)'), 문장·카드에는 '원'. ₩ 기호 사용 안 함.
// - 수량: 최대 소수 2자리, 0.5정 등 표시
// - 인하율: 소수 1자리 %
// - 날짜: 표 안 'YYYY.MM.DD', 제목 'M월 D일(요일)'

const wonFmt = new Intl.NumberFormat('ko-KR', { maximumFractionDigits: 0 })
const qtyFmt = new Intl.NumberFormat('ko-KR', { maximumFractionDigits: 2 })
const priceFmt = new Intl.NumberFormat('ko-KR', { maximumFractionDigits: 1 })

export const won = (n: number | undefined | null) => (n == null || Number.isNaN(n) ? '' : wonFmt.format(Math.round(n)))
export const wonUnit = (n: number | undefined | null) => (n == null ? '' : `${won(n)}원`)
export const qty = (n: number | undefined | null) => (n == null || Number.isNaN(n) ? '' : qtyFmt.format(n))
export const price = (n: number | undefined | null) => (n == null || !n ? '' : priceFmt.format(n))
export const pct = (r: number | undefined | null, digits = 1) => (r == null || !Number.isFinite(r) ? '' : `${(r * 100).toFixed(digits)}%`)

export function signedWon(n: number) {
  if (n === 0) return '0원'
  return n > 0 ? `${won(n)}원` : `+${won(-n)}원 이득`
}

const DOW = ['일', '월', '화', '수', '목', '금', '토']

export function parseDate(d: string): Date | null {
  if (!d) return null
  const [y, m, day] = d.slice(0, 10).split('-').map(Number)
  if (!y || !m || !day) return null
  return new Date(y, m - 1, day)
}

export const dateDot = (d?: string) => (d ? d.slice(0, 10).replaceAll('-', '.') : '')

export function dateLong(d?: string) {
  const t = d ? parseDate(d) : null
  if (!t) return ''
  return `${t.getMonth() + 1}월 ${t.getDate()}일(${DOW[t.getDay()]})`
}

export function dateTime(iso?: string) {
  if (!iso) return ''
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return iso
  const p = (n: number) => String(n).padStart(2, '0')
  return `${t.getFullYear()}.${p(t.getMonth() + 1)}.${p(t.getDate())} ${p(t.getHours())}:${p(t.getMinutes())}`
}

export function dday(days: number) {
  if (days === 0) return 'D-DAY'
  return days > 0 ? `D-${days}` : `D+${-days}`
}

export function todayISO() {
  const t = new Date()
  const p = (n: number) => String(n).padStart(2, '0')
  return `${t.getFullYear()}-${p(t.getMonth() + 1)}-${p(t.getDate())}`
}

export type Tone = 'gray' | 'blue' | 'amber' | 'green' | 'red' | 'outline'

export const recommendation: Record<string, { label: string; tone: Tone; hint: string }> = {
  document: { label: '서류보상', tone: 'gray', hint: '도매 자동보상이 보유량 이상 — 바코드 작업 없음' },
  physical_review: { label: '실물보상 검토', tone: 'blue', hint: '보유량이 서류보상보다 많음 — 바코드로 도매 확인' },
  check_price: { label: '실제가 확인 필요', tone: 'amber', hint: '약가유연계약(고시 표시가와 실제 상한금액이 다름 — 요양기관업무포털에서 확인) 또는 고시의 변경 후 가격을 읽지 못한 품목. 손실 합계에서 제외' },
  no_stock: { label: '재고 없음', tone: 'outline', hint: '보유 재고가 없습니다' },
  no_loss: { label: '손실 없음', tone: 'outline', hint: '현재 매입가가 인하 후 가격 이하입니다' },
}

export const detailLabel: Record<string, string> = {
  price_down: '인하',
  price_up: '인상',
  same: '가격 동일',
  agreed_price: '별도합의(약가유연계약)',
  name_change: '제품명 변경',
  unknown: '변경(전 가격 미상)',
  other: '기타 변경',
  new: '신설',
  delete: '삭제',
}

export const kindLabel: Record<string, string> = { new: '신설', change: '변경', delete: '삭제' }

export const methodLabel: Record<string, string> = { document: '서류보상', physical: '실물보상', none: '보상 기준 없음' }

export const basisLabel: Record<string, string> = {
  count: '실계수',
  initial: '초기재고',
  baseline: '기준일 0개',
  none: '기준 없음',
}

export const importKindLabel: Record<string, string> = {
  purchase: '사입현황',
  dispense: '조제현황',
  initial_stock: '초기재고',
  stock_count: '실사 재고',
  lot: '사입 근거',
  notice: '고시',
  drug_master: '약제급여목록',
  gtin: '표준코드',
}
