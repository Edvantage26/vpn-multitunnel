export type NavView = 'connections' | 'traffic' | 'logs' | 'settings'

interface NavBarProps {
  activeView: NavView
  onNavigate: (view: NavView) => void
  advancedMode: boolean
}

interface NavItem {
  viewId: NavView
  label: string
  icon: JSX.Element
}

function NavBar({ activeView, onNavigate, advancedMode }: NavBarProps) {
  const allNavItems: NavItem[] = [
    {
      viewId: 'connections',
      label: 'Tunnels',
      icon: (
        <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.8}
            d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
        </svg>
      ),
    },
    {
      viewId: 'traffic',
      label: 'Traffic',
      icon: (
        <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.8}
            d="M13 7h8m0 0v8m0-8l-8 8-4-4-6 6" />
        </svg>
      ),
    },
    {
      viewId: 'logs',
      label: 'Logs',
      icon: (
        <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.8}
            d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
        </svg>
      ),
    },
    {
      viewId: 'settings',
      label: 'Settings',
      icon: (
        <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.8}
            d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.8}
            d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
        </svg>
      ),
    },
  ]

  const navItems = advancedMode
    ? allNavItems
    : allNavItems.filter(item => item.viewId !== 'traffic' && item.viewId !== 'logs')

  return (
    <nav
      className="w-16 bg-dark-950 border-r border-dark-700 flex flex-col items-center py-2 flex-shrink-0"
    >
      {/* Drag region at top */}
      <div className="w-full h-6 mb-1" style={{ WebkitAppRegion: 'drag' } as React.CSSProperties} />

      {/* Navigation items */}
      <div className="flex flex-col items-center gap-1">
        {navItems.map((navItem) => {
          const isActive = activeView === navItem.viewId
          return (
            <button
              key={navItem.viewId}
              onClick={() => onNavigate(navItem.viewId)}
              className={`relative w-11 h-11 flex flex-col items-center justify-center rounded-lg transition-colors ${
                isActive
                  ? 'bg-dark-700 text-primary-400'
                  : 'text-dark-500 hover:text-dark-300 hover:bg-dark-800'
              }`}
              title={navItem.label}
              style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
            >
              {isActive && (
                <div className="absolute left-0 top-1.5 bottom-1.5 w-0.5 rounded-r bg-primary-500" />
              )}
              {navItem.icon}
              <span className="text-[9px] mt-0.5 leading-none font-medium">{navItem.label}</span>
            </button>
          )
        })}
      </div>
    </nav>
  )
}

export default NavBar
