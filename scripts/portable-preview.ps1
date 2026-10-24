param([string]$ProfilePath = '', [switch]$Stop, [switch]$NoOpen, [switch]$Offline)
$ErrorActionPreference = 'Stop'
if (-not $ProfilePath) { $ProfilePath = Join-Path $PSScriptRoot 'preview-profile' }
$ProfilePath = [IO.Path]::GetFullPath($ProfilePath)
$runtimePath = Join-Path $ProfilePath 'runtime.json'
$running = $null
if (Test-Path -LiteralPath $runtimePath) {
    try {
        $info = Get-Content -LiteralPath $runtimePath -Raw | ConvertFrom-Json
        if ($info.url -notmatch '^http://127\.0\.0\.1:\d+$') { throw 'Invalid preview runtime address' }
        $ping = Invoke-RestMethod -Uri ($info.url + '/api/v1/runtime/ping') -TimeoutSec 2
        if ($ping.pid -eq $info.pid) { $running = $info }
    } catch { }
}
if ($Stop) {
    if ($running) {
        Invoke-RestMethod -Method Post -Uri ($running.url + '/api/v1/runtime/quit') -Headers @{'X-Gateway-Token'=$running.token} -ContentType 'application/json' -Body '{}' | Out-Null
        Write-Output ('Stopping portable preview PID ' + $running.pid + '; profile retained: ' + $ProfilePath)
    } else { Write-Output 'This preview profile has no running process.' }
    exit
}
if (-not $running) {
    New-Item -ItemType Directory -Path $ProfilePath -Force | Out-Null
    $settingsPath = Join-Path $ProfilePath 'settings.json'
    if (-not (Test-Path -LiteralPath $settingsPath)) {
        $initialSettings = @{network_disabled=[bool]$Offline; headers_paused=[bool]$Offline; core_disabled=$true; core_mount_disabled=$true; serve_data=$false; serve_gateway_data=$false; share_cache=$false; onboarded=$true; lan_discovery=$false; graph_index=$false; cache_blocks=$false; privacy_mode=$true} | ConvertTo-Json
        [IO.File]::WriteAllText($settingsPath, $initialSettings, (New-Object Text.UTF8Encoding($false)))
    }
    $binary = Join-Path $PSScriptRoot 'GatewayClient.exe'
    $arguments = '-data "' + $ProfilePath + '" -background -no-tray -http 127.0.0.1:0'
    $process = Start-Process -FilePath $binary -ArgumentList $arguments -WorkingDirectory $PSScriptRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $ProfilePath 'preview-output.log') -RedirectStandardError (Join-Path $ProfilePath 'preview-errors.log')
    $deadline = [DateTime]::UtcNow.AddSeconds(60)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($process.HasExited) { throw ('Preview exited. See logs in ' + $ProfilePath) }
        try {
            $info = Get-Content -LiteralPath $runtimePath -Raw | ConvertFrom-Json
            if ($info.pid -eq $process.Id) {
                $ping = Invoke-RestMethod -Uri ($info.url + '/api/v1/runtime/ping') -TimeoutSec 2
                if ($ping.pid -eq $process.Id) { $running = $info; break }
            }
        } catch { }
        Start-Sleep -Milliseconds 100
    }
    if (-not $running) { throw ('Preview startup timed out. Profile retained: ' + $ProfilePath) }
}
$browserURL = $running.url + '/?resolve=.gateway'
Write-Output ('Browser: ' + $browserURL)
Write-Output ('Profile: ' + $ProfilePath)
Write-Output ('PID: ' + $running.pid)
if (-not $NoOpen) { Start-Process $browserURL }
