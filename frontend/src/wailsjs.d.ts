// Wails runtime types
export {}

declare global {
  interface Window {
    go: {
      main: {
        App: {
          GetProfiles: () => Promise<ProfileStatus[]>
          GetProfile: (id: string) => Promise<Profile | null>
          Connect: (id: string) => Promise<void>
          Disconnect: (id: string) => Promise<void>
          ConnectAll: () => Promise<void>
          DisconnectAll: () => Promise<void>
          ImportConfig: (path: string) => Promise<Profile>
          DeleteProfile: (id: string) => Promise<void>
          UpdateProfile: (profile: Profile) => Promise<void>
          GetEnvVars: (profileId: string) => Promise<string>
          GetAllEnvVars: () => Promise<string>
          GetSettings: () => Promise<Settings>
          UpdateSettings: (settings: Settings) => Promise<void>
          GetSystemStatus: () => Promise<SystemStatus>
          ConfigureDNS: () => Promise<DNSConfigResult>
          RestoreDNS: () => Promise<void>
          GetWireGuardConfig: (id: string) => Promise<WireGuardConfig>
          GetActiveConnections: () => Promise<ActiveConnection[]>
          TestHostConnectivity: (hostname: string, port: number) => Promise<HostTestResult>
          GetDNSProxyConfig: () => Promise<DNSProxyConfig>
          UpdateDNSProxyConfig: (config: DNSProxyConfig) => Promise<void>
          GetTCPProxyConfig: () => Promise<TCPProxyConfig>
          UpdateTCPProxyConfig: (config: TCPProxyConfig) => Promise<void>
        }
      }
    }
    runtime: {
      EventsOn: (event: string, callback: (...args: unknown[]) => void) => void
      EventsOff: (event: string) => void
      OpenFileDialog: (options: {
        title?: string
        filters?: Array<{ displayName: string; pattern: string }>
      }) => Promise<string>
    }
  }
}

interface ProfileStatus {
  id: string
  name: string
  connected: boolean
  endpoint: string
  bytesSent: number
  bytesRecv: number
  lastHandshake: string
  error?: string
}

interface Profile {
  id: string
  name: string
  configFile: string
  enabled: boolean
  healthCheck: {
    enabled: boolean
    targetIP: string
    intervalSeconds: number
  }
  dns: {
    server: string
    domains: string[]
  }
  tcpProxyPorts?: number[]
}

interface Settings {
  configDir: string
  logLevel: string
  autoConnect: string[]
  portRangeStart: number
  minimizeToTray: boolean
  startMinimized: boolean
}

interface SystemStatus {
  isAdmin: boolean
  dnsConfigured: boolean
  currentDNS: string
  dnsProxyAddress: string
  port53Free: boolean
  dnsClientRunning: boolean
  autoConfigureLoopback: boolean
  autoConfigureDNS: boolean
  usePort53: boolean
  tcpProxyEnabled: boolean
  dnsProxyEnabled: boolean
  dnsProxyPort: number
}

interface DNSConfigResult {
  success: boolean
  dnsAddress: string
  port53Free: boolean
  dnsClientDown: boolean
  error?: string
}

interface WireGuardConfig {
  Interface: {
    address: string
    dns: string
    publicKey: string
    listenPort?: number
  }
  Peer: {
    endpoint: string
    allowedIPs: string
    publicKey: string
  }
}

interface ActiveConnection {
  profileId: string
  profileName: string
  hostname: string
  localAddr: string
  remoteAddr: string
  startTime: string
  bytesIn: number
  bytesOut: number
}

interface HostTestResult {
  hostname: string
  profileId: string
  profileName: string
  dnsResolved: boolean
  realIP: string
  loopbackIP: string
  dnsServer: string
  dnsRule: string
  dnsError?: string
  usedSystemDNS: boolean
  tcpConnected: boolean
  tcpPort: number
  tcpLatencyMs: number
  tcpError?: string
}

interface DNSProxyConfig {
  enabled: boolean
  listenAddress: string
  listenPort: number
  rules: DNSRule[]
  fallback: string
}

interface DNSRule {
  suffix: string
  profileId: string
  dnsServer: string
  stripSuffix?: boolean
  hosts?: Record<string, string>
}

interface TCPProxyConfig {
  enabled: boolean
  tunnelIPs: Record<string, string>
  ports: number[]
}
