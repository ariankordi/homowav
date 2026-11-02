# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**hwclient** is an always-on tray app that shares the user's HomeWAV visitor session with their phone and puts a chat UI on top of it. `README.md` is the authoritative user and dev doc. The folder is still named `homowav`; the code is not.

## Commands

```sh
(cd web && npm install && npm run build)      # UI; Go still compiles without it (serves a "not built" notice)
go build -o hwclient ./cmd/hwclient && ./hwclient   # tray app; -headless for terminal logging
go vet ./... && go test -race ./...           # what CI runs, plus: (cd web && npm run typecheck)
go test -race ./internal/session -run TestName   # a single test
go run ./cmd/hwclient-dev -data scratch/dev-data [-one-contact]   # fake service on :8090
(cd web && npm run dev)                       # Vite live reload, proxies the API to :8090
scripts/build-release.sh <target>             # windows-x64|windows-arm64|linux-x86_64|linux-aarch64|macos-universal → dist/
act                                           # run .github/workflows/release.yml locally (.actrc; needs a container runtime)
```

## Architecture

The request path, which takes reading several packages to see:

- **Phone → proxy.** The phone's PAC (`/proxy.pac`) sends only `app.homewav.com` to hwclient. `internal/proxykit` terminates TLS for that host with a leaf from hwclient's own private CA (`internal/app/certs.go`) and hands every request to the hook in `internal/session`. Other hosts are refused unless `-open-proxy` is set (`Proxy.ListedOnly`).
- **Session capture and renewal (`internal/session`)** is the core feature. The hook records the cookies the server accepts: `PHPSESSID`, plus `asps`/`esur`, the 30-day "remember" cookies. When a reply means "logged out", it renews via `POST /api/app/refetch-session/v2` and replays the request with `Flow.Reforward`, so the app never sees the failure. A logged-out reply is HTTP 200 with `ok:false`, and not always with the same `cause`. `renew.go` also runs a 12 h keepalive. `/_/sessid|token|cookie|session` expose the credentials locally.
- **The UI's "API base" (`internal/app/forward.go`, `/hw/api/...`).** The Preact UI builds HomeWAV requests in TypeScript and sends them through this CORS-proxy-like forwarder. The forwarder attaches the session and gets the same renew-and-replay treatment through `Service.Call`. Login and session endpoints are refused. Sending a message is `GET /api/message/init` → `POST /api/message/send`.
- **The access gate (`internal/app/access.go`).** Loopback has full access. Other devices need the pairing secret (a link or `Authorization: Bearer`). Every mutating request must carry `X-HW-Client`, which blocks CSRF via `localhost`.
- **Media.** Uploads stream from the browser through `/app/upload` to the user's own S3-compatible bucket (`s3.go`: hand-rolled SigV4, `UNSIGNED-PAYLOAD`). The message carries only the public link (`gallery_url`).
- **The `web/` folder** is a Vite + Preact + Tailwind app, embedded with `go:embed all:dist`. Routes live in the URL hash (`route.ts`), so a reload stays on the open thread. New messages are found by polling `/api/visitor/heartbeat`.
- **`internal/fakehw`** is an in-memory fake of the service. `hwclient-dev` and the Go tests both run against it.
- **`internal/proxykit`** is a copy of `../go/proxykit` (the proxy-tests demo), with additions (`Flow.Reforward`, `Proxy.ListedOnly`). The two copies have diverged, so a fix to one does not reach the other.

The package comment in `internal/app/app.go` maps each file to its job.

## Rules from the user's past sessions

- **Never send to the real HomeWAV API during development.** Messages cost money and reach a real person. Test against `fakehw` / `hwclient-dev`.
- **The MITM proxy is required, not a workaround.** HomeWAV allows one session per account, so logging in directly would log the phone out. Don't propose direct login. Put onboarding effort into making the CA and PAC setup easier.
- **Naming:** the code says `hwclient` (module, binary, data folder, `HWCLIENT_*` env vars, `X-HW-Client`). The user-facing name lives only in `app.Name` (Go) and `AppName` (`web/src/constants.ts`), both currently "HW Client", and will be rebranded. "homowav" survives only as the folder name. "HomeWAV"/"homewav" means the real vendor (`app.homewav.com`), which is correct.
- **Keep packages few.** The user dislikes one-folder-per-concern Go packages: everything app-specific stays in `internal/app`. Don't add a package for something small.
- **Audio is `.m4a` only, with no conversion.** `elapsed_time` stays editable, because the user sends music as voice messages with a tiny duration. Don't add yt-dlp-style fetching or transcoding.
- **Unknowns, not facts:** how `image`/`video` sends to your own URL look (assumed: `type=image|video` + `gallery_url`), media message prices, JWT expiry, idle `PHPSESSID` lifetime. `X-HomeWAV-Signature` is passed through and appears not to be enforced.
- **Captures** (mitmproxy flows/HARs) are under `~/Downloads`. Inspect them with the mitmproxy MCP first. It silently imports 0 records from native `.flow` files, so convert with `mitmdump -nq -r <flow> --set hardump=<out>.har`. Print shapes, not secret values.
- **Releases:** five targets. Windows/Linux are pure Go (`CGO_ENABLED=0`). macOS needs cgo for the systray, so it is built with zig cc against a downloaded SDK into `toolchains/` and ad-hoc signed (rcodesign on Linux). A `v*` tag makes a draft GitHub release that includes the user-written `INSTRUCTIONS.md`. The remote is `gitea`. Ask before pushing or tagging. The user writes the repo description, `INSTRUCTIONS.md` and LICENSE.
- **Gitignored local files** that matter: `cert.pem`/`privkey.pem` (old static cert), `state.json` (captured credentials), and `dist/` and `toolchains/` (release builds). Never commit keys or `state.json`. Scratch output goes in `scratch/`.
