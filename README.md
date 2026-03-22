# VPN MultiTunnel

A Windows desktop application for managing **multiple WireGuard VPN tunnels simultaneously** with built-in DNS proxy, transparent TCP proxy, and domain-based routing.

Built with [Wails](https://wails.io/) (Go backend + React/TypeScript frontend), it uses **userspace WireGuard** (netstack) — no kernel driver required.

## Features

- **Multiple simultaneous WireGuard tunnels** — connect to several VPNs at once
- **DNS proxy with domain suffix routing** — route DNS queries to specific tunnels based on domain patterns
- **Transparent TCP proxy** — access remote services as if they were local, via loopback IPs
- **Suffix stripping** — use short hostnames like `db.office` that resolve through the correct tunnel
- **Static host mappings** — map hostnames to IPs without DNS queries
- **Windows service** — privileged operations run via a service (no UAC prompts after install)
- **System tray integration** — minimize to tray, auto-start on login
- **Health checks** — periodic connectivity monitoring per tunnel

## How It Works

### Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                        VPN MultiTunnel                               │
│                                                                      │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐              │
│  │  WireGuard   │    │  WireGuard   │    │  WireGuard   │   ...      │
│  │  Tunnel #1   │    │  Tunnel #2   │    │  Tunnel #3   │            │
│  │  (netstack)  │    │  (netstack)  │    │  (netstack)  │            │
│  └──────┬───────┘    └──────┬───────┘    └──────┬───────┘            │
│         │                   │                   │                    │
│  ┌──────┴───────────────────┴───────────────────┴───────┐            │
│  │                    DNS Proxy                          │            │
│  │         Domain suffix → Tunnel routing                │            │
│  │         .office → Tunnel #1                           │            │
│  │         .cloud → Tunnel #2                            │            │
│  │         .lab → Tunnel #3                              │            │
│  └──────────────────────┬────────────────────────────────┘            │
│                         │                                            │
│  ┌──────────────────────┴────────────────────────────────┐           │
│  │                  TCP Proxy                             │           │
│  │        Loopback IP → Real IP via correct tunnel        │           │
│  │        127.0.1.x ─── Tunnel #1                         │           │
│  │        127.0.2.x ─── Tunnel #2                         │           │
│  │        127.0.3.x ─── Tunnel #3                         │           │
│  └────────────────────────────────────────────────────────┘           │
└──────────────────────────────────────────────────────────────────────┘
```

### Multiple VPN Tunnels (Userspace)

Each VPN profile creates an independent WireGuard tunnel running entirely in **userspace** via the [wireguard-go netstack](https://git.zx2c4.com/wireguard-go). This means:

- No kernel driver installation required
- Multiple tunnels can run simultaneously without conflicts
- Each tunnel has its own encryption keys, peers, and allowed IP ranges
- Tunnels provide a `Dial()` interface — the app can open TCP/UDP connections through any specific tunnel

### DNS Proxy & Domain Suffix Routing

The built-in DNS proxy intercepts DNS queries and routes them through the correct VPN tunnel based on **domain suffix rules**.

**How suffix routing works:**

1. You define rules mapping domain suffixes to VPN profiles:
   - `.office` → Office VPN tunnel
   - `.cloud` → Cloud VPN tunnel
   - `.lab` → Lab VPN tunnel

2. When an application resolves `db.office`, the DNS proxy:
   - Matches the `.office` suffix → routes through the Office VPN tunnel
   - **Strips the suffix** (configurable): queries the tunnel's DNS server for just `db`
   - Returns the resolved IP to the application

3. Queries that don't match any rule go to the **fallback DNS** (system DNS or a configured server like `8.8.8.8`)

**Suffix stripping** is key — it lets you use invented suffixes as routing hints. Your remote DNS server doesn't need to know about `.office`; it only sees the bare hostname `db`.

**Static host mappings** can bypass DNS entirely:
```json
{
  "suffix": ".office",
  "profileId": "office-vpn",
  "dnsServer": "10.0.0.53",
  "hosts": {
    "db": "10.0.1.14",
    "api": "10.0.1.20"
  }
}
```

### Transparent TCP Proxy

The TCP proxy makes remote services accessible through **loopback IPs**, so any application (browsers, database clients, etc.) can connect without VPN routing table changes.

**How it works:**

1. Each VPN profile is assigned a unique loopback IP (e.g., `127.0.1.1`, `127.0.2.1`)
2. When the DNS proxy resolves a hostname, instead of returning the real remote IP, it:
   - Stores the real IP in a **host mapping cache** (`hostname → real IP + profile`)
   - Assigns a unique loopback IP to that hostname
   - Returns the loopback IP to the application
3. When the application connects to the loopback IP:
   - The TCP proxy intercepts the connection
   - Looks up the real destination from the host mapping cache
   - Dials through the correct WireGuard tunnel
   - Relays data bidirectionally between the application and the remote service

### End-to-End Connection Flow

Here's what happens when DBeaver connects to `db.office:5432`:

```
Step 1: DNS Resolution
   DBeaver resolves "db.office"
       ↓
   Windows sends query to system DNS (127.0.0.53)
       ↓
   DNS Proxy matches suffix ".office" → Office VPN profile
       ↓
   Strips suffix, queries "db" through Office tunnel's DNS server (10.0.0.53)
       ↓
   Remote DNS responds: db → 10.0.1.14
       ↓
   DNS Proxy stores mapping: 127.0.100.1 → 10.0.1.14 (via Office tunnel)
       ↓
   Returns 127.0.100.1 to DBeaver (not the real IP)

Step 2: TCP Connection
   DBeaver connects to 127.0.100.1:5432
       ↓
   TCP Proxy intercepts on loopback listener
       ↓
   Looks up 127.0.100.1 → real IP 10.0.1.14, profile: Office VPN
       ↓
   Dials 10.0.1.14:5432 through Office WireGuard tunnel
       ↓
   Bidirectional relay: DBeaver ↔ TCP Proxy ↔ WireGuard ↔ Remote DB

Result: DBeaver thinks it's connecting to a local address,
        but traffic flows through the encrypted VPN tunnel.
```

### System DNS Integration

When a VPN profile connects, the app automatically:

1. Configures the system DNS to point to the DNS proxy (`127.0.0.53`)
2. Adds necessary loopback IPs to the network interface
3. Flushes the Windows DNS cache

This is done transparently via a **Windows service** running with SYSTEM privileges, avoiding UAC prompts. If the service isn't available, it falls back to UAC elevation.

## Configuration

The app uses a JSON config file. Here's an example with two VPN profiles and DNS routing:

```json
{
  "settings": {
    "configDir": "./configs",
    "autoConnect": ["office-vpn"],
    "minimizeToTray": true,
    "useService": true
  },
  "profiles": [
    {
      "id": "office-vpn",
      "name": "Office Network",
      "configFile": "office.conf",
      "enabled": true,
      "healthCheck": {
        "enabled": true,
        "targetIP": "10.0.0.1",
        "intervalSeconds": 30
      },
      "dns": {
        "server": "10.0.0.53",
        "domains": [".office"]
      }
    },
    {
      "id": "cloud-vpn",
      "name": "Cloud Services",
      "configFile": "cloud.conf",
      "enabled": true,
      "dns": {
        "server": "172.16.0.53",
        "domains": [".cloud"]
      }
    }
  ],
  "dnsProxy": {
    "enabled": true,
    "listenPort": 53,
    "rules": [
      {
        "suffix": ".office",
        "profileId": "office-vpn",
        "dnsServer": "10.0.0.53",
        "stripSuffix": true,
        "hosts": {
          "db": "10.0.1.14",
          "api": "10.0.1.20"
        }
      },
      {
        "suffix": ".cloud",
        "profileId": "cloud-vpn",
        "dnsServer": "172.16.0.53",
        "stripSuffix": true
      }
    ],
    "fallback": "system"
  },
  "tcpProxy": {
    "enabled": true,
    "tunnelIPs": {
      "office-vpn": "127.0.1.1",
      "cloud-vpn": "127.0.2.1"
    }
  }
}
```

## Building

### Prerequisites

- [Go](https://go.dev/) 1.21+
- [Node.js](https://nodejs.org/) 18+
- [Wails CLI](https://wails.io/docs/gettingstarted/installation): `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- [NSIS](https://nsis.sourceforge.io/Download) (for installer only)

### Build Commands

```bash
# Development with hot reload
wails dev

# Production build
wails build

# Build the Windows service
go build -o build/bin/VPNMultiTunnel-service.exe ./cmd/service

# Build NSIS installer
wails build --nsis
```

### Output

```
build/bin/VPNMultiTunnel.exe                  # GUI application
build/bin/VPNMultiTunnel-service.exe          # Windows service
build/bin/VPNMultiTunnel-amd64-installer.exe  # Installer (with --nsis)
```

## Tech Stack

- **Backend**: Go — WireGuard tunnel management, DNS/TCP proxying, Windows service
- **Frontend**: React + TypeScript — profile management UI
- **Desktop framework**: [Wails v2](https://wails.io/) — Go ↔ JS bridge, WebView2
- **WireGuard**: [wireguard-go](https://git.zx2c4.com/wireguard-go) with netstack (userspace)
- **DNS**: [miekg/dns](https://github.com/miekg/dns) — DNS proxy server
- **IPC**: Windows named pipes ([go-winio](https://github.com/microsoft/go-winio)) — GUI ↔ Service communication

## License

MIT
