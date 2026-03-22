# VPN MultiTunnel MCP Debug Server

Servidor MCP para depurar el estado de las VPNs, testear conectividad, ver logs y errores en tiempo real.

## Requisitos

- Python 3.10+
- VPN MultiTunnel corriendo con la Debug API habilitada

## Instalación

```bash
cd mcp
pip install -r requirements.txt
```

## Uso

### 1. Verificar que VPN MultiTunnel está corriendo

La Debug API debe estar habilitada en la configuración (`config.json`):

```json
{
  "settings": {
    "debugApiEnabled": true,
    "debugApiPort": 8765
  }
}
```

### 2. Probar la API manualmente

```bash
# Verificar que la API responde
curl http://127.0.0.1:8765/api/health

# Ver estado de VPNs
curl http://127.0.0.1:8765/api/status

# Ver host mappings
curl http://127.0.0.1:8765/api/host-mappings

# Ver logs
curl http://127.0.0.1:8765/api/logs?limit=50

# Ver errores
curl http://127.0.0.1:8765/api/errors

# Ver métricas
curl http://127.0.0.1:8765/api/metrics
```

### 3. Configurar en Claude Code

Añadir a la configuración de Claude Code (`~/.claude/settings.json` o `claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "vpn-debug": {
      "command": "python",
      "args": ["C:/Users/edgar/SynologyDrive/CodeProjects/Edvantage/vpn-multitunnel/mcp/vpn_mcp_server.py"]
    }
  }
}
```

### 4. Usar las herramientas en Claude Code

Una vez configurado, Claude Code tendrá acceso a las siguientes herramientas:

#### `test_host`
Testea conectividad a un host a través de la VPN.

```
test_host hostname="db.svi" port=5432
```

Resultado:
- Resolución DNS (real IP, loopback IP, regla que matcheó)
- Conectividad TCP (éxito/error, latencia)
- Perfil VPN usado

#### `diagnose_dns`
Diagnostica por qué un hostname no resuelve.

```
diagnose_dns hostname="api.internal"
```

Resultado:
- Regla que matchea (o ninguna)
- Si resolvería o no
- Sugerencia para arreglarlo

#### `get_vpn_status`
Ver estado de todas las VPNs.

```
get_vpn_status
```

Resultado:
- Estado de conexión
- Health check
- Bytes transferidos
- Latencia promedio

#### `get_host_mappings`
Ver todos los hosts con sus IPs asignadas.

```
get_host_mappings
```

Resultado:
- Hostname → Real IP → Loopback IP → Perfil

#### `get_logs`
Ver logs filtrados.

```
get_logs level="error" component="dns" limit=50
```

Componentes disponibles:
- `dns` - DNS proxy
- `tunnel` - Túneles WireGuard
- `proxy` - TCP proxy
- `app` - Aplicación principal
- `api` - Debug API
- `frontend:*` - Logs del frontend (React)

#### `get_frontend_logs`
Ver solo logs del frontend (React).

```
get_frontend_logs limit=100
```

#### `get_errors`
Ver errores recientes.

```
get_errors limit=20
```

#### `get_metrics`
Ver métricas de rendimiento.

```
get_metrics
```

Métricas incluidas:
- DNS queries (total, éxitos, fallos, cache hits)
- Conexiones TCP proxy
- Latencias por perfil

#### `get_diagnostic_report`
Generar reporte completo de diagnóstico.

```
get_diagnostic_report
```

Incluye:
- Info del sistema
- Estado de todas las VPNs
- Configuración DNS
- Host mappings
- Errores recientes
- Logs recientes
- Métricas

#### `get_loopback_ips`
Ver todas las IPs loopback asignadas.

```
get_loopback_ips
```

## Endpoints HTTP

| Método | Endpoint | Descripción |
|--------|----------|-------------|
| GET | `/api/health` | Health check |
| GET | `/api/status` | Estado completo |
| GET | `/api/host-mappings` | Host mappings activos |
| POST | `/api/test-host` | Test de conectividad |
| POST | `/api/diagnose-dns` | Diagnóstico DNS |
| GET | `/api/logs` | Logs (filtrados) |
| GET | `/api/logs/frontend` | Solo logs del frontend |
| GET | `/api/errors` | Errores recientes |
| GET | `/api/metrics` | Métricas |
| POST | `/api/diagnostic` | Reporte completo |

## Ejemplo: Depurar por qué no conecta a un host

```
# 1. Ver estado de VPNs
get_vpn_status

# 2. Diagnosticar DNS
diagnose_dns hostname="db.svi"

# 3. Si la VPN está conectada, testear el host
test_host hostname="db.svi" port=5432

# 4. Ver errores recientes si falló
get_errors limit=10

# 5. Ver logs del componente DNS
get_logs component="dns" limit=50
```

## Desarrollo

### Ejecutar servidor manualmente

```bash
python vpn_mcp_server.py
```

### Usar el cliente directamente

```python
from vpn_client import VPNDebugClient

client = VPNDebugClient()

# Test de un host
result = client.test_host("db.svi", 5432)
print(result)

# Diagnóstico DNS
diag = client.diagnose_dns("api.internal")
print(diag)

# Ver errores
errors = client.get_errors(10)
for err in errors:
    print(f"{err['timestamp']}: {err['error']}")
```
