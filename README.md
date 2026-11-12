# gofer

Send files straight from one computer to another, peer to peer. No accounts, no
upload server, no public links.

```sh
gofer send holiday.zip anna
```

## Install

**macOS / Linux**

```sh
curl -fsSL https://github.com/NZ-Linix/gofer/releases/latest/download/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://github.com/NZ-Linix/gofer/releases/latest/download/install.ps1 | iex
```

Update later with `gofer update`. With Go installed you can also run
`go install github.com/NZ-Linix/gofer/cmd/gofer@latest`.

## Usage

```sh
gofer login anna                 # once per device: pick a username
gofer receive                    # stay online; asks before accepting each file
gofer send report.pdf ben        # send to whoever is logged in as "ben"
```

| Command | |
|---|---|
| `gofer login <username>` | Set your username (3–32 chars: `a-z 0-9 . _ -`) |
| `gofer whoami` | Show your username and peer ID |
| `gofer receive [--dir D] [--yes] [--port N] [-v]` | Receive files (default: `~/Downloads`) |
| `gofer send <file> <username>` | Send a file |
| `gofer contacts [add <name> <id> \| rm <name>]` | Manage pinned contacts |
| `gofer update` | Update to the latest release |

## How it works

- **Identity.** Each device has an Ed25519 key in `~/.gofer`. Its peer ID is derived from that key.
- **Discovery.** `gofer receive` announces *username → peer ID* on the public
  [libp2p](https://libp2p.io) / IPFS DHT. There is no gofer server.
- **Connection.** If the receiver is behind a NAT, a public libp2p relay helps the two sides
  [hole-punch](https://docs.libp2p.io/concepts/nat/hole-punching/) a direct connection.
  The file itself always travels over that direct connection. gofer refuses to send over a relay.
- **Integrity.** Data is encrypted in transit (Noise/TLS). The receiver checks a SHA-256 of the file before saving it.

### Security notes

Usernames are not globally unique: anyone can announce any name. gofer pins a username to
the peer ID the first time you send to it (trust on first use). For important transfers,
compare the pinned peer ID with the output of `gofer whoami` on the other device, or pin it
up front with `gofer contacts add`.

### Limitations

- The receiver must be running `gofer receive`.
- If both sides are behind strict (symmetric) NATs, hole punching can fail. Then forward a
  port on the receiver's router and run `gofer receive --port <n>`.
- Sends single files. Zip folders first.

## Development

```
cmd/gofer/          CLI entry point and commands
internal/config/    identity key, username, contacts (~/.gofer)
internal/p2p/       libp2p host, DHT bootstrap and announce
internal/transfer/  wire protocol, sender and receiver
internal/ui/        progress bar and spinner
internal/update/    self-update from GitHub releases
install.sh, install.ps1
```

```sh
make build     # ./gofer
make test
make dist      # cross-compile all targets into dist/
```

Set `GOFER_HOME=/some/dir` to run a second identity on the same machine.
Releases are built by GitHub Actions when you push a `v*` tag.
