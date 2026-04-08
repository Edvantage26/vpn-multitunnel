import { useState, useEffect, useRef, useCallback } from 'react'
import { getServiceByPort } from '../data/servicePortRegistry'

// Types matching Go backend
export interface TrafficEntry {
  connectionId: string
  hostname: string
  sniHostname: string
  tunnelIP: string
  realIP: string
  profileId: string
  port: number
  protocolHint: 'tls' | 'plain' | 'ws' | 'tls-long-lived'
  bytesSent: number
  bytesReceived: number
  status: 'active' | 'closed'
  startedAt: string
  closedAt?: string
  durationMs: number
}

export interface DNSLogEntry {
  timestamp: string
  domain: string
  queryType: string
  resolvedIP: string
  tunnelIP: string
  profileId: string
  responseTimeMs: number
  success: boolean
  errorMessage?: string
  viaTunnel: boolean
  sourceProcess?: string
}

export interface ProfileTrafficSummary {
  profileId: string
  activeConnections: number
  totalConnections: number
  totalBytesSent: number
  totalBytesRecv: number
  totalDnsQueries: number
}

type TabId = 'connections' | 'dns'

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const unitIndex = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / Math.pow(1024, unitIndex)
  return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${units[unitIndex]}`
}

function formatDuration(durationMs: number): string {
  if (durationMs < 1000) return `${durationMs}ms`
  const seconds = Math.floor(durationMs / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const remainingSeconds = seconds % 60
  if (minutes < 60) return `${minutes}m ${remainingSeconds}s`
  const hours = Math.floor(minutes / 60)
  const remainingMinutes = minutes % 60
  return `${hours}h ${remainingMinutes}m`
}

function formatTimestamp(isoString: string): string {
  try {
    const date = new Date(isoString)
    return date.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
  } catch {
    return ''
  }
}

function getProtocolIcon(hint: string): string {
  switch (hint) {
    case 'tls': return '🔒'
    case 'tls-long-lived': return '🔒⏱'
    case 'ws': return '📡'
    case 'plain': return '📄'
    default: return '❓'
  }
}

function getProtocolLabel(hint: string): string {
  switch (hint) {
    case 'tls': return 'TLS'
    case 'tls-long-lived': return 'TLS (long-lived)'
    case 'ws': return 'WebSocket'
    case 'plain': return 'Plain'
    default: return hint
  }
}

function getServiceName(port: number, protocolHint: string): string {
  // Protocol-detected types take priority
  if (protocolHint === 'ws') return 'WebSocket'
  if (protocolHint === 'tls-long-lived') return 'WebSocket (TLS)'

  // Use the shared service port registry
  const entry = getServiceByPort(port)
  if (entry) return entry.service

  return `Port ${port}`
}

const SYSTEM_FILTER_ID = '__system__'

interface TrafficProps {
  profiles: { id: string; name: string }[]
  activeInterfaceName?: string
  fixedProfileId?: string // When set, hides profile filter and filters to this profile only
  hideHeader?: boolean    // When embedded, hide the title bar
}

type SortDirection = 'asc' | 'desc'
type ConnectionSortKey = 'hostname' | 'tunnel' | 'service' | 'bytesSent' | 'bytesReceived' | 'startedAt' | 'closedAt' | 'durationMs' | 'realIP'
type DNSSortKey = 'timestamp' | 'sourceProcess' | 'domain' | 'queryType' | 'resolvedIP' | 'tunnel' | 'responseTimeMs'

function Traffic({ profiles, activeInterfaceName, fixedProfileId, hideHeader }: TrafficProps) {
  const [activeTab, setActiveTab] = useState<TabId>('connections')
  const [activeConnections, setActiveConnections] = useState<TrafficEntry[]>([])
  const [recentConnections, setRecentConnections] = useState<TrafficEntry[]>([])
  const [dnsQueries, setDnsQueries] = useState<DNSLogEntry[]>([])
  const [selectedProfiles, setSelectedProfiles] = useState<Set<string>>(new Set())
  const [filterDropdownOpen, setFilterDropdownOpen] = useState(false)
  const filterDropdownRef = useRef<HTMLDivElement>(null)
  const dnsLogRef = useRef<HTMLDivElement>(null)
  const [autoScroll, setAutoScroll] = useState(true)

  // Per-column filters for connections tab
  const [connFilterHostname, setConnFilterHostname] = useState('')
  const [connFilterTunnel, setConnFilterTunnel] = useState('')
  const [connFilterService, setConnFilterService] = useState('')
  const [connFilterRealIP, setConnFilterRealIP] = useState('')

  // Per-column filters for DNS tab
  const [dnsFilterProcess, setDnsFilterProcess] = useState('')
  const [dnsFilterDomain, setDnsFilterDomain] = useState('')
  const [dnsFilterType, setDnsFilterType] = useState('')
  const [dnsFilterIP, setDnsFilterIP] = useState('')
  const [dnsFilterTunnel, setDnsFilterTunnel] = useState('')

  // Sort state for connections
  const [connSortKey, setConnSortKey] = useState<ConnectionSortKey | null>(null)
  const [connSortDir, setConnSortDir] = useState<SortDirection>('desc')
  // Sort state for DNS
  const [dnsSortKey, setDnsSortKey] = useState<DNSSortKey | null>(null)
  const [dnsSortDir, setDnsSortDir] = useState<SortDirection>('desc')

  const toggleConnSort = (key: ConnectionSortKey) => {
    if (connSortKey === key) {
      setConnSortDir(prev => prev === 'asc' ? 'desc' : 'asc')
    } else {
      setConnSortKey(key)
      setConnSortDir('desc')
    }
  }

  const toggleDNSSort = (key: DNSSortKey) => {
    if (dnsSortKey === key) {
      setDnsSortDir(prev => prev === 'asc' ? 'desc' : 'asc')
    } else {
      setDnsSortKey(key)
      setDnsSortDir('desc')
    }
  }

  const sortIndicator = (active: boolean, direction: SortDirection) => {
    if (!active) return <span className="ml-1 text-dark-600">↕</span>
    return <span className="ml-1 text-primary-400">{direction === 'asc' ? '↑' : '↓'}</span>
  }

  const systemLabel = activeInterfaceName
    ? `Internet (${activeInterfaceName})`
    : 'Internet (System)'

  // Close dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (filterDropdownRef.current && !filterDropdownRef.current.contains(event.target as Node)) {
        setFilterDropdownOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  const toggleProfileFilter = (profileId: string) => {
    setSelectedProfiles(previous => {
      const updated = new Set(previous)
      if (updated.has(profileId)) {
        updated.delete(profileId)
      } else {
        updated.add(profileId)
      }
      return updated
    })
  }

  const profileNameMap = useCallback((profileId: string) => {
    if (!profileId || profileId === '__internet__') return systemLabel
    const found = profiles.find(profile => profile.id === profileId)
    return found?.name || profileId
  }, [profiles, systemLabel])

  // Initial data load
  useEffect(() => {
    const loadInitialData = async () => {
      try {
        const [active, recent, dns] = await Promise.all([
          window.go.app.App.GetActiveTrafficConnections(),
          window.go.app.App.GetTrafficConnections(200),
          window.go.app.App.GetDNSQueryLog(500),
        ])
        setActiveConnections(active || [])
        setRecentConnections(recent || [])
        setDnsQueries(dns || [])
      } catch (err) {
        console.error('Failed to load traffic data:', err)
      }
    }
    loadInitialData()
  }, [])

  // Real-time event listeners
  useEffect(() => {
    const handleConnectionOpen = (...args: unknown[]) => {
      const entry = args[0] as TrafficEntry
      if (!entry?.connectionId) return
      setActiveConnections(prev => {
        const updated = [entry, ...prev]
        return updated.length > 500 ? updated.slice(0, 500) : updated
      })
    }

    const handleConnectionUpdate = (...args: unknown[]) => {
      const update = args[0] as { connectionId: string; bytesSent?: number; bytesReceived?: number; durationMs?: number; protocolHint?: string }
      if (!update?.connectionId) return
      setActiveConnections(prev => prev.map(conn => {
        if (conn.connectionId !== update.connectionId) return conn
        return {
          ...conn,
          ...(update.bytesSent !== undefined && { bytesSent: update.bytesSent }),
          ...(update.bytesReceived !== undefined && { bytesReceived: update.bytesReceived }),
          ...(update.durationMs !== undefined && { durationMs: update.durationMs }),
          ...(update.protocolHint !== undefined && { protocolHint: update.protocolHint as TrafficEntry['protocolHint'] }),
        }
      }))
    }

    const handleConnectionClose = (...args: unknown[]) => {
      const entry = args[0] as TrafficEntry
      if (!entry?.connectionId) return
      setActiveConnections(prev => prev.filter(conn => conn.connectionId !== entry.connectionId))
      setRecentConnections(prev => [entry, ...prev].slice(0, 200))
    }

    const handleDNSQuery = (...args: unknown[]) => {
      const entry = args[0] as DNSLogEntry
      if (!entry?.domain) return
      setDnsQueries(prev => [entry, ...prev].slice(0, 500))
    }

    window.runtime.EventsOn('traffic-connection-open', handleConnectionOpen)
    window.runtime.EventsOn('traffic-connection-update', handleConnectionUpdate)
    window.runtime.EventsOn('traffic-connection-close', handleConnectionClose)
    window.runtime.EventsOn('traffic-dns-query', handleDNSQuery)

    return () => {
      window.runtime.EventsOff('traffic-connection-open')
      window.runtime.EventsOff('traffic-connection-update')
      window.runtime.EventsOff('traffic-connection-close')
      window.runtime.EventsOff('traffic-dns-query')
    }
  }, [])

  // Auto-purge closed connections older than 5 minutes
  useEffect(() => {
    const purge_interval = setInterval(() => {
      const cutoff_time = Date.now() - 5 * 60 * 1000
      setRecentConnections(prev =>
        prev.filter(conn => !conn.closedAt || new Date(conn.closedAt).getTime() > cutoff_time)
      )
    }, 30_000)
    return () => clearInterval(purge_interval)
  }, [])

  // Auto-scroll DNS log
  useEffect(() => {
    if (autoScroll && dnsLogRef.current && activeTab === 'dns') {
      dnsLogRef.current.scrollTop = 0
    }
  }, [dnsQueries, autoScroll, activeTab])

  const handleDNSScroll = () => {
    if (!dnsLogRef.current) return
    setAutoScroll(dnsLogRef.current.scrollTop < 10)
  }

  const handleClear = async () => {
    try {
      await window.go.app.App.ClearTrafficLog()
      setActiveConnections([])
      setRecentConnections([])
      setDnsQueries([])
    } catch (err) {
      console.error('Failed to clear traffic log:', err)
    }
  }

  // Filter connections — per-column filters + global profile filter
  const filterConnection = (conn: TrafficEntry) => {
    if (fixedProfileId && conn.profileId !== fixedProfileId) return false
    if (!fixedProfileId && selectedProfiles.size > 0) {
      const connFilterId = (!conn.profileId || conn.profileId === '__internet__') ? SYSTEM_FILTER_ID : conn.profileId
      if (!selectedProfiles.has(connFilterId)) return false
    }
    if (connFilterHostname) {
      const search = connFilterHostname.toLowerCase()
      if (!(conn.hostname || '').toLowerCase().includes(search) &&
          !(conn.sniHostname || '').toLowerCase().includes(search)) return false
    }
    if (connFilterTunnel) {
      if (!profileNameMap(conn.profileId).toLowerCase().includes(connFilterTunnel.toLowerCase())) return false
    }
    if (connFilterService) {
      const serviceName = getServiceName(conn.port, conn.protocolHint)
      if (!serviceName.toLowerCase().includes(connFilterService.toLowerCase()) &&
          !String(conn.port).includes(connFilterService)) return false
    }
    if (connFilterRealIP) {
      if (!conn.realIP.includes(connFilterRealIP)) return false
    }
    return true
  }

  // Sort connections
  const sortConnections = (connections: TrafficEntry[]): TrafficEntry[] => {
    if (!connSortKey) return connections
    const multiplier = connSortDir === 'asc' ? 1 : -1
    return [...connections].sort((connA, connB) => {
      let comparison = 0
      switch (connSortKey) {
        case 'hostname': comparison = (connA.hostname || '').localeCompare(connB.hostname || ''); break
        case 'tunnel': comparison = profileNameMap(connA.profileId).localeCompare(profileNameMap(connB.profileId)); break
        case 'service': comparison = getServiceName(connA.port, connA.protocolHint).localeCompare(getServiceName(connB.port, connB.protocolHint)); break
        case 'bytesSent': comparison = connA.bytesSent - connB.bytesSent; break
        case 'bytesReceived': comparison = connA.bytesReceived - connB.bytesReceived; break
        case 'startedAt': comparison = new Date(connA.startedAt).getTime() - new Date(connB.startedAt).getTime(); break
        case 'closedAt': comparison = new Date(connA.closedAt || 0).getTime() - new Date(connB.closedAt || 0).getTime(); break
        case 'durationMs': comparison = connA.durationMs - connB.durationMs; break
        case 'realIP': comparison = connA.realIP.localeCompare(connB.realIP); break
      }
      return comparison * multiplier
    })
  }

  const filteredActive = sortConnections(activeConnections.filter(filterConnection))
  const filteredRecent = sortConnections(recentConnections.filter(filterConnection))

  // Filter DNS queries — per-column filters + global profile filter
  const filteredDNS = (() => {
    const filtered = dnsQueries.filter(query => {
      if (fixedProfileId && query.profileId !== fixedProfileId) return false
      if (!fixedProfileId && selectedProfiles.size > 0) {
        const queryFilterId = (!query.profileId || query.profileId === '__internet__') ? SYSTEM_FILTER_ID : query.profileId
        if (!selectedProfiles.has(queryFilterId)) return false
      }
      if (dnsFilterProcess && !(query.sourceProcess || '').toLowerCase().includes(dnsFilterProcess.toLowerCase())) return false
      if (dnsFilterDomain && !query.domain.toLowerCase().includes(dnsFilterDomain.toLowerCase())) return false
      if (dnsFilterType && !(query.queryType || '').toLowerCase().includes(dnsFilterType.toLowerCase())) return false
      if (dnsFilterIP && !(query.resolvedIP || '').includes(dnsFilterIP)) return false
      if (dnsFilterTunnel) {
        const tunnelName = query.profileId ? profileNameMap(query.profileId).toLowerCase() : 'fallback'
        if (!tunnelName.includes(dnsFilterTunnel.toLowerCase())) return false
      }
      return true
    })
    if (!dnsSortKey) return filtered
    const multiplier = dnsSortDir === 'asc' ? 1 : -1
    return [...filtered].sort((queryA, queryB) => {
      let comparison = 0
      switch (dnsSortKey) {
        case 'timestamp': comparison = new Date(queryA.timestamp).getTime() - new Date(queryB.timestamp).getTime(); break
        case 'sourceProcess': comparison = (queryA.sourceProcess || '').localeCompare(queryB.sourceProcess || ''); break
        case 'domain': comparison = queryA.domain.localeCompare(queryB.domain); break
        case 'queryType': comparison = (queryA.queryType || '').localeCompare(queryB.queryType || ''); break
        case 'resolvedIP': comparison = (queryA.resolvedIP || '').localeCompare(queryB.resolvedIP || ''); break
        case 'tunnel': comparison = (queryA.profileId || '').localeCompare(queryB.profileId || ''); break
        case 'responseTimeMs': comparison = queryA.responseTimeMs - queryB.responseTimeMs; break
      }
      return comparison * multiplier
    })
  })()

  const filterButtonLabel = selectedProfiles.size === 0
    ? 'All connections'
    : selectedProfiles.size === 1
      ? (() => {
          const selectedId = [...selectedProfiles][0]
          if (selectedId === SYSTEM_FILTER_ID) return systemLabel
          const found = profiles.find(profile => profile.id === selectedId)
          return found?.name || selectedId
        })()
      : `${selectedProfiles.size} selected`

  const tabs: { id: TabId; label: string; count?: number }[] = [
    { id: 'connections', label: 'Connections', count: filteredActive.length },
    { id: 'dns', label: 'DNS Queries', count: filteredDNS.length },
  ]

  return (
    <div className="h-full flex flex-col">
      {/* Header — hidden when embedded in a profile panel */}
      {!hideHeader && (
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-xl font-bold text-white">Traffic</h2>
          <div className="flex items-center gap-4">
            <button
              onClick={handleClear}
              className="btn btn-secondary text-xs px-3 py-1"
            >
              Clear
            </button>
          </div>
        </div>
      )}

      {/* Profile filter + Tabs row */}
      <div className="flex items-end gap-4 mb-3">
        {/* Multi-select connection filter — hidden when fixedProfileId is set */}
        {!fixedProfileId && (
        <div className="relative" ref={filterDropdownRef}>
          <button
            onClick={() => setFilterDropdownOpen(!filterDropdownOpen)}
            className="input text-sm w-48 text-left flex items-center justify-between"
          >
            <span className="truncate">{filterButtonLabel}</span>
            <svg className="w-4 h-4 text-dark-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
            </svg>
          </button>
          {filterDropdownOpen && (
            <div className="absolute right-0 top-full mt-1 w-56 bg-dark-800 border border-dark-600 rounded-lg shadow-xl z-20 py-1 max-h-64 overflow-auto">
              {/* System / Internet option */}
              <label className="flex items-center gap-2 px-3 py-1.5 hover:bg-dark-700 cursor-pointer">
                <input
                  type="checkbox"
                  checked={selectedProfiles.has(SYSTEM_FILTER_ID)}
                  onChange={() => toggleProfileFilter(SYSTEM_FILTER_ID)}
                  className="w-3.5 h-3.5 rounded border-dark-500 text-primary-500 focus:ring-primary-500 bg-dark-700"
                />
                <span className="text-sm text-dark-200">{systemLabel}</span>
              </label>
              <div className="border-t border-dark-700 my-1" />
              {profiles.map(profile => (
                <label key={profile.id} className="flex items-center gap-2 px-3 py-1.5 hover:bg-dark-700 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={selectedProfiles.has(profile.id)}
                    onChange={() => toggleProfileFilter(profile.id)}
                    className="w-3.5 h-3.5 rounded border-dark-500 text-primary-500 focus:ring-primary-500 bg-dark-700"
                  />
                  <span className="text-sm text-dark-200">{profile.name}</span>
                </label>
              ))}
              {selectedProfiles.size > 0 && (
                <>
                  <div className="border-t border-dark-700 my-1" />
                  <button
                    onClick={() => setSelectedProfiles(new Set())}
                    className="w-full px-3 py-1.5 text-left text-xs text-dark-400 hover:text-dark-200 hover:bg-dark-700"
                  >
                    Clear filters
                  </button>
                </>
              )}
            </div>
          )}
        </div>
        )}

        {/* Tabs */}
        <div className="flex gap-1 border-b border-dark-700">
          {tabs.map(tab => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
                activeTab === tab.id
                  ? 'border-primary-500 text-primary-400'
                  : 'border-transparent text-dark-400 hover:text-dark-200'
              }`}
            >
              {tab.label}
              {tab.count !== undefined && tab.count > 0 && (
                <span className="ml-1.5 px-1.5 py-0.5 text-xs rounded-full bg-dark-700 text-dark-300">
                  {tab.count}
                </span>
              )}
            </button>
          ))}
        </div>
      </div>

      {/* Tab Content */}
      <div className="flex-1 overflow-hidden">
        {activeTab === 'connections' && (
          <div className="h-full overflow-auto">
            <table className="w-full text-sm">
              <thead className="sticky top-0 bg-dark-800 z-10">
                <tr className="text-dark-400 text-left border-b border-dark-700">
                  <th className="pb-1 pr-3 font-medium w-6">Status</th>
                  <th className="pb-1 pr-3 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleConnSort('hostname')}>Hostname{sortIndicator(connSortKey === 'hostname', connSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleConnSort('tunnel')}>Tunnel{sortIndicator(connSortKey === 'tunnel', connSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleConnSort('service')}>Service{sortIndicator(connSortKey === 'service', connSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium text-right cursor-pointer select-none hover:text-dark-200" onClick={() => toggleConnSort('bytesSent')}>Sent{sortIndicator(connSortKey === 'bytesSent', connSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium text-right cursor-pointer select-none hover:text-dark-200" onClick={() => toggleConnSort('bytesReceived')}>Recv{sortIndicator(connSortKey === 'bytesReceived', connSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleConnSort('startedAt')}>Started{sortIndicator(connSortKey === 'startedAt', connSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleConnSort('closedAt')}>Ended{sortIndicator(connSortKey === 'closedAt', connSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium text-right cursor-pointer select-none hover:text-dark-200" onClick={() => toggleConnSort('durationMs')}>Duration{sortIndicator(connSortKey === 'durationMs', connSortDir)}</th>
                  <th className="pb-1 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleConnSort('realIP')}>Real IP{sortIndicator(connSortKey === 'realIP', connSortDir)}</th>
                </tr>
                <tr className="border-b border-dark-700">
                  <th className="pb-2 pr-3"></th>
                  <th className="pb-2 pr-3"><input type="text" value={connFilterHostname} onChange={(event) => setConnFilterHostname(event.target.value)} placeholder="Filter..." className="w-full bg-dark-900 border border-dark-600 rounded px-1.5 py-0.5 text-xs text-dark-200 focus:outline-none focus:border-primary-500" /></th>
                  <th className="pb-2 pr-3"><input type="text" value={connFilterTunnel} onChange={(event) => setConnFilterTunnel(event.target.value)} placeholder="Filter..." className="w-full bg-dark-900 border border-dark-600 rounded px-1.5 py-0.5 text-xs text-dark-200 focus:outline-none focus:border-primary-500" /></th>
                  <th className="pb-2 pr-3"><input type="text" value={connFilterService} onChange={(event) => setConnFilterService(event.target.value)} placeholder="Filter..." className="w-full bg-dark-900 border border-dark-600 rounded px-1.5 py-0.5 text-xs text-dark-200 focus:outline-none focus:border-primary-500" /></th>
                  <th className="pb-2 pr-3"></th>
                  <th className="pb-2 pr-3"></th>
                  <th className="pb-2 pr-3"></th>
                  <th className="pb-2 pr-3"></th>
                  <th className="pb-2 pr-3"></th>
                  <th className="pb-2"><input type="text" value={connFilterRealIP} onChange={(event) => setConnFilterRealIP(event.target.value)} placeholder="Filter..." className="w-full bg-dark-900 border border-dark-600 rounded px-1.5 py-0.5 text-xs text-dark-200 focus:outline-none focus:border-primary-500" /></th>
                </tr>
              </thead>
              <tbody className="text-dark-200">
                {filteredActive.length === 0 && filteredRecent.length === 0 && (
                  <tr>
                    <td colSpan={10} className="py-8 text-center text-dark-500">
                      No traffic recorded yet. Connect a tunnel and browse through it.
                    </td>
                  </tr>
                )}
                {/* Active connections first */}
                {filteredActive.map(conn => (
                  <tr key={conn.connectionId} className="border-b border-dark-800 bg-dark-800/30">
                    <td className="py-1.5 pr-3">
                      <span className="inline-block w-2 h-2 rounded-full bg-green-500 animate-pulse" title="Active" />
                    </td>
                    <td className="py-1.5 pr-3 font-mono text-primary-400 truncate max-w-[200px]" title={conn.sniHostname || conn.hostname}>
                      {conn.hostname || conn.sniHostname || conn.tunnelIP}
                      {!conn.profileId && conn.sniHostname && (
                        <span className="ml-1.5 text-[10px] text-dark-400 font-sans">{conn.sniHostname}</span>
                      )}
                    </td>
                    <td className="py-1.5 pr-3 truncate max-w-[120px]" title={conn.profileId}>
                      {profileNameMap(conn.profileId)}
                    </td>
                    <td className="py-1.5 pr-3" title={`${getServiceName(conn.port, conn.protocolHint)} (${conn.port})`}>
                      <span className="text-xs">{getProtocolIcon(conn.protocolHint)}</span>
                      <span className="ml-1 text-xs text-dark-400">{getServiceName(conn.port, conn.protocolHint)}</span>
                    </td>
                    <td className="py-1.5 pr-3 text-right font-mono text-xs text-blue-400">{formatBytes(conn.bytesSent)}</td>
                    <td className="py-1.5 pr-3 text-right font-mono text-xs text-green-400">{formatBytes(conn.bytesReceived)}</td>
                    <td className="py-1.5 pr-3 font-mono text-xs text-dark-400">{formatTimestamp(conn.startedAt)}</td>
                    <td className="py-1.5 pr-3 font-mono text-xs text-dark-500">—</td>
                    <td className="py-1.5 pr-3 text-right font-mono text-xs">{formatDuration(conn.durationMs)}</td>
                    <td className="py-1.5 font-mono text-xs text-dark-400">{conn.realIP}:{conn.port}</td>
                  </tr>
                ))}
                {/* Closed connections */}
                {filteredRecent.map(conn => (
                  <tr key={conn.connectionId} className="border-b border-dark-800 opacity-60">
                    <td className="py-1.5 pr-3">
                      <span className="inline-block w-2 h-2 rounded-full bg-dark-500" title="Closed" />
                    </td>
                    <td className="py-1.5 pr-3 font-mono text-dark-300 truncate max-w-[200px]" title={conn.sniHostname || conn.hostname}>
                      {conn.hostname || conn.sniHostname || conn.tunnelIP}
                    </td>
                    <td className="py-1.5 pr-3 truncate max-w-[120px]" title={conn.profileId}>
                      {profileNameMap(conn.profileId)}
                    </td>
                    <td className="py-1.5 pr-3" title={`${getServiceName(conn.port, conn.protocolHint)} (${conn.port})`}>
                      <span className="text-xs">{getProtocolIcon(conn.protocolHint)}</span>
                      <span className="ml-1 text-xs text-dark-400">{getServiceName(conn.port, conn.protocolHint)}</span>
                    </td>
                    <td className="py-1.5 pr-3 text-right font-mono text-xs">{formatBytes(conn.bytesSent)}</td>
                    <td className="py-1.5 pr-3 text-right font-mono text-xs">{formatBytes(conn.bytesReceived)}</td>
                    <td className="py-1.5 pr-3 font-mono text-xs text-dark-400">{formatTimestamp(conn.startedAt)}</td>
                    <td className="py-1.5 pr-3 font-mono text-xs text-dark-400">{conn.closedAt ? formatTimestamp(conn.closedAt) : '—'}</td>
                    <td className="py-1.5 pr-3 text-right font-mono text-xs">{formatDuration(conn.durationMs)}</td>
                    <td className="py-1.5 font-mono text-xs text-dark-500">{conn.realIP}:{conn.port}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {activeTab === 'dns' && (
          <div
            ref={dnsLogRef}
            className="h-full overflow-auto"
            onScroll={handleDNSScroll}
          >
            <table className="w-full text-sm">
              <thead className="sticky top-0 bg-dark-800 z-10">
                <tr className="text-dark-400 text-left border-b border-dark-700">
                  <th className="pb-1 pr-3 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleDNSSort('timestamp')}>Time{sortIndicator(dnsSortKey === 'timestamp', dnsSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleDNSSort('sourceProcess')}>Process{sortIndicator(dnsSortKey === 'sourceProcess', dnsSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleDNSSort('domain')}>Domain{sortIndicator(dnsSortKey === 'domain', dnsSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleDNSSort('queryType')}>Type{sortIndicator(dnsSortKey === 'queryType', dnsSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleDNSSort('resolvedIP')}>Resolved IP{sortIndicator(dnsSortKey === 'resolvedIP', dnsSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium cursor-pointer select-none hover:text-dark-200" onClick={() => toggleDNSSort('tunnel')}>Tunnel{sortIndicator(dnsSortKey === 'tunnel', dnsSortDir)}</th>
                  <th className="pb-1 pr-3 font-medium text-right cursor-pointer select-none hover:text-dark-200" onClick={() => toggleDNSSort('responseTimeMs')}>Response{sortIndicator(dnsSortKey === 'responseTimeMs', dnsSortDir)}</th>
                  <th className="pb-1 font-medium w-6">Status</th>
                </tr>
                <tr className="border-b border-dark-700">
                  <th className="pb-2 pr-3"></th>
                  <th className="pb-2 pr-3"><input type="text" value={dnsFilterProcess} onChange={(event) => setDnsFilterProcess(event.target.value)} placeholder="Filter..." className="w-full bg-dark-900 border border-dark-600 rounded px-1.5 py-0.5 text-xs text-dark-200 focus:outline-none focus:border-primary-500" /></th>
                  <th className="pb-2 pr-3"><input type="text" value={dnsFilterDomain} onChange={(event) => setDnsFilterDomain(event.target.value)} placeholder="Filter..." className="w-full bg-dark-900 border border-dark-600 rounded px-1.5 py-0.5 text-xs text-dark-200 focus:outline-none focus:border-primary-500" /></th>
                  <th className="pb-2 pr-3"><input type="text" value={dnsFilterType} onChange={(event) => setDnsFilterType(event.target.value)} placeholder="Filter..." className="w-full bg-dark-900 border border-dark-600 rounded px-1.5 py-0.5 text-xs text-dark-200 focus:outline-none focus:border-primary-500" /></th>
                  <th className="pb-2 pr-3"><input type="text" value={dnsFilterIP} onChange={(event) => setDnsFilterIP(event.target.value)} placeholder="Filter..." className="w-full bg-dark-900 border border-dark-600 rounded px-1.5 py-0.5 text-xs text-dark-200 focus:outline-none focus:border-primary-500" /></th>
                  <th className="pb-2 pr-3"><input type="text" value={dnsFilterTunnel} onChange={(event) => setDnsFilterTunnel(event.target.value)} placeholder="Filter..." className="w-full bg-dark-900 border border-dark-600 rounded px-1.5 py-0.5 text-xs text-dark-200 focus:outline-none focus:border-primary-500" /></th>
                  <th className="pb-2 pr-3"></th>
                  <th className="pb-2"></th>
                </tr>
              </thead>
              <tbody className="text-dark-200">
                {filteredDNS.length === 0 && (
                  <tr>
                    <td colSpan={8} className="py-8 text-center text-dark-500">
                      No DNS queries recorded yet.
                    </td>
                  </tr>
                )}
                {filteredDNS.map((query, queryIndex) => (
                  <tr
                    key={`${query.timestamp}-${queryIndex}`}
                    className={`border-b border-dark-800 ${
                      !query.success ? 'bg-red-900/10' : query.viaTunnel ? '' : 'opacity-50'
                    }`}
                  >
                    <td className="py-1.5 pr-3 font-mono text-xs text-dark-400">
                      {formatTimestamp(query.timestamp)}
                    </td>
                    <td className="py-1.5 pr-3 text-xs text-dark-300 truncate max-w-[120px]" title={query.sourceProcess || ''}>
                      {query.sourceProcess || '—'}
                    </td>
                    <td className="py-1.5 pr-3 font-mono text-primary-400 truncate max-w-[200px]" title={query.domain}>
                      {query.domain}
                    </td>
                    <td className="py-1.5 pr-3 text-xs text-dark-400">
                      {query.queryType}
                    </td>
                    <td className="py-1.5 pr-3 font-mono text-xs">
                      {query.resolvedIP || '—'}
                    </td>
                    <td className="py-1.5 pr-3 truncate max-w-[120px]">
                      {query.viaTunnel ? (
                        <span className="text-blue-400 text-xs">{profileNameMap(query.profileId)}</span>
                      ) : (
                        <span className="text-dark-500 text-xs">fallback</span>
                      )}
                    </td>
                    <td className="py-1.5 pr-3 text-right font-mono text-xs text-dark-400">
                      {query.responseTimeMs}ms
                    </td>
                    <td className="py-1.5 text-center">
                      {query.success ? (
                        <span className="text-green-500 text-xs" title="Success">✓</span>
                      ) : (
                        <span className="text-red-500 text-xs" title={query.errorMessage || 'Failed'}>✗</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!autoScroll && (
              <button
                onClick={() => {
                  setAutoScroll(true)
                  if (dnsLogRef.current) dnsLogRef.current.scrollTop = 0
                }}
                className="fixed bottom-6 right-6 btn btn-primary text-xs px-3 py-1.5 shadow-lg"
              >
                ↑ Latest
              </button>
            )}
          </div>
        )}

      </div>
    </div>
  )
}

export default Traffic
