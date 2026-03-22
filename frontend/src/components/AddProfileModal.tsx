interface AddProfileModalProps {
  onClose: () => void
  onImport: () => void
}

function AddProfileModal({ onClose, onImport }: AddProfileModalProps) {
  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="card w-full max-w-md mx-4 p-6">
        <div className="flex items-center justify-between mb-6">
          <h2 className="text-xl font-bold text-white">Import Configuration</h2>
          <button
            onClick={onClose}
            className="text-dark-400 hover:text-dark-200"
          >
            <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
                d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <div className="space-y-4">
          <div className="bg-dark-900 rounded-lg p-4">
            <h3 className="text-sm font-medium text-dark-300 mb-2">Import WireGuard Configuration</h3>
            <p className="text-xs text-dark-400 mb-4">
              Select a WireGuard .conf file to import. The configuration will be copied to the app's config directory.
            </p>
            <ul className="text-xs text-dark-400 space-y-1">
              <li>• Configuration will be parsed and validated</li>
              <li>• A tunnel IP will be automatically assigned for transparent proxy</li>
              <li>• The profile will be ready to connect</li>
            </ul>
          </div>
        </div>

        <div className="flex justify-end gap-3 mt-6">
          <button
            onClick={onClose}
            className="btn btn-secondary"
          >
            Cancel
          </button>
          <button
            onClick={onImport}
            className="btn btn-primary"
          >
            Browse & Import
          </button>
        </div>
      </div>
    </div>
  )
}

export default AddProfileModal
