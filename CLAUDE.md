# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Coding Style — Variable Naming

**ZERO single-letter identifiers.** This applies to ALL identifiers: variables, parameters, receivers, loop counters, error returns — everything. Never use `i`, `j`, `k`, `n`, `x`, `a`, `e`, `r`, `w`, `m`, `s`, `p`, `c`, `t`, etc. alone.

This includes Go method receivers: use `app` not `a`, `proxy` not `p`, `server` not `s`, `config` not `c`, `manager` not `m`.

All names must be descriptive, following this format:

**`[order/count] [characteristic] [concept]`**

Examples:
- `num_pending_inserts` not `n`
- `idx_current_profile` not `i`
- `dns_rule_entry` not `r`
- `listener_port_number` not `port` (when ambiguous)
- `retry_attempt_count` not `j`
- `func (app *App)` not `func (a *App)`
- `func (proxy *DNSProxy)` not `func (p *DNSProxy)`
- `func (network_config *NetworkConfig)` not `func (n *NetworkConfig)`

Long variable names are preferred over short ambiguous ones. Don't fear verbosity — clarity always wins.

## Project Overview

**VPN MultiTunnel** is a Windows desktop application for managing multiple WireGuard VPN tunnels simultaneously with advanced proxy capabilities (DNS proxy, TCP proxy, port forwarding). Built with Wails (Go backend + React/TypeScript frontend), it uses userspace WireGuard (netstack) requiring no kernel driver.

### Key Features
- Multiple simultaneous WireGuard tunnels
- Transparent TCP proxy via loopback IPs
- DNS proxy with domain-based routing
- Windows service for privileged operations (no UAC prompts after install)
- System tray integration

## Development Commands

```bash
# Full-stack development with hot reload
wails dev

# Production build (outputs VPNMultiTunnel.exe)
wails build

# Build the Windows service
go build -o build/bin/VPNMultiTunnel-service.exe ./cmd/service

# Frontend only (Vite dev server on :5173)
cd frontend && npm run dev

# Windows quick start (sets Go path)
run-dev.bat
```

## Building the Installer

The project uses NSIS (Nullsoft Scriptable Install System) for creating Windows installers.

### Prerequisites
- [NSIS](https://nsis.sourceforge.io/Download) installed and in PATH
- Wails CLI installed (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)

### Build Commands

```bash
# Build standalone exe (no installer)
wails build

# Build service executable (required before installer)
go build -o build/bin/VPNMultiTunnel-service.exe ./cmd/service

# Build with NSIS installer (AMD64)
wails build --nsis

# Build with specific architecture
wails build --target windows/amd64 --nsis
wails build --target windows/arm64 --nsis
```

### Output Files

After building:
```
build/bin/VPNMultiTunnel.exe              # Main GUI application
build/bin/VPNMultiTunnel-service.exe      # Windows service
build/bin/VPNMultiTunnel-amd64-installer.exe  # NSIS installer (after --nsis)
```

### Manual NSIS Build (for debugging)

```bash
# First, build the service
go build -o build/bin/VPNMultiTunnel-service.exe ./cmd/service

# Do a regular NSIS build to generate wails_tools.nsh
wails build --target windows/amd64 --nsis

# Then manually invoke NSIS for debugging
cd build/windows/installer
makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\VPNMultiTunnel.exe project.nsi
```

### What the Installer Does

The installer (`build/windows/installer/project.nsi`):
1. Installs the GUI app and service executable
2. Installs and starts the Windows service (`VPNMultiTunnelService`)
3. Pre-creates loopback IPs (127.0.1.1 - 127.0.10.1)
4. Configures autostart via registry
5. Creates desktop and Start Menu shortcuts
6. Installs WebView2 runtime if missing
7. Default install path: `C:\Program Files\Edvantage\VPN MultiTunnel`

On uninstall, it reverses all of the above.

## Architecture

### Backend (Go)
```
main.go                 # Wails app entry point
app.go                  # Main controller - all frontend-exposed methods

cmd/
└── service/
    └── main.go         # Windows service entry point (install/uninstall/start/stop)

internal/
├── config/             # Configuration types and JSON persistence
│   ├── config.go       # AppConfig, Profile, DNSProxy, TCPProxy, Settings types
│   ├── loader.go       # Config file I/O
│   └── wireguard.go    # WireGuard .conf file parsing
├── ipc/                # Inter-process communication (GUI ↔ Service)
│   ├── protocol.go     # Request/Response types, operation constants
│   ├── server.go       # Named pipe server (runs in service)
│   └── client.go       # Named pipe client (used by GUI app)
├── svchost/            # Windows service implementation
│   ├── handler.go      # Service handler (implements svc.Handler)
│   └── operations.go   # Privileged operations (netsh, DNS, etc.)
├── tunnel/             # WireGuard tunnel management
│   ├── manager.go      # Tunnel lifecycle
│   ├── netstack.go     # Userspace WireGuard implementation
│   └── health.go       # Health checking
├── proxy/              # Proxy implementations
│   ├── manager.go      # Proxy lifecycle orchestration
│   ├── dns.go          # DNS proxy with rule-based routing
│   ├── tcpproxy.go     # Transparent TCP interception
│   ├── portforward.go  # TCP/UDP port forwarding
│   └── hostmapping.go  # DNS resolution cache
├── service/            # Business logic (profile.go - CRUD)
├── system/             # Windows integration (loopback IPs, DNS via netsh)
│   └── network_windows.go  # Network config with service/UAC fallback
└── tray/               # System tray integration
```

### Frontend (React/TypeScript)
```
frontend/src/
├── App.tsx             # Main component with state
├── components/         # UI components (ProfileCard, Sidebar, modals)
└── hooks/useProfiles.ts # Profile state management
```

### Service Architecture

```
┌─────────────────────┐     Named Pipe      ┌──────────────────────────────┐
│   VPN MultiTunnel   │◄──────────────────►│  VPN MultiTunnel Service     │
│   (GUI - Usuario)   │   IPC Protocol      │  (SYSTEM - Privilegiado)     │
└─────────────────────┘                     └──────────────────────────────┘
         │                                            │
         │ Wails                                      │ Ejecuta
         ▼                                            ▼
    React Frontend                           - netsh (loopback IPs)
                                             - PowerShell (DNS)
                                             - Service control (Dnscache)
```

**Named Pipe**: `\\.\pipe\VPNMultiTunnelService`

**IPC Operations**:
- `OpAddLoopbackIP` / `OpRemoveLoopbackIP` - Manage loopback addresses
- `OpSetDNS` / `OpSetDNSv6` / `OpResetDNS` - DNS server configuration
- `OpConfigureSystemDNS` / `OpRestoreSystemDNS` - Transparent DNS setup
- `OpStopDNSClient` / `OpStartDNSClient` - DNS Client service control
- `OpPing` - Health check

### Key Design Decisions

1. **Userspace tunneling**: Uses `golang.zx2c4.com/wireguard` netstack instead of kernel drivers for portability
2. **Loopback IP strategy**: Assigns unique 127.0.x.1 addresses per profile for transparent TCP proxying without driver requirements
3. **DNS proxy routing**: Domain suffix rules route queries through specific tunnels (e.g., `.internal` → office tunnel)
4. **Windows service**: Privileged operations (netsh, DNS) run via a Windows service, eliminating UAC prompts after installation
5. **UAC fallback**: If service is unavailable, falls back to UAC elevation
6. **Embedded frontend**: React app compiled and embedded in Go binary via Wails

### Data Flow

```
React UI → Wails Bridge → App.go → TunnelManager/ProxyManager → netstack/system
                                          ↓
                                   NetworkConfig
                                          ↓
                              ┌───────────┴───────────┐
                              │                       │
                        IPC Client              UAC Elevation
                              │                   (fallback)
                              ▼
                     VPNMultiTunnelService
```

Frontend communicates via Wails-generated bindings in `frontend/wailsjs/`. The `App` struct in `app.go` exposes all methods to JavaScript.

## Configuration

JSON-based config with auto-save. Key structures:
- **settings**: Global app settings (port ranges, auto-connect, tray behavior, useService)
- **profiles[]**: Individual tunnel configs with port forwards, health checks, DNS settings
- **dnsProxy**: Rule-based DNS routing with domain suffix matching
- **tcpProxy**: Transparent proxy config with loopback IP assignments per profile

### Settings Fields
| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `configDir` | string | "./configs" | WireGuard config files directory |
| `autoConnect` | string[] | [] | Profile IDs to auto-connect on startup |
| `minimizeToTray` | bool | true | Minimize to tray on close |
| `startMinimized` | bool | false | Start minimized to tray |
| `autoConfigureLoopback` | bool | true | Auto-add loopback IPs |
| `autoConfigureDNS` | bool | true | Auto-configure system DNS |
| `usePort53` | bool | true | Use port 53 for DNS proxy |
| `useService` | bool | true | Use Windows service for privileged ops |

See `config.example.json` for full structure.

## Key Files for Common Tasks

| Task | Files |
|------|-------|
| Add new App method | `app.go` (add method), frontend calls via Wails bindings |
| Modify tunnel behavior | `internal/tunnel/manager.go`, `netstack.go` |
| Change proxy logic | `internal/proxy/` (specific proxy file) |
| Update UI component | `frontend/src/components/` |
| Modify config schema | `internal/config/config.go`, update `loader.go` if needed |
| Windows system calls | `internal/system/network_windows.go` |
| Add IPC operation | `internal/ipc/protocol.go`, `internal/svchost/operations.go`, `internal/ipc/client.go` |
| Modify service behavior | `internal/svchost/handler.go`, `cmd/service/main.go` |
| Update installer | `build/windows/installer/project.nsi` |

## Service Management

### Manual Service Control (for testing)

```bash
# Install service (requires admin)
.\build\bin\VPNMultiTunnel-service.exe install

# Start service
.\build\bin\VPNMultiTunnel-service.exe start

# Check status
.\build\bin\VPNMultiTunnel-service.exe status
# or
sc query VPNMultiTunnelService

# Stop service
.\build\bin\VPNMultiTunnel-service.exe stop

# Uninstall service
.\build\bin\VPNMultiTunnel-service.exe uninstall

# Run interactively (for debugging)
.\build\bin\VPNMultiTunnel-service.exe run
```

### Service Logs

Logs are written to: `%ProgramData%\VPNMultiTunnel\service.log`

## Local Testing (Without Installer)

Para probar en tu máquina de desarrollo sin ejecutar el instalador completo.

### Opción 1: Prueba Rápida con Servicio

```powershell
# 1. Abrir PowerShell como Administrador
# 2. Ir al directorio del proyecto
cd "C:\Users\edgar\SynologyDrive\CodeProjects\Edvantage\vpn-multitunnel"

# 3. Instalar y arrancar el servicio
.\build\bin\VPNMultiTunnel-service.exe install
.\build\bin\VPNMultiTunnel-service.exe start

# 4. Verificar que está corriendo
.\build\bin\VPNMultiTunnel-service.exe status

# 5. Crear algunas IPs de loopback manualmente
netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.1.1 255.255.255.0
netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.2.1 255.255.255.0
netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.3.1 255.255.255.0
```

Luego en otra terminal (sin admin):
```powershell
# 6. Ejecutar la app
.\build\bin\VPNMultiTunnel.exe
```

### Opción 2: Desarrollo con Hot Reload

```powershell
# Terminal 1 (Admin): Servicio en modo interactivo
.\build\bin\VPNMultiTunnel-service.exe run

# Terminal 2 (normal): App con hot reload
wails dev
```

### Opción 3: Sin Servicio (con UAC)

Si no quieres instalar el servicio, la app funciona igual pero pedirá UAC para operaciones privilegiadas:

```powershell
# Ejecutar directamente
.\build\bin\VPNMultiTunnel.exe

# O en modo desarrollo
wails dev
```

### Verificar Conexión al Servicio

1. **Ver logs del servicio en tiempo real**:
```powershell
Get-Content "$env:ProgramData\VPNMultiTunnel\service.log" -Wait
```

2. **Verificar conexión IPC desde la app** (en DevTools F12):
```javascript
window.go.main.App.GetSystemStatus().then(console.log)
// Debe mostrar: serviceConnected: true
```

3. **Verificar IPs de loopback**:
```powershell
netsh interface ipv4 show addresses "Loopback Pseudo-Interface 1"
```

### Limpiar Después de Probar

```powershell
# PowerShell como Admin

# Detener y desinstalar servicio
.\build\bin\VPNMultiTunnel-service.exe stop
.\build\bin\VPNMultiTunnel-service.exe uninstall

# Remover IPs de loopback (opcional)
netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.1.1
netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.2.1
netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.3.1
```

### Troubleshooting

| Problema | Solución |
|----------|----------|
| "Service not available" en logs | El servicio no está corriendo. Ejecutar `VPNMultiTunnel-service.exe start` |
| UAC sigue apareciendo | Verificar que `serviceConnected: true` en GetSystemStatus() |
| "Access denied" al instalar servicio | Ejecutar PowerShell como Administrador |
| Loopback IP ya existe | Ignorar el error, la IP ya está configurada |
| Puerto 53 en uso | Detener DNS Client: `Stop-Service Dnscache` (el servicio lo hace automáticamente) |

## Verification Checklist

After building/installing:

1. **Verify executables**:
   - `VPNMultiTunnel.exe` exists and shows "VPN MultiTunnel" in properties
   - `VPNMultiTunnel-service.exe` exists

2. **Verify service**:
   ```bash
   sc query VPNMultiTunnelService
   ```
   Should show `STATE: RUNNING`

3. **Verify autostart**:
   ```bash
   reg query "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v "VPN MultiTunnel"
   ```

4. **Verify loopback IPs**:
   ```bash
   netsh interface ipv4 show addresses "Loopback Pseudo-Interface 1"
   ```
   Should show 127.0.1.1 through 127.0.10.1

5. **Verify app connects to service**:
   - Open the app
   - Check `GetSystemStatus()` returns `serviceConnected: true`
   - Connect a VPN profile - should work without UAC prompt

## Dependencies

Key Go dependencies (see `go.mod`):
- `github.com/wailsapp/wails/v2` - Desktop app framework
- `golang.zx2c4.com/wireguard` - WireGuard implementation
- `github.com/Microsoft/go-winio` - Named pipes for Windows
- `golang.org/x/sys` - Windows service support
- `github.com/miekg/dns` - DNS proxy
- `github.com/energye/systray` - System tray

## Security Notes

- Named pipe uses DACL restricting access to authenticated local users
- Service validates all input (IPs, interface names) before execution
- Only loopback IPs (127.x.x.x) can be added via the service
- Service logs all privileged operations
