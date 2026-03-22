import { useState, useEffect } from 'react'
import { Profile, ProfileStatus, ActiveConnection } from '../App'

// WireGuard config display type matching Go backend
export interface WireGuardConfigDisplay {
  interface: {
    address: string
    dns: string
    publicKey: string
    listenPort?: number
  }
  peer: {
    endpoint: string
    allowedIPs: string
    publicKey: string
  }
}

interface TunnelDetailPanelProps {
  profile: Profile
  status?: ProfileStatus
  onConnect: () => void
  onDisconnect: () => void
  onDelete: () => void
  onEditConfig: () => void
  onRefresh: () => void
  onUpdateProfile: (profile: Profile) => void
}

function TunnelDetailPanel({
  profile,
  status,
  onConnect,
  onDisconnect,
  onDelete,
  onEditConfig,
  onRefresh,
  onUpdateProfile,
}: TunnelDetailPanelProps) {
  const isConnected = status?.connected ?? false
  const [wgConfig, setWgConfig] = useState<WireGuardConfigDisplay | null>(null)
  const [detectedHosts, setDetectedHosts] = useState<ActiveConnection[]>([])
  const [isConnecting, setIsConnecting] = useState(false)
  const [isDisconnecting, setIsDisconnecting] = useState(false)

  // Inline editing states
  const [editingHealth, setEditingHealth] = useState(false)
  const [healthIP, setHealthIP] = useState(profile.healthCheck.targetIP)
  const [healthInterval, setHealthInterval] = useState(profile.healthCheck.intervalSeconds)

  // Editing name
  const [editingName, setEditingName] = useState(false)
  const [profileName, setProfileName] = useState(profile.name)

  // Editing manual hosts
  const [editingHost, setEditingHost] = useState<string | null>(null)
  const [editHostname, setEditHostname] = useState('')
  const [editIP, setEditIP] = useState('')
  const [addingHost, setAddingHost] = useState(false)
  const [newHostname, setNewHostname] = useState('')
  const [newIP, setNewIP] = useState('')

  // Domain suffix inline editing
  const [newSuffixInput, setNewSuffixInput] = useState('')
  const [editingSuffixIndex, setEditingSuffixIndex] = useState<number | null>(null)
  const [editingSuffixValue, setEditingSuffixValue] = useState('')

  // TCP Proxy Ports
  const [newPortInput, setNewPortInput] = useState('')

  // Fetch WireGuard config on mount
  useEffect(() => {
    const fetchConfig = async () => {
      try {
        const config = await window.go.app.App.GetWireGuardConfig(profile.id)
        setWgConfig(config)
      } catch (err) {
        console.error('Failed to fetch WireGuard config:', err)
      }
    }
    fetchConfig()
  }, [profile.id])

  // Update local state when profile changes
  useEffect(() => {
    setHealthIP(profile.healthCheck.targetIP)
    setHealthInterval(profile.healthCheck.intervalSeconds)
    setProfileName(profile.name)
    setEditingName(false)
    setNewSuffixInput('')
    setEditingSuffixIndex(null)
    setNewPortInput('')
    // Reset loading states when switching profiles
    setIsConnecting(false)
    setIsDisconnecting(false)
  }, [profile.id])

  // Fetch detected hosts for this profile
  useEffect(() => {
    const fetchDetectedHosts = async () => {
      try {
        const connections = await window.go.app.App.GetActiveConnections()
        // Filter connections for this profile
        const profileHosts = (connections || []).filter(c => c.profileId === profile.id)
        setDetectedHosts(profileHosts)
      } catch (err) {
        console.error('Failed to fetch detected hosts:', err)
      }
    }

    fetchDetectedHosts()
    const interval = setInterval(fetchDetectedHosts, 5000)
    return () => clearInterval(interval)
  }, [profile.id])

  // Inline update handlers
  const handleToggleHealth = () => {
    onUpdateProfile({
      ...profile,
      healthCheck: { ...profile.healthCheck, enabled: !profile.healthCheck.enabled }
    })
  }

  const handleSaveHealth = () => {
    onUpdateProfile({
      ...profile,
      healthCheck: {
        ...profile.healthCheck,
        targetIP: healthIP,
        intervalSeconds: healthInterval
      }
    })
    setEditingHealth(false)
  }

  const handleSaveName = () => {
    if (profileName.trim() && profileName !== profile.name) {
      onUpdateProfile({ ...profile, name: profileName.trim() })
    }
    setEditingName(false)
  }

  const handleToggleAutoConnect = () => {
    const current = profile.autoConnect === undefined ? true : profile.autoConnect
    onUpdateProfile({ ...profile, autoConnect: !current })
  }

  const hostsCount = profile.dns.hosts ? Object.keys(profile.dns.hosts).length : 0

  // Connect/Disconnect handlers with loading state
  const handleConnect = async () => {
    console.log('Attempting to connect:', profile.id)
    setIsConnecting(true)
    try {
      await onConnect()
      console.log('Connect completed')
    } catch (err) {
      console.error('Connect failed:', err)
    } finally {
      setIsConnecting(false)
    }
  }

  const handleDisconnect = async () => {
    console.log('Attempting to disconnect:', profile.id)
    setIsDisconnecting(true)
    try {
      await onDisconnect()
      console.log('Disconnect completed')
    } catch (err) {
      console.error('Disconnect failed:', err)
    } finally {
      setIsDisconnecting(false)
    }
  }

  // TCP Proxy Ports handlers
  const activePorts = profile.tcpProxyPorts || []

  const handleAddPorts = () => {
    if (!newPortInput.trim()) return
    const parsedPorts = newPortInput
      .split(/[,\s]+/)
      .map(portStr => parseInt(portStr.trim(), 10))
      .filter(portNum => !isNaN(portNum) && portNum > 0 && portNum <= 65535)
    if (parsedPorts.length === 0) return

    const currentPorts = profile.tcpProxyPorts || []
    const uniquePorts = [...new Set([...currentPorts, ...parsedPorts])].sort((portA, portB) => portA - portB)
    onUpdateProfile({
      ...profile,
      tcpProxyPorts: uniquePorts
    })
    setNewPortInput('')
  }

  const handleRemovePort = (portToRemove: number) => {
    const currentPorts = profile.tcpProxyPorts || []
    const filteredPorts = currentPorts.filter(portNum => portNum !== portToRemove)
    onUpdateProfile({
      ...profile,
      tcpProxyPorts: filteredPorts.length > 0 ? filteredPorts : undefined as unknown as number[]
    })
  }

  // Domain suffix handlers
  const handleAddSuffix = () => {
    if (!newSuffixInput.trim()) return
    let cleanedSuffix = newSuffixInput.trim().toLowerCase()
    if (cleanedSuffix.startsWith('.')) {
      cleanedSuffix = cleanedSuffix.slice(1)
    }
    const currentDomains = profile.dns.domains || []
    if (cleanedSuffix && !currentDomains.includes(cleanedSuffix)) {
      onUpdateProfile({
        ...profile,
        dns: { ...profile.dns, domains: [...currentDomains, cleanedSuffix] }
      })
      setNewSuffixInput('')
    }
  }

  const handleRemoveSuffix = (domainToRemove: string) => {
    const currentDomains = profile.dns.domains || []
    onUpdateProfile({
      ...profile,
      dns: { ...profile.dns, domains: currentDomains.filter(domain => domain !== domainToRemove) }
    })
  }

  const handleStartEditSuffix = (suffixIndex: number, domain: string) => {
    setEditingSuffixIndex(suffixIndex)
    setEditingSuffixValue(domain)
  }

  const handleSaveEditSuffix = () => {
    if (editingSuffixIndex === null) return
    let cleanedSuffix = editingSuffixValue.trim().toLowerCase()
    if (cleanedSuffix.startsWith('.')) {
      cleanedSuffix = cleanedSuffix.slice(1)
    }
    if (!cleanedSuffix) {
      setEditingSuffixIndex(null)
      return
    }
    const updatedDomains = [...(profile.dns.domains || [])]
    updatedDomains[editingSuffixIndex] = cleanedSuffix
    onUpdateProfile({
      ...profile,
      dns: { ...profile.dns, domains: updatedDomains }
    })
    setEditingSuffixIndex(null)
  }

  const handleCancelEditSuffix = () => {
    setEditingSuffixIndex(null)
  }

  const handleToggleStripSuffix = () => {
    onUpdateProfile({
      ...profile,
      dns: { ...profile.dns, stripSuffix: !profile.dns.stripSuffix }
    })
  }

  // Manual hosts handlers
  const handleAddManualHost = async () => {
    if (!newHostname || !newIP) return

    const updatedHosts = { ...(profile.dns?.hosts || {}) }
    updatedHosts[newHostname] = newIP

    onUpdateProfile({
      ...profile,
      dns: {
        ...profile.dns,
        hosts: updatedHosts
      }
    })
    setAddingHost(false)
    setNewHostname('')
    setNewIP('')
  }

  const handleEditManualHost = (hostname: string, ip: string) => {
    setEditingHost(hostname)
    setEditHostname(hostname)
    setEditIP(ip)
  }

  const handleSaveManualHost = async () => {
    if (!editingHost) return

    const updatedHosts = { ...(profile.dns?.hosts || {}) }

    // If hostname changed, delete old one
    if (editingHost !== editHostname) {
      delete updatedHosts[editingHost]
    }
    updatedHosts[editHostname] = editIP

    onUpdateProfile({
      ...profile,
      dns: {
        ...profile.dns,
        hosts: updatedHosts
      }
    })
    setEditingHost(null)
  }

  const handleDeleteManualHost = async (hostname: string) => {
    const updatedHosts = { ...(profile.dns?.hosts || {}) }
    delete updatedHosts[hostname]

    onUpdateProfile({
      ...profile,
      dns: {
        ...profile.dns,
        hosts: updatedHosts
      }
    })
  }

  const showConnectingOverlay = isConnecting || (status?.connecting && !isConnected)

  return (
    <div className="space-y-4 relative">
      {/* Connecting overlay */}
      {showConnectingOverlay && (
        <div className="absolute inset-0 z-10 flex items-center justify-center bg-dark-900/60 rounded-lg backdrop-blur-sm">
          <div className="flex flex-col items-center gap-3">
            <svg className="w-10 h-10 animate-spin text-primary-500" fill="none" viewBox="0 0 24 24">
              <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4"></circle>
              <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
            </svg>
            <span className="text-primary-400 font-medium">Connecting...</span>
          </div>
        </div>
      )}

      {/* Header */}
      <div className="card p-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            {(status?.connecting || isConnecting) ? (
              <div className="w-4 h-4 rounded-full border-2 border-primary-500 border-t-transparent animate-spin" />
            ) : (
              <div className={`w-4 h-4 rounded-full ${isConnected ? 'bg-green-500' : 'bg-dark-500'}`} />
            )}
            <div>
              {editingName ? (
                <input
                  type="text"
                  value={profileName}
                  onChange={(e) => setProfileName(e.target.value)}
                  onBlur={handleSaveName}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') handleSaveName()
                    if (e.key === 'Escape') { setProfileName(profile.name); setEditingName(false) }
                  }}
                  className="text-xl font-bold text-white bg-dark-700 border border-dark-500 rounded px-2 py-0.5 outline-none focus:border-primary-500"
                  autoFocus
                />
              ) : (
                <h2
                  className="text-xl font-bold text-white cursor-pointer hover:text-primary-400 transition-colors"
                  onClick={() => setEditingName(true)}
                  title="Click to rename"
                >{profile.name}</h2>
              )}
              <div className="flex items-center gap-3 mt-0.5">
                <p className="text-sm text-dark-400">
                  Status: <span className={isConnected ? 'text-green-400' : 'text-dark-400'}>{isConnected ? 'Active' : 'Inactive'}</span>
                </p>
                <label className="flex items-center gap-1.5 text-sm text-dark-400 cursor-pointer" title="Auto-connect on startup">
                  <input
                    type="checkbox"
                    checked={profile.autoConnect === undefined ? true : profile.autoConnect}
                    onChange={handleToggleAutoConnect}
                    className="w-3.5 h-3.5 rounded border-dark-500 text-primary-500 focus:ring-primary-500 bg-dark-700 cursor-pointer"
                  />
                  Auto-connect
                </label>
              </div>
            </div>
          </div>
          <div className="flex items-center gap-2">
            {isConnected ? (
              <button
                onClick={handleDisconnect}
                disabled={isDisconnecting}
                className="btn btn-danger disabled:opacity-50 disabled:cursor-wait"
              >
                {isDisconnecting ? (
                  <span className="flex items-center gap-2">
                    <svg className="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4"></circle>
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                    </svg>
                    Deactivating...
                  </span>
                ) : 'Deactivate'}
              </button>
            ) : (
              <button
                onClick={handleConnect}
                disabled={isConnecting}
                className="btn btn-success disabled:opacity-50 disabled:cursor-wait"
              >
                {isConnecting ? (
                  <span className="flex items-center gap-2">
                    <svg className="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4"></circle>
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                    </svg>
                    Activating...
                  </span>
                ) : 'Activate'}
              </button>
            )}
            <button onClick={onEditConfig} className="btn btn-secondary" title="Edit WireGuard config file">
              Config
            </button>
            <button
              onClick={onDelete}
              className="btn btn-secondary text-red-400 hover:text-red-300 px-3"
              title="Delete tunnel"
            >
              <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
                  d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        </div>
      </div>

      {/* Domain Suffix */}
      <div className="card p-4">
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-semibold text-dark-300 uppercase tracking-wider flex items-center gap-2">
            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9m-9 9a9 9 0 019-9" />
            </svg>
            Domain Suffix
          </h3>
          <label className="flex items-center gap-1.5 text-xs text-dark-400 cursor-pointer">
            <input
              type="checkbox"
              checked={profile.dns.stripSuffix}
              onChange={handleToggleStripSuffix}
              className="w-3.5 h-3.5 rounded border-dark-500 text-primary-500 focus:ring-primary-500 bg-dark-700 cursor-pointer"
            />
            Strip suffix
          </label>
        </div>

        {/* Suffix chips */}
        <div className="flex flex-wrap gap-2 mb-3 min-h-[28px]">
          {(!profile.dns.domains || profile.dns.domains.length === 0) ? (
            <span className="text-dark-500 text-xs italic">No suffixes configured</span>
          ) : (
            profile.dns.domains.map((domain, suffixIndex) => (
              editingSuffixIndex === suffixIndex ? (
                <div key={domain} className="inline-flex items-center gap-1">
                  <input
                    type="text"
                    value={editingSuffixValue}
                    onChange={(event) => setEditingSuffixValue(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') handleSaveEditSuffix()
                      if (event.key === 'Escape') handleCancelEditSuffix()
                    }}
                    className="w-28 px-2 py-0.5 bg-dark-600 border border-primary-500 rounded text-xs text-dark-100 focus:outline-none"
                    autoFocus
                  />
                  <button onClick={handleSaveEditSuffix} className="text-green-400 hover:text-green-300 p-0.5">
                    <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                    </svg>
                  </button>
                  <button onClick={handleCancelEditSuffix} className="text-dark-400 hover:text-red-400 p-0.5">
                    <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                    </svg>
                  </button>
                </div>
              ) : (
                <span
                  key={domain}
                  className="inline-flex items-center gap-1 px-2 py-0.5 bg-dark-700 rounded text-xs text-dark-200 cursor-pointer hover:bg-dark-600 group"
                  onClick={() => handleStartEditSuffix(suffixIndex, domain)}
                  title="Click to edit"
                >
                  .{domain}
                  <button
                    onClick={(event) => { event.stopPropagation(); handleRemoveSuffix(domain) }}
                    className="text-dark-500 hover:text-red-400 transition-colors"
                  >
                    <svg className="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                    </svg>
                  </button>
                </span>
              )
            ))
          )}
        </div>

        {/* Add suffix input */}
        <div className="flex gap-2">
          <input
            type="text"
            value={newSuffixInput}
            onChange={(event) => setNewSuffixInput(event.target.value)}
            onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); handleAddSuffix() } }}
            placeholder="Add suffix (e.g., office)"
            className="flex-1 input py-1 text-sm"
          />
          <button
            onClick={handleAddSuffix}
            disabled={!newSuffixInput.trim()}
            className="btn btn-secondary text-xs py-1 px-3"
          >
            Add
          </button>
        </div>

        {/* Hosts subsection inside Domain Suffix card */}
        <div className="border-t border-dark-700 pt-3 mt-3">
          <div className="flex items-center justify-between mb-2">
            <div className="flex items-center gap-2">
              <span className="w-2 h-2 rounded-full bg-yellow-500"></span>
              <span className="text-dark-300 text-xs font-medium uppercase tracking-wider">Static Hosts</span>
              <span className="text-dark-500 text-xs">({hostsCount})</span>
            </div>
            <button
              onClick={() => setAddingHost(!addingHost)}
              className="text-xs text-primary-400 hover:text-primary-300"
            >
              + Add
            </button>
          </div>

          {/* Add new host form */}
          {addingHost && (
            <div className="flex gap-2 mb-2 p-2 bg-dark-900 rounded">
              <input
                type="text"
                placeholder="hostname"
                value={newHostname}
                onChange={(event) => setNewHostname(event.target.value)}
                className="flex-1 bg-dark-700 border border-dark-600 rounded px-2 py-1 text-xs text-white"
              />
              <span className="text-dark-400 self-center text-xs">→</span>
              <input
                type="text"
                placeholder="IP address"
                value={newIP}
                onChange={(event) => setNewIP(event.target.value)}
                className="w-28 bg-dark-700 border border-dark-600 rounded px-2 py-1 text-xs text-white font-mono"
              />
              <button
                onClick={handleAddManualHost}
                className="px-2 py-1 bg-primary-600 hover:bg-primary-500 text-white text-xs rounded"
              >
                Add
              </button>
              <button
                onClick={() => { setAddingHost(false); setNewHostname(''); setNewIP('') }}
                className="px-2 py-1 bg-dark-600 hover:bg-dark-500 text-white text-xs rounded"
              >
                Cancel
              </button>
            </div>
          )}

          {hostsCount > 0 ? (
            <div className="space-y-1">
              {Object.entries(profile.dns.hosts || {}).map(([hostname_entry, ip_entry]) => (
                <div key={hostname_entry} className="flex items-center gap-2 text-xs group">
                  {editingHost === hostname_entry ? (
                    <>
                      <input
                        type="text"
                        value={editHostname}
                        onChange={(event) => setEditHostname(event.target.value)}
                        className="flex-1 bg-dark-700 border border-dark-600 rounded px-2 py-1 text-xs text-white max-w-32"
                      />
                      <span className="text-dark-400">→</span>
                      <input
                        type="text"
                        value={editIP}
                        onChange={(event) => setEditIP(event.target.value)}
                        className="w-28 bg-dark-700 border border-dark-600 rounded px-2 py-1 text-xs text-white font-mono"
                      />
                      <button onClick={handleSaveManualHost} className="px-2 py-0.5 bg-green-600 hover:bg-green-500 text-white text-xs rounded">Save</button>
                      <button onClick={() => setEditingHost(null)} className="px-2 py-0.5 bg-dark-600 hover:bg-dark-500 text-white text-xs rounded">Cancel</button>
                    </>
                  ) : (
                    <>
                      <span className="text-primary-400 font-mono">{hostname_entry}</span>
                      <span className="text-dark-500">→</span>
                      <span className="text-yellow-400 font-mono">{ip_entry as string}</span>
                      <div className="ml-auto opacity-0 group-hover:opacity-100 transition-opacity flex gap-1">
                        <button onClick={() => handleEditManualHost(hostname_entry, ip_entry as string)} className="px-2 py-0.5 text-xs text-dark-300 hover:text-white hover:bg-dark-600 rounded">Edit</button>
                        <button onClick={() => handleDeleteManualHost(hostname_entry)} className="px-2 py-0.5 text-xs text-red-400 hover:text-red-300 hover:bg-red-900/30 rounded">Delete</button>
                      </div>
                    </>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <span className="text-dark-500 text-xs italic">No static hosts configured</span>
          )}
        </div>

        {/* Cached Hosts subsection */}
        <div className="border-t border-dark-700 pt-3 mt-3">
          <div className="flex items-center gap-2 mb-2">
            <span className="w-2 h-2 rounded-full bg-blue-500"></span>
            <span className="text-dark-300 text-xs font-medium uppercase tracking-wider">Cached Hosts</span>
            <span className="text-dark-500 text-xs">({detectedHosts.length})</span>
          </div>
          {detectedHosts.length > 0 ? (
            <div className="space-y-1">
              {detectedHosts.map((detected_host, detected_host_index) => (
                <div key={detected_host_index} className="flex items-center gap-2 text-xs group">
                  <span className="text-primary-400 font-mono">{detected_host.hostname}</span>
                  <span className="text-dark-500">→</span>
                  <span className="text-blue-400 font-mono">{detected_host.tunnelIP}</span>
                  <span className="text-dark-600">→</span>
                  <span className="text-green-400 font-mono">{detected_host.realIP}</span>
                  <span className="text-dark-600 ml-1">({detected_host.age})</span>
                </div>
              ))}
            </div>
          ) : (
            <span className="text-dark-500 text-xs italic">No hosts detected yet</span>
          )}
        </div>
      </div>

      {/* TCP Proxy Ports */}
      <div className="card p-4">
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-semibold text-dark-300 uppercase tracking-wider flex items-center gap-2">
            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
            </svg>
            TCP Proxy Ports
          </h3>
        </div>

        <div>
          {/* Port chips */}
          <div className="flex flex-wrap gap-1.5 mb-3 min-h-[28px]">
            {activePorts.length === 0 ? (
              <span className="text-dark-500 text-xs italic">No ports configured</span>
            ) : (
              activePorts.map(portValue => (
                <span
                  key={portValue}
                  className="inline-flex items-center gap-1 px-2 py-0.5 bg-dark-700 rounded text-xs text-dark-200 group hover:bg-dark-600"
                >
                  {portValue}
                  <button
                    onClick={() => handleRemovePort(portValue)}
                    className="text-dark-500 hover:text-red-400 transition-colors"
                  >
                    <svg className="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                    </svg>
                  </button>
                </span>
              ))
            )}
          </div>

          {/* Add port input */}
          <div className="flex gap-2">
            <input
              type="text"
              value={newPortInput}
              onChange={(event) => setNewPortInput(event.target.value)}
              onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); handleAddPorts() } }}
              placeholder="Add ports (e.g., 8080, 5432)"
              className="flex-1 input py-1 text-sm"
            />
            <button
              onClick={handleAddPorts}
              disabled={!newPortInput.trim()}
              className="btn btn-secondary text-xs py-1 px-3"
            >
              Add
            </button>
          </div>
        </div>
      </div>

      {/* Interface Section */}
      <div className="card p-4">
        <h3 className="text-sm font-semibold text-dark-300 uppercase tracking-wider mb-3 flex items-center gap-2">
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2z" />
          </svg>
          Interface
        </h3>
        <div className="grid grid-cols-2 gap-x-8 gap-y-2 text-sm">
          <div className="flex justify-between">
            <span className="text-dark-400">Address:</span>
            <span className="text-dark-100 font-mono">{wgConfig?.interface.address || '-'}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-dark-400">DNS:</span>
            <span className="text-dark-100 font-mono">{wgConfig?.interface.dns || '-'}</span>
          </div>
          {wgConfig?.interface.listenPort && wgConfig.interface.listenPort > 0 && (
            <div className="flex justify-between">
              <span className="text-dark-400">Listen Port:</span>
              <span className="text-dark-100 font-mono">{wgConfig.interface.listenPort}</span>
            </div>
          )}
        </div>
      </div>

      {/* Peer Section */}
      <div className="card p-4">
        <h3 className="text-sm font-semibold text-dark-300 uppercase tracking-wider mb-3 flex items-center gap-2">
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0z" />
          </svg>
          Peer
        </h3>
        <div className="space-y-2 text-sm">
          <div className="flex justify-between">
            <span className="text-dark-400">Public Key:</span>
            <span className="text-dark-100 font-mono text-xs truncate max-w-xs" title={wgConfig?.peer.publicKey}>
              {wgConfig?.peer.publicKey ? `${wgConfig.peer.publicKey.slice(0, 20)}...` : '-'}
            </span>
          </div>
          <div className="flex justify-between">
            <span className="text-dark-400">Endpoint:</span>
            <span className="text-dark-100 font-mono">{wgConfig?.peer.endpoint || status?.endpoint || '-'}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-dark-400">Allowed IPs:</span>
            <span className="text-dark-100 font-mono">{wgConfig?.peer.allowedIPs || '-'}</span>
          </div>
          {isConnected && status && (
            <>
              <div className="flex justify-between">
                <span className="text-dark-400">Latest handshake:</span>
                <span className="text-dark-100">{status.lastHandshake || 'Never'}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-dark-400">Transfer:</span>
                <span className="text-dark-100">
                  ↓ {formatBytes(status.bytesRecv)} &nbsp; ↑ {formatBytes(status.bytesSent)}
                </span>
              </div>
            </>
          )}
        </div>
      </div>

      {/* Health Check - Inline Editable */}
      <div className="card p-4">
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-semibold text-dark-300 uppercase tracking-wider flex items-center gap-2">
            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4.318 6.318a4.5 4.5 0 000 6.364L12 20.364l7.682-7.682a4.5 4.5 0 00-6.364-6.364L12 7.636l-1.318-1.318a4.5 4.5 0 00-6.364 0z" />
            </svg>
            Health Check
          </h3>
          <button
            onClick={handleToggleHealth}
            className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
              profile.healthCheck.enabled ? 'bg-primary-600' : 'bg-dark-600'
            }`}
          >
            <span className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
              profile.healthCheck.enabled ? 'translate-x-6' : 'translate-x-1'
            }`} />
          </button>
        </div>

        {profile.healthCheck.enabled && (
          editingHealth ? (
            <div className="space-y-2">
              <div className="flex items-center gap-2 text-sm">
                <span className="text-dark-400 w-20">Target IP:</span>
                <input
                  type="text"
                  value={healthIP}
                  onChange={(e) => setHealthIP(e.target.value)}
                  className="flex-1 input py-1 text-sm"
                  placeholder="e.g., 10.0.0.1"
                />
              </div>
              <div className="flex items-center gap-2 text-sm">
                <span className="text-dark-400 w-20">Interval:</span>
                <input
                  type="number"
                  value={healthInterval}
                  onChange={(e) => setHealthInterval(parseInt(e.target.value) || 30)}
                  className="w-20 input py-1 text-sm"
                  min="5"
                  max="300"
                />
                <span className="text-dark-500">seconds</span>
              </div>
              <div className="flex justify-end gap-2 mt-2">
                <button onClick={() => { setEditingHealth(false); setHealthIP(profile.healthCheck.targetIP); setHealthInterval(profile.healthCheck.intervalSeconds) }} className="btn btn-secondary text-xs py-1 px-2">
                  Cancel
                </button>
                <button onClick={handleSaveHealth} className="btn btn-primary text-xs py-1 px-2">
                  Save
                </button>
              </div>
            </div>
          ) : (
            <div
              className="text-sm cursor-pointer hover:bg-dark-700 p-2 rounded -m-2"
              onClick={() => setEditingHealth(true)}
              title="Click to edit"
            >
              <div className="flex items-center gap-2">
                <span className="text-dark-400">Target:</span>
                <span className="text-dark-100 font-mono">{profile.healthCheck.targetIP || 'Not set'}</span>
                <span className="text-dark-600">|</span>
                <span className="text-dark-400">Every</span>
                <span className="text-dark-100">{profile.healthCheck.intervalSeconds}s</span>
              </div>
            </div>
          )
        )}
      </div>


    </div>
  )
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

export default TunnelDetailPanel
