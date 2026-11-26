import { CalendarClock, FolderOpen, KeyRound } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { api, obj, useAction, useAppInfo, useJobs, useNotifications, useSettings, type platform, type service } from '../../app/api'
import { dateTime } from '../../app/format'
import { DataTable } from '../../components/DataTable'
import { Badge, Button, Callout, Card, Field, Input, PageHeader, Segmented, Switch } from '../../components/ui'
import { ClipboardSetText } from '../../../wailsjs/runtime/runtime'
import { DataFoldersCard } from './DataFoldersCard'
import { DataGoKeyDialog } from '../../components/DataGoKeyDialog'
import { PhoneConnectPanel } from '../../components/PhoneConnect'

const EMPTY_FORM = {
  crawlEnabled: true, crawlTimes: '', crawlInclude: '', crawlExclude: '', zoom: '100', theme: 'light',
  checkOnStart: true, stdAuto: true, updateCheck: true, updateRepo: '', updateToken: '', logDebug: false,
  masterAuto: true, dataGoKey: '',
}
type Form = typeof EMPTY_FORM

/** 저장된 설정 → 화면 칸 (토큰·인증키는 다시 보여주지 않으므로 빈 값 = 그대로 둠) */
const formFrom = (d: service.Settings): Form => ({
  crawlEnabled: d.crawlEnabled, crawlTimes: d.crawlTimes, crawlInclude: d.crawlInclude, crawlExclude: d.crawlExclude,
  zoom: d.zoom || '100', theme: d.theme || 'light', checkOnStart: d.checkOnStart, stdAuto: d.stdAuto,
  updateCheck: d.updateCheck, updateRepo: d.updateRepo, updateToken: '', logDebug: d.logDebug,
  masterAuto: d.masterAuto, dataGoKey: '',
})

export function SettingsPage() {
  const s = useSettings()
  const info = useAppInfo()
  const jobs = useJobs()
  const notes = useNotifications()
  const [form, setForm] = useState<Form>(EMPTY_FORM)
  // 고쳤지만 아직 저장하지 않은 칸 — 설정을 다시 불러와도(자동 작업·다른 화면에서 저장한 뒤) 덮어쓰지 않는다
  const dirty = useRef(new Set<keyof Form>())
  const formRef = useRef(form)
  useEffect(() => {
    formRef.current = form
  })
  const edit = (patch: Partial<Form>) => {
    for (const k of Object.keys(patch) as (keyof Form)[]) dirty.current.add(k)
    setForm((f) => ({ ...f, ...patch }))
  }
  useEffect(() => {
    if (!s.data) return
    const server = formFrom(s.data)
    setForm((f) => {
      const next: Record<string, unknown> = { ...server }
      for (const k of dirty.current) next[k] = f[k]
      return next as Form
    })
  }, [s.data])
  const save = useAction(
    async (f: Form) => {
      await api.SaveSettings(obj<service.SettingsInput>(f))
      // 저장한 값 그대로인 칸만 '저장 안 함'에서 뺀다 (저장하는 사이 또 고친 칸은 남김)
      for (const k of Object.keys(f) as (keyof Form)[]) if (formRef.current[k] === f[k]) dirty.current.delete(k)
    },
    { success: '설정을 저장했습니다' },
  )
  // 한 칸만 바로 저장 (자세한 기록·토큰 지우기) — 다른 칸의 저장 안 한 수정은 함께 보내지 않는다
  const saveOnly = (patch: Partial<Form>) => s.data && save.mutate([{ ...formFrom(s.data), ...patch }])
  const stdNow = useAction(() => api.CheckStdCode(true), { success: (r) => r.message })
  const masterNow = useAction(() => api.CheckDrugMaster(true), { success: (r) => r.message })
  const [keyOpen, setKeyOpen] = useState(false)
  const [upd, setUpd] = useState<service.UpdateInfo | null>(null)
  const updNow = useAction(() => api.CheckUpdate(true), { success: (r) => r.message })
  const updApply = useAction((version: string) => api.ApplyUpdate(version), { success: (r) => r?.message ?? '업데이트했습니다' })
  const [note, setNote] = useState('')
  const [report, setReport] = useState<service.ReportResult | null>(null)
  const [copied, setCopied] = useState('')
  const sendReport = useAction(() => api.SubmitReport(note), { success: (r) => r.message })
  const install = useAction(() => api.InstallWindowsTasks(), { success: (m) => m })
  const remove = useAction(() => api.RemoveWindowsTasks(), { success: 'Windows 작업 스케줄러 등록을 해제했습니다' })
  const readAll = useAction(() => api.MarkNotificationsRead())

  return (
    <div className="space-y-5">
      <PageHeader title="설정" />
      <div className="grid grid-cols-2 gap-4 max-[1180px]:grid-cols-1">
        <Card title="화면">
          <div className="space-y-4">
          <Field label="글자·화면 크기" help="표와 글자가 작게 보이면 키우세요. 바로 적용됩니다.">
            <Segmented
              value={form.zoom}
              onChange={(z) => {
                setForm((f) => ({ ...f, zoom: z }))
                void api.SetUIPrefs(z, form.theme).then(() => s.refetch())
              }}
              items={[
                { value: '87.5', label: '작게' },
                { value: '100', label: '보통' },
                { value: '112.5', label: '조금 크게' },
                { value: '125', label: '크게' },
                { value: '137.5', label: '아주 크게' },
                { value: '150', label: '최대' },
              ]}
            />
          </Field>
          <Field label="테마" help="사이드바 아래 버튼으로도 바꿀 수 있어요. 글자 크기는 Ctrl + / Ctrl − / Ctrl 0">
            <Segmented
              value={form.theme}
              onChange={(t) => {
                setForm((f) => ({ ...f, theme: t }))
                void api.SetUIPrefs(form.zoom, t).then(() => s.refetch())
              }}
              items={[
                { value: 'light', label: '밝게' },
                { value: 'dark', label: '어둡게' },
                { value: 'system', label: 'Windows 설정 따름' },
              ]}
            />
          </Field>
          </div>
        </Card>

        <Card title="자동 확인">
          <div className="space-y-3">
            <p className="text-[0.8125rem] text-gray-600">복지부 고시(약가 변경)와 심평원 표준코드(바코드 매핑) 새 판을 확인합니다. 언제 확인할지 고르세요.</p>
            <Switch checked={form.checkOnStart} onChange={(v) => edit({ checkOnStart: v })} label="프로그램을 켤 때 확인 (30분 안에 다시 켜면 건너뜀)" />
            <Switch checked={form.crawlEnabled} onChange={(v) => edit({ crawlEnabled: v })} label="켜져 있는 동안 정해진 시각에 확인" />
            <Field label="확인 시각" help="쉼표로 구분 (예: 08:30, 12:30, 17:30)">
              <Input value={form.crawlTimes} disabled={!form.crawlEnabled} onChange={(e) => edit({ crawlTimes: e.target.value })} />
            </Field>
            <Switch checked={form.stdAuto} onChange={(v) => edit({ stdAuto: v })} label="표준코드(바코드 ↔ 보험코드) 새 판도 하루 한 번 확인" />
            <div className="flex flex-wrap items-center gap-2 rounded-lg bg-gray-100 px-3 py-2 text-[0.8125rem] text-gray-700">
              <span>
                표준코드 매핑:{' '}
                <b>
                  {s.data?.stdCount ? `${s.data.stdCount.toLocaleString('ko-KR')}건` : '아직 없음'}
                  {s.data?.stdDate && ` (${s.data.stdDate.replaceAll('-', '.')} 판)`}
                </b>
                {s.data?.stdCheckedAt && <span className="text-gray-500"> · {dateTime(s.data.stdCheckedAt)} 확인</span>}
              </span>
              <Button size="sm" variant="ghost" loading={stdNow.isPending} onClick={() => stdNow.mutate([])}>
                지금 확인
              </Button>
            </div>
            <Switch checked={form.masterAuto} onChange={(v) => edit({ masterAuto: v })} label="약제급여목록(심평원 전체 목록)을 매달 자동으로 받기" />
            <div className="flex flex-wrap items-center gap-2 rounded-lg bg-gray-100 px-3 py-2 text-[0.8125rem] text-gray-700">
              <span>
                약제급여목록:{' '}
                <b>
                  {s.data?.masterCount ? `${s.data.masterCount.toLocaleString('ko-KR')}품목` : '아직 없음'}
                  {s.data?.masterDate && ` (적용일 ${s.data.masterDate.replaceAll('-', '.')})`}
                </b>
                {s.data?.masterFetchedAt && <span className="text-gray-500"> · {dateTime(s.data.masterFetchedAt)} 자동 확인</span>}
                {s.data?.masterFailedAt && <span className="ml-1 text-red-700" title={s.data.masterFailMsg || undefined}>· {dateTime(s.data.masterFailedAt)} 받지 못함</span>}
                {s.data?.hasDataGoKey ? <Badge tone="green" className="ml-2">인증키 저장됨</Badge> : <Badge tone="amber" className="ml-2">인증키 필요</Badge>}
              </span>
              <Button size="sm" variant={s.data?.hasDataGoKey ? 'ghost' : 'primary'} icon={<KeyRound className="size-3.5" />} onClick={() => setKeyOpen(true)}>
                {s.data?.hasDataGoKey ? '인증키 바꾸기' : '인증키 넣기'}
              </Button>
              {s.data?.hasDataGoKey && (
                <Button size="sm" variant="ghost" loading={masterNow.isPending} onClick={() => masterNow.mutate([])}>
                  지금 받기
                </Button>
              )}
            </div>
            <DataGoKeyDialog open={keyOpen} onClose={() => setKeyOpen(false)} hasKey={s.data?.hasDataGoKey} />
            <details className="text-[0.8125rem]">
              <summary className="cursor-pointer text-gray-600">고시 제목 조건</summary>
              <div className="mt-2 grid grid-cols-2 gap-3">
                <Field label="제목에 모두 포함" help="| 로 구분">
                  <Input value={form.crawlInclude} onChange={(e) => edit({ crawlInclude: e.target.value })} />
                </Field>
                <Field label="제목에 있으면 제외" help="| 로 구분">
                  <Input value={form.crawlExclude} onChange={(e) => edit({ crawlExclude: e.target.value })} />
                </Field>
              </div>
            </details>
            <Button variant="primary" loading={save.isPending} onClick={() => save.mutate([form])}>
              저장
            </Button>
          </div>
        </Card>

        <Card title="사입정보 사진 판독">
          <p className="text-[0.8125rem] text-gray-600">
            약가인하 회차 3단계(손실 순위 표에서 실계수 입력 후 약품 오른쪽 화살표)에서 사입 이력 사진을 올리면 사입일자·사입도매·수량·유효기간·제조번호를 읽어 줍니다. 글자 인식 두 가지(Windows 기본 + 프로그램에 든 PaddleOCR)로 함께 읽어 칸마다 견주고, 다르게 읽은 칸은 두 값을 보여 주어 고르게 합니다. 모두 이 PC 안에서만 돌아 비용이 들지 않고 사진을 인터넷으로 보내지 않습니다.
            Windows 설정 → 시간 및 언어 → 언어에 '한국어'가 있으면 됩니다. 이팜 화면을 캡처(Win+Shift+S)해 붙여 넣는 방법이 가장 정확하고, 휴대폰 사진은 제조번호를 자주 틀리게 읽으니 꼭 확인하세요.
          </p>
          <OcrCheck />
        </Card>

        <Card title="휴대폰 연결">
          <PhoneConnectPanel />
        </Card>

        <Card title="꺼져 있어도 확인 (Windows 작업 스케줄러)">
          <p className="mb-3 text-sm text-gray-600">
            Windows에 기본으로 있는 '작업 스케줄러'에 등록하면, 프로그램이 꺼져 있어도 위 시각마다 창 없이 고시·표준코드를 확인하고 새 고시가 있으면 알림을 띄웁니다. 직접 등록·해제하며, 자동으로 등록하지 않습니다.
          </p>
          {s.data?.osTasks && <Badge tone="green" className="mb-3">등록됨</Badge>}
          <div className="flex gap-2">
            <Button icon={<CalendarClock className="size-4" />} disabled={!info.data?.isWindows} loading={install.isPending} onClick={() => install.mutate([])}>
              {s.data?.osTasks ? '다시 등록 (시각 변경 반영)' : '작업 스케줄러에 등록'}
            </Button>
            {s.data?.osTasks && (
              <Button variant="ghost" loading={remove.isPending} onClick={() => remove.mutate([])}>
                등록 해제
              </Button>
            )}
          </div>
          {!info.data?.isWindows && <p className="mt-2 text-xs text-gray-500">Windows 에서만 사용할 수 있습니다.</p>}
        </Card>

        <Card title="업데이트">
          <div className="space-y-3">
            <Switch checked={form.updateCheck} onChange={(v) => edit({ updateCheck: v })} label="켤 때 새 버전 확인 (GitHub 릴리즈)" />
            <Field label="받아올 저장소" help="소유자/저장소 — 기본값은 공개 저장소입니다. 포크를 쓸 때만 바꾸세요">
              <Input value={form.updateRepo} onChange={(e) => edit({ updateRepo: e.target.value })} placeholder="bullish-lee/dpr-pharmacy" />
            </Field>
            <Field
              label={<span>GitHub 토큰 {s.data?.hasUpdateToken && <Badge tone="green">저장됨</Badge>}</span>}
              help="공개 저장소에서는 필요 없습니다. 비공개 포크를 쓸 때만 그 저장소의 Fine-grained 토큰(Contents 읽기 + Issues 쓰기)을 넣으세요 (GitHub → Settings → Developer settings). 만료일을 두고 이 용도로만 만드세요."
            >
              <div className="flex gap-2">
                <Input type="password" value={form.updateToken} onChange={(e) => edit({ updateToken: e.target.value })} placeholder={s.data?.hasUpdateToken ? '바꿀 때만 입력' : 'github_pat_… 또는 ghp_…'} />
                {s.data?.hasUpdateToken && (
                  <Button variant="ghost" onClick={() => saveOnly({ updateToken: '-' })}>
                    지우기
                  </Button>
                )}
              </div>
            </Field>
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="primary" loading={save.isPending} onClick={() => save.mutate([form])}>
                저장
              </Button>
              <Button
                loading={updNow.isPending}
                onClick={() => updNow.mutateAsync([]).then((r) => setUpd(r ?? null)).catch(() => undefined)}
              >
                지금 확인
              </Button>
              <span className="text-[0.8125rem] text-gray-600">
                현재 v{info.data?.version}
                {s.data?.updateCheckedAt && ` · ${dateTime(s.data.updateCheckedAt)} 확인`}
              </span>
            </div>
            {upd && (
              <Callout tone={upd.available ? 'info' : 'success'}>
                {upd.message}
                {upd.available && (
                  <div className="mt-2">
                    <Button size="sm" variant="primary" disabled={!info.data?.isWindows} loading={updApply.isPending} onClick={() => window.confirm(`v${upd.latest} 로 업데이트하고 다시 시작할까요?`) && updApply.mutate([upd.latest])}>
                      업데이트하고 다시 시작
                    </Button>
                  </div>
                )}
              </Callout>
            )}
          </div>
        </Card>

        <Card title="오류 보고">
          <div className="space-y-3">
            <p className="text-[0.8125rem] text-gray-600">
              문제가 생기면 무슨 일이 있었는지 적고 보내 주세요. 버전·최근 자동 작업·최근 기록(로그)을 함께 보냅니다. 파일명·폴더 경로는 가리고, <b>약국 거래 자료(사입·조제·재고·가격)는 보내지 않습니다.</b>
            </p>
            <textarea
              value={note}
              onChange={(e) => setNote(e.target.value)}
              rows={3}
              placeholder="예: 사입현황을 올렸더니 '시트를 찾을 수 없음'이 떴어요"
              className="w-full rounded-lg border border-gray-300 bg-gray-0 px-3 py-2 text-sm outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-100"
            />
            <div className="flex items-center gap-2">
              <Button variant="primary" loading={sendReport.isPending} onClick={() => sendReport.mutateAsync([]).then((r) => setReport(r ?? null)).catch(() => undefined)}>
                오류 보고 보내기
              </Button>
              <span className="text-xs text-gray-500">{s.data?.hasUpdateToken ? '토큰으로 바로 이슈를 만듭니다' : 'GitHub 새 이슈 창이 열립니다 (보고서 파일을 끌어다 붙이기)'}</span>
            </div>
            {report && (
              <Callout tone="success">
                {report.message}
                {report.issueUrl && (
                  <button className="ml-1 font-semibold underline" onClick={() => api.OpenURL(report.issueUrl)}>
                    이슈 보기
                  </button>
                )}
                {report.openUrl && (
                  <span className="mt-1 block text-xs">
                    브라우저가 열리지 않았다면{' '}
                    <button className="font-semibold underline" onClick={() => api.OpenURL(report.openUrl)}>
                      다시 열기
                    </button>{' '}
                    ·{' '}
                    <button
                      className="font-semibold underline"
                      onClick={() =>
                        ClipboardSetText(report.openUrl)
                          .then((ok) => setCopied(ok ? '주소를 복사했습니다. 브라우저 주소창에 붙여 넣으세요.' : '복사하지 못했습니다'))
                          .catch(() => setCopied('복사하지 못했습니다'))
                      }
                    >
                      주소 복사
                    </button>
                    {copied && <span className="ml-1 text-gray-600">{copied}</span>}
                  </span>
                )}
              </Callout>
            )}
          </div>
        </Card>

        <DataFoldersCard version={info.data?.version} engine={s.data?.engine} />
      </div>

      <LogCard debug={s.data?.logDebug ?? false} saving={save.isPending} onDebug={(v) => saveOnly({ logDebug: v })} />

      <Card title="자동 확인 기록" pad={false}>
        <div className="p-4">
          <DataTable
            rows={jobs.data ?? []}
            rowKey={(r) => r.id}
            maxHeight="320px"
            columns={[
              { key: 'at', header: '시각', width: '160px', render: (r) => <span className="num">{dateTime(r.startedAt)}</span> },
              { key: 'job', header: '작업', width: '110px', render: (r) => <span className="text-gray-700">{({ crawl: '고시 확인', stdcode: '표준코드', drug_master: '약제급여목록', update: '업데이트' } as Record<string, string>)[r.job] ?? r.job}</span> },
              { key: 'st', header: '결과', width: '100px', render: (r) => <Badge tone={r.status === 'ok' ? 'outline' : r.status === 'running' ? 'blue' : 'red'}>{r.status === 'ok' ? '성공' : r.status === 'running' ? '진행 중' : '오류'}</Badge> },
              { key: 'msg', header: '내용', width: 'minmax(300px,1fr)', render: (r) => <span className="text-gray-700">{r.message}</span> },
            ]}
          />
        </div>
      </Card>

      <Card title="알림" actions={<Button size="sm" variant="ghost" onClick={() => readAll.mutate([])}>모두 읽음</Button>} pad={false}>
        <div className="divide-y divide-gray-100">
          {(notes.data ?? []).length === 0 && <p className="px-5 py-6 text-sm text-gray-600">알림이 없습니다.</p>}
          {(notes.data ?? []).map((n) => (
            <div key={n.id} className="flex gap-3 px-5 py-3 text-sm">
              <Badge tone={n.level === 'error' ? 'red' : n.level === 'warn' ? 'amber' : 'gray'}>{n.level === 'error' ? '오류' : n.level === 'warn' ? '주의' : '알림'}</Badge>
              <div className="min-w-0 flex-1">
                <div className={n.readAt ? 'text-gray-700' : 'font-semibold'}>{n.title}</div>
                <div className="text-[0.8125rem] text-gray-600">{n.body}</div>
              </div>
              <span className="num text-xs whitespace-nowrap text-gray-500">{dateTime(n.createdAt)}</span>
            </div>
          ))}
        </div>
      </Card>

      <p className="text-xs text-gray-500">
        데이터 출처: 보건복지부 「약제 급여 목록 및 급여 상한금액표」 고시, 건강보험심사평가원 약가마스터(의약품표준코드, 공공누리 제1유형). 글꼴: Pretendard (SIL OFL 1.1).
      </p>
    </div>
  )
}

/** 기록(로그) 보기 — 앱·작업 스케줄러 기록의 끝부분, 수준 거르기, 자세한 기록 켜기 */
function LogCard({ debug, saving, onDebug }: { debug: boolean; saving: boolean; onDebug: (v: boolean) => void }) {
  const [file, setFile] = useState('dpr')
  const [level, setLevel] = useState('INFO')
  const logs = useQuery({ queryKey: ['dpr', 'logs', file, level], queryFn: () => api.ReadLogs(file, 300, level) })
  const tone = (l: string) => (l === 'ERROR' ? 'red' : l === 'WARN' ? 'amber' : l === 'DEBUG' ? 'outline' : 'gray')
  return (
    <Card
      title="기록 (로그)"
      actions={
        <div className="flex items-center gap-2">
          <Button size="sm" variant="ghost" onClick={() => logs.refetch()}>
            새로고침
          </Button>
          <Button size="sm" variant="ghost" icon={<FolderOpen className="size-4" />} onClick={() => api.OpenLogFolder()}>
            폴더
          </Button>
        </div>
      }
      pad={false}
    >
      <div className="flex flex-wrap items-center gap-3 border-b border-gray-100 px-4 py-3">
        <Segmented value={file} onChange={setFile} items={[{ value: 'dpr', label: '앱' }, { value: 'dpr-cli', label: '작업 스케줄러' }]} />
        <Segmented
          value={level}
          onChange={setLevel}
          items={[
            { value: 'DEBUG', label: '전부' },
            { value: 'INFO', label: '보통' },
            { value: 'WARN', label: '경고 이상' },
            { value: 'ERROR', label: '오류만' },
          ]}
        />
        <Switch checked={debug} disabled={saving} onChange={onDebug} label="자세한 기록 (문제 찾을 때만)" />
        <span className="ml-auto text-xs text-gray-500">5MB마다 나눠 최근 5개 보관 · 약국 거래 자료는 기록하지 않음</span>
      </div>
      <div className="max-h-[360px] overflow-auto px-4 py-2 font-mono text-[0.75rem] leading-relaxed">
        {(logs.data ?? []).length === 0 && <p className="py-4 text-sm text-gray-600">{logs.isLoading ? '불러오는 중…' : '기록이 없습니다.'}</p>}
        {(logs.data ?? []).map((l, i) => (
          <div key={i} className="flex gap-2 border-b border-gray-50 py-0.5">
            <span className="num shrink-0 text-gray-500">{l.time ? dateTime(l.time) : ''}</span>
            <Badge tone={tone(l.level)}>{l.level}</Badge>
            <span className="min-w-0 break-all text-gray-800">
              {l.msg} <span className="text-gray-500">{l.attrs}</span>
            </span>
          </div>
        ))}
      </div>
    </Card>
  )
}

// 글자 인식 점검: 가상 사입현황 표 그림을 이 PC 에서 읽어 본다 (실제 약국 자료 아님)
function OcrCheck() {
  const [res, setRes] = useState<platform.OCRCheck | null>(null)
  const run = useAction(() => api.CheckOCR(), { success: (r) => (r.ok ? '글자 인식이 잘 됩니다' : '글자 인식에 문제가 있습니다 — 아래 내용을 확인하세요') })
  return (
    <div className="mt-3 space-y-2">
      <Button size="sm" loading={run.isPending} onClick={() => run.mutate([], { onSuccess: setRes })}>
        글자 인식 점검
      </Button>
      {res && (
        <Callout tone={res.ok ? (res.korean ? 'info' : 'warn') : 'danger'}>
          <div className="font-semibold">{res.ok ? `잘 됩니다 — 점검 그림의 제조번호 ${res.found}/3개를 읽었습니다 (${(res.ms / 1000).toFixed(1)}초)` : '글자 인식을 쓸 수 없습니다'}</div>
          {res.problem && <div className="mt-1">{res.problem}</div>}
          <div className="mt-1 text-xs text-gray-600">
            PowerShell: {res.powerShell || '찾지 못함'} · 인식 언어: {res.engine || '—'}
            {res.ok && !res.korean && ' (한국어 없음)'}
          </div>
          {res.paddle && (
            <div className="mt-1 text-xs text-gray-600">
              보조 글자 인식(PaddleOCR):{' '}
              {res.paddle.ok
                ? `잘 됩니다 — 제조번호 ${res.paddle.found}/3개 (${(res.paddle.ms / 1000).toFixed(1)}초, ${res.paddle.runtime || '—'})`
                : `쓸 수 없습니다 — ${res.paddle.problem}`}
            </div>
          )}
          {!res.ok && <div className="mt-1 text-xs text-gray-600">이 화면을 캡처해 보내 주시면 원인을 찾을 수 있습니다. 그동안 사진 옆 표에 직접 입력해 저장할 수 있습니다.</div>}
        </Callout>
      )}
    </div>
  )
}
