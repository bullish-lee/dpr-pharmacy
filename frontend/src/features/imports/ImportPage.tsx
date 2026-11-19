import { CheckCircle2, FileSpreadsheet, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { api, errorText, keys, obj, queryClient, useAction, useImportKinds, useImports, type ingest, type service } from '../../app/api'
import { dateDot, dateTime, importKindLabel, qty, todayISO } from '../../app/format'
import { DataTable } from '../../components/DataTable'
import { FileDrop, fileToBase64 } from '../../components/FileDrop'
import { Badge, Button, Callout, Card, Dialog, Field, Input, PageHeader, Select, Switch, cx } from '../../components/ui'
import { AutoSourcesCard } from './AutoSourcesCard'

// 자동으로 받는 자료 (직접 올리기는 받지 못할 때만)
const AUTO_KINDS = ['notice', 'gtin', 'drug_master']

type Pending = { file: File | { path: string; name: string }; base64: string; req: Partial<service.ImportRequest>; preview: service.ImportPreview }

export function ImportPage() {
  const kinds = useImportKinds()
  const history = useImports()
  const [opts, setOpts] = useState({ baselineDate: '', baselineTiming: 'start', asOf: todayISO(), countTiming: 'end', productCode: '' })
  const [busy, setBusy] = useState<string | null>(null)
  const [result, setResult] = useState<service.ImportResult | null>(null)
  const [mapping, setMapping] = useState<Pending | null>(null)

  useEffect(() => {
    void api.DefaultBaselineDate().then((d) => setOpts((o) => ({ ...o, baselineDate: o.baselineDate || d })))
  }, [])

  const run = async (kind: string, file: File | { path: string; name: string }, extra?: Partial<service.ImportRequest>, base64?: string) => {
    setBusy(kind)
    setResult(null)
    try {
      const isPath = !(file instanceof File)
      const data = isPath ? '' : (base64 ?? (await fileToBase64(file)))
      const req: Partial<service.ImportRequest> = {
        kind,
        fileName: file.name,
        dataBase64: data,
        path: isPath ? file.path : '',
        baselineDate: opts.baselineDate,
        timing: kind === 'initial_stock' ? opts.baselineTiming : kind === 'stock_count' ? opts.countTiming : '',
        asOf: opts.asOf,
        productCode: opts.productCode,
        ...extra,
      }
      const res = await api.Import(obj<service.ImportRequest>(req))
      if (res.needsConfirm) {
        if (window.confirm(`${res.message}\n\n그래도 교체할까요?`)) {
          setBusy(null)
          await run(kind, file, { ...extra, force: true }, data)
        }
        return
      }
      if (res.needsMapping && res.preview) {
        setMapping({ file, base64: data, req, preview: res.preview })
        toast.warning(res.message)
      } else {
        setResult(res)
        toast.success(res.message)
        queryClient.invalidateQueries({ queryKey: keys.all })
      }
    } catch (e) {
      toast.error(errorText(e))
    } finally {
      setBusy(null)
    }
  }

  const del = useAction((id: number) => api.DeleteImport(id), { success: '가져오기 기록과 해당 행을 삭제했습니다' })

  const kindCard = (k: { kind: string; label: string; help: string }) => (
    <Card key={k.kind} title={<span className="flex items-center gap-2"><FileSpreadsheet className="size-4 text-gray-500" />{k.label}</span>}>
      <p className="mb-3 min-h-10 text-[0.8125rem] leading-relaxed text-gray-600">{k.help}</p>
      {k.kind === 'initial_stock' && (
        <div className="mb-3 grid grid-cols-2 gap-2">
          <Field label="기준일 (재고를 센 날)" help="이 파일의 재고를 센 날. 나중에 '재고 → 기준일'에서 언제든 바꿀 수 있어요">
            <Input type="date" value={opts.baselineDate} onChange={(e) => setOpts({ ...opts, baselineDate: e.target.value })} />
          </Field>
          <Field label="센 때" help="영업 전 = 그날 조제 전 수량">
            <Select value={opts.baselineTiming} onChange={(e) => setOpts({ ...opts, baselineTiming: e.target.value })}>
              <option value="start">영업 전</option>
              <option value="end">영업 후</option>
            </Select>
          </Field>
        </div>
      )}
      {k.kind === 'stock_count' && (
        <div className="mb-3 grid grid-cols-2 gap-2">
          <Field label="실계수 날짜" help="실계수 = 약을 직접 세어 본 수량">
            <Input type="date" value={opts.asOf} onChange={(e) => setOpts({ ...opts, asOf: e.target.value })} />
          </Field>
          <Field label="센 때" help="영업 후 = 그날 조제까지 끝난 뒤">
            <Select value={opts.countTiming} onChange={(e) => setOpts({ ...opts, countTiming: e.target.value })}>
              <option value="end">영업 후</option>
              <option value="start">영업 전</option>
            </Select>
          </Field>
        </div>
      )}
      {k.kind === 'lot' && (
        <Field label="대상 약품코드 (파일에 약품코드 열이 없을 때)" className="mb-3">
          <Input value={opts.productCode} onChange={(e) => setOpts({ ...opts, productCode: e.target.value })} placeholder="제품코드 9자리" />
        </Field>
      )}
      <FileDrop
        compact
        accept={k.kind === 'gtin' ? '.csv,.xlsx' : '.xlsx,.xlsm,.csv'}
        title={busy === k.kind ? '가져오는 중…' : '파일을 끌어다 놓거나 클릭'}
        disabled={!!busy}
        onFiles={(f) => void run(k.kind, f[0])}
        onPath={(path, name) => void run(k.kind, { path, name })}
      />
    </Card>
  )

  return (
    <div className="space-y-5">
      <PageHeader
        title="데이터 가져오기"
        sub="고시·표준코드·약제급여목록은 자동으로 받습니다. 약국 자료(사입·조제·재고)는 이팜에서 내려받은 엑셀을 그대로 올리세요 — 열 위치가 달라도 이름으로 알아보고, 모르면 열을 직접 지정할 수 있습니다."
      />

      <AutoSourcesCard />

      <h2 className="pt-1 text-sm font-semibold text-gray-700">약국 자료 올리기</h2>
      <div className="grid grid-cols-3 gap-4 max-[1280px]:grid-cols-2 max-[900px]:grid-cols-1">
        {(kinds.data ?? []).filter((k) => !AUTO_KINDS.includes(k.kind)).map((k) => kindCard(k))}
      </div>

      <details className="rounded-xl border border-gray-200 bg-gray-0 px-4 py-3">
        <summary className="cursor-pointer text-sm font-semibold text-gray-700">자동으로 받지 못할 때 직접 올리기 — 고시 목록표 · 표준코드 · 약제급여목록표</summary>
        <div className="mt-3 grid grid-cols-3 gap-4 max-[1280px]:grid-cols-2 max-[900px]:grid-cols-1">
          {(kinds.data ?? []).filter((k) => AUTO_KINDS.includes(k.kind)).map((k) => kindCard(k))}
        </div>
      </details>

      {result && <ResultCard result={result} />}

      <Card title="가져오기 기록" pad={false}>
        <div className="p-4">
          <DataTable
            rows={history.data ?? []}
            rowKey={(r) => r.id}
            maxHeight="420px"
            columns={[
              { key: 'kind', header: '종류', width: '110px', render: (r) => <Badge tone="gray">{importKindLabel[r.kind] ?? r.kind}</Badge> },
              { key: 'file', header: '파일', width: 'minmax(220px,2fr)', render: (r) => <span className="truncate" title={r.fileName}>{r.fileName}</span> },
              { key: 'range', header: '기간', width: '200px', render: (r) => <span className="num text-gray-700">{r.rangeFrom ? `${dateDot(r.rangeFrom)} ~ ${dateDot(r.rangeTo)}` : ''}</span> },
              { key: 'rows', header: '행', width: '90px', align: 'right', render: (r) => qty(r.rows) },
              { key: 'live', header: '현재 유효', width: '100px', align: 'right', render: (r) => (r.live !== r.rows ? <span className="text-gray-500" title="같은 기간을 다시 올려 일부 교체됨">{qty(r.live)}</span> : qty(r.live)) },
              { key: 'at', header: '가져온 시각', width: '150px', render: (r) => <span className="num text-gray-600">{dateTime(r.createdAt)}</span> },
              {
                key: 'x',
                header: '',
                width: '48px',
                render: (r) =>
                  AUTO_KINDS.includes(r.kind) ? null : ( // 급여목록·표준코드는 목록 전체를 바꾸는 자료라 지우지 않는다 (새 목록으로 바뀜)
                  <button
                    className="text-gray-400 hover:text-red-600"
                    title="이 가져오기로 들어온 행 삭제"
                    onClick={() => confirm(`'${r.fileName}' 가져오기를 삭제할까요? 이 파일로 들어온 행이 모두 지워집니다.`) && del.mutate([r.id])}
                  >
                    <Trash2 className="size-4" />
                  </button>
                ),
              },
            ]}
          />
        </div>
      </Card>

      {mapping && (
        <MappingDialog
          pending={mapping}
          onClose={() => setMapping(null)}
          onSubmit={(overrides, save) => {
            const p = mapping
            setMapping(null)
            void run(p.req.kind!, p.file, { overrides, saveProfile: save }, p.base64)
          }}
        />
      )}
    </div>
  )
}

function ResultCard({ result }: { result: service.ImportResult }) {
  return (
    <Card title={<span className="flex items-center gap-2"><CheckCircle2 className="size-4 text-green-600" />가져오기 결과 — {result.fileName}</span>}>
      <p className="text-sm text-gray-800">{result.message}</p>
      <div className="mt-2 flex flex-wrap gap-4 text-[0.8125rem] text-gray-600">
        <span>처리 {qty(result.rows)}행</span>
        {result.replaced > 0 && <span>기존 {qty(result.replaced)}행 교체</span>}
        {result.products > 0 && <span>약품 {qty(result.products)}개</span>}
        {result.suppliers > 0 && <span>새 거래처 {result.suppliers}곳</span>}
        <span>{(result.durationMs / 1000).toFixed(1)}초</span>
      </div>
      {result.unresolved > 0 && (
        <Callout tone="warn" className="mt-3">
          약품코드를 찾지 못한 행 {result.unresolved}개 — <a className="font-semibold underline" href="#/inventory?tab=unresolved">재고 → 초기재고 확인 필요</a>에서 지정하세요.
        </Callout>
      )}
      {result.issueCount > 0 && (
        <details className="mt-3">
          <summary className="cursor-pointer text-sm font-semibold text-gray-700">확인할 행 {result.issueCount}개 보기</summary>
          <ul className="mt-2 max-h-56 space-y-1 overflow-auto text-[0.8125rem] text-gray-700">
            {(result.issues ?? []).map((is, i) => (
              <li key={i}>
                <span className="text-gray-500">{is.sheet} {is.row}행</span> · {is.message}
              </li>
            ))}
          </ul>
        </details>
      )}
    </Card>
  )
}

const colLetter = (i: number) => {
  let s = ''
  let n = i
  while (n >= 0) {
    s = String.fromCharCode(65 + (n % 26)) + s
    n = Math.floor(n / 26) - 1
  }
  return s
}

function MappingDialog({ pending, onClose, onSubmit }: { pending: Pending; onClose: () => void; onSubmit: (o: ingest.Override[], save: boolean) => void }) {
  const sheets = pending.preview.sheets ?? []
  const schema = pending.preview.schema
  const [active, setActive] = useState(0)
  const [state, setState] = useState(() =>
    sheets.map((s) => ({ use: (pending.preview.usable ?? []).includes(s.sheet) || sheets.length === 1, headerRow: Math.max(0, s.headerRow), mapping: { ...(s.mapping ?? {}) } as Record<string, number> })),
  )
  const [save, setSave] = useState(true)
  const sh = sheets[active]
  const st = state[active]
  const sampleStart = sh ? Math.max(0, sh.headerRow > 3 ? sh.headerRow - 3 : 0) : 0
  const headerCells = useMemo(() => {
    if (!sh) return []
    const row = sh.sample?.[st.headerRow - sampleStart] ?? sh.headers ?? []
    return row
  }, [sh, st, sampleStart])
  const width = Math.max(...(sh?.sample ?? [[]]).map((r) => r.length), headerCells.length, 1)
  const missing = (schema?.fields ?? []).filter((f) => f.required && st && st.mapping[f.key] == null)

  const setField = (key: string, col: string) =>
    setState((arr) =>
      arr.map((x, i) => {
        if (i !== active) return x
        const m = { ...x.mapping }
        if (col === '') delete m[key]
        else m[key] = Number(col)
        return { ...x, mapping: m }
      }),
    )

  return (
    <Dialog
      open
      onClose={onClose}
      width={1080}
      title={`열 지정 — ${pending.file.name}`}
      footer={
        <>
          <Switch checked={save} onChange={setSave} label="이 양식 기억하기 (다음부터 자동)" />
          <span className="flex-1" />
          <Button onClick={onClose}>취소</Button>
          <Button
            variant="primary"
            disabled={!state.some((s) => s.use) || missing.length > 0}
            onClick={() =>
              onSubmit(
                sheets.map((s, i) => ({ sheet: s.sheet, headerRow: state[i].use ? state[i].headerRow : -1, mapping: state[i].mapping })).filter((_, i) => state[i].use) as ingest.Override[],
                save,
              )
            }
          >
            이대로 가져오기
          </Button>
        </>
      }
    >
      {sheets.length > 1 && (
        <div className="mb-4 flex flex-wrap gap-1.5">
          {sheets.map((s, i) => (
            <button
              key={s.sheet}
              onClick={() => setActive(i)}
              className={cx('h-8 rounded-lg border px-3 text-[0.8125rem] font-semibold', i === active ? 'border-blue-600 bg-blue-50 text-blue-700' : 'border-gray-300 bg-gray-0')}
            >
              {s.sheet} {state[i].use ? '✓' : ''}
            </button>
          ))}
        </div>
      )}
      {sh && (
        <div className="space-y-4">
          <div className="flex flex-wrap items-end gap-4">
            <Switch checked={st.use} onChange={(v) => setState((a) => a.map((x, i) => (i === active ? { ...x, use: v } : x)))} label="이 시트 가져오기" />
            <Field label="제목(헤더) 행">
              <Select value={st.headerRow} onChange={(e) => setState((a) => a.map((x, i) => (i === active ? { ...x, headerRow: Number(e.target.value) } : x)))}>
                {(sh.sample ?? []).map((_, i) => (
                  <option key={i} value={sampleStart + i}>
                    {sampleStart + i + 1}행
                  </option>
                ))}
              </Select>
            </Field>
            {missing.length > 0 && <Badge tone="amber">필수: {missing.map((f) => f.label).join(', ')}</Badge>}
          </div>
          <div className="grid grid-cols-3 gap-3 max-[900px]:grid-cols-2">
            {(schema?.fields ?? []).map((f) => (
              <Field key={f.key} label={`${f.label}${f.required ? ' *' : ''}`} help={f.help}>
                <Select value={st.mapping[f.key] ?? ''} onChange={(e) => setField(f.key, e.target.value)}>
                  <option value="">(없음)</option>
                  {Array.from({ length: width }).map((_, i) => (
                    <option key={i} value={i}>
                      {colLetter(i)}열 {headerCells[i] ? `· ${headerCells[i]}` : ''}
                    </option>
                  ))}
                </Select>
              </Field>
            ))}
          </div>
          <div className="overflow-auto rounded-lg border border-gray-200">
            <table className="text-[0.8125rem]">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-2 py-1.5 text-gray-500">#</th>
                  {Array.from({ length: width }).map((_, i) => (
                    <th key={i} className="px-2 py-1.5 text-left font-semibold text-gray-600">
                      {colLetter(i)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {(sh.sample ?? []).map((r, i) => (
                  <tr key={i} className={cx('border-t border-gray-100', sampleStart + i === st.headerRow && 'bg-blue-50 font-semibold')}>
                    <td className="px-2 py-1 text-gray-500">{sampleStart + i + 1}</td>
                    {Array.from({ length: width }).map((_, j) => (
                      <td key={j} className="max-w-[180px] truncate px-2 py-1">
                        {r[j]}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </Dialog>
  )
}
