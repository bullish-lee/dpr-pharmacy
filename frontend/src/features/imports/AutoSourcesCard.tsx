import { useQuery } from '@tanstack/react-query'
import { KeyRound, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { api, keys, queryClient, useAction, useSettings } from '../../app/api'
import { dateTime } from '../../app/format'
import { DataGoKeyDialog } from '../../components/DataGoKeyDialog'
import { Badge, Button, Card } from '../../components/ui'

/** 자동으로 받는 자료 — 고시·표준코드·약제급여목록의 상태와 '지금 확인' */
export function AutoSourcesCard() {
  const s = useSettings()
  const crawl = useQuery({ queryKey: [...keys.all, 'crawlStatus'], queryFn: () => api.CrawlStatus() })
  const [keyOpen, setKeyOpen] = useState(false)
  const refresh = () => queryClient.invalidateQueries({ queryKey: keys.all })
  const crawlNow = useAction(() => api.RunCrawl(), { success: (r) => (r?.skipped ? '다른 확인이 진행 중입니다' : `고시 확인: 대상 ${r?.matched ?? 0}건, 새 고시 ${r?.new ?? 0}건`) })
  const stdNow = useAction(() => api.CheckStdCode(true), { success: (r) => r.message })
  const masterNow = useAction(() => api.CheckDrugMaster(true), { success: (r) => r.message })
  const d = s.data
  const c = crawl.data
  // 지금 목록을 어디서 받았나 (마지막으로 바꾼 곳)
  const masterFrom = d?.masterSource ? (d.masterSource.startsWith('공공데이터포털') ? '자동으로 받음' : '직접 올림') : ''

  const rows: { name: string; what: string; status: React.ReactNode; action: React.ReactNode }[] = [
    {
      name: '복지부 약가 고시',
      what: '「약제 급여 목록 및 급여 상한금액표」 일부개정 — 인하·인상·신설·삭제 품목',
      status: (
        <>
          {c?.enabled ? <Badge tone="green">자동 · {c.times}</Badge> : <Badge tone="gray">꺼짐</Badge>}
          <span className="text-gray-600">
            {c?.lastOk ? `마지막 성공 ${dateTime(c.lastOk)}` : '아직 확인 전'}
            {c?.stale && c?.lastOk && <b className="ml-1 text-amber-700">· 3일 넘게 성공 없음</b>}
          </span>
        </>
      ),
      action: (
        <Button size="sm" icon={<RefreshCw className="size-3.5" />} loading={crawlNow.isPending} onClick={() => crawlNow.mutateAsync([]).then(refresh).catch(() => undefined)}>
          지금 확인
        </Button>
      ),
    },
    {
      name: '표준코드 (바코드 ↔ 보험코드)',
      what: '심평원 약가마스터 의약품표준코드 — 새 판이 나온 날만 받습니다 (1년에 1~4번)',
      status: (
        <>
          {d?.stdAuto ? <Badge tone="green">자동 · 하루 1번</Badge> : <Badge tone="gray">꺼짐</Badge>}
          <span className="text-gray-600">
            {d?.stdCount ? `${d.stdCount.toLocaleString('ko-KR')}건` : '아직 없음'}
            {d?.stdDate && ` (${d.stdDate.replaceAll('-', '.')} 판)`}
            {d?.stdCheckedAt && ` · ${dateTime(d.stdCheckedAt)} 확인`}
          </span>
        </>
      ),
      action: (
        <Button size="sm" icon={<RefreshCw className="size-3.5" />} loading={stdNow.isPending} onClick={() => stdNow.mutateAsync([]).then(refresh).catch(() => undefined)}>
          지금 확인
        </Button>
      ),
    },
    {
      name: '약제급여목록 (전체)',
      what: '심평원 약가기준정보 — 모든 급여 약의 규격·단위·상한금액 (매달)',
      status: d?.hasDataGoKey ? (
        <>
          {d.masterAuto ? <Badge tone="green">자동 · 매달</Badge> : <Badge tone="gray">꺼짐</Badge>}
          {d.masterFailedAt && (
            <Badge tone="red" title={d.masterFailMsg || undefined}>
              {dateTime(d.masterFailedAt)} 실패
            </Badge>
          )}
          <span className="text-gray-600">
            {d.masterCount ? `${d.masterCount.toLocaleString('ko-KR')}품목` : '아직 없음'}
            {d.masterDate && ` (적용일 ${d.masterDate.replaceAll('-', '.')}${masterFrom ? ` · ${masterFrom}` : ''})`}
            {d.masterFetchedAt && ` · ${dateTime(d.masterFetchedAt)} 확인`}
          </span>
          {d.masterFailedAt && d.masterFailMsg && <span className="basis-full text-xs text-red-700">{d.masterFailMsg}</span>}
        </>
      ) : (
        <>
          <Badge tone="amber">인증키 필요</Badge>
          <span className="text-gray-600">
            {d?.masterCount ? `지금 ${d.masterCount.toLocaleString('ko-KR')}품목${masterFrom ? ` (${masterFrom})` : ''} · ` : ''}
            공공데이터포털 무료 인증키를 한 번 넣으면 매달 자동으로 받습니다
          </span>
        </>
      ),
      action: d?.hasDataGoKey ? (
        <Button size="sm" icon={<RefreshCw className="size-3.5" />} loading={masterNow.isPending} onClick={() => masterNow.mutateAsync([]).then(refresh).catch(() => undefined)}>
          지금 받기
        </Button>
      ) : (
        <Button size="sm" variant="primary" icon={<KeyRound className="size-3.5" />} onClick={() => setKeyOpen(true)}>
          인증키 넣기
        </Button>
      ),
    },
  ]

  return (
    <Card title="자동으로 받는 자료" pad={false}>
      <ul className="divide-y divide-gray-100">
        {rows.map((r) => (
          <li key={r.name} className="flex flex-wrap items-center gap-x-4 gap-y-1.5 px-5 py-3 max-[900px]:px-4">
            <div className="min-w-[220px] flex-1">
              <div className="font-semibold text-gray-900">{r.name}</div>
              <div className="text-xs text-gray-500">{r.what}</div>
            </div>
            <div className="flex min-w-[260px] flex-[1.4] flex-wrap items-center gap-2 text-[0.8125rem]">{r.status}</div>
            <div>{r.action}</div>
          </li>
        ))}
      </ul>
      <DataGoKeyDialog open={keyOpen} onClose={() => setKeyOpen(false)} hasKey={d?.hasDataGoKey} />
    </Card>
  )
}
