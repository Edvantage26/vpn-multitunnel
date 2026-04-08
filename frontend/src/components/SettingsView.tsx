import { useState, useEffect } from 'react'
import type { Settings, SystemStatus, UpdateSettingsResult, UpdateInfo } from '../App'

interface SettingsViewProps {
  onOpenChangelog: () => void
  onAdvancedModeChange: (enabled: boolean) => void
}

interface DNSTestDetails {
  proxyListening: boolean
  systemDNSConfigured: boolean
  querySuccess: boolean
  resolvedIP: string
  error: string
}

type SettingsTab = 'general' | 'dns' | 'about'

function SettingsView({ onOpenChangelog, onAdvancedModeChange }: SettingsViewProps) {
  const [settings, setSettings] = useState<Settings | null>(null)
  const [appPath, setAppPath] = useState('')
  const [dataPath, setDataPath] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [activeTab, setActiveTab] = useState<SettingsTab>('general')
  const [dnsTestDetails, setDnsTestDetails] = useState<DNSTestDetails | null>(null)
  const [dnsTestLoading, setDnsTestLoading] = useState(false)
  const [dnsStatus, setDnsStatus] = useState<SystemStatus | null>(null)
  const [dnsActionLoading, setDnsActionLoading] = useState(false)
  const [originalDnsListenAddress, setOriginalDnsListenAddress] = useState('')
  const [originalFallbackServer, setOriginalFallbackServer] = useState('')
  const [connectedTunnelCount, setConnectedTunnelCount] = useState(0)
  const [saveResult, setSaveResult] = useState<UpdateSettingsResult | null>(null)
  const [exportLoading, setExportLoading] = useState(false)
  const [importLoading, setImportLoading] = useState(false)
  const [aboutAppVersion, setAboutAppVersion] = useState('')
  const [aboutUpdateInfo, setAboutUpdateInfo] = useState<UpdateInfo | null>(null)
  const [aboutUpdateChecking, setAboutUpdateChecking] = useState(false)
  const [aboutUpdateCheckError, setAboutUpdateCheckError] = useState('')

  useEffect(() => {
    loadSettings()
  }, [])

  const loadSettings = async () => {
    try {
      const [loaded_settings, loaded_app_path, loaded_data_path, loaded_dns_status, loaded_profiles, loaded_app_version, loaded_cached_update] = await Promise.all([
        window.go.app.App.GetSettings(),
        window.go.app.App.GetAppPath(),
        window.go.app.App.GetDataPath(),
        window.go.app.App.GetSystemStatus(),
        window.go.app.App.GetProfiles(),
        window.go.app.App.GetAppVersion(),
        window.go.app.App.CheckForUpdates().catch(() => null),
      ])
      const typed_settings = loaded_settings as Settings
      setSettings(typed_settings)
      setOriginalDnsListenAddress(typed_settings.dnsListenAddress)
      setOriginalFallbackServer(typed_settings.dnsFallbackServer)
      setAppPath(loaded_app_path)
      setDataPath(loaded_data_path)
      setDnsStatus(loaded_dns_status)
      const active_count = (loaded_profiles || []).filter((profile_status: { connected: boolean }) => profile_status.connected).length
      setConnectedTunnelCount(active_count)
      setAboutAppVersion(loaded_app_version)
      if (loaded_cached_update && loaded_cached_update.available) {
        setAboutUpdateInfo(loaded_cached_update)
      }
    } catch (load_error) {
      setError(String(load_error))
    } finally {
      setLoading(false)
    }
  }

  const handleSave = async () => {
    if (!settings) return
    setSaving(true)
    setError('')
    setSaveResult(null)
    try {
      const update_result = await window.go.app.App.UpdateSettings(settings)
      setSaveResult(update_result)
      if (update_result.warning) {
        setError(update_result.warning)
      }
      // Refresh DNS status after save
      const refreshed_status = await window.go.app.App.GetSystemStatus()
      setDnsStatus(refreshed_status)
      setOriginalDnsListenAddress(settings.dnsListenAddress)
      setOriginalFallbackServer(settings.dnsFallbackServer)
      // No longer closing — settings is an integrated view
    } catch (save_error) {
      setError(String(save_error))
    } finally {
      setSaving(false)
    }
  }

  const handleDNSTest = async () => {
    if (!settings) return
    setDnsTestLoading(true)
    setDnsTestDetails(null)
    try {
      const dns_test_address = settings.dnsListenAddress || '127.0.0.53'
      const test_result = await (window.go.app.App.TestDNSConnectivity as unknown as (address: string) => Promise<DNSTestDetails>)(dns_test_address)
      setDnsTestDetails(test_result)
    } catch (test_error) {
      setDnsTestDetails({
        proxyListening: false,
        systemDNSConfigured: false,
        querySuccess: false,
        resolvedIP: '',
        error: String(test_error),
      })
    } finally {
      setDnsTestLoading(false)
    }
  }

  const handleDNSActivate = async () => {
    setDnsActionLoading(true)
    try {
      await window.go.app.App.ConfigureDNS()
      setDnsStatus(await window.go.app.App.GetSystemStatus())
    } catch {} finally { setDnsActionLoading(false) }
  }

  const handleDNSRestore = async () => {
    setDnsActionLoading(true)
    try {
      await window.go.app.App.RestoreDNS()
      setDnsStatus(await window.go.app.App.GetSystemStatus())
    } catch {} finally { setDnsActionLoading(false) }
  }

  const handleExportConfiguration = async () => {
    setExportLoading(true)
    setError('')
    try {
      await window.go.app.App.ExportConfiguration()
    } catch (export_error) {
      setError(String(export_error))
    } finally {
      setExportLoading(false)
    }
  }

  const handleForceCheckForUpdates = async () => {
    setAboutUpdateChecking(true)
    setAboutUpdateCheckError('')
    setAboutUpdateInfo(null)
    try {
      const fresh_update_info = await window.go.app.App.ForceCheckForUpdates()
      setAboutUpdateInfo(fresh_update_info)
    } catch (check_error) {
      setAboutUpdateCheckError(String(check_error))
    } finally {
      setAboutUpdateChecking(false)
    }
  }

  const handleImportConfiguration = async () => {
    const confirmed = window.confirm(
      'This will disconnect all active tunnels and replace your entire configuration (profiles, settings, and config files).\n\nA backup of your current config.json will be saved as config.json.bak.\n\nContinue?'
    )
    if (!confirmed) return

    setImportLoading(true)
    setError('')
    try {
      await window.go.app.App.ImportConfiguration()
      // Reload settings after import
      await loadSettings()
    } catch (import_error) {
      setError(String(import_error))
    } finally {
      setImportLoading(false)
    }
  }

  if (loading) {
    return (
      <div className="h-full flex items-center justify-center">
        <p className="text-dark-400">Loading settings...</p>
      </div>
    )
  }

  if (!settings) return null

  const tabs: { tab_id: SettingsTab; tab_label: string }[] = [
    { tab_id: 'general', tab_label: 'General' },
    { tab_id: 'dns', tab_label: 'DNS Proxy' },
    { tab_id: 'about', tab_label: 'About' },
  ]

  const dns_address_changed = settings.dnsListenAddress !== originalDnsListenAddress
  const fallback_changed = settings.dnsFallbackServer !== originalFallbackServer
  const has_pending_changes = dns_address_changed || fallback_changed
  const dns_is_active = dnsStatus?.dnsConfigured ?? false

  // Auto-configure status text
  const getAutoConfigureStatusText = (): { text: string; color: string } => {
    if (settings.autoConfigureDNS) {
      if (connectedTunnelCount > 0 && dns_is_active) {
        return { text: 'Active — system DNS is managed automatically', color: 'text-green-400' }
      }
      return { text: 'Waiting — will activate on next tunnel connection', color: 'text-dark-400' }
    }
    if (dns_is_active) {
      return { text: 'DNS was manually activated', color: 'text-yellow-400' }
    }
    return { text: 'Manual mode — use the buttons above to control DNS', color: 'text-dark-500' }
  }

  const auto_configure_status = getAutoConfigureStatusText()

  return (
    <div className="h-full flex flex-col">
      {/* Header */}
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-xl font-bold text-white">Settings</h2>
      </div>

      <div className="flex flex-col flex-1 min-h-0">
        {/* Tabs */}
        <div className="flex gap-1 border-b border-dark-600">
          {tabs.map(({ tab_id, tab_label }) => (
            <button
              key={tab_id}
              onClick={() => setActiveTab(tab_id)}
              className={`px-4 py-2 text-sm font-medium rounded-t-lg transition-colors ${
                activeTab === tab_id
                  ? 'bg-dark-700 text-white border-b-2 border-primary-500'
                  : 'text-dark-400 hover:text-dark-200 hover:bg-dark-800'
              }`}
            >
              {tab_label}
            </button>
          ))}
        </div>

        {/* Error */}
        {error && (
          <div className="mt-4 p-3 bg-red-900/50 border border-red-700 rounded text-red-200 text-sm">
            {error}
          </div>
        )}

        {/* Save result notification */}
        {saveResult && !saveResult.warning && (saveResult.dnsProxyRestarted || saveResult.systemDNSReconfigured) && (
          <div className="mt-4 p-3 bg-green-900/30 border border-green-700/40 rounded text-green-300 text-sm">
            {saveResult.systemDNSReconfigured
              ? 'DNS proxy restarted and system DNS reconfigured on new address.'
              : saveResult.dnsProxyRestarted
                ? 'DNS proxy restarted with updated configuration.'
                : 'Settings saved.'}
          </div>
        )}

        {/* Tab Content (scrollable) */}
        <div className="flex-1 overflow-y-auto py-4">
          {/* General Tab */}
          {activeTab === 'general' && (
            <div className="space-y-5">
              {/* App Path */}
              <div>
                <label className="block text-sm text-dark-300 mb-1">Application Path</label>
                <div className="flex items-center gap-2">
                  <div className="flex-1 px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-400 font-mono text-xs select-all">
                    {appPath}
                  </div>
                  <button
                    onClick={() => window.go.app.App.OpenFolderInExplorer(appPath)}
                    className="p-2 text-dark-400 hover:text-dark-200 hover:bg-dark-700 rounded transition-colors"
                    title="Open in Explorer"
                  >
                    <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 19a2 2 0 01-2-2V7a2 2 0 012-2h4l2 2h4a2 2 0 012 2v1M5 19h14a2 2 0 002-2v-5a2 2 0 00-2-2H9a2 2 0 00-2 2v5a2 2 0 01-2 2z" />
                    </svg>
                  </button>
                </div>
              </div>

              {/* Data Path */}
              <div>
                <label className="block text-sm text-dark-300 mb-1">Data Path</label>
                <div className="flex items-center gap-2">
                  <div className="flex-1 px-3 py-2 bg-dark-800 border border-dark-600 rounded text-dark-400 font-mono text-xs select-all">
                    {dataPath}
                  </div>
                  <button
                    onClick={() => window.go.app.App.OpenFolderInExplorer(dataPath)}
                    className="p-2 text-dark-400 hover:text-dark-200 hover:bg-dark-700 rounded transition-colors"
                    title="Open in Explorer"
                  >
                    <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 19a2 2 0 01-2-2V7a2 2 0 012-2h4l2 2h4a2 2 0 012 2v1M5 19h14a2 2 0 002-2v-5a2 2 0 00-2-2H9a2 2 0 00-2 2v5a2 2 0 01-2 2z" />
                    </svg>
                  </button>
                </div>
              </div>

              {/* Log Level */}
              <div>
                <label className="block text-sm text-dark-300 mb-1">Log Level</label>
                <select
                  value={settings.logLevel}
                  onChange={event => setSettings({ ...settings, logLevel: event.target.value })}
                  className="input w-full"
                >
                  <option value="debug">Debug</option>
                  <option value="info">Info</option>
                  <option value="warn">Warning</option>
                  <option value="error">Error</option>
                </select>
              </div>

              {/* Behavior */}
              <div className="space-y-3 pt-2">
                <label className="flex items-center gap-3 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={settings.startMinimized}
                    onChange={event => setSettings({ ...settings, startMinimized: event.target.checked })}
                    className="w-4 h-4 rounded bg-dark-700 border-dark-600 text-primary-500 focus:ring-primary-500"
                  />
                  <span className="text-dark-200">Start minimized</span>
                </label>
                <label className="flex items-center gap-3 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={settings.advancedMode}
                    onChange={event => {
                      setSettings({ ...settings, advancedMode: event.target.checked })
                      onAdvancedModeChange(event.target.checked)
                    }}
                    className="w-4 h-4 rounded bg-dark-700 border-dark-600 text-primary-500 focus:ring-primary-500"
                  />
                  <div>
                    <span className="text-dark-200">Advanced mode</span>
                    <p className="text-xs text-dark-500">Show global Traffic and Logs views in the navigation bar</p>
                  </div>
                </label>
              </div>

              {/* Configuration Backup */}
              <div className="pt-4 border-t border-dark-700">
                <h3 className="text-sm font-medium text-dark-200 mb-1">Configuration Backup</h3>
                <p className="text-xs text-dark-400 mb-3">
                  Export or import your complete configuration including all VPN profiles and settings.
                </p>
                <div className="flex gap-3">
                  <button
                    onClick={handleExportConfiguration}
                    disabled={exportLoading}
                    className="btn btn-secondary text-sm px-4 py-2 flex items-center gap-2 disabled:opacity-50"
                  >
                    <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
                    </svg>
                    {exportLoading ? 'Exporting...' : 'Export'}
                  </button>
                  <button
                    onClick={handleImportConfiguration}
                    disabled={importLoading}
                    className="btn btn-secondary text-sm px-4 py-2 flex items-center gap-2 disabled:opacity-50 border-amber-700/40 hover:border-amber-600/60"
                  >
                    <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" />
                    </svg>
                    {importLoading ? 'Importing...' : 'Import'}
                  </button>
                </div>
              </div>
            </div>
          )}

          {/* DNS Tab */}
          {activeTab === 'dns' && (
            <div className="space-y-4">
              {/* DNS System Status */}
              <div className={`rounded-lg border ${
                dns_is_active
                  ? 'bg-green-900/10 border-green-700/30'
                  : 'bg-dark-800 border-dark-600'
              }`}>
                <div className="px-4 py-3 flex items-center gap-3">
                  {/* Status pills */}
                  <div className="flex-1 flex flex-wrap items-center gap-2">
                    {/* Proxy status */}
                    <span className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium ${
                      dnsStatus?.dnsProxyEnabled
                        ? 'bg-green-900/30 text-green-300'
                        : 'bg-dark-700 text-dark-400'
                    }`}>
                      <span className={`w-1.5 h-1.5 rounded-full ${dnsStatus?.dnsProxyEnabled ? 'bg-green-400' : 'bg-dark-500'}`} />
                      Proxy {dnsStatus?.dnsProxyEnabled ? 'listening' : 'stopped'}
                    </span>

                    {/* System DNS status */}
                    <span className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium ${
                      dns_is_active
                        ? 'bg-green-900/30 text-green-300'
                        : 'bg-dark-700 text-dark-400'
                    }`}>
                      <span className={`w-1.5 h-1.5 rounded-full ${dns_is_active ? 'bg-green-400' : 'bg-dark-500'}`} />
                      System DNS {dns_is_active ? 'configured' : 'default'}
                    </span>

                    {/* Tunnel count */}
                    {connectedTunnelCount > 0 && (
                      <span className="text-xs text-dark-400">
                        ({connectedTunnelCount} tunnel{connectedTunnelCount !== 1 ? 's' : ''} active)
                      </span>
                    )}
                  </div>

                  {/* Action buttons */}
                  <div className="flex gap-2">
                    {dns_is_active ? (
                      <button
                        onClick={handleDNSRestore}
                        disabled={dnsActionLoading}
                        className="px-3 py-1.5 text-xs bg-dark-600 hover:bg-dark-500 text-dark-200 rounded disabled:opacity-50 transition-colors"
                      >
                        {dnsActionLoading ? 'Restoring...' : 'Restore Original'}
                      </button>
                    ) : (
                      <button
                        onClick={handleDNSActivate}
                        disabled={dnsActionLoading}
                        className="px-3 py-1.5 text-xs bg-blue-600 hover:bg-blue-500 text-white rounded disabled:opacity-50 transition-colors"
                      >
                        {dnsActionLoading ? 'Activating...' : 'Activate Now'}
                      </button>
                    )}
                  </div>
                </div>
              </div>

              {/* Configuration Card */}
              <div className="bg-dark-800 border border-dark-600 rounded-lg">
                {/* DNS Proxy Address */}
                <div className="px-4 py-4">
                  <label className="block text-sm font-medium text-dark-200 mb-1">DNS Proxy Address</label>
                  <p className="text-xs text-dark-400 mb-2">
                    Loopback IP where the DNS proxy binds. System DNS will point here when active.
                  </p>
                  <div className="flex gap-2">
                    <input
                      type="text"
                      value={settings.dnsListenAddress}
                      onChange={event => setSettings({ ...settings, dnsListenAddress: event.target.value })}
                      className="input flex-1 font-mono"
                      placeholder="127.0.0.53"
                    />
                    <button
                      onClick={handleDNSTest}
                      disabled={dnsTestLoading}
                      className="btn btn-secondary text-xs px-4 disabled:opacity-50"
                    >
                      {dnsTestLoading ? 'Testing...' : 'Test'}
                    </button>
                  </div>

                  {/* Contextual warning when address changed */}
                  {dns_address_changed && dns_is_active && (
                    <div className="mt-2 px-3 py-2 bg-amber-900/20 border border-amber-700/40 rounded text-xs text-amber-300">
                      Saving will restart the DNS proxy and reconfigure system DNS to use the new address.
                    </div>
                  )}
                  {dns_address_changed && !dns_is_active && (
                    <div className="mt-2 px-3 py-2 bg-blue-900/20 border border-blue-700/40 rounded text-xs text-blue-300">
                      Saving will restart the DNS proxy on the new address.
                    </div>
                  )}

                  {/* Test results */}
                  {dnsTestDetails && (
                    <div className="mt-2 space-y-1 px-3 py-2 bg-dark-900 rounded text-xs">
                      <div className="flex items-center gap-2">
                        {dnsTestDetails.proxyListening ? (
                          <svg className="w-3.5 h-3.5 text-green-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" /></svg>
                        ) : (
                          <svg className="w-3.5 h-3.5 text-red-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" /></svg>
                        )}
                        <span className={dnsTestDetails.proxyListening ? 'text-green-300' : 'text-red-300'}>
                          Proxy {dnsTestDetails.proxyListening ? 'responding' : 'NOT responding'} on {settings.dnsListenAddress || '127.0.0.53'}:53
                        </span>
                      </div>
                      <div className="flex items-center gap-2">
                        {dnsTestDetails.querySuccess ? (
                          <svg className="w-3.5 h-3.5 text-green-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" /></svg>
                        ) : (
                          <svg className="w-3.5 h-3.5 text-red-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" /></svg>
                        )}
                        <span className={dnsTestDetails.querySuccess ? 'text-green-300' : 'text-red-300'}>
                          {dnsTestDetails.querySuccess
                            ? `DNS query resolved (${dnsTestDetails.resolvedIP})`
                            : `DNS query failed${dnsTestDetails.error ? ': ' + dnsTestDetails.error : ''}`}
                        </span>
                      </div>
                      <div className="flex items-center gap-2">
                        {dnsTestDetails.systemDNSConfigured ? (
                          <svg className="w-3.5 h-3.5 text-green-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" /></svg>
                        ) : (
                          <svg className="w-3.5 h-3.5 text-dark-500 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M20 12H4" /></svg>
                        )}
                        <span className={dnsTestDetails.systemDNSConfigured ? 'text-green-300' : 'text-dark-400'}>
                          System DNS {dnsTestDetails.systemDNSConfigured ? 'pointing to proxy' : 'not configured (normal if no tunnels active)'}
                        </span>
                      </div>
                    </div>
                  )}
                </div>

                {/* Divider */}
                <div className="border-t border-dark-700" />

                {/* Fallback DNS Server */}
                <div className="px-4 py-4">
                  <label className="block text-sm font-medium text-dark-200 mb-1">Fallback DNS Server</label>
                  <p className="text-xs text-dark-400 mb-2">
                    Used for domains that don't match any VPN tunnel rule.
                  </p>
                  <input
                    type="text"
                    value={settings.dnsFallbackServer}
                    onChange={event => setSettings({ ...settings, dnsFallbackServer: event.target.value })}
                    className="input w-full font-mono"
                    placeholder="8.8.8.8"
                  />
                  {fallback_changed && (
                    <div className="mt-2 px-3 py-2 bg-blue-900/20 border border-blue-700/40 rounded text-xs text-blue-300">
                      Saving will restart the DNS proxy with the new fallback server.
                    </div>
                  )}
                </div>

                {/* Divider */}
                <div className="border-t border-dark-700" />

                {/* Auto-configure DNS */}
                <div className="px-4 py-4">
                  <label className="flex items-center gap-3 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={settings.autoConfigureDNS}
                      onChange={event => setSettings({ ...settings, autoConfigureDNS: event.target.checked })}
                      className="w-4 h-4 rounded bg-dark-700 border-dark-600 text-primary-500 focus:ring-primary-500"
                    />
                    <div>
                      <span className="text-sm font-medium text-dark-200">Auto-configure DNS</span>
                      <p className="text-xs text-dark-400">
                        Activate when a tunnel connects, restore when all disconnect.
                      </p>
                    </div>
                  </label>
                  <p className={`text-xs mt-2 ml-7 ${auto_configure_status.color}`}>
                    {auto_configure_status.text}
                  </p>
                </div>
              </div>
            </div>
          )}

          {/* About Tab */}
          {activeTab === 'about' && (
            <div className="space-y-5">
              {/* App Info */}
              <div className="text-center py-4">
                <h3 className="text-lg font-bold text-white">VPN MultiTunnel</h3>
                <p className="text-sm text-dark-400 mt-1">
                  Version <span className="text-dark-200 font-mono">{aboutAppVersion}</span>
                </p>
              </div>

              {/* Update Section */}
              <div className="bg-dark-800 border border-dark-600 rounded-lg p-4">
                <div className="flex items-center justify-between mb-3">
                  <h4 className="text-sm font-medium text-dark-200">Software Updates</h4>
                  <button
                    onClick={handleForceCheckForUpdates}
                    disabled={aboutUpdateChecking}
                    className="btn btn-secondary text-xs px-3 py-1.5 disabled:opacity-50"
                  >
                    {aboutUpdateChecking ? 'Checking...' : 'Check for updates'}
                  </button>
                </div>

                {/* Error */}
                {aboutUpdateCheckError && (
                  <div className="p-3 bg-red-900/50 border border-red-700 rounded text-red-200 text-sm">
                    {aboutUpdateCheckError}
                  </div>
                )}

                {/* Update available */}
                {aboutUpdateInfo?.available ? (
                  <div className="space-y-3">
                    <div className="flex items-center gap-2">
                      <span className="w-2 h-2 rounded-full bg-primary-400" />
                      <p className="text-sm text-dark-200">
                        v{aboutUpdateInfo.currentVersion}
                        <span className="text-dark-500 mx-1">&rarr;</span>
                        <span className="text-primary-400 font-mono">v{aboutUpdateInfo.latestVersion}</span>
                      </p>
                    </div>
                    <button
                      onClick={onOpenChangelog}
                      className="text-sm text-primary-400 hover:text-primary-300 underline underline-offset-2"
                    >
                      View details &amp; update
                    </button>
                  </div>
                ) : aboutUpdateInfo && !aboutUpdateInfo.available ? (
                  <div className="flex items-center gap-2">
                    <svg className="w-4 h-4 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                    </svg>
                    <p className="text-sm text-green-300">You're up to date</p>
                  </div>
                ) : null}
              </div>

              {/* Credits */}
              <div className="text-center text-xs text-dark-500 pt-2">
                <p>&copy; {new Date().getFullYear()} Edvantage</p>
              </div>
            </div>
          )}
        </div>

        {/* Footer */}
        {activeTab !== 'about' && (
          <div className="flex items-center justify-between pt-4 border-t border-dark-700">
            <div>
              {has_pending_changes && activeTab === 'dns' && (
                <span className="text-xs text-amber-400">Unsaved DNS changes</span>
              )}
            </div>
            <button
              onClick={handleSave}
              disabled={saving}
              className="btn btn-primary disabled:opacity-50"
            >
              {saving ? 'Saving...' : 'Save Settings'}
            </button>
          </div>
        )}
      </div>
    </div>
  )
}

export default SettingsView
