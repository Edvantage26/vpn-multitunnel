import { useState, useEffect, useCallback } from 'react'
import Sidebar from './components/Sidebar'
import TunnelDetailPanel, { WireGuardConfigDisplay } from './components/TunnelDetailPanel'
import AddProfileModal from './components/AddProfileModal'
import SettingsModal from './components/SettingsModal'
import ConfigFileEditor from './components/ConfigFileEditor'

// Types matching Go backend
export interface Profile {
  id: string
  name: string
  configFile: string
  enabled: boolean
  autoConnect?: boolean // nil/undefined = true (default)
  healthCheck: {
    enabled: boolean
    targetIP: string
    intervalSeconds: number
  }
  dns: {
    server: string
    domains: string[]
    stripSuffix: boolean
    hosts: Record<string, string>
  }
  tcpProxyPorts?: number[] // Per-profile TCP proxy ports (nil/undefined = use global defaults)
}

export interface ProfileStatus {
  id: string
  name: string
  configFile: string
  connected: boolean
  connecting: boolean
  healthy: boolean
  tunnelIP: string
  bytesSent: number
  bytesRecv: number
  lastHandshake: string
  endpoint: string
}

export interface ActiveConnection {
  hostname: string
  tunnelIP: string
  realIP: string
  profileId: string
  age: string
}

export interface TCPProxyConfig {
  enabled: boolean
  tunnelIPs: Record<string, string>
  ports: number[]
}

export interface SystemStatus {
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

// DNSConfigResult is imported from wailsjs/go/models.ts (main.DNSConfigResult)

export interface Settings {
  logLevel: string
  autoConnect: string[]
  portRangeStart: number
  minimizeToTray: boolean
  startMinimized: boolean
  autoConfigureLoopback: boolean
  autoConfigureDNS: boolean
  dnsListenAddress: string
  dnsFallbackServer: string
}

export interface UpdateSettingsResult {
  dnsProxyRestarted: boolean
  systemDNSReconfigured: boolean
  loopbackIPChanged: boolean
  warning?: string
}

declare global {
  interface Window {
    go: {
      main: {
        App: {
          GetProfiles: () => Promise<ProfileStatus[]>
          GetProfile: (id: string) => Promise<Profile>
          Connect: (id: string) => Promise<void>
          Disconnect: (id: string) => Promise<void>
          DeleteProfile: (id: string) => Promise<void>
          ImportConfig: () => Promise<Profile>
          UpdateProfile: (profile: Profile) => Promise<void>
          TestConnection: (profileId: string, host: string, port: number) => Promise<[boolean, string]>
          GetSettings: () => Promise<Settings>
          UpdateSettings: (settings: Settings) => Promise<UpdateSettingsResult>
          GetTCPProxyConfig: () => Promise<TCPProxyConfig>
          UpdateTCPProxyConfig: (config: TCPProxyConfig) => Promise<void>
          GetActiveConnections: () => Promise<ActiveConnection[]>
          GetTunnelIPs: () => Promise<Record<string, string>>
          IsTCPProxyEnabled: () => Promise<boolean>
          GetTCPProxyListenerCount: () => Promise<number>
          IsRunningAsAdmin: () => Promise<boolean>
          IsDNSConfigured: () => Promise<boolean>
          RestoreDNS: () => Promise<void>
          ConfigureDNS: () => Promise<void>
          GetSystemStatus: () => Promise<SystemStatus>
          GetWireGuardConfig: (profileId: string) => Promise<WireGuardConfigDisplay>
          GetConfigFileContent: (profileId: string) => Promise<string>
          SaveConfigFileContent: (profileId: string, content: string) => Promise<void>
ReorderProfiles: (orderedIDs: string[]) => Promise<void>
          GetAppPath: () => Promise<string>
          TestDNSConnectivity: (address: string) => Promise<{ proxyListening: boolean; systemDNSConfigured: boolean; querySuccess: boolean; resolvedIP: string; error: string }>
        }
      }
    }
  }
}

function App() {
  const [profiles, setProfiles] = useState<ProfileStatus[]>([])
  const [selectedProfile, setSelectedProfile] = useState<Profile | null>(null)
  const [selectedId, setSelectedId] = useState<string>()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showAddModal, setShowAddModal] = useState(false)
  const [showSettings, setShowSettings] = useState(false)
  const [showConfigEditor, setShowConfigEditor] = useState(false)
  const [notification, setNotification] = useState<{ type: 'success' | 'error'; message: string } | null>(null)
  const [systemStatus, setSystemStatus] = useState<SystemStatus | null>(null)

  const showNotification = (type: 'success' | 'error', message: string) => {
    setNotification({ type, message })
    setTimeout(() => setNotification(null), 3000)
  }

  const fetchProfiles = useCallback(async () => {
    try {
      const data = await window.go.main.App.GetProfiles()
      setProfiles(data || [])
      setError(null)
    } catch (err) {
      setError(String(err))
    } finally {
      setLoading(false)
    }
  }, [])

  const fetchSystemStatus = useCallback(async () => {
    try {
      const status = await window.go.main.App.GetSystemStatus()
      setSystemStatus(previous_status => {
        // Detect DNS configuration change: false → true
        if (previous_status && !previous_status.dnsConfigured && status.dnsConfigured) {
          showNotification('success', `DNS proxy configured on ${status.dnsProxyAddress || '127.0.0.53'}`)
        }
        // Detect DNS restore: true → false
        if (previous_status && previous_status.dnsConfigured && !status.dnsConfigured) {
          showNotification('success', 'DNS restored to original configuration')
        }
        return status
      })
    } catch (err) {
      console.error('Failed to fetch system status:', err)
    }
  }, [])

  const fetchSelectedProfile = useCallback(async (id: string) => {
    try {
      const profile = await window.go.main.App.GetProfile(id)
      setSelectedProfile(profile)
    } catch (err) {
      console.error('Failed to fetch profile:', err)
    }
  }, [])

  useEffect(() => {
    fetchProfiles()
    fetchSystemStatus()
    const profileInterval = setInterval(fetchProfiles, 2000)
    const statusInterval = setInterval(fetchSystemStatus, 3000)
    return () => {
      clearInterval(profileInterval)
      clearInterval(statusInterval)
    }
  }, [fetchProfiles, fetchSystemStatus])

  useEffect(() => {
    if (selectedId) {
      fetchSelectedProfile(selectedId)
    }
  }, [selectedId, fetchSelectedProfile])

  const handleConnect = async (id: string) => {
    console.log('[App] handleConnect called for:', id)
    try {
      console.log('[App] Calling backend Connect...')
      await window.go.main.App.Connect(id)
      console.log('[App] Connect succeeded')
      showNotification('success', 'Connected successfully')
      await fetchProfiles()
    } catch (err) {
      console.error('[App] Connect failed:', err)
      showNotification('error', `Failed to connect: ${err}`)
    }
  }

  const handleDisconnect = async (id: string) => {
    console.log('[App] handleDisconnect called for:', id)
    try {
      console.log('[App] Calling backend Disconnect...')
      await window.go.main.App.Disconnect(id)
      console.log('[App] Disconnect succeeded')
      showNotification('success', 'Disconnected')
      await fetchProfiles()
    } catch (err) {
      console.error('[App] Disconnect failed:', err)
      showNotification('error', `Failed to disconnect: ${err}`)
    }
  }

  const handleDelete = async (id: string) => {
    if (!confirm('Are you sure you want to delete this tunnel?')) return
    try {
      await window.go.main.App.DeleteProfile(id)
      showNotification('success', 'Tunnel deleted')
      if (selectedId === id) {
        setSelectedId(undefined)
        setSelectedProfile(null)
      }
      await fetchProfiles()
    } catch (err) {
      showNotification('error', `Failed to delete: ${err}`)
    }
  }

  const handleImport = async () => {
    try {
      const profile = await window.go.main.App.ImportConfig()
      showNotification('success', `Imported ${profile.name}`)
      setShowAddModal(false)
      await fetchProfiles()
    } catch (err) {
      if (String(err) !== 'no file selected') {
        showNotification('error', `Failed to import: ${err}`)
      }
    }
  }

  const handleSelectProfile = (id: string) => {
    setSelectedId(id)
  }

  const handleUpdateProfile = async (profile: Profile) => {
    try {
      await window.go.main.App.UpdateProfile(profile)
      showNotification('success', 'Profile updated')
      await fetchProfiles()
      await fetchSelectedProfile(profile.id)
    } catch (err) {
      showNotification('error', `Failed to update: ${err}`)
    }
  }

  const handleReorderProfiles = async (orderedIDs: string[]) => {
    // Optimistically reorder in local state
    const profileMap = new Map(profiles.map(profile => [profile.id, profile]))
    const reordered = orderedIDs.map(id => profileMap.get(id)!).filter(Boolean)
    setProfiles(reordered)

    try {
      await window.go.main.App.ReorderProfiles(orderedIDs)
    } catch (err) {
      console.error('Failed to reorder profiles:', err)
      await fetchProfiles()
    }
  }

  const selectedStatus = profiles.find(p => p.id === selectedId)

  if (loading) {
    return (
      <div className="h-screen flex items-center justify-center bg-dark-900">
        <div className="text-dark-300">Loading...</div>
      </div>
    )
  }

  return (
    <div className="h-screen flex bg-dark-900 text-dark-100">
      {/* Notification */}
      {notification && (
        <div className={`fixed top-4 right-4 z-50 px-4 py-2 rounded-lg shadow-lg ${
          notification.type === 'success' ? 'bg-green-600' : 'bg-red-600'
        } text-white`}>
          {notification.message}
        </div>
      )}

      {/* Sidebar */}
      <Sidebar
        profiles={profiles}
        selectedId={selectedId}
        onSelect={handleSelectProfile}
        onAddProfile={() => setShowAddModal(true)}
        onOpenSettings={() => setShowSettings(true)}
        onReorder={handleReorderProfiles}
      />

      {/* Main Content */}
      <main className="flex-1 overflow-auto p-6">
        {/* DNS status is now shown via toast notifications when it changes */}

        {error && (
          <div className="mb-4 p-4 bg-red-900/50 border border-red-700 rounded-lg text-red-200">
            {error}
          </div>
        )}

        {selectedProfile && selectedStatus ? (
          <TunnelDetailPanel
            profile={selectedProfile}
            status={selectedStatus}
            onConnect={() => handleConnect(selectedProfile.id)}
            onDisconnect={() => handleDisconnect(selectedProfile.id)}
            onDelete={() => handleDelete(selectedProfile.id)}
            onEditConfig={() => setShowConfigEditor(true)}
            onRefresh={() => fetchSelectedProfile(selectedProfile.id)}
            onUpdateProfile={handleUpdateProfile}
          />
        ) : (
          <div className="h-full flex items-center justify-center text-dark-400">
            <div className="text-center">
              <svg className="w-16 h-16 mx-auto mb-4 text-dark-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5}
                  d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
              </svg>
              <p className="text-lg">Select a tunnel to view details</p>
              {profiles.length === 0 && (
                <button
                  onClick={() => setShowAddModal(true)}
                  className="mt-4 btn btn-primary"
                >
                  Import Configuration
                </button>
              )}
            </div>
          </div>
        )}
      </main>

      {/* Modals */}
      {showAddModal && (
        <AddProfileModal
          onClose={() => setShowAddModal(false)}
          onImport={handleImport}
        />
      )}

      {showSettings && (
        <SettingsModal
          onClose={() => setShowSettings(false)}
        />
      )}

      {showConfigEditor && selectedProfile && (
        <ConfigFileEditor
          profileId={selectedProfile.id}
          profileName={selectedProfile.name}
          configFile={selectedProfile.configFile}
          onClose={() => setShowConfigEditor(false)}
        />
      )}

    </div>
  )
}

export default App
