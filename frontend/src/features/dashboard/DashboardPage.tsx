import { ArrowRight, CalendarClock, CircleAlert, RefreshCw, Info } from 'lucide-react'
import { api, useAction, useDashboard, type service } from '../../app/api'
import { dateDot, dateLong, dateTime, dday, qty, won, wonUnit } from '../../app/format'
import { navigate, paths } from '../../app/router'
import { DataTable } from '../../components/DataTable'
import { Badge, Button, Callout, Card, Empty, ErrorBox, Kpi, PageHeader, Skeleton } from '../../components/ui'

export function DashboardPage() {
  const { data, isLoading, error, refetch } = useDashboard()
  const crawl = useAction(() => api.RunCrawl(), {
    success: (r) => (r.skipped ? '다른 수집이 진행 중입니다' : r.new > 0 ? `새 고시 ${r.new}건을 가져왔습니다` : '새 고시가 없습니다'),
  })

  if (isLoading) return <Skeleton rows={10} />
  if (error || !data) return <ErrorBox error={error} onRetry={() => refetch()} />

  const upcoming = data.upcoming ?? []
  // 보유 영향이 있는 가장 가까운 회차(다가오는 것 우선, 없으면 최근 45일 이내 지난 회차)
  const impactful = (u: (typeof upcoming)[number]) => u.heldDown > 0 || u.heldDelete > 0
  const next = upcoming.filter((u) => u.daysLeft >= 0 && impactful(u)).sort((a, b) => a.daysLeft - b.daysLeft)[0]
  const recent = upcoming.filter((u) => u.daysLeft < 0 && u.daysLeft >= -45 && impactful(u)).sort((a, b) => b.daysLeft - a.daysLeft)[0]
  const focus = next ?? recent ?? upcoming.filter((u) => u.daysLeft >= 0).sort((a, b) => a.daysLeft - b.daysLeft)[0] ?? upcoming[0]
  const p = data.pending
  const todo = [
    p.unresolved > 0 && { label: `초기재고 약품 지정 ${p.unresolved}건`, to: paths.inventory() + '?tab=unresolved' },
    p.rulesToReview > 0 && { label: `보상 기준 확인 ${p.rulesToReview}건 (기본값 사용 중)`, to: paths.suppliers() },
    p.ambiguousScans > 0 && { label: `도매 확인이 필요한 바코드 ${p.ambiguousScans}개`, to: focus ? paths.cut(focus.effectiveDate) : paths.cuts() },
    p.planned > 0 && { label: `처리 예정(미완료) ${p.planned}건`, to: paths.reports() },
  ].filter(Boolean) as { label: string; to: string }[]

  return (
    <div className="space-y-5">
      <PageHeader
        title="대시보드"
        sub={`${dateLong(data.today)} · 사입 ${dateDot(data.lastPurchase) || '없음'} · 조제 ${dateDot(data.lastDispense) || '없음'}까지 반영`}
        actions={
          <Button icon={<RefreshCw className="size-4" />} loading={crawl.isPending} onClick={() => crawl.mutate([])}>
            지금 고시 확인
          </Button>
        }
      />

      {data.crawl.stale && (
        <Callout tone="warn" action={<Button size="sm" onClick={() => navigate(paths.settings())}>설정</Button>}>
          최근 3일간 고시 확인에 성공한 기록이 없습니다. 복지부 RSS는 최근 30건만 보여주므로 오래 꺼져 있으면 고시를 놓칠 수 있어요.
          {data.crawl.message && <span className="block text-xs opacity-80">마지막 결과: {data.crawl.message}</span>}
        </Callout>
      )}
      {(data.warnings?.length ?? 0) > 0 && (
        <Callout tone="info">
          <ul className="space-y-0.5">
            {data.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </Callout>
      )}

      <div className="grid grid-cols-4 gap-3 max-[1180px]:grid-cols-2">
        <Kpi
          label={focus && focus.daysLeft < 0 ? '진행 중인 시행일' : '다가오는 시행일'}
          value={focus ? dday(focus.daysLeft) : '—'}
          sub={focus ? `${dateLong(focus.effectiveDate)} 시행` : '수집된 인하 고시 없음'}
          onClick={focus ? () => navigate(paths.cut(focus.effectiveDate)) : undefined}
        />
        <Kpi label="보유 중인 인하 품목" value={focus?.heldDown ?? 0} unit="개" sub={focus ? `전체 인하 ${focus.down}개 중` : undefined} onClick={focus ? () => navigate(paths.cut(focus.effectiveDate)) : undefined} />
        <Kpi label="예상 손실 (보상 전)" value={won(focus?.grossLoss ?? 0)} unit="원" sub={focus ? `예상 보상 ${wonUnit(focus.recovery)}` : undefined} />
        <Kpi
          label="보상 후 예상 순손실"
          value={won(Math.max(0, focus?.netLoss ?? 0))}
          unit="원"
          sub={focus && focus.netLoss < 0 ? `오히려 ${wonUnit(-focus.netLoss)} 이득 예상` : '실계수·바코드 확인 후 정확해집니다'}
        />
      </div>

      <div className="grid grid-cols-[minmax(0,2fr)_minmax(0,1fr)] gap-4 max-[1180px]:grid-cols-1">
        <Card title="시행일별 약가인하" actions={<Button size="sm" variant="ghost" onClick={() => navigate(paths.cuts())}>전체 보기 <ArrowRight className="size-3.5" /></Button>} pad={false}>
          {upcoming.length === 0 ? (
            <Empty title="아직 수집된 약가 고시가 없습니다" action={<Button variant="primary" onClick={() => crawl.mutate([])}>지금 고시 확인</Button>}>
              자동 수집을 기다리거나, '고시' 화면에서 목록표 엑셀을 직접 올릴 수 있어요.
            </Empty>
          ) : (
            <div className="divide-y divide-gray-100">
              {upcoming.map((u) => (
                <button key={u.effectiveDate} onClick={() => navigate(paths.cut(u.effectiveDate))} className="flex w-full items-center gap-4 px-5 py-3.5 text-left hover:bg-gray-50">
                  <div className="flex w-24 shrink-0 flex-col">
                    <span className="num text-[0.9375rem] font-semibold">{dateDot(u.effectiveDate)}</span>
                    <span className={u.daysLeft >= 0 ? 'text-xs font-semibold text-blue-700' : 'text-xs text-gray-500'}>{dday(u.daysLeft)}</span>
                  </div>
                  <div className="min-w-0 flex-1 text-sm text-gray-700">
                    인하 <b className="num">{u.down}</b> · 보유 <b className="num">{u.heldDown}</b>
                    {u.heldDelete > 0 && <span className="ml-2 text-red-700">보유 중 급여삭제 {u.heldDelete}</span>}
                    <div className="truncate text-xs text-gray-500">고시 {(u.notices ?? []).map((n) => `제${n}호`).join(', ') || '번호 미상'}</div>
                  </div>
                  <div className="text-right">
                    <div className="num text-sm font-semibold">{u.computed ? `${won(u.grossLoss)}원` : '—'}</div>
                    <div className="text-xs text-gray-500">예상 손실</div>
                  </div>
                  {u.done > 0 && <Badge tone="outline">처리 {u.done}</Badge>}
                  <ArrowRight className="size-4 text-gray-400" />
                </button>
              ))}
            </div>
          )}
        </Card>

        <div className="space-y-4">
          <Card title="할 일">
            {todo.length === 0 ? (
              <p className="text-sm text-gray-600">지금 처리할 일이 없습니다.</p>
            ) : (
              <ul className="space-y-2">
                {todo.map((t) => (
                  <li key={t.label}>
                    <a href={`#${t.to}`} className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm font-medium text-gray-800 hover:bg-gray-50">
                      <CircleAlert className="size-4 text-amber-600" />
                      {t.label}
                      <ArrowRight className="ml-auto size-3.5 text-gray-400" />
                    </a>
                  </li>
                ))}
              </ul>
            )}
          </Card>
          <HealthCard items={data.health ?? []} />
          <Card title="데이터 상태">
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
              <dt className="text-gray-600">초기재고 기준일</dt>
              <dd className="num">{dateDot(data.baseline) || '없음'}</dd>
              <dt className="text-gray-600">사입현황</dt>
              <dd className="num">{dateDot(data.lastPurchase) || '없음'}</dd>
              <dt className="text-gray-600">조제현황</dt>
              <dd className="num">{dateDot(data.lastDispense) || '없음'}</dd>
              <dt className="text-gray-600">약품 수</dt>
              <dd className="num">{qty(data.products)}</dd>
              <dt className="text-gray-600">고시 확인</dt>
              <dd>{data.crawl.lastRun ? `${dateTime(data.crawl.lastRun)} (${data.crawl.lastStatus === 'ok' ? '성공' : data.crawl.lastStatus})` : '기록 없음'}</dd>
            </dl>
            <Button className="mt-4 w-full" icon={<CalendarClock className="size-4" />} onClick={() => navigate(paths.imports())}>
              최신 이팜 자료 올리기
            </Button>
          </Card>
        </div>
      </div>

      {(data.deletions?.length ?? 0) > 0 && (
        <Card title={<span className="flex items-center gap-2">보유 중인 급여 삭제 품목 <Badge tone="red">{data.deletions.length}</Badge></span>} pad={false}>
          <div className="p-4 pb-0 text-sm text-gray-600">급여 목록에서 삭제되면 유예기간 이후 보험 청구가 불가합니다. 반품·소진 계획을 세우세요.</div>
          <div className="p-4">
            <DataTable
              rows={data.deletions}
              rowKey={(r) => r.code}
              maxHeight="320px"
              columns={[
                { key: 'name', header: '약품명', width: 'minmax(240px,2fr)', render: (r) => <span className="font-medium">{r.name}</span>, sort: (r) => r.name },
                { key: 'stock', header: '재고', width: '90px', align: 'right', render: (r) => qty(r.stock), sort: (r) => r.stock },
                { key: 'value', header: '재고 금액(원)', width: '120px', align: 'right', render: (r) => won(r.value), sort: (r) => r.value },
                { key: 'eff', header: '삭제 시행', width: '110px', render: (r) => <span className="num">{dateDot(r.effectiveDate)}</span> },
                { key: 'grace', header: '급여 유지(유예)', width: '130px', render: (r) => (r.graceUntil ? <span className="num">{dateDot(r.graceUntil)}까지</span> : <Badge tone="red">유예 없음</Badge>) },
                { key: 'no', header: '고시', width: '110px', render: (r) => <span className="num text-gray-600">{r.noticeNo ? `제${r.noticeNo}호` : ''}</span> },
              ]}
              defaultSort={{ key: 'value', dir: 'desc' }}
            />
          </div>
        </Card>
      )}
    </div>
  )
}

/** 데이터 점검: 재고·단위가 이상해 보이는 곳을 자동으로 찾아 보여준다 */
function HealthCard({ items }: { items: service.HealthItem[] }) {
  const warn = items.filter((i) => i.level === 'warn').length
  return (
    <Card
      title={
        <span className="flex items-center gap-2">
          데이터 점검
          {items.length === 0 ? <Badge tone="green">이상 없음</Badge> : warn > 0 ? <Badge tone="amber">확인 {warn}</Badge> : <Badge tone="gray">참고 {items.length}</Badge>}
        </span>
      }
    >
      {items.length === 0 ? (
        <p className="text-sm text-gray-600">재고·단위에서 이상한 점을 찾지 못했습니다. 자료를 새로 올릴 때마다 다시 확인합니다.</p>
      ) : (
        <ul className="space-y-1.5">
          {items.map((it) => (
            <li key={it.key}>
              <a href={`#${it.link}`} className="block rounded-lg px-2 py-1.5 hover:bg-gray-50" title={it.detail}>
                <span className="flex items-center gap-2 text-sm font-medium text-gray-800">
                  {it.level === 'warn' ? <CircleAlert className="size-4 shrink-0 text-amber-600" /> : <Info className="size-4 shrink-0 text-blue-600" />}
                  <span className="min-w-0 flex-1">{it.title}</span>
                  <ArrowRight className="size-3.5 shrink-0 text-gray-400" />
                </span>
                {it.examples?.length > 0 && <span className="mt-0.5 block truncate pl-6 text-xs text-gray-500">{it.examples.join(' · ')}</span>}
              </a>
            </li>
          ))}
        </ul>
      )}
      <a href="#/inventory?tab=history" className="mt-3 block text-right text-xs font-medium text-blue-700 hover:underline">
        고친 내역(수정 기록) 보기 →
      </a>
    </Card>
  )
}
