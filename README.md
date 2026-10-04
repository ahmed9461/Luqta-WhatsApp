# Luqta WhatsApp helper

Independent, replaceable WhatsApp screenshot transport using whatsmeow. No browser,
Chromium, WebView, Selenium, local HTTP listener or developer runtime is required.
The host communicates using private newline-delimited JSON on stdio.

The helper is licensed GPL-3.0-only. Whatsmeow is MPL-2.0 and its Signal dependency
is GPL-3.0. Dependency licenses must accompany binary distributions. This repository
contains only this component; it contains no Luqta application source or credentials.

Build on Windows x64 with official Go 1.27.1 and PowerShell 7:

```powershell
./build.ps1
```

Dependencies are pinned in go.mod/go.sum and verified before build. Output contains
the standalone executable, notices and a source/binary fingerprint. Tests operate
on synthetic local state without WhatsApp authentication or external messages.

The host protects a random 64-byte database key with OS user protection (Windows
DPAPI or a future macOS Keychain adapter). AES-256-XTS protects SQLite and WAL.
The key is passed over private IPC, never command-line arguments/environment.
Passwords, message contents, account identities and QR pairing data are not logged.

Send preparation is durable before relay. Retries reuse the exact protobuf and ID;
only WhatsApp's explicit server acknowledgement confirms completion. Old-transport
ambiguous receipts are retained for reconciliation, never converted into new sends.
WhatsApp is an external service; no universal exactly-once delivery guarantee is made.
