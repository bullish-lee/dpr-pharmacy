import { useQuery } from '@tanstack/react-query'
import { Search } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api, keys, obj, type service } from '../app/api'
import { dateTime } from '../app/format'
import { DataTable } from './DataTable'
import { Badge, Empty, Input, Select } from './ui'

export const CHANGE_KIND: Record<string, string> = {
  count: '실계수',
  count_delete: '기준점 삭제',
  unit_factor: '단위 배율',
  pack_size: '바코드 수량',
  initial_resolve: '초기재고 지정',
  initial_ignore: '초기재고 제외',
  baseline: '기준일',
  ack: '확인함',
  unack: '확인 취소',
  window_override: '보상 기간',
  window_clear: '보상 기간 해제',
  import_delete: '가져오기 삭제',
  notice_delete: '고시 삭제',
  data_dir: '데이터 폴더',
  code_change: '제품코드 변경',
  backup_dir: '백업 폴더',
}

/** 수정 기록 표 — productCode 를 주면 그 약만, 아니면 검색·종류 거르기 */
export function ChangeLogTable({ productCode, maxHeight = '520px' }: { productCode?: string; maxHeight?: string }) {
  const [q, setQ] = useState('')
  const [dq, setDq] = useState('')
  const [kind, setKind] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setDq(q), 250)
    return () => clearTimeout(t)
  }, [q])
  const filter = { productCode: productCode ?? '', kind, query: productCode ? '' : dq, limit: 1000 }
  const log = useQuery({ queryKey: [...keys.all, 'changeLog', filter], queryFn: () => api.ChangeLog(obj<service.ChangeFilter>(filter)) })
  return (
    <div className="space-y-3">
      {!productCode && (
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative">
            <Search className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-gray-400" />
            <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="약품명 또는 코드" className="w-64 pl-8" />
          </div>
          <Select value={kind} onChange={(e) => setKind(e.target.value)} className="w-40">
            <option value="">모든 종류</option>
            {Object.entries(CHANGE_KIND).map(([k, l]) => (
              <option key={k} value={k}>
                {l}
              </option>
            ))}
          </Select>
          <span className="text-xs text-gray-500">고친 내역은 지워지지 않고 남습니다.</span>
        </div>
      )}
      <DataTable
        rows={log.data ?? []}
        rowKey={(r) => r.id}
        maxHeight={maxHeight}
        empty={<Empty title={log.isLoading ? '불러오는 중…' : '수정 기록이 없습니다'}>재고·단위·초기재고를 고치면 여기에 남습니다.</Empty>}
        columns={[
          { key: 'at', header: '시각', width: '150px', sort: (r) => r.at, render: (r) => <span className="num text-gray-700">{dateTime(r.at)}</span> },
          { key: 'kind', header: '종류', width: '110px', render: (r) => <Badge tone={r.kind === 'ack' ? 'green' : r.kind.endsWith('delete') || r.kind === 'initial_ignore' ? 'gray' : 'blue'}>{CHANGE_KIND[r.kind] ?? r.kind}</Badge> },
          ...(productCode
            ? []
            : [
                {
                  key: 'name',
                  header: '약품',
                  width: 'minmax(200px,2fr)',
                  render: (r: service.ChangeEntry) =>
                    r.productCode ? (
                      <div className="min-w-0">
                        <div className="truncate">{r.productName || r.productCode}</div>
                        <div className="code text-xs text-gray-500">{r.productCode}</div>
                      </div>
                    ) : (
                      <span className="text-gray-500">—</span>
                    ),
                },
              ]),
          {
            key: 'change',
            header: '이전 → 이후',
            width: 'minmax(200px,2fr)',
            render: (r) => (
              <span className="min-w-0 truncate">
                {r.oldValue && <span className="text-gray-500">{r.oldValue} → </span>}
                <b>{r.newValue}</b>
              </span>
            ),
          },
          { key: 'note', header: '메모', width: 'minmax(180px,2fr)', render: (r) => <span className="truncate text-gray-600" title={r.note}>{r.note}</span> },
          { key: 'src', header: '화면', width: '100px', render: (r) => <span className="text-xs text-gray-500">{r.source}</span> },
        ]}
        defaultSort={{ key: 'at', dir: 'desc' }}
      />
    </div>
  )
}
