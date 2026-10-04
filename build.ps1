[CmdletBinding()]
param([string]$Output = (Join-Path $PSScriptRoot 'output'), [string]$GoExecutable = 'go', [string]$DependencyProxy)
$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
$previousCgo = $env:CGO_ENABLED
$previousHelper = $env:LUQTA_WHATSAPP_HELPER
$previousProxy = $env:GOPROXY
$previousSumdb = $env:GOSUMDB
$previousToolchain = $env:GOTOOLCHAIN
try {
    $env:GOTOOLCHAIN = 'local'
    if ($DependencyProxy) {
        $env:GOPROXY = ([Uri]([IO.Path]::GetFullPath($DependencyProxy).TrimEnd('\') + '\')).AbsoluteUri
        $env:GOSUMDB = 'off'
        & $GoExecutable mod download
        if ($LASTEXITCODE -ne 0) { throw 'Offline dependency restore failed.' }
    }
    $goVersion = (& $GoExecutable version)
    if ($LASTEXITCODE -ne 0 -or $goVersion -notmatch 'go1\.27\.1 windows/amd64') { throw 'Use the pinned official Go 1.27.1 Windows x64 SDK.' }
    & $GoExecutable mod verify
    if ($LASTEXITCODE -ne 0) { throw 'Dependency checksum verification failed.' }
    & $GoExecutable test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Helper tests failed.' }
    $env:CGO_ENABLED = '0'
    New-Item -ItemType Directory -Path $Output -Force | Out-Null
    & $GoExecutable build -trimpath -buildvcs=false -ldflags '-s -w' -o (Join-Path $Output 'Luqta.WhatsApp.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Helper build failed.' }
    $env:LUQTA_WHATSAPP_HELPER = Join-Path $Output 'Luqta.WhatsApp.exe'
    & $GoExecutable test -run TestPackagedHelperOfflineLifecycleAndProtectedStorageFailure -count=1 .
    if ($LASTEXITCODE -ne 0) { throw 'Packaged helper lifecycle/protection smoke failed.' }
    $notices = Join-Path $Output 'Notices'
    New-Item -ItemType Directory -Path $notices -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'LICENSE') -Destination (Join-Path $notices 'HELPER-LICENSE.txt')
    # Inventory modules actually linked into this target. The complete module
    # graph includes unused upstream test/tools dependencies without a Dir.
    $modules = @(& $GoExecutable list -deps -f '{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}' . | Where-Object { $_ } | Sort-Object -Unique)
    if ($LASTEXITCODE -ne 0) { throw 'Dependency inventory failed.' }
    $inventory = @()
    foreach ($module in $modules) {
        $parts = $module -split '\|'
        if ($parts[0] -eq 'github.com/ahmed9461/Luqta-WhatsApp') { continue }
        if ($parts.Length -ne 3 -or !$parts[2] -or !(Test-Path -LiteralPath $parts[2] -PathType Container)) { throw "Dependency source directory is unavailable: $($parts[0])" }
        $licenseFiles = @(Get-ChildItem -LiteralPath $parts[2] -File -Recurse | Where-Object { $_.Name -match '^(LICENSE|LICENCE|COPYING|NOTICE)(\..*)?$' })
        if (!$licenseFiles) { throw "Dependency license is missing: $($parts[0])" }
        $moduleNotices = Join-Path $notices ($parts[0] -replace '[^a-zA-Z0-9.-]', '_')
        New-Item -ItemType Directory -Path $moduleNotices -Force | Out-Null
        foreach ($file in $licenseFiles) {
            $relative = [IO.Path]::GetRelativePath($parts[2],$file.FullName) -replace '[\\/]', '_'
            Copy-Item -LiteralPath $file.FullName -Destination (Join-Path $moduleNotices $relative)
        }
        $inventory += [ordered]@{ module=$parts[0]; version=$parts[1] }
    }
    $goRoot = (& $GoExecutable env GOROOT).Trim()
    Copy-Item -LiteralPath (Join-Path $goRoot 'LICENSE') -Destination (Join-Path $notices 'GO-LICENSE.txt')
    $inventory | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $notices 'DEPENDENCIES.json') -Encoding utf8NoBOM
    $source = @(Get-ChildItem -LiteralPath $PSScriptRoot -File | Where-Object { $_.Extension -eq '.go' -or $_.Name -in 'go.mod','go.sum','build.ps1','export-source.ps1','LICENSE','README.md','STORE_PROTOCOL.md' } | Sort-Object Name | ForEach-Object { [ordered]@{file=$_.Name;sha256=(Get-FileHash -LiteralPath $_.FullName).Hash.ToLowerInvariant()} })
    [ordered]@{format=1;toolchain='go1.27.1';helperSha256=(Get-FileHash -LiteralPath (Join-Path $Output 'Luqta.WhatsApp.exe')).Hash.ToLowerInvariant();source=$source} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $Output 'build.json') -Encoding utf8NoBOM
} finally { $env:CGO_ENABLED = $previousCgo; $env:LUQTA_WHATSAPP_HELPER = $previousHelper; $env:GOPROXY = $previousProxy; $env:GOSUMDB = $previousSumdb; $env:GOTOOLCHAIN = $previousToolchain; Pop-Location }
