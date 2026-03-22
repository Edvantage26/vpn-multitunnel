import { useState, useEffect } from 'react'
import { ActiveConnection } from '../App'

function ActiveConnections() {
  const [connections, setConnections] = useState<ActiveConnection[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [tcpProxyEnabled, setTcpProxyEnabled] = useState(false)
  const [listenerCount, setListenerCount] = useState(0)

  useEffect(() => {
    const fetchData = async () => {
      try {
        const [conns, enabled, count] = await Promise.all([
          window.go.app.App.GetActiveConnections(),
          window.go.app.App.IsTCPProxyEnabled(),
          window.go.app.App.GetTCPProxyListenerCount()
        ])
        setConnections(conns || [])
        setTcpProxyEnabled(enabled)
        setListenerCount(count)
      } catch (err) {
        console.error('Failed to fetch active connections:', err)
      } finally {
        setIsLoading(false)
      }
    }

    fetchData()
    const interval = setInterval(fetchData, 5000)
    return () => clearInterval(interval)
  }, [])

  if (isLoading) {
    return (
      <div className="card p-6">
        <h3 className="text-lg font-semibold text-white mb-4">Active Connections</h3>
        <div className="text-dark-400">Loading...</div>
      </div>
    )
  }

  return (
    <div className="card p-6">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-lg font-semibold text-white">Transparent Proxy</h3>
        <div className="flex items-center gap-4">
          <span className={`text-sm ${tcpProxyEnabled ? 'text-green-400' : 'text-dark-400'}`}>
            {tcpProxyEnabled ? 'Enabled' : 'Disabled'}
          </span>
          {tcpProxyEnabled && (
            <span className="text-sm text-dark-400">
              {listenerCount} listeners
            </span>
          )}
        </div>
      </div>

      {!tcpProxyEnabled && (
        <div className="text-sm text-dark-400 mb-4">
          Enable transparent proxy in settings to route traffic based on domain suffixes.
        </div>
      )}

      {tcpProxyEnabled && connections.length === 0 && (
        <div className="text-sm text-dark-400">
          No active DNS mappings. Configure your system DNS to use 127.0.0.1:10053 and
          access services using domain suffixes (e.g., db.svi, api.contabo).
        </div>
      )}

      {connections.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-dark-400 text-left border-b border-dark-700">
                <th className="pb-2 font-medium">Hostname</th>
                <th className="pb-2 font-medium">Tunnel IP</th>
                <th className="pb-2 font-medium">Real IP</th>
                <th className="pb-2 font-medium">Profile</th>
                <th className="pb-2 font-medium">Age</th>
              </tr>
            </thead>
            <tbody className="text-dark-200">
              {connections.map((conn, index) => (
                <tr key={index} className="border-b border-dark-800">
                  <td className="py-2 text-primary-400 font-mono">{conn.hostname}</td>
                  <td className="py-2 font-mono">{conn.tunnelIP}</td>
                  <td className="py-2 font-mono">{conn.realIP}</td>
                  <td className="py-2">{conn.profileId}</td>
                  <td className="py-2 text-dark-400">{conn.age}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {tcpProxyEnabled && (
        <div className="mt-4 p-3 bg-dark-900 rounded text-xs text-dark-400">
          <strong className="text-dark-300">How it works:</strong>
          <ol className="mt-2 ml-4 list-decimal space-y-1">
            <li>Set your system DNS to 127.0.0.1 (one-time setup)</li>
            <li>Access services using domain suffixes (e.g., db.svi, api.contabo)</li>
            <li>DNS proxy resolves the real IP via the tunnel's DNS server</li>
            <li>Returns a unique loopback IP that routes through the correct tunnel</li>
          </ol>
        </div>
      )}
    </div>
  )
}

export default ActiveConnections
