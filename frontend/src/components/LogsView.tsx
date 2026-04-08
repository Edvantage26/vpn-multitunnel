import { useState, useEffect, useRef, useCallback } from 'react'
import type { LogEntry, SystemLogEntry } from '../App'

type LogsTab = 'connections' | 'system'

interface LogsViewProps {
  profiles: { id: string; name: string }[]
  activeInterfaceName?: string
  fixedProfileId?: string // When set, hides profile filter and shows only this profile's logs
  hideHeader?: boolean    // When embedded, hide the title bar
}

const SYSTEM_FILTER_ID = '__system__'

const levelColorMap: Record<string, string> = {
  debug: 'text-dark-500',
  info: 'text-blue-400',
  warn: 'text-yellow-400',
  error: 'text-red-400',
}

const levelBadgeColorMap: Record<string, string> = {
  debug: 'bg-dark-700 text-dark-400',
  info: 'bg-blue-900/40 text-blue-400',
  warn: 'bg-yellow-900/40 text-yellow-400',
  error: 'bg-red-900/40 text-red-400',
}

const componentColorMap: Record<string, string> = {
  tunnel: 'text-purple-400',
  health: 'text-green-400',
  dns: 'text-cyan-400',
  proxy: 'text-orange-400',
}

const sourceBadgeColorMap: Record<string, string> = {
  service: 'bg-indigo-900/40 text-indigo-300',
  eventlog: 'bg-cyan-900/40 text-cyan-300',
  crash: 'bg-red-900/40 text-red-300',
}

// Stable color palette for profile badges
const profileBadgeColors = [
  'bg-blue-900/40 text-blue-300',
  'bg-purple-900/40 text-purple-300',
  'bg-teal-900/40 text-teal-300',
  'bg-orange-900/40 text-orange-300',
  'bg-pink-900/40 text-pink-300',
  'bg-indigo-900/40 text-indigo-300',
  'bg-emerald-900/40 text-emerald-300',
  'bg-amber-900/40 text-amber-300',
]

function formatLogTimestamp(timestampStr: string): string {
  try {
    const logDate = new Date(timestampStr)
    if (isNaN(logDate.getTime())) return timestampStr
    return logDate.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
  } catch {
    return timestampStr
  }
}

function formatSystemTimestamp(timestampStr: string): string {
  try {
    const logDate = new Date(timestampStr)
    if (isNaN(logDate.getTime())) return timestampStr
    return logDate.toLocaleString('en-US', {
      hour12: false,
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    })
  } catch {
    return timestampStr
  }
}

function LogsView({ profiles, activeInterfaceName, fixedProfileId, hideHeader }: LogsViewProps) {
  const [activeTab, setActiveTab] = useState<LogsTab>('connections')

  // When embedded for a specific profile, only show connection logs (no system tab)
  if (fixedProfileId) {
    return (
      <div className="h-full flex flex-col">
        <ConnectionLogsPanel profiles={profiles} activeInterfaceName={activeInterfaceName} fixedProfileId={fixedProfileId} />
      </div>
    )
  }

  return (
    <div className="h-full flex flex-col">
      {/* Header */}
      {!hideHeader && (
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-xl font-bold text-white">Logs</h2>
        </div>
      )}

      {/* Tabs */}
      <div className="flex gap-1 mb-4 border-b border-dark-700">
        <button
          onClick={() => setActiveTab('connections')}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            activeTab === 'connections'
              ? 'border-primary-500 text-primary-400'
              : 'border-transparent text-dark-400 hover:text-dark-200'
          }`}
        >
          Connections
        </button>
        <button
          onClick={() => setActiveTab('system')}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            activeTab === 'system'
              ? 'border-primary-500 text-primary-400'
              : 'border-transparent text-dark-400 hover:text-dark-200'
          }`}
        >
          System
        </button>
      </div>

      {/* Tab Content */}
      {activeTab === 'connections' ? (
        <ConnectionLogsPanel profiles={profiles} activeInterfaceName={activeInterfaceName} />
      ) : (
        <SystemLogsPanel />
      )}
    </div>
  )
}

// ─── Connection Logs Panel ──────────────────────────────────────────────────

function ConnectionLogsPanel({ profiles, activeInterfaceName, fixedProfileId }: { profiles: { id: string; name: string }[]; activeInterfaceName?: string; fixedProfileId?: string }) {
  const [logEntries, setLogEntries] = useState<LogEntry[]>([])
  const [levelFilter, setLevelFilter] = useState<string>('all')
  const [searchText, setSearchText] = useState('')
  const [selectedProfiles, setSelectedProfiles] = useState<Set<string>>(new Set())
  const [filterDropdownOpen, setFilterDropdownOpen] = useState(false)
  const [pinnedToBottom, setPinnedToBottom] = useState(true)
  const logContainerRef = useRef<HTMLDivElement>(null)
  const filterDropdownRef = useRef<HTMLDivElement>(null)

  const systemLabel = activeInterfaceName
    ? `System (${activeInterfaceName})`
    : 'System'

  const profileColorMap = useCallback((profileId: string): string => {
    if (!profileId) return 'bg-dark-700 text-dark-400'
    const profileIndex = profiles.findIndex(profile => profile.id === profileId)
    if (profileIndex === -1) return 'bg-dark-700 text-dark-400'
    return profileBadgeColors[profileIndex % profileBadgeColors.length]
  }, [profiles])

  const profileNameMap = useCallback((profileId: string): string => {
    if (!profileId || profileId === '__internet__') return systemLabel
    const found = profiles.find(profile => profile.id === profileId)
    return found?.name || profileId
  }, [profiles, systemLabel])

  // Fetch initial logs
  useEffect(() => {
    const fetchInitialLogs = async () => {
      try {
        const initialLogs = await window.go.app.App.GetDebugLogs('', '', 500)
        setLogEntries(initialLogs || [])
      } catch (fetchError) {
        console.error('Failed to fetch logs:', fetchError)
      }
    }
    fetchInitialLogs()
  }, [])

  // Subscribe to real-time log events
  useEffect(() => {
    const handleLogEvent = (...eventArgs: unknown[]) => {
      const logEntry = eventArgs[0] as LogEntry
      if (!logEntry) return
      setLogEntries(previousEntries => {
        const updatedEntries = [...previousEntries, logEntry]
        if (updatedEntries.length > 1000) {
          return updatedEntries.slice(updatedEntries.length - 1000)
        }
        return updatedEntries
      })
    }

    window.runtime.EventsOn('profile-log', handleLogEvent)
    return () => {
      window.runtime.EventsOff('profile-log')
    }
  }, [])

  // Auto-scroll to bottom
  useEffect(() => {
    if (pinnedToBottom && logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
    }
  }, [logEntries, pinnedToBottom])

  const handleScroll = useCallback(() => {
    if (!logContainerRef.current) return
    const container = logContainerRef.current
    const distanceFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight
    setPinnedToBottom(distanceFromBottom < 30)
  }, [])

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

  // Filter entries
  const filteredEntries = logEntries.filter(entry => {
    if (levelFilter !== 'all' && entry.level !== levelFilter) return false
    if (fixedProfileId && entry.profileId !== fixedProfileId) return false
    if (!fixedProfileId && selectedProfiles.size > 0) {
      const entryFilterId = (!entry.profileId || entry.profileId === '__internet__') ? SYSTEM_FILTER_ID : entry.profileId
      if (!selectedProfiles.has(entryFilterId)) return false
    }
    if (searchText) {
      const lowerSearch = searchText.toLowerCase()
      return (
        entry.message.toLowerCase().includes(lowerSearch) ||
        entry.component.toLowerCase().includes(lowerSearch)
      )
    }
    return true
  })

  const filterButtonLabel = selectedProfiles.size === 0
    ? 'All connections'
    : selectedProfiles.size === 1
      ? profileNameMap([...selectedProfiles][0] === SYSTEM_FILTER_ID ? '' : [...selectedProfiles][0])
      : `${selectedProfiles.size} selected`

  return (
    <>
      {/* Filters */}
      <div className="flex gap-3 mb-3">
        <input
          type="text"
          placeholder="Search logs..."
          value={searchText}
          onChange={(event) => setSearchText(event.target.value)}
          className="input text-sm flex-1"
        />
        <select
          value={levelFilter}
          onChange={(event) => setLevelFilter(event.target.value)}
          className="input text-sm w-28"
        >
          <option value="all">All levels</option>
          <option value="debug">Debug</option>
          <option value="info">Info</option>
          <option value="warn">Warn</option>
          <option value="error">Error</option>
        </select>

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

        <span className="text-xs text-dark-500 self-center">{filteredEntries.length} entries</span>
      </div>

      {/* Log entries */}
      <div
        ref={logContainerRef}
        onScroll={handleScroll}
        className="flex-1 overflow-y-auto font-mono text-xs bg-dark-900 rounded-lg p-3 space-y-0.5 min-h-0"
      >
        {filteredEntries.length === 0 ? (
          <div className="text-dark-500 text-center py-8">
            No logs to display.
          </div>
        ) : (
          filteredEntries.map((entry, entryIndex) => (
            <div key={`${entry.timestamp}-${entryIndex}`} className="flex gap-2 py-0.5 hover:bg-dark-800/50 rounded px-1">
              <span className="text-dark-500 flex-shrink-0">{formatLogTimestamp(entry.timestamp)}</span>
              <span className={`flex-shrink-0 px-1.5 rounded text-[10px] font-medium uppercase ${levelBadgeColorMap[entry.level] || 'bg-dark-700 text-dark-400'}`}>
                {entry.level}
              </span>
              <span className={`flex-shrink-0 px-1.5 rounded text-[10px] font-medium truncate max-w-[100px] ${profileColorMap(entry.profileId)}`}>
                {profileNameMap(entry.profileId)}
              </span>
              <span className={`flex-shrink-0 ${componentColorMap[entry.component] || 'text-dark-400'}`}>
                [{entry.component}]
              </span>
              <span className={`flex-1 ${levelColorMap[entry.level] || 'text-dark-300'}`}>
                {entry.message}
                {entry.fields && Object.keys(entry.fields).length > 0 && (
                  <span className="text-dark-600 ml-2">
                    {Object.entries(entry.fields).map(([fieldKey, fieldValue]) =>
                      `${fieldKey}=${fieldValue}`
                    ).join(' ')}
                  </span>
                )}
              </span>
            </div>
          ))
        )}
      </div>

      {!pinnedToBottom && (
        <button
          onClick={() => {
            setPinnedToBottom(true)
            if (logContainerRef.current) {
              logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
            }
          }}
          className="fixed bottom-6 right-6 btn btn-primary text-xs px-3 py-1.5 shadow-lg z-10"
        >
          ↓ Latest
        </button>
      )}
    </>
  )
}

// ─── System Logs Panel ──────────────────────────────────────────────────────

function SystemLogsPanel() {
  const [systemEntries, setSystemEntries] = useState<SystemLogEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [levelFilter, setLevelFilter] = useState<string>('all')
  const [sourceFilter, setSourceFilter] = useState<string>('all')
  const [searchText, setSearchText] = useState('')
  const [pinnedToBottom, setPinnedToBottom] = useState(true)
  const logContainerRef = useRef<HTMLDivElement>(null)

  const fetchSystemLogs = useCallback(async () => {
    setLoading(true)
    try {
      const entries = await window.go.app.App.GetSystemLogs(500)
      setSystemEntries(entries || [])
    } catch (fetchError) {
      console.error('Failed to fetch system logs:', fetchError)
    } finally {
      setLoading(false)
    }
  }, [])

  // Fetch on mount
  useEffect(() => {
    fetchSystemLogs()
  }, [fetchSystemLogs])

  // Auto-scroll to bottom
  useEffect(() => {
    if (pinnedToBottom && logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
    }
  }, [systemEntries, pinnedToBottom])

  const handleScroll = useCallback(() => {
    if (!logContainerRef.current) return
    const container = logContainerRef.current
    const distanceFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight
    setPinnedToBottom(distanceFromBottom < 30)
  }, [])

  // Filter entries
  const filteredEntries = systemEntries.filter(entry => {
    if (levelFilter !== 'all' && entry.level !== levelFilter) return false
    if (sourceFilter !== 'all' && entry.source !== sourceFilter) return false
    if (searchText) {
      return entry.message.toLowerCase().includes(searchText.toLowerCase())
    }
    return true
  })

  const sourceLabel = (source: string): string => {
    switch (source) {
      case 'service': return 'Service'
      case 'eventlog': return 'Event Log'
      case 'crash': return 'Crash'
      default: return source
    }
  }

  return (
    <>
      {/* Filters */}
      <div className="flex gap-3 mb-3">
        <input
          type="text"
          placeholder="Search system logs..."
          value={searchText}
          onChange={(event) => setSearchText(event.target.value)}
          className="input text-sm flex-1"
        />
        <select
          value={levelFilter}
          onChange={(event) => setLevelFilter(event.target.value)}
          className="input text-sm w-28"
        >
          <option value="all">All levels</option>
          <option value="info">Info</option>
          <option value="warn">Warn</option>
          <option value="error">Error</option>
        </select>
        <select
          value={sourceFilter}
          onChange={(event) => setSourceFilter(event.target.value)}
          className="input text-sm w-36"
        >
          <option value="all">All sources</option>
          <option value="service">Service Log</option>
          <option value="eventlog">Event Log</option>
          <option value="crash">Crashes</option>
        </select>
        <button
          onClick={fetchSystemLogs}
          disabled={loading}
          className="btn btn-secondary text-xs px-3 flex items-center gap-1.5"
          title="Refresh system logs"
        >
          <svg className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          Refresh
        </button>
        <span className="text-xs text-dark-500 self-center">{filteredEntries.length} entries</span>
      </div>

      {/* Log entries */}
      <div
        ref={logContainerRef}
        onScroll={handleScroll}
        className="flex-1 overflow-y-auto font-mono text-xs bg-dark-900 rounded-lg p-3 space-y-0.5 min-h-0"
      >
        {loading && systemEntries.length === 0 ? (
          <div className="text-dark-500 text-center py-8">
            Loading system logs...
          </div>
        ) : filteredEntries.length === 0 ? (
          <div className="text-dark-500 text-center py-8">
            No system logs found. Service logs appear here when the VPN MultiTunnel service is running.
          </div>
        ) : (
          filteredEntries.map((entry, entryIndex) => (
            <div
              key={`${entry.timestamp}-${entryIndex}`}
              className={`flex gap-2 py-0.5 hover:bg-dark-800/50 rounded px-1 ${
                entry.source === 'crash' ? 'bg-red-900/10' : ''
              }`}
            >
              <span className="text-dark-500 flex-shrink-0">{formatSystemTimestamp(entry.timestamp)}</span>
              <span className={`flex-shrink-0 px-1.5 rounded text-[10px] font-medium uppercase ${levelBadgeColorMap[entry.level] || 'bg-dark-700 text-dark-400'}`}>
                {entry.level}
              </span>
              <span className={`flex-shrink-0 px-1.5 rounded text-[10px] font-medium ${sourceBadgeColorMap[entry.source] || 'bg-dark-700 text-dark-400'}`}>
                {sourceLabel(entry.source)}
              </span>
              <span className={`flex-1 ${levelColorMap[entry.level] || 'text-dark-300'}`}>
                {entry.message}
              </span>
            </div>
          ))
        )}
      </div>

      {!pinnedToBottom && (
        <button
          onClick={() => {
            setPinnedToBottom(true)
            if (logContainerRef.current) {
              logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
            }
          }}
          className="fixed bottom-6 right-6 btn btn-primary text-xs px-3 py-1.5 shadow-lg z-10"
        >
          ↓ Latest
        </button>
      )}
    </>
  )
}

export default LogsView
