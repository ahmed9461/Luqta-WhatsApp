# Private stdio protocol

One UTF-8 JSON object per line. Parent commands contain `id`, `method`, and `args`. Responses repeat `id` and contain either `result` or a safe `error` category. Binary values use base64 strings. Maximum accepted message length is 4 MB. Payloads may contain sensitive account state and must never be logged.

Commands:

| Method | Arguments | Result |
| --- | --- | --- |
| `connect` | `root`: absolute private state directory | null; pairing/connection state arrives separately |
| `disconnect` | empty object | null after explicit account logout |
| `destinations` | empty object | sendable joined groups with `id`, `title`, `group` |
| `resolve` | `input`: official group invite, group ID or E.164 phone number | one destination object |
| `deliver` | `id`, `account`, `destination`, `file`, `allowNewSend` | object with `status` (`confirmed`, `pending`, `uncertain`) and numeric-only `timing` |
| `release` | `root`, `id`, `account`, `destination` | null; releases only confirmed metadata after durable host cleanup, without network connection |
| `replies` / `reply.ack` | owning `session`; ACK also has `id` | bounded screenshot-reply inbox / durable ACK |
| `capture.configure` | nullable `scope`: hash, session, account, destination, command | selected-chat exact text filter; null disables |
| `capture.read` / `capture.ack` | empty object / hashed `id` | bounded pending identities / durable ACK |
| `close` | empty object | null after durable writes; closes transport without logout |

State events have `method: "state"` and `args: { step, account?, qr? }`. Steps are `disconnected`, `connecting`, `qr`, and `ready`. QR is transient pairing material: display it locally, never save it or include it in diagnostics.

The helper requests parent storage using IDs prefixed `n`. Methods `store.get`, `store.put`, and `store.remove` have an opaque `key` within `wa/`; puts also contain `value`. Gets return the stored JSON value or null. Put/remove replies return null only after durable success. On failure reply `error: "storage"`; the helper stops rather than continue with unpersisted cryptographic state. The parent must serialize atomic writes, encrypt data at rest and isolate it to its OS user. No Telegram or parent-application secrets belong in this namespace.

Minimal offline close command:

```json
{"id":"offline-test","method":"close","args":{}}
```

Expected response:

```json
{"id":"offline-test","result":null}
```

Capture availability events use `method: "capture.available"` without text. The encrypted DB retains hashed identity dedup metadata. Ordinary/foreign-chat text is discarded by the handler. Self and remote text messages are allowed; edits and media captions are ignored.
