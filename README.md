<div align="center">

# 🛰️ dnsmasq-web

**The full-coverage web console for [dnsmasq](https://thekelleys.org.uk/dnsmasq/doc.html)**

DNS · DHCP · TFTP · PXE — every option, one beautiful realtime dashboard.

![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)
![stdlib only](https://img.shields.io/badge/dependencies-zero-3ddc97)
![no CDN](https://img.shields.io/badge/frontend-vanilla%20JS%2C%20no%20CDN-38e1ff)
![dnsmasq](https://img.shields.io/badge/dnsmasq-2.80%2B-ffc857)
![license](https://img.shields.io/badge/license-MIT-a78bfa)

*Line-preserving config editing · `dnsmasq --test` validation before every write ·
automatic backups · Server-Sent-Events realtime · IBM Plex dark NOC theme*

</div>

---

## Table of contents

- [Why dnsmasq-web](#why-dnsmasq-web)
- [Feature tour](#feature-tour)
- [How it stays safe](#how-it-stays-safe)
- [Requirements](#requirements)
- [Installation](#installation)
  - [Option A — one-command install (recommended)](#option-a--one-command-install-recommended)
  - [Option B — manual install](#option-b--manual-install)
  - [Run without installing (development)](#run-without-installing-development)
  - [Updating](#updating)
  - [Uninstalling](#uninstalling)
- [Configuration](#configuration)
- [Remote access & security](#remote-access--security)
- [Realtime architecture](#realtime-architecture)
- [HTTP API reference](#http-api-reference)
- [Project layout](#project-layout)
- [Troubleshooting](#troubleshooting)
- [FAQ](#faq)

---

## Why dnsmasq-web

dnsmasq is a small daemon with an enormous surface: DNS server, DHCP (v4 + v6), router
advertisement, TFTP and PXE boot — configured through a single text file with ~200 options.
Most web frontends model a handful of those options and silently destroy the rest of your file
on every save.

**dnsmasq-web takes the opposite approach:**

- 🧬 **Line-preserving model** — your config file is treated as an ordered list of lines.
  Comments, blank lines, ordering and directives the UI doesn't know about survive every edit
  **byte-for-byte**. New entries are inserted next to their siblings, not dumped at the end.
- 🗂️ **Registry-driven UI** — ~95 directives across 18 categories get rich forms with
  man-page help, syntax hints and structured field composers. Anything beyond that is still
  fully editable in the built-in raw editor and line explorer. *Nothing is off-limits.*
- 🛡️ **Impossible to save a broken config** — every write is checked with `dnsmasq --test`
  first, snapshotted second, and applied with an atomic rename third.
- ⚡ **Actually realtime** — one SSE stream pushes service status, lease changes, config
  changes, the live journal, and a parsed DNS query stream. No polling, no flicker, no manual
  refresh.
- 📦 **Zero dependencies** — pure Go standard library on the backend, hand-written vanilla
  JS/CSS on the frontend. The only external request the UI ever makes is the IBM Plex font.

## Feature tour

| Page | What you get |
|------|--------------|
| **📊 Dashboard** | Live service state & uptime, start/stop/restart/reload controls, config summary, a DNS **query tester** that resolves through your local dnsmasq, a full-height live activity feed of queries & DHCP handshakes, and recent leases |
| **🌐 DNS** | **Encrypted upstream (DoH)** — one-click dnscrypt-proxy integration with provider choice (Cloudflare / Quad9 / Google) and live status · **Resolver health** — passive DoH-auto-upgrade warnings plus a one-click *"is my browser using this resolver?"* bypass test with a tri-state verdict · upstream servers & **conditional forwarding**, `rev-server`, resolv options · every record type: `address` (wildcard/blocking), `host-record`, `cname`, `srv-host`, `txt-record`, `ptr-record`, `mx-host`, `naptr-record`, `caa-record`, raw `dns-rr`, `interface-name` · local domains & extra hosts files · cache size and every TTL knob · **DNSSEC** with trust anchors · rebind protection & filtering (`bogus-nxdomain`, `ignore-address`, `filter-AAAA`, …) · **ipset / nftset** |
| **📡 DHCP** | Multiple ranges with tags, modes (`static`, `proxy`) and IPv6/RA modes (`slaac`, `ra-names`, …) · static hosts with **one-click "Reserve" from a live lease** · per-tag DHCP options · boot files, PXE menus & arch matching · tag engine (`dhcp-mac`, vendor/user class, option matching, `tag-if`) · relay · a **live lease table** that diffs row-by-row as devices join |
| **🚀 TFTP** | The complete built-in TFTP server: root(s), secure mode, ports, limits, netboot guide |
| **🔌 Network** | Live host interfaces with one-click *listen on this interface*, listen addresses, bind modes, DNS port |
| **⚙️ Settings** | Query/DHCP logging, log facility, run-as user/group, config includes (`conf-file`, `conf-dir`), *start at boot* toggle |
| **📝 Config File** | Raw editor with syntax **Validate** button, revision-guarded saves (no lost updates), and a **line explorer** that labels every known directive, highlights unmanaged ones, and lets you edit or delete any single line |
| **📜 Logs** | Realtime journal follow (`journalctl -f` over SSE) with severity colouring, plus a parsed **DNS query stream** — pre-filled from history so you see the action instantly. All log and table views (Logs, live leases, config editor/explorer, backups) stretch to the **full viewport height** and scroll internally with sticky headers |
| **💾 Backups** | Automatic snapshot before *every* write · manual snapshots · view any backup, see whether it differs from the current config, restore or delete |

## How it stays safe

Every configuration write — whether from a form, a toggle, or the raw editor — goes through the
same pipeline:

```
        ┌─────────────┐    ┌──────────────────┐    ┌──────────────┐    ┌───────────────┐
change ─▶ dnsmasq --test ──▶ snapshot current  ──▶ write temp file ──▶ atomic rename   │
        │  (validate)  │    │ file to backups  │    │              │    │ into place    │
        └─────┬────────┘    └──────────────────┘    └──────────────┘    └───────────────┘
              │ invalid? → rejected with dnsmasq's own error, disk never touched
```

On top of that:

- **Optimistic concurrency** — line edits carry the exact text they expect to replace, raw
  saves carry a content revision. If someone (or something) changed the file underneath you,
  the API answers `409 Conflict` instead of clobbering, and the UI reloads the fresh state.
- **Restart awareness** — `SIGHUP` does *not* make dnsmasq re-read `dnsmasq.conf`. When the
  file on disk is newer than the running daemon, an amber **"restart to apply"** banner appears
  with a one-click restart.
- **Path-traversal-proof backups** — backup names are basename-sanitised and must match the
  backup pattern.
- Security headers (`X-Frame-Options: DENY`, `nosniff`, referrer policy) on every response.

## Requirements

| | Minimum | Notes |
|---|---|---|
| **OS** | Linux with systemd | service control & journal use `systemctl` / `journalctl` |
| **dnsmasq** | 2.80+ (2.9x recommended) | needed for `--test` validation; the console runs without it but skips validation |
| **Go** | 1.22+ | build-time only |
| **Privileges** | root (or equivalent sudo) | to write `/etc/dnsmasq.conf`, control the service and follow the journal |

## Installation

### Option A — one-command install (recommended)

From the repository root:

```bash
sudo ./scripts/install.sh
```

That single command:

1. builds the binary (`go build`)
2. installs everything to **`/opt/dnsmasq-web/`**
3. registers the **`dnsmasq-web.service`** systemd unit
4. **enables it at boot** and starts it immediately
5. **offers DNS interception** — routing this machine's own DNS through dnsmasq so you get
   local caching and a live query stream (skip it if dnsmasq only serves other clients)

then open **http://localhost:8053** 🎉

Non-interactive installs can decide up front:

```bash
sudo ./scripts/install.sh --intercept      # also point this machine at dnsmasq
sudo ./scripts/install.sh --no-intercept   # never ask
```

Interception persists across reboots (it's written into the NetworkManager connection
profile) and is fully reversible:

```bash
sudo bash /opt/dnsmasq-web/scripts/dnsmasq-manager.sh stop   # restore original DNS
```

Manage it like any service:

```bash
systemctl status dnsmasq-web        # state
journalctl -u dnsmasq-web -f        # logs
sudo systemctl restart dnsmasq-web  # restart
```

### Option B — manual install

<details>
<summary>Click to expand the manual steps</summary>

```bash
# 1. build
go build -o dnsmasq-web .

# 2. install files
sudo mkdir -p /opt/dnsmasq-web
sudo cp -r dnsmasq-web templates static scripts /opt/dnsmasq-web/

# 3. install the unit
sudo cp scripts/dnsmasq-web.service /etc/systemd/system/
sudo systemctl daemon-reload

# 4. enable at boot + start now
sudo systemctl enable --now dnsmasq-web
```

</details>

### Run without installing (development)

```bash
go build -o dnsmasq-web . && sudo ./dnsmasq-web
```

You can point it at a sandbox config instead of the real one — perfect for experimenting:

```bash
DNSMASQ_CONF=/tmp/test.conf BACKUP_DIR=/tmp/backups PORT=8054 ./dnsmasq-web
```

### Updating

```bash
git pull
sudo ./scripts/install.sh     # rebuilds, swaps the binary atomically, restarts
```

### Uninstalling

```bash
sudo ./scripts/install.sh uninstall   # keeps your config backups
```

## Configuration

Everything is configured with environment variables (set them in the `[Service]` section of
`/etc/systemd/system/dnsmasq-web.service`, then `sudo systemctl daemon-reload && sudo systemctl
restart dnsmasq-web`):

| Variable | Default | Description |
|----------|---------|-------------|
| `HOST` | *(all interfaces)* — unit sets `127.0.0.1` | bind address |
| `PORT` | `8053` | HTTP port |
| `DNSMASQ_CONF` | `/etc/dnsmasq.conf` | the config file to manage |
| `DNSMASQ_LEASES` | `/var/lib/dnsmasq/dnsmasq.leases` | lease database (Debian/Ubuntu: `/var/lib/misc/dnsmasq.leases`) |
| `BACKUP_DIR` | `/var/backups/dnsmasq-web` | where snapshots are stored |
| `TEMPLATE_DIR` | `./templates` | HTML templates |
| `STATIC_DIR` | `./static` | CSS / JS assets |

> 💡 **Tip:** for the live DNS query stream, enable **log-queries** under *Settings → Logging*
> (use the value `extra` to also capture client addresses), then restart dnsmasq.

## Encrypted upstream DNS (DoH)

By default dnsmasq forwards to upstream resolvers in plaintext — your ISP can read and tamper
with every query. The **Encrypted upstream** card on *DNS → Upstream* fixes that hop:

```
apps → dnsmasq (127.0.0.1:53, cache + local records) ──┬── .lan / rev-lookups → router (LAN, plain)
                                                       └── everything else → dnscrypt-proxy
                                                            (127.0.0.1:5053) ⇒ HTTPS/DoH ⇒ provider
```

- **Enable/disable with one click**; choose Cloudflare, Quad9 (malware-filtered) or Google DoH —
  switching providers while enabled re-applies instantly.
- The toggle rewrites only the *global* `server=` lines through the same validated + backed-up
  pipeline as everything else. `no-resolv`, conditional `.lan` forwarding, `rev-server`,
  `bind-interfaces`, caching and query logging are all preserved.
- Live status chips show: forwarder running, actually resolving (real probe query), negotiated
  protocol (from dnscrypt-proxy's own logs) and whether dnsmasq is routed through it.
- dnscrypt-proxy runs as its own systemd service, enabled at boot while the feature is on and
  fully disabled when you turn it off. The installer installs the package automatically
  (dnf / apt / pacman) but never activates encryption without you.
- dnscrypt-proxy's cache is turned off — dnsmasq remains the single cache layer.

API: `GET /api/encdns` (status + providers), `PUT /api/encdns` `{enabled, provider}`.

### Don't let the browser bypass the chain with its own DoH

Modern browsers ship their own "Secure DNS" (DoH). In Chrome's default **automatic** mode the
browser may silently upgrade to a provider's DoH endpoint when it recognises a known resolver in
your system DNS config — at which point it **stops using dnsmasq entirely**: no local records,
no `.lan` names, no blocking, no query log, and a second, separate encryption path you didn't
choose. This is especially likely if `/etc/resolv.conf` lists a fallback like `1.1.1.1` or
`8.8.8.8` (browsers map exactly those to DoH).

Since dnsmasq already encrypts upstream, the right setting is to **turn the browser's own DoH
off**:

- **Chrome / Chromium / Edge** → `chrome://settings/security` → *Use secure DNS* → **Off**
  (or pin it to a provider only if you deliberately want to bypass dnsmasq)
- **Firefox** → Settings → Privacy & Security → *Enable DNS over HTTPS* → **Off**
  (Firefox also respects `network.trr.mode=5` for "explicitly off")

**Verify it, don't trust it** — the console has a one-click test on *DNS → Upstream* →
*Resolver health*: **"Test this browser"** makes the browser resolve a random marker name
(`browser-check-….test`) and then checks whether that query actually reached dnsmasq. Green
means the browser rides your chain; red means its DoH is bypassing you. The same card passively
warns when `resolv.conf` contains a DoH-upgradable public resolver.

Manual equivalent of the test: add a marker only dnsmasq knows —
`address=/dnsmasq-doh-check.test/10.66.66.66` — restart, then look the name up *in the browser*.
If the browser sees `10.66.66.66` it's using dnsmasq; a DoH-ing browser gets NXDOMAIN from its
provider and the name never appears in your query log. Remove the marker afterwards.

## Remote access & security

dnsmasq-web has **no built-in authentication** and it edits a root-owned system service —
treat it like you'd treat `/etc/dnsmasq.conf` itself:

- The shipped systemd unit binds to **`127.0.0.1` only** (change `HOST=` to open it up).
- For LAN access, prefer a reverse proxy with auth in front, e.g. Caddy:

  ```
  dns.example.lan {
      basic_auth { admin <hash> }
      reverse_proxy 127.0.0.1:8053
  }
  ```

  or an SSH tunnel for occasional use: `ssh -L 8053:127.0.0.1:8053 server`.
- Never expose it to the public internet.

## Realtime architecture

One `EventSource` connection to `/api/events` drives the entire UI — every open tab converges
on the same state within ~2 seconds of any change, whatever caused it (the UI, another admin,
`vim /etc/dnsmasq.conf`, a device joining the network…).

| Event | Producer | Payload | UI reaction |
|-------|----------|---------|-------------|
| `status` | systemd poll (~4 s, change-detected) | state, PID, memory, uptime, `stale_config` | status chips, dashboard, apply banner |
| `leases` | lease-file mtime watch (2 s) | full lease list | keyed row diff — new devices glow in |
| `config` | config-file mtime watch (2 s) | content revision | silent refetch + re-render |
| `log` | persistent `journalctl -f` | raw journal line | journal viewer append |
| `query` | parsed dnsmasq query log | kind, name, rtype, value, client | query stream + dashboard feed |
| `dhcp` | parsed DHCP transaction log | kind, IP, MAC, hostname, iface | dashboard activity feed |

Clients reconnect automatically and receive a full snapshot on connect; the topbar shows
`LIVE` / `RECONNECTING` so you always know where you stand.

## HTTP API reference

Everything the UI does is a plain JSON API you can script against:

<details>
<summary><b>Configuration model</b></summary>

```
GET    /api/schema                directive registry: categories + per-key metadata
GET    /api/conf                  parsed config { path, rev, lines:[{idx,raw,key,value,flag}] }
GET    /api/conf/raw              { content, rev, path }
PUT    /api/conf/raw              { content, rev }          → 409 if rev is stale, 422 if invalid
POST   /api/conf/validate         { content }               → { valid, error? }
POST   /api/conf/lines            { key, value, flag }      add a directive (grouped with siblings)
PUT    /api/conf/lines/{idx}      { key, value, flag, expect_raw }  or  { raw, expect_raw }
DELETE /api/conf/lines/{idx}?expect_raw=…
PUT    /api/conf/scalar           { key, value }             set / update / remove ("" removes)
PUT    /api/conf/flag             { key, on }                toggle a flag directive
```

All mutations return the fresh parsed config. `expect_raw` / `rev` mismatches → `409`;
`dnsmasq --test` rejections → `422` with dnsmasq's error message.

</details>

<details>
<summary><b>Service, data, backups, events</b></summary>

```
GET    /api/service/status        status + enabled-at-boot + stale_config
POST   /api/service/{start|stop|restart|reload|enable|disable}
GET    /api/service/logs?lines=N  last N journal lines (≤2000)

GET    /api/dhcp/leases           parsed leases (IPv4 + IPv6, client-ids, expiry)
GET    /api/interfaces            host NICs with state and addresses
GET    /api/lookup?name=&type=    resolve via the local dnsmasq (A AAAA CNAME TXT MX SRV NS PTR)

GET    /api/resolver-check       resolv.conf state + browser DoH-auto-upgrade risk flags
GET    /api/resolver-check/verify?name=&control=[&fire=1]   browser-bypass marker verification

GET    /api/backups               list (newest first)
POST   /api/backups               create a snapshot now
GET    /api/backups/{name}        backup content + current config (for diffing)
POST   /api/backups/restore       { filename }   validated & snapshotted first
DELETE /api/backups/{name}

GET    /api/events                SSE stream: status, leases, config, log, query, dhcp
```

</details>

Example — add a static lease from the shell:

```bash
curl -X POST localhost:8053/api/conf/lines \
     -d '{"key":"dhcp-host","value":"aa:bb:cc:dd:ee:ff,192.168.1.50,nas,infinite"}'
```

## Project layout

```
dnsmasq-web/
├── main.go                       entry point & env config
├── internal/
│   ├── api/
│   │   ├── server.go             routes + handlers (net/http, Go 1.22 route patterns)
│   │   └── sse.go                event hub · watchers · journal follow · log parsing
│   ├── dnsmasq/
│   │   ├── conf.go               line-preserving config model + guarded mutations
│   │   ├── registry.go           ~95-directive catalogue (kind · category · help · syntax)
│   │   ├── leases.go             lease-file parser (v4 + v6)
│   │   ├── writer.go             validate → backup → atomic-write pipeline
│   │   └── types.go              shared types
│   └── service/manager.go        systemctl wrapper + journalctl follow
├── templates/                    Go html/template page shells (one per page)
├── static/
│   ├── app.js                    the console — registry-driven forms, composers, SSE client
│   └── style.css                 IBM Plex dark NOC theme
└── scripts/
    ├── install.sh                build + install + enable-at-boot (and uninstall)
    ├── dnsmasq-web.service       systemd unit
    └── dnsmasq-manager.sh        service control helper (resolv.conf handling)
```

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| **"config rejected by dnsmasq --test"** on every save | Your existing config contains a line dnsmasq itself rejects. The error names the line — fix or delete it in *Config File → Line explorer* (repairs that make the file valid are accepted). |
| Query stream / activity feed stays empty | Enable `log-queries` (*Settings → Logging*) and restart dnsmasq. |
| Leases table empty but devices have IPs | Check `DNSMASQ_LEASES` — Debian/Ubuntu uses `/var/lib/misc/dnsmasq.leases`. |
| `LIVE` indicator shows `RECONNECTING` | dnsmasq-web isn't reachable — `systemctl status dnsmasq-web`. The UI resyncs automatically when it returns. |
| Service buttons fail with a sudo error | The unit runs as root, so this only affects dev mode: run the binary with `sudo`, or grant NOPASSWD for `systemctl` and `scripts/dnsmasq-manager.sh`. |
| Port 8053 already in use | Change `Environment=PORT=` in the unit, `daemon-reload`, restart. |
| Amber "restart to apply" banner won't go away | That's correct until dnsmasq restarts — the daemon only reads `dnsmasq.conf` at startup. Click **Restart now**. |

## FAQ

<details>
<summary><b>Will it mangle my hand-written config?</b></summary>
No — that's the core design constraint. Untouched lines are reproduced byte-for-byte, comments
and ordering included. Directives the UI has no form for are shown (highlighted) in the line
explorer and left exactly as written.
</details>

<details>
<summary><b>What about <code>conf-dir</code> include files?</b></summary>
Includes are listed and editable as directives under <i>Settings → System & Includes</i>, and
<code>dnsmasq --test</code> validates the whole tree. The rich editors operate on the main file;
point <code>DNSMASQ_CONF</code> at an included file to manage it directly.
</details>

<details>
<summary><b>Does it support DHCPv6 / router advertisement?</b></summary>
Yes — IPv6 ranges (<code>slaac</code>, <code>ra-names</code>, <code>ra-stateless</code>, …),
<code>enable-ra</code>, <code>ra-param</code>, DUID and quiet-RA logging all have forms, and
IPv6 leases show up in the live lease table tagged <code>v6</code>.
</details>

<details>
<summary><b>Why no authentication?</b></summary>
Auth done badly is worse than a clear boundary. The unit binds to localhost by default; put
your reverse proxy's battle-tested auth (or an SSH tunnel) in front for remote access.
</details>

---

<div align="center">
<sub>Built with the Go standard library and nothing else · IBM Plex · Made for people who read their config files</sub>
</div>
