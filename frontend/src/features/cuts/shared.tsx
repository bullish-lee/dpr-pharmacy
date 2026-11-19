import { Barcode, Check, FileCheck2, CircleHelp, AlertTriangle, Scale } from 'lucide-react'
import type { ReactNode } from 'react'
import type { service } from '../../app/api'
import { recommendation, methodLabel } from '../../app/format'
import { Badge } from '../../components/ui'

export function RecBadge({ rec, settle, upside = 0 }: { rec: string; settle?: string; upside?: number }) {
  if (settle === 'done') {
    return (
      <Badge tone="outline" icon={<Check className="size-3.5" />}>
        완료 · {recommendation[rec]?.label ?? rec}
      </Badge>
    )
  }
  const r = recommendation[rec] ?? { label: rec, tone: 'gray' as const, hint: '' }
  const icon = rec === 'physical_review' ? <Barcode className="size-3.5" /> : rec === 'document' ? <FileCheck2 className="size-3.5" /> : undefined
  return (
    <Badge tone={r.tone} icon={icon} title={r.hint}>
      {r.label}
      {rec === 'document' && upside > 0 && <span className="font-medium text-blue-700" title="도매별로 보면 실물이 더 유리한 도매가 있습니다">· 실물 +{new Intl.NumberFormat('ko-KR').format(upside)}</span>}
      {settle === 'planned' && <span className="font-medium opacity-70">· 예정</span>}
      {settle === 'partial' && <span className="font-medium opacity-70">· 일부 완료</span>}
    </Badge>
  )
}

export function MethodBadge({ method }: { method: string }) {
  if (method === 'physical') return <Badge tone="blue" icon={<Barcode className="size-3.5" />}>실물보상</Badge>
  if (method === 'document') return <Badge tone="gray" icon={<FileCheck2 className="size-3.5" />}>서류보상</Badge>
  return <Badge tone="outline">{methodLabel[method] ?? method}</Badge>
}

export function NeedsCheck({ children = '확인 필요', title }: { children?: ReactNode; title?: string }) {
  return (
    <Badge tone="amber" icon={<AlertTriangle className="size-3.5" />} title={title}>
      {children}
    </Badge>
  )
}

export function FlagBadges({ row }: { row: service.RankRow }) {
  return (
    <span className="inline-flex flex-wrap gap-1">
      {row.flexible && <NeedsCheck title="약가유연계약: 실제 상한금액은 요양기관업무포털에서 확인 필요">유연계약</NeedsCheck>}
      {row.unitStatus === 'check' && <NeedsCheck title={row.unitNote || '사입 단가와 고시 가격의 단위가 맞지 않습니다'}>단위 확인</NeedsCheck>}
      {(row.unitStatus === 'auto_unit' || row.unitStatus === 'auto_pack') && (
        <Badge tone="outline" title={`사입 기록을 고시 단위로 자동 환산했습니다 — ${row.unitNote}`}>
          <Scale className="size-3.5" /> 단위 환산
        </Badge>
      )}
      {row.priceSource === 'ceiling' && row.recommendation !== 'check_price' && (
        <Badge tone="outline" title="최근 매입가가 이번 고시의 변경 전 가격보다 높아 변경 전 가격으로 계산합니다 — 이전 인하 전에 산 약이면 그 차이는 이전 회차의 손실입니다">
          <CircleHelp className="size-3.5" /> 변경 전 가격 기준
        </Badge>
      )}
      {row.priceSource === 'notice' && (
        <Badge tone="outline" title="사입 기록이 없어 고시 변경 전 가격을 현재가로 사용">
          <CircleHelp className="size-3.5" /> 매입가 없음
        </Badge>
      )}
    </span>
  )
}

/** '서류 합계 432 ≥ 보유 420 → 서류보상' 같은 한 줄 설명 */
export function decisionSentence(d: { stock: number; totalDocQty: number; recommendation: string; delta: number; scanned?: boolean; physicalUpside?: number; upsideAmount?: number; detail?: string }) {
  const f = (n: number) => new Intl.NumberFormat('ko-KR', { maximumFractionDigits: 2 }).format(n)
  switch (d.recommendation) {
    case 'document':
      return (
        `도매 서류보상 합계 ${f(d.totalDocQty)} ≥ 보유 ${f(d.stock)} → 서류보상으로 손실을 메울 수 있습니다.` +
        ((d.physicalUpside ?? 0) > 0
          ? ` 다만 도매별로 보면 실물보상이 더 유리한 도매가 있어 바코드로 확인하면 ${f(d.physicalUpside!)}개(약 ${f(d.upsideAmount ?? 0)}원)를 더 받을 수 있습니다.`
          : ' 바코드 작업이 필요 없습니다.')
      )
    case 'check_price':
      if (d.detail && d.detail !== 'agreed_price') {
        return '고시에서 변경 후 가격을 읽지 못했습니다(빈칸 등). 고시 원문으로 인하 후 가격을 확인한 뒤 판단하세요. 손실 합계에서는 제외했습니다.'
      }
      return '약가유연계약 품목입니다. 고시에는 표시가만 있어 실제 상한금액(별도합의)을 요양기관업무포털에서 확인한 뒤 판단하세요. 손실 합계에서는 제외했습니다.'
    case 'physical_review':
      return `보유 ${f(d.stock)} > 서류보상 합계 ${f(d.totalDocQty)} → 최대 ${f(d.stock - d.totalDocQty)}개를 더 보상받을 수 있는지 바코드로 도매를 확인하세요`
    case 'no_stock':
      return '보유 재고가 없어 손실이 없습니다'
    case 'no_loss':
      return '현재 매입가가 인하 후 가격 이하라 손실이 없습니다'
  }
  return ''
}
