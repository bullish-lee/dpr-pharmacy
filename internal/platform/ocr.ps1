# Windows 기본 OCR(Windows.Media.Ocr)로 이미지를 읽어 단어와 위치를 JSON 배열로 내보낸다.
# 인터넷·유료 서비스를 쓰지 않는다 (Windows 10/11 에 들어 있는 기능).
# 실행: DPR 이 -EncodedCommand 로 넘긴다 (스크립트 파일을 만들지 않음 — 임시 폴더 스크립트를 막는 보안 정책을 피함).
# 이미지 경로: 환경 변수 DPR_OCR_PATHS (JSON 배열).
# 결과: [{ width, height, angle, engine, warn, error, lines: [{ text, words: [{t,x,y,w,h}] }] }]
#   error: constrained(보안 정책이 PowerShell 을 제한 모드로 돌림) · no_engine(글자 인식 언어 없음) · 그 밖의 문장(그 이미지만 실패)
#   warn : no_korean(한국어 글자 인식이 없어 다른 언어로 읽음 — 숫자·영문은 읽힘)
$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch {}

function Out-Json($v) { Write-Output (ConvertTo-Json @($v) -Depth 7 -Compress) }

if ($ExecutionContext.SessionState.LanguageMode -ne 'FullLanguage') {
    Write-Output ('[{"error":"constrained","mode":"' + $ExecutionContext.SessionState.LanguageMode + '"}]')
    exit 0
}

try {
    $Paths = @(ConvertFrom-Json $env:DPR_OCR_PATHS)

    Add-Type -AssemblyName System.Runtime.WindowsRuntime
    $null = [Windows.Storage.StorageFile, Windows.Storage, ContentType = WindowsRuntime]
    $null = [Windows.Graphics.Imaging.BitmapDecoder, Windows.Graphics.Imaging, ContentType = WindowsRuntime]
    $null = [Windows.Media.Ocr.OcrEngine, Windows.Foundation, ContentType = WindowsRuntime]
    $null = [Windows.Globalization.Language, Windows.Globalization, ContentType = WindowsRuntime]

    $asTask = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
            $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1'
        })[0]
    function Wait-Op($op, [Type]$type) {
        $t = $asTask.MakeGenericMethod($type).Invoke($null, @($op))
        $t.Wait(-1) | Out-Null
        $t.Result
    }

    # 한국어 → 없으면 사용자 언어 → 없으면 설치된 아무 언어 (숫자·영문 제조번호는 어느 언어로도 읽힌다)
    $warn = ''
    $engine = $null
    $lang = New-Object Windows.Globalization.Language 'ko'
    if ([Windows.Media.Ocr.OcrEngine]::IsLanguageSupported($lang)) {
        $engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($lang)
    }
    if ($engine -eq $null) {
        $warn = 'no_korean'
        $engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromUserProfileLanguages()
    }
    if ($engine -eq $null) {
        foreach ($l in [Windows.Media.Ocr.OcrEngine]::AvailableRecognizerLanguages) {
            $engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($l)
            if ($engine -ne $null) { break }
        }
    }
    if ($engine -eq $null) {
        Write-Output '[{"error":"no_engine"}]'
        exit 0
    }
    $engineLang = $engine.RecognizerLanguage.LanguageTag

    $all = @()
    foreach ($Path in $Paths) {
        try {
            $file = Wait-Op ([Windows.Storage.StorageFile]::GetFileFromPathAsync($Path)) ([Windows.Storage.StorageFile])
            $stream = Wait-Op ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) ([Windows.Storage.Streams.IRandomAccessStream])
            try {
                $decoder = Wait-Op ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
                $bitmap = Wait-Op ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
                $result = Wait-Op ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])
            } finally { $stream.Dispose() }
            $lines = @()
            foreach ($l in $result.Lines) {
                $words = @()
                foreach ($w in $l.Words) {
                    $r = $w.BoundingRect
                    $words += [ordered]@{ t = $w.Text; x = [math]::Round($r.X); y = [math]::Round($r.Y); w = [math]::Round($r.Width); h = [math]::Round($r.Height) }
                }
                $lines += [ordered]@{ text = $l.Text; words = $words }
            }
            $angle = 0
            if ($result.TextAngle -ne $null) { $angle = [double]$result.TextAngle }
            $all += [ordered]@{ width = $bitmap.PixelWidth; height = $bitmap.PixelHeight; angle = $angle; engine = $engineLang; warn = $warn; lines = $lines }
        } catch {
            $all += [ordered]@{ error = ('이미지를 읽지 못함: ' + $_.Exception.Message); engine = $engineLang; lines = @() }
        }
    }
    Out-Json $all
} catch {
    Out-Json ([ordered]@{ error = ('글자 인식 준비 실패: ' + $_.Exception.Message) })
}
