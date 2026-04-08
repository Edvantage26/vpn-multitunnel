import { useState, useEffect, useCallback } from 'react'
import NavBar from './components/NavBar'
import type { NavView } from './components/NavBar'
import Sidebar from './components/Sidebar'
import TunnelDetailPanel, { WireGuardConfigDisplay } from './components/TunnelDetailPanel'
import ImportWizard from './components/ImportWizard'
import SettingsView from './components/SettingsView'
import ConfigFileEditor from './components/ConfigFileEditor'
import ChangelogModal from './components/ChangelogModal'
import Traffic from './components/Traffic'
import LogsView from './components/LogsView'

// Types matching Go backend
export interface Profile {
  id: string
  name: string
  type: string
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
  credentials?: {
    username?: string
    password?: string
  }
}

export interface ProfileStatus {
  id: string
  name: string
  type: string
  configFile: string
  connected: boolean
  connecting: boolean
  healthy: boolean
  tunnelIP: string
  bytesSent: number
  bytesRecv: number
  lastHandshake: string
  endpoint: string
  lastError?: string
  dnsIssue?: string
  clientVersion?: string
}

export interface ActiveConnection {
  hostname: string
  tunnelIP: string
  realIP: string
  profileId: string
  age: string
}

export interface AdapterSummary {
  name: string
  description: string
  ipv4Addrs: string[]
  dnsServers: string[]
  isUp: boolean
  isVPN: boolean
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
  activeInterface?: string
  dnsIssue?: string
}

// DNSConfigResult is imported from wailsjs/go/models.ts (app.DNSConfigResult)

export interface Settings {
  logLevel: string
  autoConnect: string[]
  portRangeStart: number
  startMinimized: boolean
  autoConfigureLoopback: boolean
  autoConfigureDNS: boolean
  dnsListenAddress: string
  dnsFallbackServer: string
  advancedMode: boolean
}

export interface UpdateSettingsResult {
  dnsProxyRestarted: boolean
  systemDNSReconfigured: boolean
  loopbackIPChanged: boolean
  warning?: string
}

export interface ReleaseEntry {
  version: string
  name: string
  notes: string
  publishedAt: string
}

export interface UpdateInfo {
  available: boolean
  currentVersion: string
  latestVersion: string
  releases: ReleaseEntry[]
  installerURL: string
}

export interface DNSDiagnosticStep {
  name: string
  status: 'ok' | 'fail' | 'warn' | 'skip'
  detail: string
  fix?: string
}

export interface DNSDiagnosticDetail {
  steps: DNSDiagnosticStep[]
  activeInterface: string
  currentSystemDNS: string[]
  expectedDnsAddress: string
  dnsProxyEnabled: boolean
  dnsProxyListenPort: number
  dnsClientRunning: boolean
  serviceConnected: boolean
  systemDnsConfigured: boolean
  hasMatchingRule: boolean
  matchedRuleSuffix?: string
  matchedRuleProfile?: string
  matchedRuleDns?: string
  tunnelConnected: boolean
  tcpProxyEnabled: boolean
  tcpProxyTunnelIPs: Record<string, string>
  profileHasTunnelIP: boolean
  profileTunnelIP?: string
  tcpProxyListenerCount: number
  resolvedToLoopback: boolean
  resolvedAddress?: string
  directTunnelDnsResult?: string
  directTunnelDnsOk: boolean
  proxyDirectResult?: string
  proxyDirectOk: boolean
  rootCause: string
}

export interface SystemLogEntry {
  timestamp: string
  source: 'service' | 'eventlog' | 'crash'
  level: 'info' | 'warn' | 'error'
  message: string
}

export interface LogEntry {
  timestamp: string
  level: 'debug' | 'info' | 'warn' | 'error'
  component: string
  profileId: string
  message: string
  fields?: Record<string, unknown>
}

export interface ErrorEntry {
  timestamp: string
  component: string
  profileId: string
  operation: string
  error: string
  stackTrace?: string
  context?: Record<string, unknown>
  resolved: boolean
  resolvedAt?: string
}

export interface HostTestResult {
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
  dnsDiagnostics?: DNSDiagnosticDetail
  tcpConnected: boolean
  tcpPort: number
  tcpLatencyMs: number
  tcpError?: string
}

declare global {
  interface Window {
    go: {
      app: {
        App: {
          GetProfiles: () => Promise<ProfileStatus[]>
          GetProfile: (id: string) => Promise<Profile>
          Connect: (id: string) => Promise<void>
          ConnectWithCredentials: (id: string, username: string, password: string) => Promise<void>
          ProfileNeedsCredentials: (id: string) => Promise<boolean>
          IsOpenVPNInstalled: () => Promise<boolean>
          GetOpenVPNStatus: () => Promise<{ installed: boolean; version: string; path: string; needsUpgrade: boolean }>
          InstallOpenVPN: () => Promise<void>
          EnsureTAPAdapter: () => Promise<void>
          IsWatchGuardInstalled: () => Promise<boolean>
          GetWatchGuardDownloadURL: (profileId: string) => Promise<string>
          Disconnect: (id: string) => Promise<void>
          GetProfileConfigPath: (id: string) => Promise<string>
          DeleteProfile: (id: string, deleteConfigFile: boolean) => Promise<void>
          ImportConfig: () => Promise<Profile>
          ImportConfigByType: (vpnType: string) => Promise<Profile>
          CreateConfigFromText: (configName: string, configContent: string) => Promise<Profile>
          CreateConfigFromTextWithType: (configName: string, configContent: string, vpnType: string) => Promise<Profile>
          CreateWatchGuardProfile: (profileName: string, serverAddress: string, serverPort: string, username: string) => Promise<Profile>
          CreateExternalProfile: (profileName: string, adapterName: string, adapterAutoDetect: boolean, dnsServer: string) => Promise<Profile>
          GetNetworkAdapters: () => Promise<AdapterSummary[]>
          UpdateProfile: (profile: Profile) => Promise<void>
          TestConnection: (profileId: string, host: string, port: number) => Promise<[boolean, string]>
          TestHost: (hostname: string, port: number, profileId: string, useSystemDNS: boolean) => Promise<HostTestResult>
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
          GetDataPath: () => Promise<string>
          OpenFolderInExplorer: (folderPath: string) => Promise<void>
          TestDNSConnectivity: (address: string) => Promise<{ proxyListening: boolean; systemDNSConfigured: boolean; querySuccess: boolean; resolvedIP: string; error: string }>
          ExportConfiguration: () => Promise<void>
          ImportConfiguration: () => Promise<void>
          CheckForUpdates: () => Promise<UpdateInfo>
          ForceCheckForUpdates: () => Promise<UpdateInfo>
          DownloadAndInstallUpdate: () => Promise<void>
          GetAppVersion: () => Promise<string>
          GetDebugLogs: (level: string, component: string, limit: number) => Promise<LogEntry[]>
          GetSystemLogs: (limit: number) => Promise<import('./App').SystemLogEntry[]>
          GetProfileLogs: (profileId: string, level: string, limit: number) => Promise<LogEntry[]>
          GetProfileErrors: (profileId: string, limit: number) => Promise<ErrorEntry[]>
          GetTrafficConnections: (limit: number) => Promise<import('./components/Traffic').TrafficEntry[]>
          GetActiveTrafficConnections: () => Promise<import('./components/Traffic').TrafficEntry[]>
          GetDNSQueryLog: (limit: number) => Promise<import('./components/Traffic').DNSLogEntry[]>
          GetProfileTrafficSummaries: () => Promise<import('./components/Traffic').ProfileTrafficSummary[]>
          ClearTrafficLog: () => Promise<void>
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
  // showSettings removed — settings is now a NavBar view
  const [showConfigEditor, setShowConfigEditor] = useState(false)
  const [notification, setNotification] = useState<{ type: 'success' | 'error'; message: string } | null>(null)
  const [systemStatus, setSystemStatus] = useState<SystemStatus | null>(null)
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null)
  // Credentials modal state
  const [credentialsModal, setCredentialsModal] = useState<{ profileId: string; profileName: string } | null>(null)
  const [credUsername, setCredUsername] = useState('')
  const [credPassword, setCredPassword] = useState('')
  // Dependency install modal state
  const [depModal, setDepModal] = useState<{
    type: 'openvpn-install' | 'openvpn-upgrade'
    profileId: string
    profileName: string
    downloadURL?: string
  } | null>(null)
  const [depInstalling, setDepInstalling] = useState(false)
  const [depProgressLogs, setDepProgressLogs] = useState<string[]>([])
  const [updateDownloading, setUpdateDownloading] = useState(false)
  const [appVersion, setAppVersion] = useState<string>('')
  const [showChangelogModal, setShowChangelogModal] = useState(false)
  const [activeView, setActiveView] = useState<NavView>('connections')
  const [advancedMode, setAdvancedMode] = useState(false)
  // Delete confirmation modal state
  const [deleteModal, setDeleteModal] = useState<{
    profileId: string
    profileName: string
    configPath?: string
    deleteConfig: boolean
  } | null>(null)

  const showNotification = (type: 'success' | 'error', message: string) => {
    setNotification({ type, message })
    setTimeout(() => setNotification(null), 3000)
  }

  const fetchProfiles = useCallback(async () => {
    try {
      const data = await window.go.app.App.GetProfiles()
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
      const status = await window.go.app.App.GetSystemStatus()
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
      const profile = await window.go.app.App.GetProfile(id)
      setSelectedProfile(profile)
    } catch (err) {
      console.error('Failed to fetch profile:', err)
    }
  }, [])

  useEffect(() => {
    fetchProfiles()
    fetchSystemStatus()
    // Load advancedMode from settings on startup
    window.go.app.App.GetSettings().then((loaded_settings: Settings) => {
      setAdvancedMode(loaded_settings.advancedMode ?? false)
    }).catch(() => {})
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

  // Fetch app version on mount
  useEffect(() => {
    window.go.app.App.GetAppVersion().then(setAppVersion).catch(console.error)
  }, [])

  // Listen for update-available event from backend
  useEffect(() => {
    const handleUpdateAvailable = (...args: unknown[]) => {
      if (args[0]) setUpdateInfo(args[0] as UpdateInfo)
    }
    window.runtime.EventsOn('update-available', handleUpdateAvailable)
    return () => {
      window.runtime.EventsOff('update-available')
    }
  }, [])

  // Listen for config-imported event from backend (after zip import)
  useEffect(() => {
    const handleConfigImported = () => {
      setSelectedId(undefined)
      setSelectedProfile(null)
      fetchProfiles()
      fetchSystemStatus()
    }
    window.runtime.EventsOn('config-imported', handleConfigImported)
    return () => {
      window.runtime.EventsOff('config-imported')
    }
  }, [fetchProfiles, fetchSystemStatus])

  // Listen for install progress events from backend
  useEffect(() => {
    const handleInstallProgress = (...args: unknown[]) => {
      const message = String(args[0] || '')
      setDepProgressLogs(prev => [...prev, message])
    }
    window.runtime.EventsOn('install-progress', handleInstallProgress)
    return () => { window.runtime.EventsOff('install-progress') }
  }, [])

  const handleConnect = async (id: string) => {
    console.log('[App] handleConnect called for:', id)
    const profileEntry = profiles.find(entry => entry.id === id)
    const profileType = profileEntry?.type
    const profileName = profileEntry?.name || id

    try {
      // Check if VPN client dependency is installed
      if (profileType === 'openvpn') {
        const ovpnStatus = await window.go.app.App.GetOpenVPNStatus()
        if (!ovpnStatus.installed) {
          setDepModal({ type: 'openvpn-install', profileId: id, profileName })
          return
        }
      }

      // Check if this profile needs credentials before connecting
      const needsAuth = await window.go.app.App.ProfileNeedsCredentials(id)
      if (needsAuth) {
        setCredUsername('')
        setCredPassword('')
        setCredentialsModal({ profileId: id, profileName })
        return
      }

      console.log('[App] Calling backend Connect...')
      await window.go.app.App.Connect(id)
      // For async VPN types, Connect returns immediately — polling picks up state
      if (profileType !== 'openvpn' && profileType !== 'external') {
        showNotification('success', 'Connected successfully')
      }
      await fetchProfiles()
    } catch (err) {
      console.error('[App] Connect failed:', err)
      showNotification('error', `Failed to connect: ${err}`)
    }
  }

  const handleInstallOpenVPN = async () => {
    if (!depModal) return
    const { profileId } = depModal
    setDepInstalling(true)
    setDepProgressLogs([])
    try {
      await window.go.app.App.InstallOpenVPN()
      showNotification('success', 'OpenVPN installed successfully')
      setDepModal(null)
      setDepInstalling(false)
      setDepProgressLogs([])
    } catch (err) {
      setDepInstalling(false)
      setDepProgressLogs(prev => [...prev, `Error: ${err}`])
    }
  }

  const handleConnectWithCredentials = async () => {
    if (!credentialsModal) return
    const { profileId } = credentialsModal
    setCredentialsModal(null)
    try {
      await window.go.app.App.ConnectWithCredentials(profileId, credUsername, credPassword)
      // Connection runs in background — don't show success notification.
      // Profile polling will detect connected/error state.
      await fetchProfiles()
    } catch (err) {
      console.error('[App] ConnectWithCredentials failed:', err)
      showNotification('error', `Failed to connect: ${err}`)
    }
    setCredUsername('')
    setCredPassword('')
  }

  const handleDisconnect = async (id: string) => {
    console.log('[App] handleDisconnect called for:', id)
    try {
      console.log('[App] Calling backend Disconnect...')
      await window.go.app.App.Disconnect(id)
      console.log('[App] Disconnect succeeded')
      showNotification('success', 'Disconnected')
      await fetchProfiles()
    } catch (err) {
      console.error('[App] Disconnect failed:', err)
      showNotification('error', `Failed to disconnect: ${err}`)
    }
  }

  const handleDelete = async (id: string) => {
    const profile_entry = profiles.find(entry => entry.id === id)
    const is_external = profile_entry?.type === 'external'
    let config_path: string | undefined
    try {
      config_path = await window.go.app.App.GetProfileConfigPath(id)
    } catch {
      // Ignore — config path is optional
    }
    setDeleteModal({
      profileId: id,
      profileName: profile_entry?.name || id,
      configPath: is_external ? undefined : config_path || undefined,
      deleteConfig: is_external, // Always delete .extjson files
    })
  }

  const handleConfirmDelete = async () => {
    if (!deleteModal) return
    const { profileId, deleteConfig } = deleteModal
    setDeleteModal(null)
    try {
      await window.go.app.App.DeleteProfile(profileId, deleteConfig)
      showNotification('success', 'Tunnel deleted')
      if (selectedId === profileId) {
        setSelectedId(undefined)
        setSelectedProfile(null)
      }
      await fetchProfiles()
    } catch (err) {
      showNotification('error', `Failed to delete: ${err}`)
    }
  }

  const handleImportComplete = async (profile_id: string) => {
    setShowAddModal(false)
    await fetchProfiles()
    setSelectedId(profile_id)
    showNotification('success', 'Tunnel imported successfully')
  }

  const handleSelectProfile = (id: string) => {
    setSelectedId(id)
  }

  const handleUpdateProfile = async (profile: Profile) => {
    try {
      await window.go.app.App.UpdateProfile(profile)
      showNotification('success', 'Profile updated')
      await fetchProfiles()
      await fetchSelectedProfile(profile.id)
    } catch (err) {
      showNotification('error', `Failed to update: ${err}`)
    }
  }

  const handleUpdateInstall = async () => {
    setUpdateDownloading(true)
    try {
      await window.go.app.App.DownloadAndInstallUpdate()
    } catch (err) {
      showNotification('error', `Update failed: ${err}`)
      setUpdateDownloading(false)
    }
  }

  const handleReorderProfiles = async (orderedIDs: string[]) => {
    // Optimistically reorder in local state
    const profileMap = new Map(profiles.map(profile => [profile.id, profile]))
    const reordered = orderedIDs.map(id => profileMap.get(id)!).filter(Boolean)
    setProfiles(reordered)

    try {
      await window.go.app.App.ReorderProfiles(orderedIDs)
    } catch (err) {
      console.error('Failed to reorder profiles:', err)
      await fetchProfiles()
    }
  }

  const selectedStatus = profiles.find(profile_status => profile_status.id === selectedId)

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
        <div className={`fixed top-4 right-4 z-50 px-4 py-2 rounded-lg shadow-lg select-text cursor-text max-w-lg break-words ${
          notification.type === 'success' ? 'bg-green-600' : 'bg-red-600'
        } text-white`}>
          {notification.message}
        </div>
      )}

      {/* NavBar */}
      <NavBar activeView={activeView} onNavigate={setActiveView} advancedMode={advancedMode} />

      {/* Sidebar (only in connections view) */}
      {activeView === 'connections' && (
        <Sidebar
          profiles={profiles}
          selectedId={selectedId}
          onSelect={(id) => { setActiveView('connections'); handleSelectProfile(id) }}
          onAddProfile={() => setShowAddModal(true)}
          onReorder={handleReorderProfiles}
          appVersion={appVersion}
          updateInfo={updateInfo}
          updateDownloading={updateDownloading}
          onUpdateInstall={handleUpdateInstall}
          onOpenChangelog={() => setShowChangelogModal(true)}
        />
      )}

      {/* Main Content */}
      <main className="flex-1 overflow-auto p-6">
        {error && (
          <div className="mb-4 p-4 bg-red-900/50 border border-red-700 rounded-lg text-red-200">
            {error}
          </div>
        )}

        {activeView === 'connections' && (
          selectedProfile && selectedStatus ? (
            <TunnelDetailPanel
              profile={selectedProfile}
              status={selectedStatus}
              profiles={profiles.map(profileEntry => ({ id: profileEntry.id, name: profileEntry.name }))}
              onConnect={() => handleConnect(selectedProfile.id)}
              onDisconnect={() => handleDisconnect(selectedProfile.id)}
              onDelete={() => handleDelete(selectedProfile.id)}
              onEditConfig={() => setShowConfigEditor(true)}
              onRefresh={() => fetchSelectedProfile(selectedProfile.id)}
              onUpdateProfile={handleUpdateProfile}
              onUpgradeOpenVPN={() => {
                setDepModal({
                  type: 'openvpn-upgrade',
                  profileId: selectedProfile.id,
                  profileName: selectedProfile.name,
                  downloadURL: selectedStatus?.clientVersion?.replace('OpenVPN ', '') || ''
                })
              }}
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
          )
        )}

        {activeView === 'traffic' && (
          <Traffic
            profiles={profiles.map(profile => ({ id: profile.id, name: profile.name }))}
            activeInterfaceName={systemStatus?.activeInterface}
          />
        )}

        {activeView === 'logs' && (
          <LogsView
            profiles={profiles.map(profile => ({ id: profile.id, name: profile.name }))}
            activeInterfaceName={systemStatus?.activeInterface}
          />
        )}

        {activeView === 'settings' && (
          <SettingsView
            onOpenChangelog={() => setShowChangelogModal(true)}
            onAdvancedModeChange={(enabled) => {
              setAdvancedMode(enabled)
              if (!enabled) {
                setActiveView('connections')
              }
            }}
          />
        )}
      </main>

      {/* Delete Confirmation Modal */}
      {deleteModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="card w-full max-w-sm mx-4 p-6">
            <h2 className="text-lg font-bold text-white mb-2">Delete Connection</h2>
            <p className="text-sm text-dark-400 mb-4">
              Are you sure you want to delete <span className="text-dark-200 font-medium">{deleteModal.profileName}</span>?
            </p>
            {deleteModal.configPath && (
              <label className="flex items-start gap-2 text-sm text-dark-400 cursor-pointer mb-4 p-2 bg-dark-800 rounded">
                <input
                  type="checkbox"
                  checked={deleteModal.deleteConfig}
                  onChange={(event) => setDeleteModal({ ...deleteModal, deleteConfig: event.target.checked })}
                  className="mt-0.5 rounded border-dark-500 text-primary-500"
                />
                <div>
                  <span className="text-dark-300">Also delete config file</span>
                  <p className="text-xs text-dark-500 font-mono mt-0.5 break-all">{deleteModal.configPath}</p>
                </div>
              </label>
            )}
            <div className="flex justify-end gap-3">
              <button onClick={() => setDeleteModal(null)} className="btn btn-secondary">
                Cancel
              </button>
              <button onClick={handleConfirmDelete} className="btn bg-red-600 hover:bg-red-500 text-white">
                Delete
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Modals */}
      {showAddModal && (
        <ImportWizard
          onClose={() => setShowAddModal(false)}
          onComplete={handleImportComplete}
        />
      )}

      {/* Dependency Install Modal */}
      {depModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="card w-full max-w-md mx-4 p-6">
            {(depModal.type === 'openvpn-install' || depModal.type === 'openvpn-upgrade') ? (
              <>
                <h2 className="text-lg font-bold text-white mb-1">
                  {depModal.type === 'openvpn-upgrade' ? 'OpenVPN Upgrade Recommended' : 'OpenVPN Not Installed'}
                </h2>
                <p className="text-sm text-dark-400 mb-4">
                  {depModal.type === 'openvpn-upgrade' ? (
                    <>Your OpenVPN version (<span className="text-dark-200">v{depModal.downloadURL}</span>) is outdated and may have issues with TAP adapters. Upgrade to the latest version with Wintun support?</>
                  ) : (
                    <><span className="text-dark-200">{depModal.profileName}</span> requires OpenVPN to connect. Would you like to download and install it automatically?</>
                  )}
                </p>
                <p className="text-xs text-dark-500 mb-4">
                  OpenVPN 2.7 will be downloaded from the official source (openvpn.net). This requires administrator privileges.
                  Your VPN configuration files will not be affected.
                </p>
                {depProgressLogs.length > 0 && (
                  <div className="mb-4 bg-dark-900 rounded-lg p-3 max-h-32 overflow-y-auto">
                    {depProgressLogs.map((logMessage, logIndex) => (
                      <div key={logIndex} className="text-xs font-mono text-dark-300 py-0.5 flex items-start gap-2">
                        <span className="text-dark-500 flex-shrink-0">{logIndex + 1}.</span>
                        <span>{logMessage}</span>
                      </div>
                    ))}
                  </div>
                )}
                <div className="flex justify-end gap-3">
                  <button onClick={() => { setDepModal(null); setDepInstalling(false); setDepProgressLogs([]) }} className="btn btn-secondary" disabled={depInstalling}>
                    Cancel
                  </button>
                  <button onClick={handleInstallOpenVPN} className="btn btn-primary" disabled={depInstalling}>
                    {depInstalling ? (
                      <span className="flex items-center gap-2">
                        <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24">
                          <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                          <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
                        </svg>
                        {depModal.type === 'openvpn-upgrade' ? 'Upgrading...' : 'Installing...'}
                      </span>
                    ) : depModal.type === 'openvpn-upgrade' ? 'Upgrade OpenVPN' : 'Install OpenVPN'}
                  </button>
                </div>
              </>
            ) : (
              <>
              </>
            )}
          </div>
        </div>
      )}

      {credentialsModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="card w-full max-w-md mx-4 p-6">
            <h2 className="text-lg font-bold text-white mb-1">Authentication Required</h2>
            <p className="text-sm text-dark-400 mb-4">
              <span className="text-dark-200">{credentialsModal.profileName}</span> requires username and password.
            </p>
            <div className="space-y-3">
              <div>
                <label className="block text-sm font-medium text-dark-300 mb-1">Username</label>
                <input
                  type="text"
                  value={credUsername}
                  onChange={(event) => setCredUsername(event.target.value)}
                  className="w-full input"
                  placeholder="VPN username"
                  autoFocus
                  onKeyDown={(event) => { if (event.key === 'Enter' && credUsername && credPassword) handleConnectWithCredentials() }}
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-dark-300 mb-1">Password</label>
                <input
                  type="password"
                  value={credPassword}
                  onChange={(event) => setCredPassword(event.target.value)}
                  className="w-full input"
                  placeholder="VPN password"
                  onKeyDown={(event) => { if (event.key === 'Enter' && credUsername && credPassword) handleConnectWithCredentials() }}
                />
              </div>
            </div>
            <div className="flex justify-end gap-3 mt-5">
              <button onClick={() => setCredentialsModal(null)} className="btn btn-secondary">Cancel</button>
              <button
                onClick={handleConnectWithCredentials}
                disabled={!credUsername.trim() || !credPassword.trim()}
                className="btn btn-primary"
              >
                Connect
              </button>
            </div>
          </div>
        </div>
      )}

      {showChangelogModal && updateInfo && updateInfo.available && (
        <ChangelogModal
          updateInfo={updateInfo}
          updateDownloading={updateDownloading}
          onUpdateInstall={handleUpdateInstall}
          onClose={() => setShowChangelogModal(false)}
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
