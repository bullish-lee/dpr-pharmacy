import { ArrowLeft, Download, ExternalLink, RefreshCw, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { api, obj, useAction, useNoticeChanges, useNotices, type service } from '../../app/api'
import { dateDot, dateTime, detailLabel, kindLabel, pct, price, qty } from '../../app/format'
import { Link, navigate, paths } from '../../app/router'
import { DataTable } from '../../components/DataTable'
import { FileDrop, fileToBase64 } from '../../components/FileDrop'
import { Badge, Button, Callout, Card, Empty, ErrorBox, Input, PageHeader, Segmented, Skeleton, Switch } from '../../components/ui'

const statusBadge: Record<string, { label: string; tone: 'gray' | 'green' | 'amber' | 'red' | 'outline' }> = {
  parsed: { label: '분석 완료', tone: 'outline' },
  detected: { label: '감지됨', tone: 'amber' },
  failed: { label: '실패', tone: 'red' },
  no_attachment: { label: '목록표 없음', tone: 'amber' },
}

export function NoticesPage() {
  const { data, isLoading, error, refetch } = useNotices()
  const crawl = useAction(() => api.RunCrawl(), {
    success: (r) => (r.skipped ? '다른 수집이 진행 중입니다' : r.new > 0 ? `새 고시 ${r.new}건을 가져왔습니다` : `확인 완료 — 새 고시 없음 (RSS ${r.checked}건 중 대상 ${r.matched}건)`),
  })
  const upload = useAction(
    async (f: File) => api.Import(obj<service.ImportRequest>({ kind: 'notice', fileName: f.name, dataBase64: await fileToBase64(f) })),
    { success: (r) => r.message },
  )
  const [postLink, setPostLink] = useState('')
  const fromPost = useAction((link: string) => api.ImportNoticePost(link), { success: (r) => r.message })
  if (isLoading) return <Skeleton rows={8} />
  if (error) return <ErrorBox error={error} onRetry={() => refetch()} />
  return (
    <div className="space-y-5">
      <PageHeader
        title="고시"
        sub="보건복지부 「약제 급여 목록 및 급여 상한금액표」 일부개정 고시를 하루 여러 번 자동으로 확인합니다."
        actions={
          <Button variant="primary" icon={<RefreshCw className="size-4" />} loading={crawl.isPending} onClick={() => crawl.mutate([])}>
            지금 확인
          </Button>
        }
      />
      <div className="grid grid-cols-[minmax(0,1fr)_340px] gap-4 max-[1180px]:grid-cols-1">
        <DataTable
          rows={data ?? []}
          rowKey={(r) => r.id}
          onRowClick={(r) => navigate(paths.notice(r.id))}
          empty={<Empty title="수집된 고시가 없습니다">'지금 확인'을 누르거나 오른쪽에 목록표 엑셀을 올려주세요.</Empty>}
          columns={[
            { key: 'no', header: '고시', width: '120px', render: (r) => <span className="num font-semibold">{r.noticeNo ? `제${r.noticeNo}호` : '번호 미상'}</span> },
            { key: 'title', header: '제목 / 파일', width: 'minmax(220px,2fr)', render: (r) => <span className="truncate text-gray-700" title={r.title || r.fileName}>{r.title || r.fileName}</span> },
            { key: 'pub', header: '게시일', width: '110px', sort: (r) => r.publishedAt, render: (r) => <span className="num">{dateDot(r.publishedAt)}</span> },
            { key: 'eff', header: '시행일', width: '150px', render: (r) => <span className="num text-[0.8125rem]">{(r.dates ?? []).map(dateDot).join(', ')}</span> },
            { key: 'cnt', header: '인하 · 인상 · 신설 · 삭제', width: '200px', render: (r) => <span className="num text-gray-700">{r.counts?.price_down ?? 0} · {r.counts?.price_up ?? 0} · {r.counts?.new ?? 0} · {r.counts?.delete ?? 0}</span> },
            {
              key: 'held',
              header: '보유 영향',
              width: '160px',
              render: (r) => (
                <span className="flex gap-1">
                  {r.heldDown > 0 && <Badge tone="blue">인하 {r.heldDown}</Badge>}
                  {r.heldDelete > 0 && <Badge tone="red">삭제 {r.heldDelete}</Badge>}
                </span>
              ),
            },
            {
              key: 'st',
              header: '상태',
              width: '110px',
              render: (r) => {
                const b = statusBadge[r.status] ?? { label: r.status, tone: 'gray' as const }
                return <Badge tone={b.tone} title={r.error}>{b.label}</Badge>
              },
            },
            { key: 'src', header: '출처', width: '80px', render: (r) => <span className="text-gray-600">{r.source === 'mohw' ? '자동' : '업로드'}</span> },
          ]}
          defaultSort={{ key: 'pub', dir: 'desc' }}
        />
        <div className="space-y-4">
          <Card title="목록표 직접 올리기">
            <FileDrop
              title="고시 '별표1 목록표' 엑셀"
              hint="자동 수집이 실패했거나 도매에서 받은 파일이 있을 때"
              onFiles={(f) => upload.mutate([f[0]])}
              disabled={upload.isPending}
            />
          </Card>
          <Card title="지난 고시 가져오기">
            <form
              className="space-y-2"
              onSubmit={(e) => {
                e.preventDefault()
                if (!postLink.trim()) return
                fromPost.mutate([postLink.trim()], {
                  onSuccess: (r) => {
                    setPostLink('')
                    if (r.noticeId) navigate(paths.notice(r.noticeId))
                  },
                })
              }}
            >
              <Input value={postLink} onChange={(e) => setPostLink(e.target.value)} placeholder="복지부 게시글 주소 또는 번호 (list_no)" aria-label="복지부 게시글 주소" />
              <Button type="submit" size="sm" icon={<Download className="size-3.5" />} loading={fromPost.isPending} disabled={!postLink.trim()}>
                가져오기
              </Button>
              <p className="text-xs text-gray-500">
                자동 확인은 최근 게시글만 봅니다. 예전에 고시됐지만 시행일이 남은 인하(예: 제2025-188호의 2026.11.23. 시행분)는 그 게시글 주소를 넣어 가져오세요. 누를 때만 글 하나를 받습니다.
              </p>
            </form>
          </Card>
          {(data ?? []).some((n) => n.status === 'failed') && (
            <Callout tone="danger">
              수집에 실패한 고시가 있습니다. 게시판에서 목록표 엑셀을 내려받아 위에 올려주세요.
            </Callout>
          )}
        </div>
      </div>
    </div>
  )
}

type Group = 'down' | 'change' | 'new' | 'delete' | 'all'

export function NoticeDetailPage({ id }: { id: number }) {
  const notices = useNotices()
  const n = notices.data?.find((x) => x.id === id)
  const [group, setGroup] = useState<Group>('down')
  const [held, setHeld] = useState(false)
  const changes = useNoticeChanges(id, group, held)
  const del = useAction(() => api.DeleteNotice(id), { success: '고시를 삭제했습니다' })
  return (
    <div className="space-y-5">
      <PageHeader
        back={
          <Link to={paths.notices()} className="inline-flex items-center gap-1 text-[0.8125rem] font-semibold text-gray-600 hover:text-gray-900">
            <ArrowLeft className="size-3.5" /> 고시 목록
          </Link>
        }
        title={n ? (n.noticeNo ? `고시 제${n.noticeNo}호` : n.fileName) : '고시'}
        sub={n && `${n.title || n.fileName} · 게시 ${dateDot(n.publishedAt)} · 가져온 시각 ${dateTime(n.createdAt)}`}
        actions={
          <>
            {n?.url && (
              <Button icon={<ExternalLink className="size-4" />} onClick={() => api.OpenURL(n.url)}>
                원문 보기
              </Button>
            )}
            <Button
              variant="danger"
              icon={<Trash2 className="size-4" />}
              onClick={() => {
                if (confirm('이 고시와 분석 결과를 삭제할까요? (다시 수집하거나 올릴 수 있습니다)')) del.mutate([], { onSuccess: () => navigate(paths.notices()) })
              }}
            >
              삭제
            </Button>
          </>
        }
      />
      {(n?.warnings ?? []).map((w) => (
        <Callout key={w} tone="warn">
          {w}
        </Callout>
      ))}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Segmented<Group>
          value={group}
          onChange={setGroup}
          items={[
            { value: 'down', label: '인하', count: n?.counts?.price_down },
            { value: 'change', label: '변경 전체' },
            { value: 'new', label: '신설', count: n?.counts?.new },
            { value: 'delete', label: '삭제', count: n?.counts?.delete },
            { value: 'all', label: '전체' },
          ]}
        />
        <Switch checked={held} onChange={setHeld} label="보유 품목만" />
      </div>
      {changes.error ? (
        <ErrorBox error={changes.error} />
      ) : (
        <DataTable
          rows={changes.data ?? []}
          rowKey={(r) => r.id}
          maxHeight="calc(100vh - 300px)"
          empty={<Empty title={changes.isLoading ? '불러오는 중…' : '해당 품목이 없습니다'} />}
          onRowClick={(r) => r.detail === 'price_down' && r.held && navigate(paths.product(r.effectiveDate, r.code))}
          rowClassName={(r) => (r.held ? undefined : 'text-gray-600')}
          columns={[
            {
              key: 'name',
              header: '제품명',
              width: 'minmax(280px,3fr)',
              sort: (r) => r.name,
              render: (r) => (
                <div className="min-w-0">
                  <div className="truncate font-medium" title={r.name}>
                    {r.efarmName || r.name}
                  </div>
                  <div className="truncate text-xs text-gray-500">
                    <span className="code">{r.code}</span> · {r.company}
                  </div>
                </div>
              ),
            },
            {
              key: 'kind',
              header: '구분',
              width: '150px',
              render: (r) => (
                <span className="flex flex-wrap gap-1">
                  <Badge tone={r.kind === 'delete' ? 'red' : r.detail === 'price_down' ? 'blue' : 'gray'}>{r.kind === 'change' ? detailLabel[r.detail] ?? r.detail : kindLabel[r.kind]}</Badge>
                  {r.flexible && <Badge tone="amber">유연계약</Badge>}
                </span>
              ),
            },
            { key: 'price', header: '상한금액 전 → 후', width: '170px', align: 'right', render: (r) => (r.priceBefore || r.priceAfter ? `${price(r.priceBefore)} → ${price(r.priceAfter)}` : '') },
            { key: 'rate', header: '인하율', width: '80px', align: 'right', sort: (r) => r.rate, render: (r) => (r.rate > 0 ? pct(r.rate) : '') },
            { key: 'unit', header: '규격/단위', width: '110px', render: (r) => <span className="text-gray-600">{r.spec} {r.unit}</span> },
            { key: 'eff', header: '시행일', width: '110px', render: (r) => <span className="num">{dateDot(r.effectiveDate)}</span> },
            { key: 'grace', header: '유예', width: '110px', render: (r) => <span className="num">{dateDot(r.graceUntil)}</span> },
            { key: 'stock', header: '재고', width: '90px', align: 'right', sort: (r) => r.stock, render: (r) => (r.held ? <b>{qty(r.stock)}</b> : '') },
            { key: 'note', header: '비고', width: 'minmax(160px,2fr)', render: (r) => <span className="truncate text-xs text-gray-600" title={r.note}>{r.note}</span> },
          ]}
        />
      )}
    </div>
  )
}
