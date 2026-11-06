# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**HW Client** (`hwclient` in code; shipped to users as "homowav") is an always-on tray app that shares the user's HomeWAV visitor session with their phone and puts a chat UI on top of it. `README.md` is the authoritative user and dev doc. The folder and the repo are named `homowav`; the code is not.

## Commands

```sh
(cd web && npm install && npm run build)      # UI; Go still compiles without it (serves a "not built" notice)
go build -o hwclient ./cmd/hwclient && ./hwclient   # tray app; -headless for terminal logging
go vet ./... && go test -race ./...           # what CI runs, plus: (cd web && npm run typecheck)
go test -race ./internal/session -run TestName   # a single test
go run ./cmd/hwclient-dev -data scratch/dev-data [-one-contact] [-signed-out]   # fake service on :8090; -signed-out shows the first-run screen (fake login sam / correct horse)
(cd web && npm run dev)                       # Vite live reload, proxies the API to :8090
scripts/build-release.sh <target>             # windows-x64|windows-arm64|linux-x86_64|linux-aarch64|macos-universal → dist/
act                                           # run .github/workflows/release.yml locally (.actrc; needs a container runtime)
```

## Architecture

The request path, which takes reading several packages to see:

- **Phone → proxy.** The phone's PAC (`/proxy.pac`) sends only `app.homewav.com` to hwclient. `internal/proxykit` terminates TLS for that host with a leaf from hwclient's own private CA (`internal/app/certs.go`) and hands every request to the hook in `internal/session`. Other hosts are refused unless `-open-proxy` is set (`Proxy.ListedOnly`).
- **Session capture and renewal (`internal/session`)** is the core feature. The hook records the cookies the server accepts: `PHPSESSID`, plus `asps`/`esur`, the 30-day "remember" cookies. When a reply means "logged out", it renews via `POST /api/app/refetch-session/v2` and replays the request with `Flow.Reforward`, so the app never sees the failure. A logged-out reply is HTTP 200 with `ok:false`, and not always with the same `cause`. `renew.go` also runs a 12 h keepalive. `/_/sessid|token|cookie|session` expose the credentials locally.
- **Password login (`internal/session/login.go`).** The default way in, offered first on the first-run screen (`Welcome.tsx`) and under Settings → Sign in. Both post `/app/account`, which logs in the way the app does. If the user saves the password, a failed refetch falls back to logging in with it. A refused password is never retried until a different one is saved (the service counts `login_attempts`), and automatic logins are at most one a minute.
- **The UI's "API base" (`internal/app/forward.go`, `/hw/api/...`).** The Preact UI builds HomeWAV requests in TypeScript and sends them through this CORS-proxy-like forwarder. The forwarder attaches the session and gets the same renew-and-replay treatment through `Service.Call`. Login and session endpoints are refused. Sending a message is `GET /api/message/init` → `POST /api/message/send`.
- **The access gate (`internal/app/access.go`).** Loopback has full access. Other devices need the pairing secret (a link or `Authorization: Bearer`). Every mutating request must carry `X-HW-Client`, which blocks CSRF via `localhost`.
- **Media.** Uploads stream from the browser through `/app/upload` to the user's own S3-compatible bucket (`s3.go`: hand-rolled SigV4, `UNSIGNED-PAYLOAD`). The message carries only the public link (`gallery_url`).
- **The `web/` folder** is a Vite + Preact + Tailwind app, embedded with `go:embed all:dist`. Routes live in the URL hash (`route.ts`), so a reload stays on the open thread. New messages are found by polling `/api/visitor/heartbeat`.
- **`internal/fakehw`** is an in-memory fake of the service. `hwclient-dev` and the Go tests both run against it.
- **`internal/proxykit`** is a generic PAC + MITM proxy library, copied from a standalone demo, with additions (`Flow.Reforward`, `Proxy.ListedOnly`).

The package comment in `internal/app/app.go` maps each file to its job.

Other details:
- **Data folder:** `state.json` (credentials), `config.json` (settings, including the bucket secret, the port, and the saved HomeWAV password in plain text when the user opts in) and `certs/`. An existing CA is always kept, and only the leaf is reissued. So a rename of `app.Name` only changes the CA name for new installs. With `-cert`/`-key` there is no CA for the UI to offer.
- **Port:** `config.json` `port` (Settings → Network), overridden by `-listen`. "Save and restart" makes `App.Run` return `ErrRestart`, and `cmd/hwclient/restart*.go` relaunches (exec in place on Unix; a new process on Windows).
- **Upload names:** `<prefix>YYYY-MM-DD_HHMMSS_<slug>_<16 hex>.<ext>` in local time (`media.go`). The slug is up to 32 characters of the file name, with case kept. A yt-dlp `[id]` is always kept whole, and camera names (`IMG_1234`) are dropped.
- **Limits:** uploads go up to 5 GiB (S3's single-PUT maximum) and are abandoned after 2 minutes without progress. The `/hw` forwarder holds request bodies in memory, up to 64 MiB, so it can replay them after a renewal.
- **Media hosts:** the recipient's side only shows media from hosts it allows. `<bucket>.s3.amazonaws.com` is known to work, which is why it is the default public address.

## HomeWAV API (as observed)

Learned from captures of iOS app 2.28.0. Field and type names are in `web/src/types.ts`.

- **Send:** `GET /api/message/init?inmate_id=&visitor_id=` returns `body.pubid`. It works without a cookie, but one is sent anyway. Then `POST /api/message/send`:
  - text, GIF and links: urlencoded `type, body, elapsed_time, gallery_url, inmate_id, visitor_id, pubid, sender=visitor`.
  - voice: multipart with a `voice` part (`audio/m4a`), and without `visitor_id` or `gallery_url`.
  - The reply has no `created_timestamp`, so the UI fills one in.
- **Contacts:** `POST /api/app/inmates-by-visitor`, with form `id` set to the visitor id. Each row has about 70 flat fields, mostly strings, including numbers and booleans.
- **Thread:** `GET /api/app/visitor/chat?inmate_id&page&limit`.
- **Login:** `POST /api/app/login/?username=` with form `device_type, fcm_token, password, version, voipToken`. The reply sets `PHPSESSID`, `asps` and `esur`, and its body has `jwt_access`, `jwt_refresh` and `visitor_id`.
- **Prices:** `GET /api/app/prisons/<prison_id>` (the contact row's `prison_id`) has `messaging_price{video, library, image, gif, text}` as numbers, and `price_per_voice_message_minute` as a string. At the facility seen, GIF ($0.10) costs less than Image ($0.25). A still picture sent as `type=gif` displays fine, so the composer picks the cheaper of the two.
- **Heartbeat:** `GET /api/visitor/heartbeat?ctx=mobile`. The app polls it about every 10 s. It returns `status{<contact id>: green|red}`, `unread_messages_count` as a string, and `credit_balances`.
- **Session:**
  - `PHPSESSID` authorizes calls. The JWT is only issued by login.
  - Refetch posts `jwtAccess`, `jwtRefresh` and `token` (the VoIP token) with a `Refresh-Token` header. The new `PHPSESSID` arrives in `Set-Cookie`. The reply body's `php_session_id` is the *previous* session, so don't read it.
  - The app itself fires parallel refetches on a burst of failures, and each one rotates `PHPSESSID`. hwclient rate-limits its own renewals (`renew.go`).
  - A dead session doesn't always say "You must be logged in": `prisons/<id>` answers `Invalid request`. Any `ok:false` other than a permission error is treated as possibly stale.
  - `pusher-auth` replies have no `ok` field.

## Conventions

- **Never send to the real HomeWAV API during development.** Messages cost money and are delivered. Test against `fakehw` / `hwclient-dev`.
- **Two equal ways in; password is the default.** HomeWAV allows one session per account, so a password login from hwclient signs out a phone that is not using the proxy. Phone mode (the MITM proxy) keeps the phone signed in and its notifications, and stays fully supported. The first-run screen (`Welcome.tsx`) offers both, with password picked first, matching the README.
- **Naming:** the code says `hwclient` (module, binary, data folder, `HWCLIENT_*` env vars, `X-HW-Client`). The user-facing name lives only in `app.Name` (Go) and `AppName` (`web/src/constants.ts`), both "homowav" (lowercase). "homowav" is the external name only: the repo, the folder, the README title and what the user sees. In docs, comments and discussion call the program "the client" or "HW Client". "HomeWAV"/"homewav" means the real vendor (`app.homewav.com`), which is correct.
- **Keep packages few.** No one-folder-per-concern Go packages: everything app-specific stays in `internal/app`. Don't add a package for something small.
- **Audio is `.m4a` (what the app sends) or `.mp3` (accepted with an "untested" warning, sent as `audio/mpeg`), with no conversion.** Formats live in `VoiceFormats` (`web/src/constants.ts`). The `elapsed_time` sent with audio is user-editable on purpose. Don't add yt-dlp-style fetching or transcoding.
- **Unknowns, not facts:** how `image`/`video` sends to your own URL look (assumed: `type=image|video` + `gallery_url`), what `library` is, JWT expiry, idle `PHPSESSID` lifetime. `X-HomeWAV-Signature` is passed through and appears not to be enforced.
- **Releases:** five targets. Windows/Linux are pure Go (`CGO_ENABLED=0`). macOS needs cgo for the systray, so it is built with zig cc against a downloaded SDK into `toolchains/` and ad-hoc signed (rcodesign on Linux). A `v*` tag makes a draft GitHub release. File names carry no version (`hwclient-<target>`), so the README's `releases/latest/download/...` links stay valid; the version is stamped inside via `-X main.version`.
- **Never commit keys or credentials:** `*.pem`, `*.key` and `state.json` (captured session cookies) are gitignored for that reason. Scratch output goes in the gitignored `scratch/`.
