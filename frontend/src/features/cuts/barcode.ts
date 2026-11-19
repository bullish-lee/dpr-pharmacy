// 바코드 판독(사진)·생성(도매 앱 등록용). 이 파일은 스캔/등록 화면에서만 지연 로드된다.
import { drawingSVG, gs1datamatrix } from 'bwip-js/browser'
import { prepareZXingModule, readBarcodes } from 'zxing-wasm/reader'
import wasmUrl from 'zxing-wasm/reader/zxing_reader.wasm?url'

// WASM 을 앱에 포함된 파일에서 읽는다 (기본값은 인터넷 CDN → 오프라인에서 실패)
prepareZXingModule({
  overrides: {
    locateFile: (path: string, prefix: string) => (path.endsWith('.wasm') ? wasmUrl : prefix + path),
  },
})

export type Decoded = { text: string; isGS1: boolean }

/** 사진 1장에서 2D 바코드(DataMatrix)를 모두 읽는다. 여러 포장을 한 번에 찍어도 된다. */
export async function decodeImage(file: Blob): Promise<Decoded[]> {
  const results = await readBarcodes(file, {
    formats: ['DataMatrix'],
    tryHarder: true,
    tryRotate: true,
    tryInvert: true,
    tryDownscale: true,
    maxNumberOfSymbols: 40,
    textMode: 'HRI', // GS1 은 (01)…(17)…(10)…(21)… 괄호 표기로
  })
  return results.filter((r) => r.isValid).map((r) => ({ text: r.text, isGS1: String(r.contentType) === 'GS1' || r.text.startsWith('(01)') }))
}

/** GS1 DataMatrix SVG (흰 바탕·여백 포함, 화면에서 휴대폰 앱으로 다시 스캔하기 좋게) */
export function gs1DataMatrixSVG(human: string, scale = 8): string {
  return gs1datamatrix(
    { bcid: 'gs1datamatrix', text: human, scale, padding: 6, backgroundcolor: 'FFFFFF' } as Parameters<typeof gs1datamatrix>[0],
    drawingSVG(),
  )
}
