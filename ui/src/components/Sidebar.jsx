import { useState } from 'react'
import { LayoutDashboard, Wallet, BookOpen, TrendingUp, Send, Landmark, Zap, LogOut, ChevronRight, GraduationCap } from 'lucide-react'

const nav = [
  { id: 'dashboard',    label: 'Dashboard',    icon: LayoutDashboard, desc: 'Overview & health' },
  { id: 'accounts',     label: 'Accounts',     icon: Wallet,          desc: 'Wallets & holds' },
  { id: 'ledger',       label: 'Ledger',       icon: BookOpen,        desc: 'Journal entries' },
  { id: 'fx',           label: 'FX Exchange',  icon: TrendingUp,      desc: 'Rates & quotes' },
  { id: 'transactions', label: 'Transactions', icon: Send,            desc: 'Payment sagas' },
  { id: 'payouts',      label: 'Payouts',      icon: Landmark,        desc: 'Bank withdrawals' },
  { id: 'tutorial',     label: 'Tutorial',     icon: GraduationCap,   desc: 'End-to-end example', highlight: true },
]

function getJwtUser() {
  const jwt = sessionStorage.getItem('jwt')
  if (!jwt) return 'Admin'
  try {
    const parts = jwt.split('.')
    if (parts.length < 2) return 'Admin'
    const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')))
    return payload.sub || payload.username || payload.name || 'Admin'
  } catch { return 'Admin' }
}

export default function Sidebar({ current, onNavigate, onLogout }) {
  const [tutSeen, setTutSeen] = useState(() => localStorage.getItem('tutorial_visited') === '1')
  const username = getJwtUser()
  return (
    <aside className="w-56 flex-shrink-0 flex flex-col h-full border-r border-slate-800/80"
      style={{ background: 'linear-gradient(180deg, #0d0f17 0%, #0a0c12 100%)' }}>

      {/* Logo */}
      <div className="px-4 h-14 border-b border-slate-800/80 flex items-center gap-2.5 flex-shrink-0">
        <div className="relative w-7 h-7 flex-shrink-0">
          <div className="absolute inset-0 rounded-lg bg-indigo-600 opacity-30 blur-sm" />
          <div className="relative w-7 h-7 rounded-lg bg-gradient-to-br from-indigo-500 to-indigo-700
                          flex items-center justify-center shadow-lg shadow-indigo-900/50">
            <Zap size={13} className="text-white" />
          </div>
        </div>
        <div className="min-w-0">
          <p className="text-sm font-bold leading-tight" style={{
            background: 'linear-gradient(135deg, #e2e8f0 0%, #94a3b8 100%)',
            WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent',
          }}>Ledger</p>
          <p className="text-[9px] text-slate-600 leading-tight tracking-wide uppercase">Payment Platform</p>
        </div>
      </div>

      {/* Nav */}
      <nav className="flex-1 px-2.5 py-3 space-y-0.5 overflow-auto">
        {nav.map((item) => {
          const { id, label, icon: Icon, desc } = item
          const active = current === id
          return (
            <button
              key={id}
              onClick={() => {
                if (id === 'tutorial' && !tutSeen) { localStorage.setItem('tutorial_visited', '1'); setTutSeen(true) }
                onNavigate(id)
              }}
              className={`w-full flex items-center gap-2.5 px-2.5 py-2 rounded-lg text-left
                transition-all duration-200 group relative overflow-hidden
                ${active ? 'text-indigo-300' : item.highlight ? 'text-amber-600/80 hover:text-amber-400' : 'text-slate-500 hover:text-slate-100'}`}
              style={active ? {
                background: 'linear-gradient(90deg, rgba(99,102,241,0.12) 0%, rgba(99,102,241,0.04) 100%)',
                boxShadow: 'inset 0 0 0 1px rgba(99,102,241,0.15)',
              } : {}}
            >
              {/* Active left bar */}
              {active && (
                <div className="absolute left-0 top-1/2 -translate-y-1/2 w-0.5 h-4 rounded-full bg-indigo-500"
                  style={{ boxShadow: '0 0 8px rgba(99,102,241,0.8)' }} />
              )}

              {/* Hover bg */}
              {!active && (
                <div className="absolute inset-0 bg-slate-800/0 group-hover:bg-slate-800/40 rounded-lg transition-colors duration-200" />
              )}

              <div className={`relative z-10 flex-shrink-0 transition-colors duration-200
                ${active ? 'text-indigo-400' : item.highlight ? 'text-amber-600 group-hover:text-amber-400' : 'text-slate-600 group-hover:text-slate-300'}`}>
                <Icon size={15} />
              </div>

              <div className="relative z-10 min-w-0 flex-1">
                <div className="flex items-center gap-1.5">
                  <p className={`text-[13px] font-medium leading-tight transition-colors duration-200
                    ${active ? 'text-indigo-300' : item.highlight ? 'text-amber-500/80 group-hover:text-amber-400' : 'text-slate-400 group-hover:text-slate-100'}`}>
                    {label}
                  </p>
                  {item.highlight && !active && !tutSeen && (
                    <span className="text-[8px] font-bold uppercase tracking-wide px-1 py-px rounded
                                     bg-amber-900/60 text-amber-500 border border-amber-800/40">
                      new
                    </span>
                  )}
                </div>
                <p className={`text-[10px] leading-tight mt-0.5 transition-colors duration-200
                  ${active ? 'text-indigo-600' : 'text-slate-700 group-hover:text-slate-500'}`}>
                  {desc}
                </p>
              </div>

              {active && (
                <ChevronRight size={11} className="relative z-10 text-indigo-600 flex-shrink-0" />
              )}
            </button>
          )
        })}
      </nav>

      {/* Divider */}
      <div className="mx-3 h-px bg-gradient-to-r from-transparent via-slate-800 to-transparent" />

      {/* Footer */}
      <div className="px-2.5 py-3 flex-shrink-0 space-y-1">
        {/* User row */}
        <div className="flex items-center gap-2.5 px-2.5 py-2">
          <div className="relative flex-shrink-0">
            <div className="w-6 h-6 rounded-full bg-gradient-to-br from-indigo-500 to-purple-600
                            flex items-center justify-center">
              <span className="text-[10px] font-bold text-white">A</span>
            </div>
            <div className="absolute -bottom-0.5 -right-0.5 w-2 h-2 rounded-full bg-emerald-500 border border-[#0d0f17]"
              style={{ boxShadow: '0 0 6px rgba(16,185,129,.7)' }} />
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-xs font-medium text-slate-300 leading-tight">{username}</p>
            <p className="text-[10px] text-slate-600 leading-tight">Connected</p>
          </div>
        </div>

        {/* Sign out */}
        <button
          onClick={onLogout}
          className="w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-[11px]
                     text-slate-600 hover:text-red-400 hover:bg-red-950/20 transition-all duration-150 group"
        >
          <LogOut size={12} className="group-hover:rotate-12 transition-transform duration-150" />
          Sign out
        </button>
      </div>
    </aside>
  )
}
