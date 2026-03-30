import { useState, useEffect } from 'react'
import { Profile } from '../App'
import ServicePortSelector from './ServicePortSelector'

interface ProfileEditorProps {
  profile: Profile
  onSave: (profile: Profile) => void
  onClose: () => void
}

function ProfileEditor({ profile, onSave, onClose }: ProfileEditorProps) {
  const [editedProfile, setEditedProfile] = useState<Profile>({ ...profile })
  const [newDomain, setNewDomain] = useState('')
  const [editingDomainIndex, setEditingDomainIndex] = useState<number | null>(null)
  const [editingDomainValue, setEditingDomainValue] = useState('')
  const [showConfigEditor, setShowConfigEditor] = useState(false)
  const [configContent, setConfigContent] = useState('')
  const [configLoading, setConfigLoading] = useState(false)
  const [configError, setConfigError] = useState('')
  const [configSaved, setConfigSaved] = useState(false)
  const loadConfigContent = async () => {
    setConfigLoading(true)
    setConfigError('')
    try {
      const content = await window.go.app.App.GetConfigFileContent(profile.id)
      setConfigContent(content)
    } catch (err) {
      setConfigError(String(err))
    } finally {
      setConfigLoading(false)
    }
  }

  const handleSaveConfig = async () => {
    setConfigLoading(true)
    setConfigError('')
    setConfigSaved(false)
    try {
      await window.go.app.App.SaveConfigFileContent(profile.id, configContent)
      setConfigSaved(true)
      setTimeout(() => setConfigSaved(false), 2000)
    } catch (err) {
      setConfigError(String(err))
    } finally {
      setConfigLoading(false)
    }
  }

  useEffect(() => {
    if (showConfigEditor && !configContent) {
      loadConfigContent()
    }
  }, [showConfigEditor])

  const handleSave = () => {
    onSave(editedProfile)
  }

  const handleAddDomain = () => {
    if (!newDomain.trim()) return

    let domain = newDomain.trim()
    if (domain.startsWith('.')) {
      domain = domain.slice(1)
    }

    if (domain && !editedProfile.dns.domains.includes(domain)) {
      setEditedProfile({
        ...editedProfile,
        dns: {
          ...editedProfile.dns,
          domains: [...editedProfile.dns.domains, domain]
        }
      })
      setNewDomain('')
    }
  }

  const handleRemoveDomain = (domain: string) => {
    setEditedProfile({
      ...editedProfile,
      dns: {
        ...editedProfile.dns,
        domains: editedProfile.dns.domains.filter(domain_value => domain_value !== domain)
      }
    })
  }

  const handleStartEditDomain = (index: number, domain: string) => {
    setEditingDomainIndex(index)
    setEditingDomainValue(domain)
  }

  const handleSaveEditDomain = () => {
    if (editingDomainIndex === null) return

    let domain = editingDomainValue.trim()
    if (domain.startsWith('.')) {
      domain = domain.slice(1)
    }

    if (domain) {
      const newDomains = [...editedProfile.dns.domains]
      // Check if domain already exists (excluding current position)
      const existsElsewhere = newDomains.some((domain_value, domain_index) => domain_index !== editingDomainIndex && domain_value === domain)
      if (!existsElsewhere) {
        newDomains[editingDomainIndex] = domain
        setEditedProfile({
          ...editedProfile,
          dns: {
            ...editedProfile.dns,
            domains: newDomains
          }
        })
      }
    }
    setEditingDomainIndex(null)
    setEditingDomainValue('')
  }

  const handleCancelEditDomain = () => {
    setEditingDomainIndex(null)
    setEditingDomainValue('')
  }

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-dark-800 rounded-lg shadow-xl w-full max-w-lg mx-4 max-h-[90vh] overflow-y-auto">
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b border-dark-700 sticky top-0 bg-dark-800">
          <h2 className="text-lg font-semibold text-white">Edit Profile</h2>
          <button
            onClick={onClose}
            className="text-dark-400 hover:text-white transition-colors"
          >
            <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* Content */}
        <div className="p-4 space-y-6">
          {/* Basic Info */}
          <div>
            <h3 className="text-sm font-semibold text-dark-300 uppercase tracking-wider mb-3">
              Basic Info
            </h3>
            <div className="space-y-3">
              <div>
                <label className="block text-sm font-medium text-dark-300 mb-1">
                  Name
                </label>
                <input
                  type="text"
                  value={editedProfile.name}
                  onChange={(event) => setEditedProfile({ ...editedProfile, name: event.target.value })}
                  className="w-full input"
                />
              </div>
              <div className="flex items-center gap-2">
                <input
                  type="checkbox"
                  id="enabled"
                  checked={editedProfile.enabled}
                  onChange={(event) => setEditedProfile({ ...editedProfile, enabled: event.target.checked })}
                  className="rounded bg-dark-700 border-dark-600 text-primary-500 focus:ring-primary-500"
                />
                <label htmlFor="enabled" className="text-sm text-dark-300">
                  Profile enabled
                </label>
              </div>
            </div>
          </div>

          {/* Health Check Settings */}
          <div>
            <h3 className="text-sm font-semibold text-dark-300 uppercase tracking-wider mb-3">
              Health Check
            </h3>
            <div className="space-y-3">
              <div className="flex items-center gap-2">
                <input
                  type="checkbox"
                  id="healthEnabled"
                  checked={editedProfile.healthCheck.enabled}
                  onChange={(event) => setEditedProfile({
                    ...editedProfile,
                    healthCheck: { ...editedProfile.healthCheck, enabled: event.target.checked }
                  })}
                  className="rounded bg-dark-700 border-dark-600 text-primary-500 focus:ring-primary-500"
                />
                <label htmlFor="healthEnabled" className="text-sm text-dark-300">
                  Enable health checks
                </label>
              </div>
              {editedProfile.healthCheck.enabled && (
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="block text-sm font-medium text-dark-300 mb-1">
                      Target IP
                    </label>
                    <input
                      type="text"
                      value={editedProfile.healthCheck.targetIP}
                      readOnly
                      className="w-full input opacity-60 cursor-not-allowed"
                      title="Resolved from WireGuard .conf Address field"
                    />
                  </div>
                  <div>
                    <label className="block text-sm font-medium text-dark-300 mb-1">
                      Interval (seconds)
                    </label>
                    <input
                      type="number"
                      value={editedProfile.healthCheck.intervalSeconds}
                      onChange={(event) => setEditedProfile({
                        ...editedProfile,
                        healthCheck: { ...editedProfile.healthCheck, intervalSeconds: parseInt(event.target.value) || 30 }
                      })}
                      className="w-full input"
                      min="5"
                      max="300"
                    />
                  </div>
                </div>
              )}
            </div>
          </div>

          {/* DNS Settings */}
          <div>
            <h3 className="text-sm font-semibold text-dark-300 uppercase tracking-wider mb-3">
              DNS Settings
            </h3>
            <div className="space-y-3">
              <div>
                <label className="block text-sm font-medium text-dark-300 mb-1">
                  DNS Server
                </label>
                <input
                  type="text"
                  value={editedProfile.dns.server || '(not configured in .conf)'}
                  readOnly
                  className="w-full input opacity-60 cursor-not-allowed"
                  title="Resolved from WireGuard .conf DNS field"
                />
                <p className="text-xs text-dark-400 mt-1">
                  Resolved from WireGuard .conf file
                </p>
              </div>

              <div>
                <label className="block text-sm font-medium text-dark-300 mb-1">
                  Domain Suffixes
                </label>
                <p className="text-xs text-dark-400 mb-2">
                  Domains ending with these suffixes will be resolved through this tunnel
                </p>

                {/* Domain chips */}
                <div className="flex flex-wrap gap-2 mb-2 min-h-[32px]">
                  {editedProfile.dns.domains.length === 0 ? (
                    <span className="text-dark-500 text-sm italic">No suffixes configured</span>
                  ) : (
                    editedProfile.dns.domains.map((domain, index) => (
                      editingDomainIndex === index ? (
                        <div key={domain} className="inline-flex items-center gap-1">
                          <input
                            type="text"
                            value={editingDomainValue}
                            onChange={(event) => setEditingDomainValue(event.target.value)}
                            onKeyPress={(event) => {
                              if (event.key === 'Enter') {
                                event.preventDefault()
                                handleSaveEditDomain()
                              }
                            }}
                            onKeyDown={(event) => {
                              if (event.key === 'Escape') {
                                handleCancelEditDomain()
                              }
                            }}
                            className="w-32 px-2 py-1 bg-dark-600 border border-primary-500 rounded text-sm text-dark-100 focus:outline-none"
                            autoFocus
                          />
                          <button
                            onClick={handleSaveEditDomain}
                            className="text-green-400 hover:text-green-300 transition-colors p-1"
                            title="Save"
                          >
                            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                            </svg>
                          </button>
                          <button
                            onClick={handleCancelEditDomain}
                            className="text-dark-400 hover:text-red-400 transition-colors p-1"
                            title="Cancel"
                          >
                            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                            </svg>
                          </button>
                        </div>
                      ) : (
                        <span
                          key={domain}
                          className="inline-flex items-center gap-1 px-2 py-1 bg-dark-700 rounded text-sm text-dark-200 group cursor-pointer hover:bg-dark-600"
                          onClick={() => handleStartEditDomain(index, domain)}
                          title="Click to edit"
                        >
                          .{domain}
                          <button
                            onClick={(event) => {
                              event.stopPropagation()
                              handleRemoveDomain(domain)
                            }}
                            className="text-dark-400 hover:text-red-400 transition-colors"
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

                {/* Add domain input */}
                <div className="flex gap-2">
                  <input
                    type="text"
                    value={newDomain}
                    onChange={(event) => setNewDomain(event.target.value)}
                    onKeyPress={(event) => event.key === 'Enter' && (event.preventDefault(), handleAddDomain())}
                    placeholder="Add suffix (e.g., office)"
                    className="flex-1 input"
                  />
                  <button
                    onClick={handleAddDomain}
                    disabled={!newDomain.trim()}
                    className="btn btn-secondary px-3"
                  >
                    Add
                  </button>
                </div>

                {/* Strip Suffix Option */}
                <div className="flex items-center gap-2 mt-3 pt-3 border-t border-dark-700">
                  <input
                    type="checkbox"
                    id="stripSuffix"
                    checked={editedProfile.dns.stripSuffix}
                    onChange={(event) => setEditedProfile({
                      ...editedProfile,
                      dns: { ...editedProfile.dns, stripSuffix: event.target.checked }
                    })}
                    className="rounded bg-dark-700 border-dark-600 text-primary-500 focus:ring-primary-500"
                  />
                  <label htmlFor="stripSuffix" className="text-sm text-dark-300">
                    Strip suffix when forwarding
                  </label>
                </div>
                <p className="text-xs text-dark-500 mt-1">
                  When enabled, <code className="bg-dark-800 px-1 rounded">db.svi</code> becomes <code className="bg-dark-800 px-1 rounded">db</code> for DNS resolution
                </p>
              </div>
            </div>
          </div>

          {/* TCP Proxy Ports */}
          <div>
            <h3 className="text-sm font-semibold text-dark-300 uppercase tracking-wider mb-3">
              TCP Proxy Ports
            </h3>
            <p className="text-xs text-dark-400 mb-2">
              Ports proxied through this tunnel. Only traffic to these ports will be intercepted.
            </p>
            <ServicePortSelector
              selectedPorts={editedProfile.tcpProxyPorts || []}
              onPortsChange={(ports) => setEditedProfile({
                ...editedProfile,
                tcpProxyPorts: ports
              })}
              size="md"
            />
          </div>

          {/* Config File Editor */}
          <div className="bg-dark-900 rounded-lg overflow-hidden">
            <div
              className="p-3 flex items-center justify-between cursor-pointer hover:bg-dark-800 transition-colors"
              onClick={() => setShowConfigEditor(!showConfigEditor)}
            >
              <div>
                <p className="text-sm text-dark-300 font-medium">WireGuard Config</p>
                <p className="text-xs text-dark-500">{editedProfile.configFile}</p>
              </div>
              <svg
                className={`w-5 h-5 text-dark-400 transition-transform ${showConfigEditor ? 'rotate-180' : ''}`}
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
              </svg>
            </div>

            {showConfigEditor && (
              <div className="p-3 pt-0 border-t border-dark-700">
                {configLoading && !configContent ? (
                  <div className="text-center py-4 text-dark-400">Loading...</div>
                ) : (
                  <>
                    {configError && (
                      <div className="mb-2 p-2 bg-red-900/50 border border-red-700 rounded text-red-200 text-xs">
                        {configError}
                      </div>
                    )}
                    {configSaved && (
                      <div className="mb-2 p-2 bg-green-900/50 border border-green-700 rounded text-green-200 text-xs">
                        Config saved successfully
                      </div>
                    )}
                    <textarea
                      value={configContent}
                      onChange={(event) => setConfigContent(event.target.value)}
                      className="w-full h-64 p-2 bg-dark-800 border border-dark-600 rounded text-xs font-mono text-dark-100 focus:outline-none focus:border-primary-500 resize-y"
                      spellCheck={false}
                    />
                    <div className="flex justify-end gap-2 mt-2">
                      <button
                        onClick={loadConfigContent}
                        disabled={configLoading}
                        className="btn btn-secondary text-xs py-1 px-3"
                      >
                        Reload
                      </button>
                      <button
                        onClick={handleSaveConfig}
                        disabled={configLoading}
                        className="btn btn-primary text-xs py-1 px-3"
                      >
                        {configLoading ? 'Saving...' : 'Save Config'}
                      </button>
                    </div>
                    <p className="text-xs text-dark-500 mt-2">
                      Changes will be validated before saving. Tunnel must be reconnected for changes to take effect.
                    </p>
                  </>
                )}
              </div>
            )}
          </div>
        </div>

        {/* Footer */}
        <div className="flex justify-end gap-2 p-4 border-t border-dark-700 sticky bottom-0 bg-dark-800">
          <button
            onClick={onClose}
            className="btn btn-secondary"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            className="btn btn-primary"
          >
            Save Changes
          </button>
        </div>
      </div>
    </div>
  )
}

export default ProfileEditor
