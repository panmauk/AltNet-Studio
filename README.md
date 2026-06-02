# AltNet Studio (Linux)

The desktop client for [AltNet](https://github.com/panmauk/AltNet) — the
peer-to-peer `.alt` network. AltNet Studio runs a node, lets you browse
`*.alt` sites in any browser, and (with an account) request and publish
`.alt` domains.

This repository is the **Linux** build of the Studio client. The
protocol/daemon lives in the [AltNet](https://github.com/panmauk/AltNet)
repo; the account/registration backend is separate and private.

Built with [Wails v2](https://wails.io) (Go + a vanilla-JS frontend) and
WebKit2GTK.

## What it does

- **Be a node** — starts a background daemon that joins the network and
  helps route/serve content; autostarts on login (XDG autostart).
- **Browse `.alt`** — adds a `systemd-resolved` drop-in so `name.alt`
  resolves through your local node (daemon DNS on `127.0.0.1:5354`).
- **Accounts & domains** — request a `.alt` name (admin-approved) and
  publish a site folder.
- **HTTPS** — trusts AltNet's local CA via the system trust store.

## ⚠️ Run as root

On Linux the app must run **as root** — it needs to bind port 80 (so
`.alt` sites open at `http://name.alt/`), edit `systemd-resolved`, and
install its local CA. The app shows a blocking warning and disables
"Be a node" if it isn't root. Launch with:

```sh
sudo -E ./AltNetStudio
```

(A future release will split privileges so the GUI can run unprivileged.)

## Build

Requires Go 1.23+, Node.js, the Wails CLI, and the GTK/WebKit dev libs:

```sh
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev nodejs npm
go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
wails build -tags webkit2_41        # -> build/bin/AltNetStudio
```

(`-tags webkit2_41` selects WebKit2GTK 4.1, current on modern distros.)

## Run

AltNet Studio launches the **`altnet` daemon** as a child process and
expects the binary (named `altnet`) **next to** `AltNetStudio`. Build it
from the protocol repo and place it alongside:

```sh
# in a clone of github.com/panmauk/AltNet
go build -o altnet ./cli
```

Then `sudo -E ./AltNetStudio`, click **Be a node**, and browse a `.alt`
site. A `.deb` packaging script that bundles the daemon and wires up the
launcher is the recommended distribution method.

## License

GNU General Public License v3.0 — see [LICENSE](LICENSE).
