import { useState, useRef } from 'react'
import { ProfileStatus, UpdateInfo } from '../App'

interface SidebarProps {
  profiles: ProfileStatus[]
  selectedId?: string
  onSelect: (id: string) => void
  onAddProfile: () => void
  onOpenSettings: () => void
  onReorder: (orderedIDs: string[]) => void
  appVersion?: string
  updateInfo?: UpdateInfo | null
  updateDownloading?: boolean
  onUpdateInstall?: () => void
  onOpenChangelog?: () => void
}

function Sidebar({
  profiles,
  selectedId,
  onSelect,
  onAddProfile,
  onOpenSettings,
  onReorder,
  appVersion,
  updateInfo,
  updateDownloading,
  onUpdateInstall,
  onOpenChangelog,
}: SidebarProps) {
  const connectedCount = profiles.filter(profile => profile.connected).length
  const [draggedId, setDraggedId] = useState<string | null>(null)
  const [dropTargetId, setDropTargetId] = useState<string | null>(null)
  const [dropPosition, setDropPosition] = useState<'above' | 'below' | null>(null)
  const itemRefs = useRef<Map<string, HTMLElement>>(new Map())

  const handleDragStart = (event: React.DragEvent, profileId: string) => {
    setDraggedId(profileId)
    event.dataTransfer.effectAllowed = 'move'
    // Make the drag image slightly transparent
    if (event.currentTarget instanceof HTMLElement) {
      event.dataTransfer.setDragImage(event.currentTarget, 0, 0)
    }
  }

  const handleDragOver = (event: React.DragEvent, profileId: string) => {
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'

    if (profileId === draggedId) {
      setDropTargetId(null)
      setDropPosition(null)
      return
    }

    const element = itemRefs.current.get(profileId)
    if (!element) return

    const rect = element.getBoundingClientRect()
    const midpoint = rect.top + rect.height / 2
    const position = event.clientY < midpoint ? 'above' : 'below'

    setDropTargetId(profileId)
    setDropPosition(position)
  }

  const handleDrop = (event: React.DragEvent) => {
    event.preventDefault()

    if (!draggedId || !dropTargetId || draggedId === dropTargetId) {
      resetDragState()
      return
    }

    const currentOrder = profiles.map(profile => profile.id)
    const draggedIndex = currentOrder.indexOf(draggedId)
    // Remove dragged item
    currentOrder.splice(draggedIndex, 1)
    // Find new position
    let targetIndex = currentOrder.indexOf(dropTargetId)
    if (dropPosition === 'below') {
      targetIndex += 1
    }
    currentOrder.splice(targetIndex, 0, draggedId)

    onReorder(currentOrder)
    resetDragState()
  }

  const handleDragEnd = () => {
    resetDragState()
  }

  const resetDragState = () => {
    setDraggedId(null)
    setDropTargetId(null)
    setDropPosition(null)
  }

  const getDropIndicatorClass = (profileId: string) => {
    if (profileId !== dropTargetId || !dropPosition) return ''
    if (dropPosition === 'above') return 'border-t-2 border-t-primary-500'
    return 'border-b-2 border-b-primary-500'
  }

  return (
    <aside className="w-72 bg-dark-800 border-r border-dark-700 flex flex-col">
      {/* Header */}
      <div className="p-4 border-b border-dark-700" style={{ WebkitAppRegion: 'drag' } as React.CSSProperties}>
        <h1 className="text-xl font-bold text-white flex items-center gap-2">
          <svg className="w-6 h-6 text-primary-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
              d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
          </svg>
          VPN MultiTunnel
        </h1>
        <p className="text-sm text-dark-400 mt-1">
          {connectedCount > 0
            ? `${connectedCount} tunnel${connectedCount > 1 ? 's' : ''} active`
            : 'No tunnels active'
          }
        </p>
      </div>

      {/* Profile List */}
      <div className="flex-1 overflow-auto py-2">
        {profiles.length === 0 ? (
          <div className="px-4 py-8 text-center text-dark-400">
            <p>No profiles configured</p>
            <button
              onClick={onAddProfile}
              className="mt-2 text-primary-400 hover:text-primary-300"
            >
              Import a configuration
            </button>
          </div>
        ) : (
          profiles.map(profile => (
            <div
              key={profile.id}
              ref={(element) => {
                if (element) itemRefs.current.set(profile.id, element)
                else itemRefs.current.delete(profile.id)
              }}
              draggable
              onDragStart={(event) => handleDragStart(event, profile.id)}
              onDragOver={(event) => handleDragOver(event, profile.id)}
              onDrop={handleDrop}
              onDragEnd={handleDragEnd}
              onClick={() => onSelect(profile.id)}
              className={`w-full px-4 py-3 flex items-center gap-3 hover:bg-dark-700 transition-colors text-left cursor-grab active:cursor-grabbing select-none ${
                selectedId === profile.id
                  ? 'bg-dark-700 border-l-2 border-primary-500'
                  : 'border-l-2 border-transparent'
              } ${draggedId === profile.id ? 'opacity-40' : ''} ${getDropIndicatorClass(profile.id)}`}
            >
              {/* Status indicator - spinner when connecting, green when connected, red on error */}
              {profile.connecting ? (
                <div className="w-3 h-3 flex-shrink-0 rounded-full border-2 border-primary-500 border-t-transparent animate-spin" />
              ) : (
                <div className={`w-3 h-3 rounded-full flex-shrink-0 ${
                  profile.connected
                    ? (profile.dnsIssue ? 'bg-yellow-500 animate-pulse' : 'bg-green-500')
                    : profile.lastError ? 'bg-red-500' : 'bg-dark-500'
                }`} title={profile.dnsIssue || profile.lastError || ''} />
              )}

              <span className="font-medium text-dark-100 truncate">{profile.name}</span>
            </div>
          ))
        )}
      </div>

      {/* Footer */}
      <div className="border-t border-dark-700">
        {/* Version / Update Banner */}
        <div className="px-3 pt-2 pb-1">
          {updateInfo?.available ? (
            <div className="px-3 py-2 bg-primary-900/30 border border-primary-700/50 rounded-lg">
              <p className="text-xs text-dark-200 font-medium">
                v{updateInfo.currentVersion} <span className="text-dark-500 mx-1">&rarr;</span> <span className="text-primary-400">v{updateInfo.latestVersion}</span>
              </p>
              <div className="flex items-center justify-between mt-1.5">
                <button
                  onClick={() => onOpenChangelog?.()}
                  className="text-xs text-primary-400 hover:text-primary-300"
                >
                  + info
                </button>
                <button
                  onClick={onUpdateInstall}
                  disabled={updateDownloading}
                  className="text-xs px-2.5 py-1 bg-primary-600 hover:bg-primary-500 rounded text-white disabled:opacity-50"
                >
                  {updateDownloading ? 'Downloading...' : 'Update'}
                </button>
              </div>
            </div>
          ) : (
            appVersion && (
              <p className="text-xs text-dark-500 text-center">v{appVersion}</p>
            )
          )}
        </div>

        {/* Action Buttons */}
        <div className="px-3 pb-3 pt-1">
          <div className="flex gap-2">
            <button
              onClick={onAddProfile}
              className="flex-1 btn btn-primary text-sm"
            >
              + Add tunnel
            </button>
            <button
              onClick={onOpenSettings}
              className="btn btn-secondary text-sm px-3"
              title="Settings"
            >
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
                  d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
                  d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
              </svg>
            </button>
          </div>
        </div>
      </div>

    </aside>
  )
}

export default Sidebar
