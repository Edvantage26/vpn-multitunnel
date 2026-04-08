import type { UpdateInfo, ReleaseEntry } from '../App'

function ReleaseNotes({ markdown }: { markdown: string }) {
  const lines = markdown.split('\n')
  const elements: JSX.Element[] = []

  for (let line_index = 0; line_index < lines.length; line_index++) {
    const line = lines[line_index]
    if (line.startsWith('## ')) {
      elements.push(
        <h4 key={line_index} className="text-xs font-semibold text-dark-200 uppercase tracking-wider mt-2 first:mt-0 mb-1">
          {line.slice(3)}
        </h4>
      )
    } else if (line.startsWith('- ')) {
      elements.push(
        <div key={line_index} className="flex gap-1.5 text-xs text-dark-400 py-0.5">
          <span className="text-dark-500 flex-shrink-0">•</span>
          <span>{line.slice(2)}</span>
        </div>
      )
    }
  }

  return <div>{elements}</div>
}

interface ChangelogModalProps {
  updateInfo: UpdateInfo
  updateDownloading: boolean
  onUpdateInstall: () => void
  onClose: () => void
}

function ChangelogModal({ updateInfo, updateDownloading, onUpdateInstall, onClose }: ChangelogModalProps) {
  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-dark-800 border border-dark-700 rounded-xl shadow-2xl w-[480px] max-h-[80vh] flex flex-col">
        <div className="p-4 border-b border-dark-700 flex items-center justify-between">
          <h2 className="text-lg font-semibold text-white">What's New</h2>
          <button
            onClick={onClose}
            className="text-dark-400 hover:text-dark-200 text-xl leading-none"
          >
            &times;
          </button>
        </div>

        <div className="flex-1 overflow-auto p-4 space-y-4">
          {updateInfo.releases.map((release_entry: ReleaseEntry) => (
            <div key={release_entry.version} className="border-b border-dark-700/50 pb-4 last:border-0">
              <div className="flex items-baseline justify-between mb-1">
                <h3 className="text-sm font-semibold text-primary-400">v{release_entry.version}</h3>
                {release_entry.publishedAt && (
                  <span className="text-xs text-dark-500">
                    {new Date(release_entry.publishedAt).toLocaleDateString()}
                  </span>
                )}
              </div>
              {release_entry.name && release_entry.name !== `v${release_entry.version}` && (
                <p className="text-sm text-dark-200 mb-1">{release_entry.name}</p>
              )}
              {release_entry.notes && (
                <ReleaseNotes markdown={release_entry.notes} />
              )}
            </div>
          ))}
        </div>

        <div className="p-4 border-t border-dark-700 flex justify-end gap-2">
          <button
            onClick={onClose}
            className="btn btn-secondary text-sm"
          >
            Close
          </button>
          <button
            onClick={() => { onClose(); onUpdateInstall() }}
            disabled={updateDownloading}
            className="btn btn-primary text-sm disabled:opacity-50"
          >
            {updateDownloading ? 'Downloading...' : 'Update Now'}
          </button>
        </div>
      </div>
    </div>
  )
}

export default ChangelogModal
