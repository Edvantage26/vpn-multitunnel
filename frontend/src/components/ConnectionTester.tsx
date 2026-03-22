import { useState } from 'react'

interface ConnectionTesterProps {
  profileId: string
  profileName: string
  isConnected: boolean
}

function ConnectionTester({ profileId, profileName, isConnected }: ConnectionTesterProps) {
  const [host, setHost] = useState('')
  const [port, setPort] = useState('')
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<{ success: boolean; message: string } | null>(null)

  const handleTest = async () => {
    if (!host || !port) {
      setResult({ success: false, message: 'Please enter host and port' })
      return
    }

    const portNum = parseInt(port, 10)
    if (isNaN(portNum) || portNum < 1 || portNum > 65535) {
      setResult({ success: false, message: 'Invalid port number' })
      return
    }

    setTesting(true)
    setResult(null)

    try {
      const [success, message] = await window.go.app.App.TestConnection(profileId, host, portNum)
      setResult({ success, message })
    } catch (err) {
      setResult({ success: false, message: String(err) })
    } finally {
      setTesting(false)
    }
  }

  const handleKeyPress = (event: React.KeyboardEvent) => {
    if (event.key === 'Enter' && !testing) {
      handleTest()
    }
  }

  // Common services for quick testing
  const quickTests = [
    { name: 'SSH', port: 22 },
    { name: 'HTTP', port: 80 },
    { name: 'HTTPS', port: 443 },
    { name: 'MySQL', port: 3306 },
    { name: 'PostgreSQL', port: 5432 },
    { name: 'Redis', port: 6379 },
  ]

  return (
    <div className="card p-6">
      <h3 className="text-lg font-semibold text-white mb-4">
        Connection Tester
        <span className="text-sm font-normal text-dark-400 ml-2">via {profileName}</span>
      </h3>

      {!isConnected ? (
        <div className="text-dark-400 text-sm">
          Connect the tunnel first to test connectivity.
        </div>
      ) : (
        <>
          <div className="flex gap-3 mb-4">
            <div className="flex-1">
              <label className="block text-xs text-dark-400 mb-1">Host / IP</label>
              <input
                type="text"
                value={host}
                onChange={(event) => setHost(event.target.value)}
                onKeyPress={handleKeyPress}
                placeholder="e.g., 172.17.0.2 or db.svi"
                className="w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded-lg text-white placeholder-dark-500 focus:border-primary-500 focus:outline-none"
                disabled={testing}
              />
            </div>
            <div className="w-28">
              <label className="block text-xs text-dark-400 mb-1">Port</label>
              <input
                type="number"
                value={port}
                onChange={(event) => setPort(event.target.value)}
                onKeyPress={handleKeyPress}
                placeholder="5432"
                className="w-full px-3 py-2 bg-dark-800 border border-dark-600 rounded-lg text-white placeholder-dark-500 focus:border-primary-500 focus:outline-none"
                disabled={testing}
                min="1"
                max="65535"
              />
            </div>
            <div className="flex items-end">
              <button
                onClick={handleTest}
                disabled={testing || !host || !port}
                className={`px-4 py-2 rounded-lg font-medium transition-colors ${
                  testing || !host || !port
                    ? 'bg-dark-600 text-dark-400 cursor-not-allowed'
                    : 'bg-primary-600 hover:bg-primary-500 text-white'
                }`}
              >
                {testing ? (
                  <span className="flex items-center gap-2">
                    <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
                    </svg>
                    Testing...
                  </span>
                ) : (
                  'Test'
                )}
              </button>
            </div>
          </div>

          {/* Quick port buttons */}
          <div className="flex flex-wrap gap-2 mb-4">
            <span className="text-xs text-dark-400 mr-2 self-center">Quick ports:</span>
            {quickTests.map((qt) => (
              <button
                key={qt.port}
                onClick={() => setPort(qt.port.toString())}
                className="px-2 py-1 text-xs bg-dark-700 hover:bg-dark-600 text-dark-300 rounded transition-colors"
              >
                {qt.name} ({qt.port})
              </button>
            ))}
          </div>

          {/* Result */}
          {result && (
            <div className={`p-3 rounded-lg ${
              result.success
                ? 'bg-green-900/30 border border-green-700/50'
                : 'bg-red-900/30 border border-red-700/50'
            }`}>
              <div className="flex items-center gap-3">
                {result.success ? (
                  <svg className="w-5 h-5 text-green-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                  </svg>
                ) : (
                  <svg className="w-5 h-5 text-red-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                  </svg>
                )}
                <div className="flex-1">
                  <div className={`font-medium ${result.success ? 'text-green-400' : 'text-red-400'}`}>
                    {result.success ? 'Connection successful' : 'Connection failed'}
                  </div>
                  <div className="text-sm text-dark-400">{result.message}</div>
                </div>
              </div>
              {result.success && (
                <div className="mt-2 pt-2 border-t border-dark-700 flex items-center gap-2 text-sm">
                  <svg className="w-4 h-4 text-primary-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
                  </svg>
                  <span className="text-dark-400">Routed via</span>
                  <span className="text-primary-400 font-medium">{profileName}</span>
                  <span className="text-dark-500">tunnel</span>
                </div>
              )}
            </div>
          )}

          {/* Usage hint */}
          <div className="mt-4 text-xs text-dark-500">
            This tests TCP connectivity through the tunnel. Enter the internal IP/hostname and port of the service you want to reach.
          </div>
        </>
      )}
    </div>
  )
}

export default ConnectionTester
