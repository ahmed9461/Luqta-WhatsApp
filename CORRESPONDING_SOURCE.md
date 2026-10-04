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
