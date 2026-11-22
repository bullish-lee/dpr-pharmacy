# Windows 기본 OCR(Windows.Media.Ocr, 한국어)로 이미지를 읽어 단어와 위치를 JSON 배열로 내보낸다.
# 사용: powershell -NoProfile -ExecutionPolicy Bypass -File ocr.ps1 <이미지 경로> [<이미지 경로> ...]
# 인터넷·유료 서비스를 쓰지 않는다 (Windows 10/11 에 들어 있는 기능).
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Paths)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

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

$lang = New-Object Windows.Globalization.Language 'ko'
if (-not [Windows.Media.Ocr.OcrEngine]::IsLanguageSupported($lang)) {
    Write-Output '[{"error":"no_korean"}]'
    exit 0
}
$engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($lang)

$all = @()
foreach ($Path in $Paths) {
    $file = Wait-Op ([Windows.Storage.StorageFile]::GetFileFromPathAsync($Path)) ([Windows.Storage.StorageFile])
    $stream = Wait-Op ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) ([Windows.Storage.Streams.IRandomAccessStream])
    $decoder = Wait-Op ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
    $bitmap = Wait-Op ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
    $result = Wait-Op ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])
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
    $all += [ordered]@{ width = $bitmap.PixelWidth; height = $bitmap.PixelHeight; angle = $angle; lines = $lines }
    $stream.Dispose()
}
Write-Output (ConvertTo-Json @($all) -Depth 7 -Compress)
