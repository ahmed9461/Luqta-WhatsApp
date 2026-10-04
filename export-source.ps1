[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$Destination, [string]$GoExecutable = 'go')
$ErrorActionPreference = 'Stop'
$target = [IO.Path]::GetFullPath($Destination)
if (Test-Path -LiteralPath $target) { throw 'Export requires a new directory.' }
New-Item -ItemType Directory -Path $target | Out-Null
# Only this independently licensed helper's reviewed root files are exported.
# No parent Git history, client source, credentials or runtime state is copied.
$files = @(Get-ChildItem -LiteralPath $PSScriptRoot -File | Where-Object {
    $_.Extension -eq '.go' -or $_.Name -in 'go.mod','go.sum','build.ps1','export-source.ps1','LICENSE','README.md','STORE_PROTOCOL.md'
})
foreach ($file in $files) { Copy-Item -LiteralPath $file.FullName -Destination $target }
Push-Location $PSScriptRoot
try {
    $json = (& $GoExecutable mod download -json) -join "`n"
    if ($LASTEXITCODE -ne 0) { throw 'Dependency source download failed.' }
    # Go emits consecutive JSON objects; wrap them as a JSON array.
    $modules = @(('[' + ($json -replace '}\s*{', '},{') + ']') | ConvertFrom-Json)
    # Lazy module loading still reads go.mod files of superseded versions.
    # Include the complete public module graph, not only selected zip files.
    $graph = @(& $GoExecutable mod graph)
    if ($LASTEXITCODE -ne 0) { throw 'Dependency graph unavailable.' }
    $versions = @($graph | ForEach-Object { ($_ -split ' ')[1] } | Where-Object { $_ -notmatch '^(go|toolchain)@' } | Sort-Object -Unique)
    foreach ($version in $versions) {
        $module = (& $GoExecutable mod download -json $version) -join "`n" | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0 -or $module.Error) { throw 'Dependency graph source unavailable.' }
        $modules += $module
    }
    foreach ($tool in @('github.com/ncruces/wasm2go@v0.4.15','golang.org/x/tools@v0.50.0')) {
        $module = (& $GoExecutable mod download -json $tool) -join "`n" | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0 -or $module.Error) { throw 'SQLite generator preferred source unavailable.' }
        $modules += $module
    }
    $cache = (& $GoExecutable env GOMODCACHE).Trim()
    $proxy = Join-Path $target 'dependencies/proxy'
    foreach ($module in $modules) {
        if ($module.Error -or !$module.Zip -or !$module.GoMod -or !$module.Info) { throw 'Incomplete dependency preferred source.' }
        foreach ($path in @($module.Zip, $module.GoMod, $module.Info)) {
            $relative = [IO.Path]::GetRelativePath((Join-Path $cache 'cache/download'), $path)
            if ($relative.StartsWith('..') -or [IO.Path]::IsPathRooted($relative)) { throw 'Dependency cache boundary mismatch.' }
            $copy = Join-Path $proxy $relative
            New-Item -ItemType Directory -Path (Split-Path -Parent $copy) -Force | Out-Null
            Copy-Item -LiteralPath $path -Destination $copy
        }
    }
    $sqlite = Join-Path $target 'dependencies/sqlite-3.53.4'
    New-Item -ItemType Directory -Path $sqlite | Out-Null
    $archive = Join-Path $sqlite 'sqlite-autoconf-3530400.tar.gz'
    Invoke-WebRequest 'https://sqlite.org/2026/sqlite-autoconf-3530400.tar.gz' -OutFile $archive
    $expected = '454e45f61c6bd75b7420e7190732dea03ce6639c63ada47bbc592f67fc340338'
    if ([Convert]::ToHexString([Security.Cryptography.SHA3_256]::HashData([IO.File]::ReadAllBytes($archive))) -ine $expected) { throw 'SQLite preferred-source checksum mismatch.' }
    foreach ($name in 'decimal','ieee754','regexp','series','uint') {
        Invoke-WebRequest "https://raw.githubusercontent.com/sqlite/sqlite/version-3.53.4/ext/misc/$name.c" -OutFile (Join-Path $sqlite "$name.c")
    }
    Invoke-WebRequest 'https://raw.githubusercontent.com/sqlite/sqlite/version-3.53.4/mptest/mptest.c' -OutFile (Join-Path $sqlite 'mptest.c')
    Invoke-WebRequest 'https://raw.githubusercontent.com/sqlite/sqlite/version-3.53.4/test/speedtest1.c' -OutFile (Join-Path $sqlite 'speedtest1.c')
    @'
# Corresponding source

This archive contains the complete Luqta WhatsApp helper source, tests,
installation/build scripts, and exact Go module archives with their notices.
The generated SQLite Go module includes its C wrapper, patches, build recipes
and pinned tool versions. Its original SQLite 3.53.4 amalgamation/extensions
and wasm2go/x-tools generator source are included separately here.

Install official Go 1.27.1 Windows x64 and PowerShell 7. Then run:

    ./build.ps1 -GoExecutable /path/to/go.exe -DependencyProxy ./dependencies/proxy -Output ./output

An empty module cache and disabled network reproduce the helper executable.
Go's standard toolchain is available at https://go.dev/dl/ and its source at
https://go.googlesource.com/go/+/refs/tags/go1.27.1 . Rebuilding the generated
SQLite module uses the included upstream build recipes, WASI SDK 34.0 and
Binaryen 133, with SQLite inputs above. No Luqta application code or user
sessions are included. The helper is GPL-3.0-only; dependency licenses apply
to their respective files. No account credentials are needed to build/test.
'@ | Set-Content -LiteralPath (Join-Path $target 'CORRESPONDING_SOURCE.md') -Encoding utf8NoBOM
    $prefix = $target.TrimEnd('\') + '\'
    $inventory = @(Get-ChildItem -LiteralPath $target -File -Recurse | Sort-Object FullName | ForEach-Object {
        if (!$_.FullName.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)) { throw 'Export boundary mismatch.' }
        [ordered]@{file=[IO.Path]::GetRelativePath($target,$_.FullName).Replace('\','/');sha256=(Get-FileHash -LiteralPath $_.FullName).Hash.ToLowerInvariant()}
    })
    $inventory | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $target 'SOURCE_FILES.json') -Encoding utf8NoBOM
    Write-Output "Independent helper export complete: $($inventory.Count) source/dependency files."
} finally { Pop-Location }
