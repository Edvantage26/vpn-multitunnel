import { useState, useEffect } from 'react'

interface SystemStatusData {
  isAdmin: boolean
  dnsConfigured: boolean
  autoConfigureLoopback: boolean
  autoConfigureDNS: boolean
  usePort53: boolean
  tcpProxyEnabled: boolean
  dnsProxyEnabled: boolean
  dnsProxyPort: number
}

function SystemStatus() {
  const [status, setStatus] = useState<SystemStatusData | null>(null)

  useEffect(() => {
    const fetchStatus = async () => {
      try {
        const data = await window.go.app.App.GetSystemStatus()
        setStatus(data as SystemStatusData)
      } catch (err) {
        console.error('Failed to fetch system status:', err)
      }
    }

    fetchStatus()
    const interval = setInterval(fetchStatus, 5000)
    return () => clearInterval(interval)
  }, [])

  if (!status) {
    return null
  }

  return (
    <div className="card p-4 mb-4">
      <h3 className="text-sm font-semibold text-dark-300 mb-3">System Status</h3>

      <div className="grid grid-cols-2 gap-3 text-sm">
        {/* Admin Status */}
        <div className="flex items-center gap-2">
          <div className={`w-2 h-2 rounded-full ${status.isAdmin ? 'bg-green-500' : 'bg-yellow-500'}`} />
          <span className="text-dark-400">
            {status.isAdmin ? 'Running as Admin' : 'Not Admin'}
          </span>
        </div>

        {/* DNS Proxy */}
        <div className="flex items-center gap-2">
          <div className={`w-2 h-2 rounded-full ${status.dnsProxyEnabled ? 'bg-green-500' : 'bg-dark-600'}`} />
          <span className="text-dark-400">
            DNS Proxy {status.dnsProxyEnabled ? `(:${status.dnsProxyPort})` : 'Off'}
          </span>
        </div>

        {/* System DNS */}
        <div className="flex items-center gap-2">
          <div className={`w-2 h-2 rounded-full ${status.dnsConfigured ? 'bg-green-500' : 'bg-dark-600'}`} />
          <span className="text-dark-400">
            {status.dnsConfigured ? 'DNS Configured' : 'DNS Not Set'}
          </span>
        </div>

        {/* TCP Proxy */}
        <div className="flex items-center gap-2">
          <div className={`w-2 h-2 rounded-full ${status.tcpProxyEnabled ? 'bg-green-500' : 'bg-dark-600'}`} />
          <span className="text-dark-400">
            TCP Proxy {status.tcpProxyEnabled ? 'Active' : 'Off'}
          </span>
        </div>
      </div>

      {/* Warnings */}
      {!status.isAdmin && (status.autoConfigureLoopback || status.autoConfigureDNS) && (
        <div className="mt-3 p-2 bg-yellow-900/30 border border-yellow-700/50 rounded text-xs text-yellow-400">
          Run as Administrator for automatic network configuration
        </div>
      )}

      {/* Transparent Proxy Info */}
      {status.tcpProxyEnabled && status.dnsProxyEnabled && status.dnsConfigured && (
        <div className="mt-3 p-2 bg-green-900/30 border border-green-700/50 rounded text-xs text-green-400">
          Transparent proxy active - use hostnames like <code className="bg-dark-800 px-1 rounded">db.svi</code> directly
        </div>
      )}
    </div>
  )
}

export default SystemStatus
