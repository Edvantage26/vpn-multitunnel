import { useState, useEffect } from 'react'
import type { Profile } from '../App'

interface ImportWizardProps {
  onClose: () => void
  onComplete: (profile_id: string) => void
}

type WizardStep = 1 | 2 | 3 | 4

const STEP_LABELS = ['Import', 'DNS Suffix', 'Test', 'Done']

function StepIndicator({ current_step }: { current_step: WizardStep }) {
  return (
    <div className="flex items-center justify-center mb-8">
      {STEP_LABELS.map((label, step_index) => {
        const step_number = (step_index + 1) as WizardStep
        const is_completed = step_number < current_step
        const is_current = step_number === current_step
        const is_future = step_number > current_step

        return (
          <div key={label} className="flex items-center">
            <div className="flex flex-col items-center">
              <div
                className={`w-8 h-8 rounded-full flex items-center justify-center text-sm font-semibold transition-colors ${
                  is_completed
                    ? 'bg-primary-600 text-white'
                    : is_current
                    ? 'bg-primary-600 text-white ring-2 ring-primary-400 ring-offset-2 ring-offset-dark-800'
                    : 'bg-dark-700 text-dark-400'
                }`}
              >
                {is_completed ? (
                  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                  </svg>
                ) : (
                  step_number
                )}
              </div>
              <span
                className={`text-xs mt-1 ${
                  is_future ? 'text-dark-500' : 'text-dark-300'
                }`}
              >
                {label}
              </span>
            </div>
            {step_index < STEP_LABELS.length - 1 && (
              <div
                className={`w-16 h-0.5 mx-2 mb-5 ${
                  step_number < current_step ? 'bg-primary-600' : 'bg-dark-700'
                }`}
              />
            )}
          </div>
        )
      })}
    </div>
  )
}

function ImportWizard({ onClose, onComplete }: ImportWizardProps) {
  // Step tracking
  const [current_step, setCurrentStep] = useState<WizardStep>(1)

  // Step 1 state
  const [imported_profile, setImportedProfile] = useState<Profile | null>(null)
  const [profile_display_name, setProfileDisplayName] = useState('')
  const [is_importing, setIsImporting] = useState(false)
  const [import_error, setImportError] = useState('')

  // Step 2 state
  const [dns_suffix_input, setDnsSuffixInput] = useState('')
  const [dns_suffix_error, setDnsSuffixError] = useState('')
  const [is_saving_suffix, setIsSavingSuffix] = useState(false)

  // Step 3 state
  const [is_connecting, setIsConnecting] = useState(false)
  const [is_connected, setIsConnected] = useState(false)
  const [connection_error, setConnectionError] = useState('')
  const [test_url_input, setTestUrlInput] = useState('')
  const [test_port_input, setTestPortInput] = useState('443')
  const [is_testing, setIsTesting] = useState(false)
  const [test_result, setTestResult] = useState<{ success: boolean; message: string } | null>(null)

  const has_dns_server = imported_profile?.dns?.server ? true : false

  // Auto-connect when entering step 3
  useEffect(() => {
    if (current_step === 3 && imported_profile && !is_connected && !is_connecting && !connection_error) {
      handleConnect()
    }
  }, [current_step])

  const handleBrowseAndImport = async () => {
    setIsImporting(true)
    setImportError('')
    try {
      const profile = await window.go.app.App.ImportConfig()
      setImportedProfile(profile)
      setProfileDisplayName(profile.name)
    } catch (err) {
      if (String(err) !== 'no file selected') {
        setImportError(String(err))
      }
    } finally {
      setIsImporting(false)
    }
  }

  const handleStep1Next = async () => {
    if (!imported_profile) return
    // Update profile name if changed
    if (profile_display_name !== imported_profile.name) {
      try {
        const updated_profile = { ...imported_profile, name: profile_display_name }
        await window.go.app.App.UpdateProfile(updated_profile)
        setImportedProfile(updated_profile)
      } catch (err) {
        setImportError(`Failed to update name: ${err}`)
        return
      }
    }
    setCurrentStep(2)
  }

  const validateSuffix = (suffix: string): string => {
    if (!suffix.trim()) return 'DNS suffix is required'
    if (!suffix.startsWith('.')) return 'Suffix must start with a dot (e.g., .internal)'
    if (suffix.length < 3) return 'Suffix must be at least 3 characters (e.g., .internal)'
    if (/\s/.test(suffix)) return 'Suffix must not contain spaces'
    if (!/^\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$/.test(suffix)) {
      return 'Invalid suffix format (use letters, numbers, hyphens only)'
    }
    return ''
  }

  const handleStep2Next = async () => {
    if (!imported_profile) return
    const validation_error = validateSuffix(dns_suffix_input)
    if (validation_error) {
      setDnsSuffixError(validation_error)
      return
    }
    setDnsSuffixError('')
    setIsSavingSuffix(true)
    try {
      const updated_profile: Profile = {
        ...imported_profile,
        name: profile_display_name,
        dns: {
          ...imported_profile.dns,
          domains: [dns_suffix_input],
        },
      }
      await window.go.app.App.UpdateProfile(updated_profile)
      setImportedProfile(updated_profile)
      setCurrentStep(3)
    } catch (err) {
      setDnsSuffixError(`Failed to save: ${err}`)
    } finally {
      setIsSavingSuffix(false)
    }
  }

  const handleConnect = async () => {
    if (!imported_profile) return
    setIsConnecting(true)
    setConnectionError('')
    try {
      await window.go.app.App.Connect(imported_profile.id)
      setIsConnected(true)
    } catch (err) {
      setConnectionError(String(err))
    } finally {
      setIsConnecting(false)
    }
  }

  const handleTestUrl = async () => {
    if (!imported_profile || !test_url_input.trim()) return
    // Strip protocol and path to get hostname
    let hostname = test_url_input.trim()
    hostname = hostname.replace(/^https?:\/\//, '')
    hostname = hostname.split('/')[0]
    hostname = hostname.split(':')[0]

    const port_number = parseInt(test_port_input, 10) || 443

    setIsTesting(true)
    setTestResult(null)
    try {
      const [success, message] = await window.go.app.App.TestConnection(imported_profile.id, hostname, port_number)
      setTestResult({ success, message })
    } catch (err) {
      setTestResult({ success: false, message: String(err) })
    } finally {
      setIsTesting(false)
    }
  }

  const handleStep3Back = async () => {
    if (is_connected && imported_profile) {
      try {
        await window.go.app.App.Disconnect(imported_profile.id)
      } catch (_err) {
        // Best effort disconnect
      }
      setIsConnected(false)
    }
    setConnectionError('')
    setTestResult(null)
    setTestUrlInput('')
    setCurrentStep(2)
  }

  const handleFinish = () => {
    setCurrentStep(4)
  }

  const handleCloseWizard = async () => {
    if (current_step === 4) {
      // Step 4: profile is complete, just close
      onComplete(imported_profile!.id)
      return
    }
    if (imported_profile) {
      // Profile was created, ask if they want to discard
      if (!confirm('Discard imported profile? The imported configuration will be deleted.')) {
        return
      }
      // Disconnect if connected
      if (is_connected) {
        try {
          await window.go.app.App.Disconnect(imported_profile.id)
        } catch (_err) { /* best effort */ }
      }
      // Delete the profile
      try {
        await window.go.app.App.DeleteProfile(imported_profile.id)
      } catch (_err) { /* best effort */ }
    }
    onClose()
  }

  const handleTestKeyPress = (key_event: React.KeyboardEvent) => {
    if (key_event.key === 'Enter' && !is_testing && test_url_input.trim()) {
      handleTestUrl()
    }
  }

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="card w-full max-w-2xl mx-4 p-6 max-h-[90vh] overflow-y-auto">
        {/* Header */}
        <div className="flex items-center justify-between mb-2">
          <h2 className="text-xl font-bold text-white">Import Tunnel</h2>
          <button
            onClick={handleCloseWizard}
            className="text-dark-400 hover:text-dark-200"
          >
            <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
                d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* Step Indicator */}
        <StepIndicator current_step={current_step} />

        {/* Step Content */}
        {current_step === 1 && (
          <div className="space-y-4">
            <div className="bg-dark-900 rounded-lg p-4">
              <h3 className="text-sm font-medium text-dark-300 mb-2">Import WireGuard Configuration</h3>
              <p className="text-xs text-dark-400 mb-4">
                Select a WireGuard .conf file to import. The configuration will be parsed and validated.
              </p>

              {!imported_profile ? (
                <div className="flex flex-col items-center py-6">
                  <svg className="w-12 h-12 text-dark-500 mb-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5}
                      d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12" />
                  </svg>
                  <button
                    onClick={handleBrowseAndImport}
                    disabled={is_importing}
                    className="btn btn-primary"
                  >
                    {is_importing ? (
                      <span className="flex items-center gap-2">
                        <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24">
                          <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                          <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
                        </svg>
                        Importing...
                      </span>
                    ) : (
                      'Browse & Import'
                    )}
                  </button>
                </div>
              ) : (
                <div className="space-y-3">
                  {/* Imported file info */}
                  <div className="flex items-center gap-2 p-2 bg-dark-700 rounded-lg">
                    <svg className="w-5 h-5 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                    </svg>
                    <span className="text-sm text-dark-200">{imported_profile.configFile}</span>
                    <span className="text-xs text-green-400 ml-auto">Imported</span>
                  </div>

                  {/* Editable name */}
                  <div>
                    <label className="block text-sm font-medium text-dark-300 mb-1">
                      Connection Name
                    </label>
                    <input
                      type="text"
                      value={profile_display_name}
                      onChange={(event) => setProfileDisplayName(event.target.value)}
                      className="w-full input"
                      placeholder="Enter a name for this connection"
                    />
                    <p className="text-xs text-dark-500 mt-1">
                      Name derived from filename. Change it to something descriptive.
                    </p>
                  </div>
                </div>
              )}

              {import_error && (
                <div className="mt-3 p-3 bg-red-900/30 border border-red-700/50 rounded-lg text-sm text-red-400">
                  {import_error}
                </div>
              )}
            </div>

            {/* Footer */}
            <div className="flex justify-end gap-3 mt-6">
              <button onClick={handleCloseWizard} className="btn btn-secondary">
                Cancel
              </button>
              <button
                onClick={handleStep1Next}
                disabled={!imported_profile || !profile_display_name.trim()}
                className="btn btn-primary"
              >
                Next
              </button>
            </div>
          </div>
        )}

        {current_step === 2 && (
          <div className="space-y-4">
            <div className="bg-dark-900 rounded-lg p-4">
              <h3 className="text-sm font-medium text-dark-300 mb-2">DNS Suffix</h3>
              <p className="text-xs text-dark-400 mb-4">
                Enter a domain suffix for routing URLs through this VPN tunnel. Any DNS query ending
                with this suffix will be resolved through this connection.
              </p>

              <div>
                <label className="block text-sm font-medium text-dark-300 mb-1">
                  Domain Suffix <span className="text-red-400">*</span>
                </label>
                <input
                  type="text"
                  value={dns_suffix_input}
                  onChange={(event) => {
                    setDnsSuffixInput(event.target.value)
                    setDnsSuffixError('')
                  }}
                  className="w-full input"
                  placeholder=".internal"
                />
                {dns_suffix_error && (
                  <p className="text-xs text-red-400 mt-1">{dns_suffix_error}</p>
                )}
                <p className="text-xs text-dark-500 mt-1">
                  Example: <code className="text-dark-400">.svi</code>, <code className="text-dark-400">.internal</code>, <code className="text-dark-400">.corp</code>
                </p>
              </div>

              {/* DNS server detection info */}
              <div className="mt-4">
                {has_dns_server ? (
                  <div className="p-3 bg-green-900/20 border border-green-700/30 rounded-lg">
                    <div className="flex items-center gap-2">
                      <svg className="w-5 h-5 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                      </svg>
                      <span className="text-sm text-green-300">
                        DNS server detected: <code className="font-mono text-green-200">{imported_profile?.dns.server}</code>
                      </span>
                    </div>
                    <p className="text-xs text-dark-400 mt-1 ml-7">
                      DNS queries for <code className="text-dark-300">*{dns_suffix_input || '.suffix'}</code> will
                      be routed through this DNS server via the tunnel.
                    </p>
                  </div>
                ) : (
                  <div className="p-3 bg-amber-900/20 border border-amber-700/30 rounded-lg">
                    <div className="flex items-center gap-2">
                      <svg className="w-5 h-5 text-amber-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
                          d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
                      </svg>
                      <span className="text-sm text-amber-300">No DNS server found in configuration</span>
                    </div>
                    <p className="text-xs text-dark-400 mt-1 ml-7">
                      You will need to add static host mappings (hostname → internal IP) in the profile
                      settings after setup to route traffic through this tunnel.
                    </p>
                  </div>
                )}
              </div>
            </div>

            {/* Footer */}
            <div className="flex justify-between mt-6">
              <button
                onClick={() => setCurrentStep(1)}
                className="btn btn-secondary"
              >
                Back
              </button>
              <button
                onClick={handleStep2Next}
                disabled={!dns_suffix_input.trim() || is_saving_suffix}
                className="btn btn-primary"
              >
                {is_saving_suffix ? (
                  <span className="flex items-center gap-2">
                    <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
                    </svg>
                    Saving...
                  </span>
                ) : (
                  'Next'
                )}
              </button>
            </div>
          </div>
        )}

        {current_step === 3 && (
          <div className="space-y-4">
            <div className="bg-dark-900 rounded-lg p-4">
              <h3 className="text-sm font-medium text-dark-300 mb-2">Test Connection</h3>

              {/* Connection status */}
              {is_connecting && (
                <div className="flex items-center gap-3 p-3 bg-dark-700 rounded-lg">
                  <svg className="animate-spin h-5 w-5 text-primary-400" viewBox="0 0 24 24">
                    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
                  </svg>
                  <span className="text-dark-300">Connecting to VPN...</span>
                </div>
              )}

              {connection_error && (
                <div className="space-y-3">
                  <div className="p-3 bg-red-900/30 border border-red-700/50 rounded-lg">
                    <div className="flex items-center gap-2">
                      <svg className="w-5 h-5 text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                      </svg>
                      <span className="text-sm text-red-400">Connection failed: {connection_error}</span>
                    </div>
                  </div>
                  <button onClick={handleConnect} className="btn btn-primary">
                    Retry Connection
                  </button>
                </div>
              )}

              {is_connected && (
                <div className="space-y-4">
                  {/* Connected badge */}
                  <div className="flex items-center gap-2 p-3 bg-green-900/20 border border-green-700/30 rounded-lg">
                    <div className="w-2.5 h-2.5 bg-green-400 rounded-full animate-pulse" />
                    <span className="text-sm text-green-300 font-medium">Connected</span>
                    <span className="text-xs text-dark-400 ml-auto">{profile_display_name}</span>
                  </div>

                  {/* DNS info */}
                  {has_dns_server ? (
                    <div className="text-xs text-dark-400">
                      <p>DNS server: <code className="text-dark-300">{imported_profile?.dns.server}</code></p>
                      <p className="mt-1">
                        Enter an internal URL to verify DNS resolution and connectivity through the tunnel.
                      </p>
                    </div>
                  ) : (
                    <div className="p-3 bg-amber-900/20 border border-amber-700/30 rounded-lg">
                      <div className="flex items-start gap-2">
                        <svg className="w-5 h-5 text-amber-400 mt-0.5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
                            d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
                        </svg>
                        <div>
                          <p className="text-sm text-amber-300">No DNS server configured</p>
                          <p className="text-xs text-dark-400 mt-1">
                            DNS resolution for <code className="text-dark-300">*{dns_suffix_input}</code> domains won't
                            work until you add static host mappings. You can still test connectivity using an IP address.
                          </p>
                        </div>
                      </div>
                    </div>
                  )}

                  {/* URL test */}
                  <div className="flex gap-3">
                    <div className="flex-1">
                      <label className="block text-xs text-dark-400 mb-1">
                        {has_dns_server ? 'Hostname or IP' : 'IP Address'}
                      </label>
                      <input
                        type="text"
                        value={test_url_input}
                        onChange={(event) => setTestUrlInput(event.target.value)}
                        onKeyDown={handleTestKeyPress}
                        className="w-full input"
                        placeholder={has_dns_server ? `myapp${dns_suffix_input}` : '10.0.0.1'}
                        disabled={is_testing}
                      />
                    </div>
                    <div className="w-24">
                      <label className="block text-xs text-dark-400 mb-1">Port</label>
                      <input
                        type="number"
                        value={test_port_input}
                        onChange={(event) => setTestPortInput(event.target.value)}
                        onKeyDown={handleTestKeyPress}
                        className="w-full input"
                        placeholder="443"
                        disabled={is_testing}
                        min="1"
                        max="65535"
                      />
                    </div>
                    <div className="flex items-end">
                      <button
                        onClick={handleTestUrl}
                        disabled={is_testing || !test_url_input.trim()}
                        className={`px-4 py-2 rounded-lg font-medium transition-colors ${
                          is_testing || !test_url_input.trim()
                            ? 'bg-dark-600 text-dark-400 cursor-not-allowed'
                            : 'bg-primary-600 hover:bg-primary-500 text-white'
                        }`}
                      >
                        {is_testing ? (
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

                  {/* Test result */}
                  {test_result && (
                    <div className={`p-3 rounded-lg ${
                      test_result.success
                        ? 'bg-green-900/30 border border-green-700/50'
                        : 'bg-red-900/30 border border-red-700/50'
                    }`}>
                      <div className="flex items-center gap-3">
                        {test_result.success ? (
                          <svg className="w-5 h-5 text-green-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                          </svg>
                        ) : (
                          <svg className="w-5 h-5 text-red-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                          </svg>
                        )}
                        <div className="flex-1">
                          <div className={`font-medium ${test_result.success ? 'text-green-400' : 'text-red-400'}`}>
                            {test_result.success ? 'Connection successful' : 'Connection failed'}
                          </div>
                          <div className="text-sm text-dark-400">{test_result.message}</div>
                        </div>
                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>

            {/* Footer */}
            <div className="flex justify-between mt-6">
              <button
                onClick={handleStep3Back}
                disabled={is_connecting}
                className="btn btn-secondary"
              >
                Back
              </button>
              <button
                onClick={handleFinish}
                disabled={is_connecting}
                className="btn btn-primary"
              >
                {is_connected ? 'Finish' : 'Skip & Finish'}
              </button>
            </div>
          </div>
        )}

        {current_step === 4 && (
          <div className="space-y-4">
            <div className="flex flex-col items-center py-6">
              {/* Success icon */}
              <div className="w-16 h-16 bg-green-900/30 rounded-full flex items-center justify-center mb-4">
                <svg className="w-8 h-8 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                </svg>
              </div>
              <h3 className="text-lg font-semibold text-white mb-1">Connection Added Successfully</h3>
              <p className="text-sm text-dark-400">Your VPN tunnel is configured and ready to use.</p>
            </div>

            {/* Summary */}
            <div className="bg-dark-900 rounded-lg p-4 space-y-3">
              <div className="flex justify-between text-sm">
                <span className="text-dark-400">Name</span>
                <span className="text-white">{profile_display_name}</span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-dark-400">DNS Suffix</span>
                <code className="text-primary-400">{dns_suffix_input}</code>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-dark-400">DNS Server</span>
                <span className="text-white">
                  {has_dns_server ? (
                    <code>{imported_profile?.dns.server}</code>
                  ) : (
                    <span className="text-amber-400">None (static hosts needed)</span>
                  )}
                </span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-dark-400">Status</span>
                <span className="flex items-center gap-1.5">
                  {is_connected ? (
                    <>
                      <div className="w-2 h-2 bg-green-400 rounded-full" />
                      <span className="text-green-400">Connected</span>
                    </>
                  ) : (
                    <>
                      <div className="w-2 h-2 bg-dark-500 rounded-full" />
                      <span className="text-dark-400">Disconnected</span>
                    </>
                  )}
                </span>
              </div>
            </div>

            {/* Footer */}
            <div className="flex justify-end mt-6">
              <button
                onClick={() => onComplete(imported_profile!.id)}
                className="btn btn-primary"
              >
                Close
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

export default ImportWizard
