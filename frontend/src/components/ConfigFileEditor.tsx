import { useState, useEffect } from 'react'

interface ConfigFileEditorProps {
  profileId: string
  profileName: string
  configFile: string
  onClose: () => void
}

function ConfigFileEditor({ profileId, profileName, configFile, onClose }: ConfigFileEditorProps) {
  const [content, setContent] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    loadContent()
  }, [profileId])

  const loadContent = async () => {
    setLoading(true)
    setError('')
    try {
      const data = await window.go.app.App.GetConfigFileContent(profileId)
      setContent(data)
    } catch (err) {
      setError(String(err))
    } finally {
      setLoading(false)
    }
  }

  const handleSave = async () => {
    setSaving(true)
    setError('')
    setSaved(false)
    try {
      await window.go.app.App.SaveConfigFileContent(profileId, content)
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
    } catch (err) {
      setError(String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-dark-800 rounded-lg shadow-xl w-full max-w-4xl mx-4 h-[85vh] flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b border-dark-700">
          <div>
            <h2 className="text-lg font-semibold text-white">WireGuard Config</h2>
            <p className="text-xs text-dark-400">{profileName} - {configFile}</p>
          </div>
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
        <div className="flex-1 p-4 overflow-hidden flex flex-col">
          {error && (
            <div className="mb-3 p-2 bg-red-900/50 border border-red-700 rounded text-red-200 text-sm">
              {error}
            </div>
          )}
          {saved && (
            <div className="mb-3 p-2 bg-green-900/50 border border-green-700 rounded text-green-200 text-sm">
              Config saved successfully. Reconnect tunnel to apply changes.
            </div>
          )}

          {loading ? (
            <div className="flex-1 flex items-center justify-center text-dark-400">
              Loading...
            </div>
          ) : (
            <textarea
              value={content}
              onChange={(event) => setContent(event.target.value)}
              className="flex-1 w-full p-3 bg-dark-900 border border-dark-600 rounded-lg text-sm font-mono text-dark-100 focus:outline-none focus:border-primary-500 resize-none"
              spellCheck={false}
            />
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between p-4 border-t border-dark-700">
          <p className="text-xs text-dark-500">
            Changes require tunnel reconnection to take effect
          </p>
          <div className="flex gap-2">
            <button
              onClick={loadContent}
              disabled={loading}
              className="btn btn-secondary"
            >
              Reload
            </button>
            <button
              onClick={onClose}
              className="btn btn-secondary"
            >
              Close
            </button>
            <button
              onClick={handleSave}
              disabled={saving || loading}
              className="btn btn-primary"
            >
              {saving ? 'Saving...' : 'Save'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

export default ConfigFileEditor
