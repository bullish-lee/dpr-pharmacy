import { FileSpreadsheet, Upload } from 'lucide-react'
import { useRef, useState, type ReactNode } from 'react'
import { api, hasBackend } from '../app/api'
import { cx } from './ui'

export async function fileToBase64(file: File): Promise<string> {
  const buf = new Uint8Array(await file.arrayBuffer())
  let bin = ''
  const chunk = 0x8000
  for (let i = 0; i < buf.length; i += chunk) {
    bin += String.fromCharCode(...buf.subarray(i, i + chunk))
  }
  return btoa(bin)
}

/** 파일 끌어다 놓기 + 클릭해서 선택.
 *  onPath 를 주면 데스크탑 앱에서 클릭 시 운영체제 파일 창을 열어 '경로'만 넘긴다 (파일 내용을 화면↔앱 사이로 옮기지 않아 빠르다). */
export function FileDrop({
  onFiles,
  onPath,
  accept = '.xlsx,.xlsm,.csv',
  multiple = false,
  title = '엑셀 파일을 끌어다 놓거나 클릭해서 선택',
  hint,
  compact,
  icon,
  disabled,
}: {
  onFiles: (files: File[]) => void
  onPath?: (path: string, name: string) => void
  accept?: string
  multiple?: boolean
  title?: ReactNode
  hint?: ReactNode
  compact?: boolean
  icon?: ReactNode
  disabled?: boolean
}) {
  const input = useRef<HTMLInputElement>(null)
  const [over, setOver] = useState(false)
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={() => {
        if (disabled) return
        if (onPath && hasBackend()) {
          void api.PickFile(typeof title === 'string' ? title : '파일 선택').then((path) => {
            if (path) onPath(path, path.split(/[\\/]/).pop() ?? path)
          })
          return
        }
        input.current?.click()
      }}
      onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && input.current?.click()}
      onDragOver={(e) => {
        e.preventDefault()
        setOver(true)
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault()
        setOver(false)
        if (disabled) return
        const files = Array.from(e.dataTransfer.files)
        if (files.length) onFiles(multiple ? files : files.slice(0, 1))
      }}
      className={cx(
        'flex cursor-pointer flex-col items-center justify-center gap-1.5 rounded-xl border-2 border-dashed text-center transition-colors',
        compact ? 'px-4 py-4' : 'px-6 py-8',
        over ? 'border-blue-500 bg-blue-50' : 'border-gray-300 bg-gray-50 hover:border-gray-400 hover:bg-gray-0',
        disabled && 'pointer-events-none opacity-50',
      )}
    >
      <span className="text-gray-500">{icon ?? (compact ? <Upload className="size-5" /> : <FileSpreadsheet className="size-7" />)}</span>
      <span className="text-sm font-semibold text-gray-800">{title}</span>
      {hint && <span className="text-xs text-gray-600">{hint}</span>}
      <input
        ref={input}
        type="file"
        hidden
        accept={accept}
        multiple={multiple}
        onChange={(e) => {
          const files = Array.from(e.target.files ?? [])
          if (files.length) onFiles(files)
          e.target.value = ''
        }}
      />
    </div>
  )
}
