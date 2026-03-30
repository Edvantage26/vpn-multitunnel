import { useState, useEffect, useRef, useCallback } from 'react'
import { HostTestResult, DNSDiagnosticStep } from '../App'
import { getServiceByPort } from '../data/servicePortRegistry'

export interface QuickTestRequest {
  hostname: string
  timestamp: number
}

interface CachedTestState {
  hostname: string
  selectedPort: number | null
  selectedSuffix: string
  testResult: HostTestResult | null
}

interface ConnectionTesterProps {
  profileId: string
  profileName: string
  isConnected: boolean
  domainSuffixes: string[]
  tcpProxyPorts: number[]
  quickTestRequest?: QuickTestRequest | null
}

// Module-level cache so it survives component remounts within the session
const profileTestCache = new Map<string, CachedTestState>()

function ConnectionTester({ profileId, profileName, isConnected, domainSuffixes, tcpProxyPorts, quickTestRequest }: ConnectionTesterProps) {
  const [hostname, setHostname] = useState('')
  const [selectedPort, setSelectedPort] = useState<number | null>(null)
  const [selectedSuffix, setSelectedSuffix] = useState('')
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<HostTestResult | null>(null)
  const previousProfileIdRef = useRef<string>(profileId)

  // Save current state to cache, then restore or reset for new profile
  useEffect(() => {
    const previousProfileId = previousProfileIdRef.current
    if (previousProfileId && previousProfileId !== profileId) {
      // Save outgoing profile state
      profileTestCache.set(previousProfileId, {
        hostname,
        selectedPort,
        selectedSuffix,
        testResult,
      })
    }
    previousProfileIdRef.current = profileId

    // Restore cached state or reset
    const cachedState = profileTestCache.get(profileId)
    if (cachedState) {
      setHostname(cachedState.hostname)
      setSelectedPort(cachedState.selectedPort)
      setSelectedSuffix(cachedState.selectedSuffix)
      setTestResult(cachedState.testResult)
    } else {
      setHostname('')
      setTestResult(null)
      setSelectedSuffix(domainSuffixes.length === 1 ? domainSuffixes[0] : '')
      setSelectedPort(null)
    }
  }, [profileId])

  const buildFullHostname = (): string => {
    const trimmedHost = hostname.trim()
    if (!trimmedHost) return ''
    if (selectedSuffix) {
      return `${trimmedHost}.${selectedSuffix}`
    }
    return trimmedHost
  }

  const runTest = useCallback(async (testHostname: string, testPort: number) => {
    setTesting(true)
    setTestResult(null)

    try {
      const result = await window.go.app.App.TestHost(testHostname, testPort, profileId, true)
      setTestResult(result)
    } catch (error) {
      setTestResult({
        hostname: testHostname,
        profileId: profileId,
        profileName: profileName,
        dnsResolved: false,
        realIP: '',
        loopbackIP: '',
        dnsServer: '',
        dnsRule: '',
        dnsError: String(error),
        usedSystemDNS: true,
        tcpConnected: false,
        tcpPort: testPort,
        tcpLatencyMs: 0,
        tcpError: '',
      })
    } finally {
      setTesting(false)
    }
  }, [profileId, profileName])

  const handleTest = async () => {
    const fullHostname = buildFullHostname()
    if (!fullHostname || !selectedPort) {
      return
    }
    runTest(fullHostname, selectedPort)
  }

  // Handle quick test requests from host rows
  const lastQuickTestTimestampRef = useRef<number>(0)
  useEffect(() => {
    if (!quickTestRequest || quickTestRequest.timestamp <= lastQuickTestTimestampRef.current) return
    lastQuickTestTimestampRef.current = quickTestRequest.timestamp

    const requestedHostname = quickTestRequest.hostname
    // Parse hostname: try to match a domain suffix and extract the base
    let parsedBase = requestedHostname
    let parsedSuffix = ''
    for (const suffix of domainSuffixes) {
      if (requestedHostname.endsWith(`.${suffix}`)) {
        parsedBase = requestedHostname.slice(0, -(suffix.length + 1))
        parsedSuffix = suffix
        break
      }
    }

    // If no suffix matched and there's exactly one available, use it
    if (!parsedSuffix && domainSuffixes.length === 1) {
      parsedSuffix = domainSuffixes[0]
    }

    setHostname(parsedBase)
    setSelectedSuffix(parsedSuffix)

    // Pick first available port if none selected
    const portToUse = selectedPort ?? (tcpProxyPorts.length > 0 ? Math.abs(tcpProxyPorts[0]) : null)
    if (portToUse) {
      setSelectedPort(portToUse)
    }

    // Build full hostname and run test
    const fullHost = parsedSuffix ? `${parsedBase}.${parsedSuffix}` : parsedBase
    if (fullHost && portToUse) {
      runTest(fullHost, portToUse)
    }
  }, [quickTestRequest])

  const handleHostKeyPress = (event: React.KeyboardEvent) => {
    if (event.key === 'Enter' && !testing && hostname.trim() && selectedPort && selectedSuffix) {
      handleTest()
    }
  }

  // Detect if hostname looks like an IP address
  const looksLikeIP = /^(\d{1,3}\.){1,3}\d{1,3}$/.test(hostname.trim())

  const hasSuffix = domainSuffixes.length > 0 && selectedSuffix !== ''
  const isTestReady = hostname.trim() && selectedPort && hasSuffix && !testing && !looksLikeIP
  const fullHostname = buildFullHostname()

  // Determine overall result status
  const getResultStatus = (): 'success' | 'dns_fail' | 'tcp_fail' | null => {
    if (!testResult) return null
    if (!testResult.dnsResolved && testResult.dnsError) return 'dns_fail'
    if (testResult.dnsResolved && !testResult.tcpConnected) return 'tcp_fail'
    if (testResult.tcpConnected) return 'success'
    return 'dns_fail'
  }

  const resultStatus = getResultStatus()

  // Build port options from tcpProxyPorts with service labels
  // Negative port = custom (user explicitly chose custom even if a service exists for that port)
  const portOptions = tcpProxyPorts.map(portValue => {
    const absolutePort = Math.abs(portValue)
    const isCustom = portValue < 0
    const serviceEntry = !isCustom ? getServiceByPort(absolutePort) : null
    return {
      port: absolutePort,
      label: serviceEntry ? `${serviceEntry.service} (${absolutePort})` : `Custom (${absolutePort})`,
    }
  })

  return (
    <div className="card p-4">
      <h3 className="text-sm font-semibold text-dark-300 uppercase tracking-wider mb-3 flex items-center gap-2">
        <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
        </svg>
        Connection Tester
        <span className="text-xs font-normal text-dark-500 normal-case tracking-normal">via {profileName}</span>
      </h3>

      {!isConnected ? (
        <div className="text-dark-500 text-xs italic">
          Connect the tunnel first to test connectivity.
        </div>
      ) : domainSuffixes.length === 0 ? (
        <div className="text-dark-500 text-xs italic">
          Configure domain suffixes first to test connectivity.
        </div>
      ) : tcpProxyPorts.length === 0 ? (
        <div className="text-dark-500 text-xs italic">
          Configure TCP proxy ports first to test connectivity.
        </div>
      ) : (
        <>
          {/* Input row: Host + Suffix + Port + Test button */}
          <div className="flex gap-2 mb-3">
            {/* Hostname input */}
            <div className="flex-1 min-w-0">
              <label className="block text-xs text-dark-400 mb-1">Host</label>
              <input
                type="text"
                value={hostname}
                onChange={(event) => setHostname(event.target.value)}
                onKeyPress={handleHostKeyPress}
                placeholder="e.g., db, myserver"
                className={`w-full input py-1.5 text-sm ${looksLikeIP ? 'border-red-500 focus:border-red-500' : ''}`}
                disabled={testing}
              />
            </div>

            {/* Suffix selector - always dropdown, pre-selected if only one */}
            {domainSuffixes.length > 0 && (
              <div className="w-32">
                <label className="block text-xs text-dark-400 mb-1">Suffix</label>
                <select
                  value={selectedSuffix}
                  onChange={(event) => setSelectedSuffix(event.target.value)}
                  className="w-full input py-1.5 text-sm cursor-pointer"
                  disabled={testing}
                >
                  {domainSuffixes.length > 1 && <option value="">Select suffix...</option>}
                  {domainSuffixes.map((suffix) => (
                    <option key={suffix} value={suffix}>.{suffix}</option>
                  ))}
                </select>
              </div>
            )}

            {/* Port selector - only from configured TCP proxy ports */}
            <div className="w-44">
              <label className="block text-xs text-dark-400 mb-1">Port</label>
              <select
                value={selectedPort ?? ''}
                onChange={(event) => setSelectedPort(event.target.value ? parseInt(event.target.value, 10) : null)}
                className="w-full input py-1.5 text-sm cursor-pointer"
                disabled={testing}
              >
                <option value="">Select port...</option>
                {portOptions.map((option) => (
                  <option key={option.port} value={option.port}>{option.label}</option>
                ))}
              </select>
            </div>

            {/* Test button */}
            <div className="flex items-end">
              <button
                onClick={handleTest}
                disabled={!isTestReady}
                className={`px-4 py-1.5 rounded-lg text-sm font-medium transition-colors ${
                  !isTestReady
                    ? 'bg-dark-600 text-dark-400 cursor-not-allowed'
                    : 'bg-primary-600 hover:bg-primary-500 text-white'
                }`}
              >
                {testing ? (
                  <span className="flex items-center gap-1.5">
                    <svg className="animate-spin h-3.5 w-3.5" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
                    </svg>
                    Testing
                  </span>
                ) : 'Test'}
              </button>
            </div>
          </div>

          {/* IP address error */}
          {looksLikeIP && (
            <div className="text-xs text-red-400 mb-2">
              IP addresses are not allowed. Enter a hostname instead (e.g., db, myserver).
            </div>
          )}

          {/* Constructed URL preview */}
          {hostname.trim() && !looksLikeIP && (
            <div className="text-xs text-dark-500 mb-3">
              <span className="text-dark-300 font-mono">{fullHostname}{selectedPort ? `:${selectedPort}` : ''}</span>
            </div>
          )}

          {/* Results */}
          {testResult && (
            <div className={`rounded-lg border ${
              resultStatus === 'success'
                ? 'bg-green-900/20 border-green-700/40'
                : resultStatus === 'tcp_fail'
                  ? 'bg-yellow-900/20 border-yellow-700/40'
                  : 'bg-red-900/20 border-red-700/40'
            }`}>
              {/* Header */}
              <div className="flex items-center gap-2 px-3 py-2 border-b border-dark-700/50">
                {resultStatus === 'success' ? (
                  <svg className="w-4 h-4 text-green-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                  </svg>
                ) : resultStatus === 'tcp_fail' ? (
                  <svg className="w-4 h-4 text-yellow-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4.832c-.77-.833-2.694-.833-3.464 0L3.34 16.5c-.77.833.192 2.5 1.732 2.5z" />
                  </svg>
                ) : (
                  <svg className="w-4 h-4 text-red-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                  </svg>
                )}
                <span className={`text-sm font-medium ${
                  resultStatus === 'success' ? 'text-green-400'
                    : resultStatus === 'tcp_fail' ? 'text-yellow-400'
                      : 'text-red-400'
                }`}>
                  {resultStatus === 'success' && 'Connection successful'}
                  {resultStatus === 'tcp_fail' && 'DNS resolved but TCP connection failed'}
                  {resultStatus === 'dns_fail' && 'DNS resolution failed'}
                </span>
              </div>

              {/* Detail rows */}
              <div className="px-3 py-2 space-y-1.5 text-xs">
                {/* DNS Resolution step */}
                <div className="flex items-start gap-2">
                  <span className={`mt-0.5 w-3.5 h-3.5 flex-shrink-0 flex items-center justify-center rounded-full ${
                    testResult.dnsResolved ? 'bg-green-900/50 text-green-400' : 'bg-red-900/50 text-red-400'
                  }`}>
                    {testResult.dnsResolved ? (
                      <svg className="w-2.5 h-2.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                      </svg>
                    ) : (
                      <svg className="w-2.5 h-2.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    )}
                  </span>
                  <div className="flex-1 min-w-0">
                    <span className="text-dark-300">DNS Resolution</span>
                    {testResult.dnsResolved ? (
                      <div className="text-dark-400 mt-0.5">
                        <span className="font-mono text-primary-400">{testResult.hostname}</span>
                        <span className="text-dark-500"> → </span>
                        <span className="font-mono text-green-400">{testResult.realIP}</span>
                        {testResult.loopbackIP && (
                          <>
                            <span className="text-dark-500"> (loopback: </span>
                            <span className="font-mono text-blue-400">{testResult.loopbackIP}</span>
                            <span className="text-dark-500">)</span>
                          </>
                        )}
                      </div>
                    ) : (
                      <div className="text-red-400/80 mt-0.5">{testResult.dnsError}</div>
                    )}
                    {testResult.dnsRule && (
                      <div className="text-dark-500 mt-0.5">
                        Rule: <span className="text-dark-400">{testResult.dnsRule}</span>
                        {testResult.dnsServer && (
                          <> via <span className="font-mono text-dark-400">{testResult.dnsServer}</span></>
                        )}
                      </div>
                    )}
                  </div>
                </div>

                {/* TCP Connection step */}
                {testResult.dnsResolved && (
                  <div className="flex items-start gap-2">
                    <span className={`mt-0.5 w-3.5 h-3.5 flex-shrink-0 flex items-center justify-center rounded-full ${
                      testResult.tcpConnected ? 'bg-green-900/50 text-green-400' : 'bg-red-900/50 text-red-400'
                    }`}>
                      {testResult.tcpConnected ? (
                        <svg className="w-2.5 h-2.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                        </svg>
                      ) : (
                        <svg className="w-2.5 h-2.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M6 18L18 6M6 6l12 12" />
                        </svg>
                      )}
                    </span>
                    <div className="flex-1 min-w-0">
                      <span className="text-dark-300">TCP Connection</span>
                      <span className="text-dark-500 ml-1">→ {testResult.realIP}:{testResult.tcpPort}</span>
                      {testResult.tcpConnected ? (
                        <div className="text-dark-400 mt-0.5">
                          Connected in <span className="text-green-400 font-medium">{testResult.tcpLatencyMs}ms</span>
                          <span className="text-dark-500 ml-2">via {profileName}</span>
                        </div>
                      ) : (
                        <div className="text-red-400/80 mt-0.5">{testResult.tcpError}</div>
                      )}
                    </div>
                  </div>
                )}

                {/* Full diagnostics — shown on any failure (DNS or TCP) when diagnostics are available */}
                {(resultStatus === 'dns_fail' || resultStatus === 'tcp_fail') && testResult.dnsDiagnostics && (
                  <div className="mt-2 pt-2 border-t border-dark-700/50">
                    {/* Root cause banner */}
                    <div className="bg-red-900/30 border border-red-700/40 rounded px-3 py-2 mb-3">
                      <div className="text-xs font-semibold text-red-300 mb-0.5">Root Cause</div>
                      <div className="text-xs text-red-200">{testResult.dnsDiagnostics.rootCause}</div>
                    </div>

                    {/* Diagnostic chain steps */}
                    <div className="text-xs font-semibold text-dark-300 mb-2">Diagnostic Chain</div>
                    <div className="space-y-1.5 mb-3">
                      {testResult.dnsDiagnostics.steps.map((step: DNSDiagnosticStep, stepIndex: number) => (
                        <div key={stepIndex} className={`rounded border px-2.5 py-1.5 ${
                          step.status === 'ok' ? 'bg-green-900/10 border-green-800/30' :
                          step.status === 'warn' ? 'bg-yellow-900/10 border-yellow-800/30' :
                          'bg-red-900/10 border-red-800/30'
                        }`}>
                          <div className="flex items-center gap-1.5">
                            {step.status === 'ok' ? (
                              <svg className="w-3 h-3 text-green-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                              </svg>
                            ) : step.status === 'warn' ? (
                              <svg className="w-3 h-3 text-yellow-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M12 9v2m0 4h.01" />
                              </svg>
                            ) : (
                              <svg className="w-3 h-3 text-red-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M6 18L18 6M6 6l12 12" />
                              </svg>
                            )}
                            <span className={`font-medium ${
                              step.status === 'ok' ? 'text-green-400' :
                              step.status === 'warn' ? 'text-yellow-400' : 'text-red-400'
                            }`}>{step.name}</span>
                          </div>
                          <div className="text-dark-400 mt-0.5 ml-[18px]">{step.detail}</div>
                          {step.fix && (
                            <div className="mt-1 ml-[18px] text-primary-400 bg-primary-900/20 rounded px-2 py-1">
                              <span className="font-medium">Fix:</span> {step.fix}
                            </div>
                          )}
                        </div>
                      ))}
                    </div>

                    {/* System state snapshot */}
                    <details className="group">
                      <summary className="text-xs font-semibold text-dark-300 cursor-pointer hover:text-dark-200 select-none">
                        System State Snapshot
                        <span className="ml-1 text-dark-500 font-normal">click to expand</span>
                      </summary>
                      <div className="mt-2 bg-dark-800/50 rounded border border-dark-700/50 overflow-hidden">
                        <table className="w-full text-xs">
                          <tbody className="divide-y divide-dark-700/30">
                            <tr><td className="px-2.5 py-1 text-dark-500 w-40">Active Interface</td><td className="px-2.5 py-1 font-mono text-dark-300">{testResult.dnsDiagnostics.activeInterface || 'unknown'}</td></tr>
                            <tr><td className="px-2.5 py-1 text-dark-500">Current System DNS</td><td className="px-2.5 py-1 font-mono text-dark-300">{testResult.dnsDiagnostics.currentSystemDNS?.join(', ') || 'unknown'}</td></tr>
                            <tr><td className="px-2.5 py-1 text-dark-500">Expected DNS Address</td><td className="px-2.5 py-1 font-mono text-dark-300">{testResult.dnsDiagnostics.expectedDnsAddress}</td></tr>
                            <tr>
                              <td className="px-2.5 py-1 text-dark-500">System DNS → Proxy</td>
                              <td className="px-2.5 py-1">
                                <span className={`font-mono ${testResult.dnsDiagnostics.systemDnsConfigured ? 'text-green-400' : 'text-red-400'}`}>
                                  {testResult.dnsDiagnostics.systemDnsConfigured ? 'YES' : 'NO'}
                                </span>
                              </td>
                            </tr>
                            <tr><td className="px-2.5 py-1 text-dark-500">DNS Proxy</td><td className="px-2.5 py-1 font-mono text-dark-300">{testResult.dnsDiagnostics.dnsProxyEnabled ? `Enabled (port ${testResult.dnsDiagnostics.dnsProxyListenPort})` : 'Disabled'}</td></tr>
                            <tr>
                              <td className="px-2.5 py-1 text-dark-500">TCP Proxy</td>
                              <td className="px-2.5 py-1">
                                <span className={`font-mono ${testResult.dnsDiagnostics.tcpProxyEnabled ? 'text-green-400' : 'text-red-400'}`}>
                                  {testResult.dnsDiagnostics.tcpProxyEnabled ? `Enabled (${testResult.dnsDiagnostics.tcpProxyListenerCount} listeners)` : 'DISABLED'}
                                </span>
                              </td>
                            </tr>
                            <tr>
                              <td className="px-2.5 py-1 text-dark-500">Profile Tunnel IP</td>
                              <td className="px-2.5 py-1">
                                <span className={`font-mono ${testResult.dnsDiagnostics.profileHasTunnelIP ? 'text-green-400' : 'text-red-400'}`}>
                                  {testResult.dnsDiagnostics.profileHasTunnelIP ? testResult.dnsDiagnostics.profileTunnelIP : 'NOT ASSIGNED'}
                                </span>
                              </td>
                            </tr>
                            {testResult.dnsDiagnostics.tcpProxyTunnelIPs && Object.keys(testResult.dnsDiagnostics.tcpProxyTunnelIPs).length > 0 && (
                              <tr>
                                <td className="px-2.5 py-1 text-dark-500">All Tunnel IPs</td>
                                <td className="px-2.5 py-1 font-mono text-dark-300">
                                  {Object.entries(testResult.dnsDiagnostics.tcpProxyTunnelIPs).map(([profileId, tunnelIP]) => (
                                    <div key={profileId}>{tunnelIP} → {profileId.substring(0, 8)}...</div>
                                  ))}
                                </td>
                              </tr>
                            )}
                            {testResult.dnsDiagnostics.resolvedAddress && (
                              <tr>
                                <td className="px-2.5 py-1 text-dark-500">DNS Returned</td>
                                <td className="px-2.5 py-1">
                                  <span className={`font-mono ${testResult.dnsDiagnostics.resolvedToLoopback ? 'text-green-400' : 'text-yellow-400'}`}>
                                    {testResult.dnsDiagnostics.resolvedAddress}
                                    {testResult.dnsDiagnostics.resolvedToLoopback ? ' (loopback)' : ' (real IP — no transparent proxy)'}
                                  </span>
                                </td>
                              </tr>
                            )}
                            <tr>
                              <td className="px-2.5 py-1 text-dark-500">Dnscache Service</td>
                              <td className="px-2.5 py-1">
                                <span className={`font-mono ${testResult.dnsDiagnostics.dnsClientRunning ? 'text-yellow-400' : 'text-green-400'}`}>
                                  {testResult.dnsDiagnostics.dnsClientRunning ? 'RUNNING (may interfere)' : 'STOPPED'}
                                </span>
                              </td>
                            </tr>
                            <tr>
                              <td className="px-2.5 py-1 text-dark-500">Service Connected</td>
                              <td className="px-2.5 py-1">
                                <span className={`font-mono ${testResult.dnsDiagnostics.serviceConnected ? 'text-green-400' : 'text-yellow-400'}`}>
                                  {testResult.dnsDiagnostics.serviceConnected ? 'YES' : 'NO'}
                                </span>
                              </td>
                            </tr>
                            {testResult.dnsDiagnostics.hasMatchingRule && (
                              <>
                                <tr><td className="px-2.5 py-1 text-dark-500">Matched Rule</td><td className="px-2.5 py-1 font-mono text-dark-300">{testResult.dnsDiagnostics.matchedRuleSuffix} → {testResult.dnsDiagnostics.matchedRuleProfile}</td></tr>
                                <tr><td className="px-2.5 py-1 text-dark-500">Rule DNS Server</td><td className="px-2.5 py-1 font-mono text-dark-300">{testResult.dnsDiagnostics.matchedRuleDns}</td></tr>
                                <tr>
                                  <td className="px-2.5 py-1 text-dark-500">Tunnel Connected</td>
                                  <td className="px-2.5 py-1">
                                    <span className={`font-mono ${testResult.dnsDiagnostics.tunnelConnected ? 'text-green-400' : 'text-red-400'}`}>
                                      {testResult.dnsDiagnostics.tunnelConnected ? 'YES' : 'NO'}
                                    </span>
                                  </td>
                                </tr>
                              </>
                            )}
                            {testResult.dnsDiagnostics.proxyDirectResult && (
                              <tr>
                                <td className="px-2.5 py-1 text-dark-500">Proxy Direct Query</td>
                                <td className="px-2.5 py-1">
                                  <span className={`font-mono ${testResult.dnsDiagnostics.proxyDirectOk ? 'text-green-400' : 'text-red-400'}`}>
                                    {testResult.dnsDiagnostics.proxyDirectResult}
                                  </span>
                                </td>
                              </tr>
                            )}
                            {testResult.dnsDiagnostics.directTunnelDnsResult && (
                              <tr>
                                <td className="px-2.5 py-1 text-dark-500">Direct Tunnel DNS</td>
                                <td className="px-2.5 py-1">
                                  <span className={`font-mono ${testResult.dnsDiagnostics.directTunnelDnsOk ? 'text-green-400' : 'text-red-400'}`}>
                                    {testResult.dnsDiagnostics.directTunnelDnsResult}
                                  </span>
                                </td>
                              </tr>
                            )}
                          </tbody>
                        </table>
                      </div>
                    </details>
                  </div>
                )}

                {/* DNS fail without diagnostics (fallback) */}
                {resultStatus === 'dns_fail' && !testResult.dnsDiagnostics && (
                  <div className="mt-2 pt-2 border-t border-dark-700/50 text-dark-400">
                    <span className="font-medium text-dark-300">DNS resolution failed:</span>
                    <div className="text-red-400/80 mt-1">{testResult.dnsError}</div>
                  </div>
                )}

                {/* TCP Failure without diagnostics (fallback — e.g. connection refused, not a proxy issue) */}
                {resultStatus === 'tcp_fail' && !testResult.dnsDiagnostics && (
                  <div className="mt-2 pt-2 border-t border-dark-700/50 text-dark-400">
                    <span className="font-medium text-dark-300">Possible causes:</span>
                    <ul className="mt-1 space-y-0.5 list-disc list-inside text-dark-500">
                      {testResult.tcpError?.includes('refused') && (
                        <li>Port {testResult.tcpPort} is not open on {testResult.realIP}. The service may not be running.</li>
                      )}
                      {testResult.tcpError?.includes('timeout') && (
                        <li>Connection timed out. A firewall may be blocking port {testResult.tcpPort}.</li>
                      )}
                      {testResult.tcpError?.includes('unreachable') && (
                        <li>Host {testResult.realIP} is unreachable. Check network routing through the tunnel.</li>
                      )}
                      <li>Verify port {testResult.tcpPort} is open and the service is running on the remote host.</li>
                    </ul>
                  </div>
                )}
              </div>
            </div>
          )}
        </>
      )}
    </div>
  )
}

export default ConnectionTester
