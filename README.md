# hwclient

A small always-on program that shares a HomeWAV visitor session with the phone app and puts a chat UI on top of it, so you can read threads and send text, audio and media from a browser on your computer or phone.

The service allows one session per account. Logging in anywhere else would log the phone out, so hwclient never logs in. Instead the phone's PAC file sends only `app.homewav.com` through this program, which decrypts that one host with its own certificate, records the session the server accepts, and keeps it alive.

## Run it

```sh
(cd web && npm install && npm run build)   # once, and after UI changes
go build -o hwclient ./cmd/hwclient
./hwclient
```

It sits in the menu bar (macOS) or tray (Windows) and opens the UI in the browser. The menu has Open, Show data folder and Quit. Starting it again while it runs just opens the browser. Logs go to `hwclient.log` in the data folder. `-headless` drops the icon and logs to the terminal instead, for servers.

On Windows, build with `-ldflags=-H=windowsgui` so no console window opens. It cross-compiles from a Mac (`GOOS=windows`); the macOS build needs cgo, so it has to be built on a Mac.

The UI is at `http://localhost:8080`. **Settings** walks through the phone setup:

1. **Trust the certificate.** Scan the code (or open the link) in Safari on the phone, install the profile, then switch it on under *Settings > General > About > Certificate Trust Settings*.
2. **Set the proxy.** *Settings > Wi-Fi > (network) > Configure Proxy > Automatic*, with the PAC URL shown.
3. **Open the app once.** The step turns green when the session is captured.

After that the app does not need to be open. The session is renewed every 12 h (`-keepalive`), which also pushes the 30-day expiry of the remember cookies forward.

Settings, credentials and the certificate authority live in the data folder (`-data`, by default `hwclient` under your user config directory). Keep it private.

### Flags

| Flag | Meaning |
|---|---|
| `-listen` | Address for the proxy, the UI and the local API. Default `:8080`. |
| `-data` | Data folder. |
| `-cert`, `-key` | Use an existing certificate instead of the managed one. The UI then cannot offer the CA download. |
| `-state` | Credentials file. Default `state.json` in the data folder. |
| `-keepalive` | Renewal interval. `0` disables it. |
| `-open-proxy` | Also tunnel hosts that are not intercepted. Off by default, see "Who can reach it". |
| `-headless` | No tray icon; log to the terminal. |
| `-no-browser` | Do not open the browser on start. |
| `-pac-addr`, `-https-listen`, `-host`, `-no-access-log` | As before. |

### Branding

The user-facing name and icon are kept apart from the internal name `hwclient` (module, binary, data folder, env vars), so renaming does not move anyone's settings. To rebrand, change:

- `Name` in `internal/app/app.go`. Used by the tray, the CA certificate's name (new installs only: an existing CA is kept), and logs.
- `AppName` in `web/src/constants.ts`. Used by the page title and header.
- `cmd/hwclient/icon.png`: the tray icon on Windows and Linux. Square; 256 px works well.
- `cmd/hwclient/icon-template.png`: the macOS menu bar icon. Black on transparent; macOS recolors it.

## The UI

- **Chat:** contacts on the left, the thread on the right, a composer below.
  - Text, with the facility's character limit.
  - Audio: `.m4a` only, nothing is converted. The length reported to the service is filled in from the file and can be edited.
  - Picture, GIF or video: sent as a public link (`gallery_url`). Paste a link, or upload a file to your own bucket and the link is filled in. You choose whether it goes out as GIF, image or video.
- **Settings:** connection status, phone setup, the pairing link for other devices, and the bucket.

### Media storage

Uploads go to an S3-compatible bucket you own: Amazon S3 by default, or any provider with an endpoint (R2, B2, MinIO). The recipient side only shows media from hosts it allows; `<bucket>.s3.amazonaws.com` is known to work, which is why that is the default public address.

## Who can reach it

- **This machine** (loopback) has full access.
- **Other devices** need the pairing link from Settings, opened once in their browser. It is a password for the account: anyone with it can read and send.
- **Scripts** can send `Authorization: Bearer <pairing secret>`. `HWCLIENT_API_SECRET` overrides the stored secret.
- Requests that change anything must carry an `X-HW-Client` header, so a website open in your browser cannot send messages through `localhost`.
- The proxy only serves the intercepted host. With `-open-proxy` it tunnels anything, which also lets any device on the network reach services on this machine's loopback, including this one as a "local" caller.

## Local endpoints

| Path | Returns |
|---|---|
| `GET /proxy.pac`, `GET /ca.pem` | The PAC file and the CA certificate. Public: the phone needs them first. |
| `GET /_/sessid`, `/_/token`, `/_/cookie` | The credentials as plain text. Add `?renew=1` to renew first. |
| `GET /_/session` | Status as JSON, without secret values. |
| `/hw/api/...` | The service API with the session attached. Login and session calls are refused. |
| `/app/status`, `/app/config`, `/app/renew`, `/app/upload` | What the UI uses. |

```sh
curl -s 'localhost:8080/_/sessid?renew=1'
```

## How the session works

Learned from captures of iOS app 2.28.0.

- **`PHPSESSID`** authorizes API calls and expires after a period of inactivity.
- **`asps` and `esur`** are "remember" cookies that last 30 days. `POST /api/app/refetch-session/v2` swaps them for a new `PHPSESSID` without the password. They are the credential that matters.
- **Logged out** is HTTP 200 with `{"ok":false,"cause":"You must be logged in to access the requested page"}`. Both the proxy hook and the UI's API base then get a working session and replay the request, so neither the app nor the UI sees the failure.
- **Sending** is `GET /api/message/init` for an id, then `POST /api/message/send`: urlencoded for text and links, multipart with a `voice` part for audio.
- **`X-HomeWAV-Signature`** is passed through from the app unchanged and does not appear to be enforced.

## Layout

Go has no `src/` folder by convention. Code under `internal/` cannot be imported by other modules, which is the closest equivalent.

| Path | Role |
|---|---|
| `cmd/hwclient/` | The program: tray icon by default, `-headless` for a plain server. |
| `cmd/hwclient-dev/` | The same program against an in-memory fake of the service, for UI work. |
| `internal/app/` | Everything specific to this program: the server and listeners, the `/app/` settings API, the `/hw/` API base, the access gate, certificates, settings, media uploads. The package comment in `app.go` lists which file does what. |
| `internal/session/` | Keeping the account logged in: credential store, capture, renewal, the proxy hook, `Call`, the `/_/` API. |
| `internal/proxykit/` | Generic PAC plus MITM proxy library, copied from the `proxy-tests` demo. Additions: `Flow.Reforward`, `Proxy.ListedOnly`. |
| `internal/fakehw/` | The fake service. Also used by the tests. |
| `web/` | The UI: Preact, TypeScript, Tailwind, built with Vite and embedded in the binary. Sources in `web/src/`. |

## Working on the UI

```sh
go run ./cmd/hwclient-dev -data scratch/dev-data   # fake service on :8090
(cd web && npm run dev)                            # live reload, forwards the API to :8090
```

Nothing in dev mode reaches the real service.

## Not known yet

- How `image` and `video` messages that point at your own link are sent. The UI assumes `type=image|video` with `gallery_url`, like the captured `gif` send.
- What a picture or audio message costs. Only the text price is shown.
- What happens when the JWT expires, and how long an idle `PHPSESSID` lasts.
