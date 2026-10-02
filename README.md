# homowav

A small always-on proxy that keeps a HomeWAV visitor session alive, so local tools can use it without anyone opening the app first.

The phone's PAC file sends only `app.homewav.com` through this proxy; everything else goes direct. The proxy decrypts that one host with your own certificate, records the credentials the server accepts, and renews the session when it expires. The credentials are then available at `http://localhost:8080/_/sessid`.

## Setup

```sh
go build -o homowav ./cmd/homowav
./scripts/make-cert.sh certs app.homewav.com
./homowav -cert certs/leaf.pem -key certs/leaf.key -state state.json
```

Then set up the phone:

1. **Trust the CA.** Send `certs/ca.pem` to the phone and install the profile. Then turn it on under *Settings > General > About > Certificate Trust Settings*.
2. **Set the proxy.** Go to *Settings > Wi-Fi > (network) > Configure Proxy > Automatic* and enter `http://<this machine's LAN IP>:8080/proxy.pac`.
3. **Open the app once.** The proxy logs `credentials updated`, and `curl localhost:8080/_/session` shows `"can_renew": true`.

From then on the app doesn't need to be opened again. By default the proxy renews the session every 12 h (`-keepalive`). Each renewal also pushes the 30-day expiry of the remember cookies forward.

## Endpoints

| Path | Returns |
|---|---|
| `GET /proxy.pac` | The PAC file. This is public, because the phone has to fetch it. |
| `GET /_/sessid` | `PHPSESSID` as plain text, with no trailing newline. |
| `GET /_/token` | The JWT access token from the last login. It may have expired: see `jwt_expires` in `/_/session`. |
| `GET /_/cookie` | A ready-to-use `Cookie:` header value (session plus remember cookies). |
| `GET /_/session` | Status as JSON: timestamps, expiries, and whether renewal is possible. It never contains the secret values. |

Add `?renew=1` to `sessid`, `token` or `cookie` to get a brand-new session first. Use it before sending anything if the session might be stale:

```sh
curl -s 'localhost:8080/_/sessid?renew=1'
```

The `/_/` endpoints only answer loopback callers by default. To allow other devices on the LAN, such as a future web UI opened on the phone, start the proxy with `HOMOWAV_API_SECRET=<secret>` and send `Authorization: Bearer <secret>`.

## How the session works

This is learned from a capture of iOS app 2.28.0. Values were compared by MD5, never read directly.

- **`PHPSESSID`** authorizes API calls. It expires after a period of inactivity.
- **`asps` and `esur`** are "remember" cookies that last 30 days. `POST /api/app/refetch-session/v2` swaps them for a new `PHPSESSID` and doesn't need the password or a signature. They are the credential that actually matters, and the state file keeps them across restarts.
- **The JWT** (RS256, 1 h) comes only from the login response. The server accepted calls without one, so it's recorded but not relied on.
- **Logged out** usually looks like HTTP 200 with `{"ok":false,"cause":"You must be logged in to access the requested page"}`, not a 401. Not always: a dead session on `GET /api/app/prisons/<id>` (which opening a conversation calls) gives `{"ok":false,"cause":"Invalid request"}`, and the app shows "The data couldn't be read because it's missing."
- **What the proxy does about it**, for every `/api/` call except login and refetch:
  1. It sends the stored session instead of the phone's if they differ, so a dead cookie on the phone costs no failed round trip.
  2. If the server still answers `ok:false` (any cause except "You do not have permission"), it tries the phone's own session, then a session another request already renewed, then one shared refetch. It replays the request and returns that reply.
  3. Renewals prompted by an ambiguous cause (anything other than the logged-out wording) happen at most once a minute, so a request that is simply invalid can't churn sessions.
  4. When the session it used differs from the phone's, the reply carries `Set-Cookie: PHPSESSID=<live>` and the log shows `offered live session to app`. If those lines keep repeating with the same `app_had` value, the app ignores the cookie.
  5. If nothing works, the phone gets the original failure and runs its own recovery.
- **`X-HomeWAV-Signature`** is an RSA signature over the `Date` header, created on the device. The proxy passes the app's value through unchanged. It doesn't appear to be enforced.

## Layout

| Package | Role |
|---|---|
| `proxykit/` | A generic PAC plus MITM proxy library, copied from the `proxy-tests` demo. The one addition is `Flow.Reforward`. |
| `session/` | The credential store, capture, renewal, the proxy hook, and the `/_/` API. No flags and no globals. |
| `server/` | `server.New(Config)` plus `Run(ctx)`. It's the single unit a CLI or UI embeds, and `Handle` adds extra routes such as a web UI. |
| `cmd/homowav/` | Flags → `server.Config`. Nothing else. |

`state.json`, `certs/` and `*.key` are git-ignored: they hold live credentials and private keys. Keep them private.

## Not known yet

- What happens when the JWT expires, and whether refetch ever returns a new one. The capture was shorter than an hour.
- How long an idle `PHPSESSID` lasts.
