import { useCutDates } from '../../app/api'
import { dateDot, dateLong, dday, won } from '../../app/format'
import { navigate, paths } from '../../app/router'
import { DataTable } from '../../components/DataTable'
import { Badge, Empty, ErrorBox, PageHeader, Skeleton } from '../../components/ui'

export function CutsPage() {
  const { data, isLoading, error, refetch } = useCutDates()
  if (isLoading) return <Skeleton rows={8} />
  if (error) return <ErrorBox error={error} onRetry={() => refetch()} />
  // 오늘과 가까운 회차부터 (지난 1년 고시와 1~2년 뒤 시행 예정분까지 있어, 날짜순이면 먼 미래가 맨 위에 온다). 머리글을 누르면 날짜순 등으로 바뀐다
  const rows = [...(data ?? [])].sort((a, b) => Math.abs(a.daysLeft) - Math.abs(b.daysLeft))
  return (
    <div>
      <PageHeader title="약가인하 대응" sub="시행일별로 보유 재고의 손실을 확인하고 도매 보상을 처리합니다." />
      <DataTable
        rows={rows}
        rowKey={(r) => r.effectiveDate}
        onRowClick={(r) => navigate(paths.cut(r.effectiveDate))}
        empty={<Empty title="수집된 약가 고시가 없습니다">'고시' 화면에서 수집 상태를 확인하거나 목록표 파일을 직접 올려주세요.</Empty>}
        columns={[
          {
            key: 'date',
            header: '시행일',
            width: '180px',
            sort: (r) => r.effectiveDate,
            render: (r) => (
              <div className="flex items-center gap-2">
                <span className="num font-semibold" title={dateLong(r.effectiveDate)}>
                  {dateDot(r.effectiveDate)}
                </span>
                <span className={r.daysLeft >= 0 ? 'text-xs font-bold text-blue-700' : 'text-xs text-gray-500'}>{dday(r.daysLeft)}</span>
              </div>
            ),
          },
          { key: 'notice', header: '고시', width: 'minmax(110px,1fr)', render: (r) => <span className="num block truncate text-gray-700">{(r.notices ?? []).map((n) => `제${n}호`).join(', ') || '—'}</span> },
          { key: 'down', header: '인하', width: '80px', align: 'right', sort: (r) => r.down, render: (r) => r.down },
          { key: 'held', header: '보유 인하', width: '100px', align: 'right', sort: (r) => r.heldDown, render: (r) => (r.computed ? <b>{r.heldDown}</b> : '') },
          { key: 'loss', header: '예상 손실(원)', width: '130px', align: 'right', sort: (r) => r.grossLoss, render: (r) => (r.computed ? won(r.grossLoss) : '') },
          { key: 'rec', header: '예상 보상(원)', width: '130px', align: 'right', sort: (r) => r.recovery, render: (r) => (r.computed ? won(r.recovery) : '') },
          { key: 'net', header: '순손실(원)', width: '120px', align: 'right', sort: (r) => r.netLoss, render: (r) => (r.computed ? won(Math.max(0, r.netLoss)) : '') },
          {
            key: 'etc',
            header: '기타',
            width: 'minmax(180px,1fr)',
            render: (r) => (
              <span className="flex flex-wrap gap-1">
                {r.heldDelete > 0 && <Badge tone="red">보유 급여삭제 {r.heldDelete}</Badge>}
                {r.agreed > 0 && <Badge tone="amber">유연계약 {r.agreed}</Badge>}
                {r.up > 0 && <Badge tone="outline">인상 {r.up}</Badge>}
                {r.done > 0 && <Badge tone="outline">처리 완료 {r.done}</Badge>}
              </span>
            ),
          },
        ]}
      />
    </div>
  )
}
