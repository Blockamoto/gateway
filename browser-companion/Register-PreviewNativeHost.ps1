[CmdletBinding()]
param(
    [string]$PackageRoot = (Split-Path -Parent $PSScriptRoot),
    [ValidateSet('Chrome', 'Edge')][string]$Browser = 'Edge',
    [switch]$Apply,
    [switch]$Remove
)
$ErrorActionPreference = 'Stop'
$previewRoot = [System.IO.Path]::GetFullPath($PackageRoot)
$identity = Get-Content -LiteralPath (Join-Path $previewRoot 'browser-companion\PREVIEW-IDENTITY.json') -Raw | ConvertFrom-Json
if ($identity.host -ne 'com.gateway.preview078' -or $identity.extension_id -notmatch '^[a-p]{32}$') { throw 'Unexpected preview identity.' }
$extensionManifest = Get-Content -LiteralPath (Join-Path $previewRoot 'browser-companion\manifest.json') -Raw | ConvertFrom-Json
if ($extensionManifest.key -ne $identity.key) { throw 'This is not the separately identified preview companion.' }
$hostExe = Join-Path $previewRoot 'GatewayPreviewNativeHost.exe'
if (-not (Test-Path -LiteralPath $hostExe -PathType Leaf) -or -not (Test-Path -LiteralPath (Join-Path $previewRoot 'portable.marker') -PathType Leaf)) { throw 'Preview host and portable.marker are required.' }
if (-not $Remove) {
    $profilePath = Join-Path $previewRoot 'blocks-on-demand-data'
    $runtimeRecord = Get-Content -LiteralPath (Join-Path $profilePath 'runtime.json') -Raw | ConvertFrom-Json
    if ($runtimeRecord.pid -le 0) { throw 'Start this preview with its default portable profile first.' }
    $runtimeProcess = Get-CimInstance Win32_Process -Filter ('ProcessId = ' + [int]$runtimeRecord.pid)
    $clientExe = Join-Path $previewRoot 'GatewayClient.exe'
    if (-not $runtimeProcess -or -not [string]::Equals($runtimeProcess.ExecutablePath, $clientExe, [StringComparison]::OrdinalIgnoreCase)) { throw 'The default profile does not belong to this preview executable.' }
    $dataArgument = [regex]::Match($runtimeProcess.CommandLine, '(?:^|\s)-data(?:=|\s+)(?:"([^"]+)"|([^\s]+))')
    if ($dataArgument.Success) {
        $selectedData = if ($dataArgument.Groups[1].Success) { $dataArgument.Groups[1].Value } else { $dataArgument.Groups[2].Value }
        if (-not [System.IO.Path]::IsPathRooted($selectedData) -or -not [string]::Equals([System.IO.Path]::GetFullPath($selectedData), $profilePath, [StringComparison]::OrdinalIgnoreCase)) { throw 'A custom -data session is standalone only; the native companion cannot select it.' }
    }
    $runtimeUrl = [uri]$runtimeRecord.url
    if ($runtimeUrl.Scheme -ne 'http' -or $runtimeUrl.Host -notin @('127.0.0.1', 'localhost') -or $runtimeUrl.IsDefaultPort -or $runtimeUrl.AbsolutePath -ne '/' -or $runtimeUrl.Query -or $runtimeUrl.Fragment -or $runtimeUrl.UserInfo) { throw 'Unsafe preview runtime URL.' }
    Invoke-RestMethod -Uri ($runtimeRecord.url + '/api/v1/runtime/ping') -TimeoutSec 2 | Out-Null
    Write-Output ('Verified default portable profile: ' + $profilePath + ' (PID ' + $runtimeRecord.pid + ')')
}
$manifestPath = Join-Path $previewRoot 'gateway-preview-native-host.json'
$vendor = if ($Browser -eq 'Edge') { 'Microsoft\Edge' } else { 'Google\Chrome' }
$registryPath = 'HKCU:\Software\' + $vendor + '\NativeMessagingHosts\' + $identity.host
$existing = $null
if (Test-Path -LiteralPath $registryPath) {
    $existingKey = Get-Item -LiteralPath $registryPath
    $existing = [string]$existingKey.GetValue('')
    if (-not [string]::Equals($existing, $manifestPath, [StringComparison]::OrdinalIgnoreCase)) { throw 'The preview host key is already owned by another location; nothing was changed.' }
    if (@(Get-ChildItem -LiteralPath $registryPath).Count -ne 0 -or @($existingKey.GetValueNames() | Where-Object { $_ -ne '' }).Count -ne 0) { throw 'The preview key contains additional data; nothing was changed.' }
}
$operation = if ($Remove) { 'Remove owned preview registration' } else { 'Register separate preview host' }
Write-Output ($operation + ': ' + $registryPath)
Write-Output ('Manifest: ' + $manifestPath)
Write-Output ('Allowed extension: ' + $identity.extension_id)
if (-not $Apply) { Write-Output 'DRY RUN ONLY. Add -Apply to make this explicit current-user change.'; return }
if ($Remove) {
    if ($null -ne $existing) { Remove-Item -LiteralPath $registryPath -Force }
    Write-Output 'Only the matching preview key was removed. Production registrations were not touched.'
    return
}
$manifest = [ordered]@{ name = $identity.host; description = 'Gateway isolated 0.7.8 preview'; path = $hostExe; type = 'stdio'; allowed_origins = @('chrome-extension://' + $identity.extension_id + '/') }
if (Test-Path -LiteralPath $manifestPath) {
    $prior = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    if ($prior.name -ne $identity.host -or $prior.type -ne 'stdio' -or @($prior.allowed_origins).Count -ne 1 -or $prior.allowed_origins[0] -ne $manifest.allowed_origins[0] -or -not [string]::Equals($prior.path, $hostExe, [StringComparison]::OrdinalIgnoreCase)) { throw 'The manifest file is not owned by this exact preview location; nothing was changed.' }
}
[System.IO.File]::WriteAllText($manifestPath, ($manifest | ConvertTo-Json -Depth 4), [System.Text.UTF8Encoding]::new($false))
New-Item -Path $registryPath -Force | Out-Null
Set-Item -LiteralPath $registryPath -Value $manifestPath
Write-Output 'Preview registration written for the current user only. No production host, DNS, policy, or browser profile was changed.'
