# AltNet Studio

The desktop client for [AltNet](https://github.com/panmauk/AltNet) — the
peer-to-peer `.alt` network. AltNet Studio runs a node on your machine, lets
you browse `*.alt` sites in any browser, and (with an account) request and
publish `.alt` domains.

One cross-platform codebase (Windows + Linux). The protocol/daemon lives in
the [AltNet](https://github.com/panmauk/AltNet) repo; the
account/registration backend is separate and private.

Built with [Wails v2](https://wails.io) (Go + a vanilla-JS frontend).

## What it does

- **Be a node** — one click starts a background daemon that joins the
  network, helps route/serve content, and comes back on every login.
- **Browse `.alt`** — routes `name.alt` through your local node
  (Windows: NRPT rule; Linux: a systemd-resolved drop-in).
- **Accounts & domains** — request a `.alt` name (admin-approved) and
  publish a site folder.
- **Admin** — approve/decline requests and take sites down, from the app.

## Build

Requires [Go 1.23+](https://go.dev), [Node.js](https://nodejs.org), and the
[Wails CLI](https://wails.io/docs/gettingstarted/installation):

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
```

**Windows:**
```sh
wails build                       # -> build/bin/AltNetStudio.exe
```

**Linux** (needs GTK3 + WebKit2GTK):
```sh
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev nodejs npm
wails build -tags webkit2_41      # -> build/bin/AltNetStudio
```

The same source builds both — platform-specific behavior is isolated in
build-tagged files (`nrpt_windows.go` vs `nrpt_linux.go`, etc.); Go compiles
only the ones matching the target OS.

## Run

AltNet Studio launches the **`altnet` daemon** as a child process and
expects the binary next to it (`altnet.exe` on Windows, `altnet` on Linux).
Build it from the protocol repo and place it alongside:

```sh
# in a clone of github.com/panmauk/AltNet
go build -o altnet ./cli          # add .exe on Windows
```

- **Windows:** run `AltNetStudio.exe`, click **Be a node** (accept the UAC
  prompt to install the `.alt` NRPT rule). Closing the window hides it; the
  node keeps running.
- **Linux:** the app must run **as root** (port 80 + resolver config + local
  CA): `sudo -E ./AltNetStudio`. It shows a warning and disables node mode
  otherwise.

## License

GNU General Public License v3.0 — see [LICENSE](LICENSE).
