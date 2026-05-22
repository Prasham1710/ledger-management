import { useCallback, useEffect, useRef, useState } from 'react'
import { Activity, RefreshCw, Wallet, TrendingUp, Send, Landmark, BookOpen, CheckCircle, XCircle, Zap, Clock } from 'lucide-react'
import { api } from '../api/client.js'

const REFRESH = 30

function LatencyBar({ latency }) {
  const ms  = parseInt(latency ?? '0') || 0
  const pct = Math.min(100, (ms / 300) * 100)
  const color = ms < 80 ? '#10b981' : ms < 180 ? '#f59e0b' : '#ef4444'
  return (
    <div className="flex items-center gap-2">
      <div className="w-16 h-1 bg-slate-800 rounded-full overflow-hidden">
        <div className="h-full rounded-full transition-all duration-700"
          style={{ width: `${pct}%`, backgroundColor: color }} />
      </div>
      <span className="text-[11px] font-mono w-12 text-right"
        style={{ color: ms === 0 ? '#475569' : color }}>
        {latency ?? '—'}
      </span>
    </div>
  )
}

function SvcRow({ svc, index }) {
  return (
    <div
      className="flex items-center gap-3 py-2.5 border-b border-slate-800/30 last:border-0"
      style={{ animation: `fade-up 0.3s ease-out ${index * 0.05}s both` }}
    >
      <div className="flex-shrink-0">
        {svc.healthy
          ? <div className="dot-green" />
          : <div className="dot-red" />}
      </div>
      <span className="text-sm text-slate-300 flex-1 font-medium">{svc.name}</span>
      <LatencyBar latency={svc.latency} />
      {svc.healthy
        ? <CheckCircle size={12} className="text-emerald-500 flex-shrink-0" />
        : <XCircle     size={12} className="text-red-500 flex-shrink-0" />}
    </div>
  )
}

function SvcSkeleton({ i }) {
  return (
    <div className="flex items-center gap-3 py-2.5 border-b border-slate-800/30 last:border-0">
      <div className="w-2 h-2 rounded-full skeleton flex-shrink-0" />
      <div className="h-3 skeleton flex-1 rounded" style={{ animationDelay: `${i * 0.1}s` }} />
      <div className="h-1 skeleton w-16 rounded" />
      <div className="w-3 h-3 skeleton rounded" />
    </div>
  )
}

function StatCard({ label, value, sub, color, icon: Icon }) {
  return (
    <div className="card-flush p-4 flex flex-col gap-2 relative overflow-hidden">
      <div className="absolute inset-0 opacity-[0.03]"
        style={{ background: `radial-gradient(circle at 80% 50%, ${color} 0%, transparent 70%)` }} />
      <div className="flex items-center justify-between relative z-10">
        <span className="text-[11px] text-slate-600 uppercase tracking-wide font-medium">{label}</span>
        <div className="w-6 h-6 rounded-md flex items-center justify-center"
          style={{ background: `${color}15`, border: `1px solid ${color}25` }}>
          <Icon size={12} style={{ color }} />
        </div>
      </div>
      <p className="text-2xl font-bold font-mono tabular-nums relative z-10"
        style={{ color }}>{value}</p>
      {sub && <p className="text-[10px] text-slate-600 relative z-10">{sub}</p>}
    </div>
  )
}

function CountdownRing({ cd, total }) {
  const r   = 14
  const circ = 2 * Math.PI * r
  const pct  = cd / total
  const dash = circ * pct
  const color = pct > 0.5 ? '#10b981' : pct > 0.2 ? '#f59e0b' : '#ef4444'
  return (
    <div className="relative flex items-center justify-center w-10 h-10">
      <svg className="absolute" width="40" height="40" style={{ transform: 'rotate(-90deg)' }}>
        <circle cx="20" cy="20" r={r} fill="none" stroke="rgba(255,255,255,0.05)" strokeWidth="2" />
        <circle cx="20" cy="20" r={r} fill="none" stroke={color} strokeWidth="2"
          strokeDasharray={`${dash} ${circ}`}
          style={{ transition: 'stroke-dasharray 1s linear, stroke 0.5s ease' }} />
      </svg>
      <span className="text-[10px] font-bold font-mono" style={{ color }}>{cd}</span>
    </div>
  )
}

const ACTIONS = [
  { id: 'accounts',     label: 'New Account',  sub: 'Create wallet',        icon: Wallet,     from: '#6366f1', to: '#8b5cf6' },
  { id: 'fx',           label: 'FX Quote',     sub: 'Lock live rate',       icon: TrendingUp, from: '#10b981', to: '#0ea5e9' },
  { id: 'transactions', label: 'Send Payment', sub: 'Saga payment',         icon: Send,       from: '#3b82f6', to: '#6366f1' },
  { id: 'payouts',      label: 'Bank Payout',  sub: 'NACH · SEPA · SWIFT',  icon: Landmark,   from: '#f59e0b', to: '#ef4444' },
]

const FLOW = [
  { n: 1, title: 'Create accounts',  desc: 'Multi-currency wallets per owner',  page: 'accounts' },
  { n: 2, title: 'Credit balance',   desc: 'Top up via credit operation',       page: 'accounts' },
  { n: 3, title: 'Get FX quote',     desc: 'Lock rate — valid 30 seconds',      page: 'fx' },
  { n: 4, title: 'Send payment',     desc: 'Saga: debit → validate → credit',   page: 'transactions' },
  { n: 5, title: 'Audit ledger',     desc: 'Double-entry journal entries',      page: 'ledger' },
  { n: 6, title: 'Initiate payout',  desc: 'Withdraw to bank account',          page: 'payouts' },
]

export default function Dashboard({ onNavigate }) {
  const [health,  setHealth]  = useState(null)
  const [loading, setLoading] = useState(false)
  const [cd,      setCd]      = useState(REFRESH)
  const timerRef              = useRef(null)

  const load = useCallback(async () => {
    setLoading(true)
    try { setHealth(await api.health()) } catch { setHealth(null) }
    setLoading(false)
    setCd(REFRESH)
  }, [])

  useEffect(() => { load() }, [load])

  useEffect(() => {
    timerRef.current = setInterval(() => {
      setCd(c => { if (c <= 1) { load(); return REFRESH } return c - 1 })
    }, 1000)
    return () => clearInterval(timerRef.current)
  }, [load])

  const svcs    = health?.services ?? []
  const upCount = svcs.filter(s => s.healthy).length
  const allUp   = svcs.length > 0 && upCount === svcs.length
  const avgMs   = svcs.length
    ? Math.round(svcs.reduce((a, s) => a + (parseInt(s.latency) || 0), 0) / svcs.length)
    : 0

  return (
    <div className="page">

      {/* Header */}
      <div className="ph">
        <div className="flex items-center gap-4">
          <div>
            <h1 className="text-sm font-bold text-slate-100">Payment Platform</h1>
            <p className="text-[11px] text-slate-600 mt-0.5">Multi-currency · Event-driven · Saga payments</p>
          </div>
          {svcs.length > 0 && (
            <div className={`hidden sm:flex items-center gap-1.5 text-[11px] font-medium
                             px-2.5 py-1 rounded-full border transition-all duration-500
                             ${allUp
                               ? 'bg-emerald-950/50 border-emerald-700/30 text-emerald-400'
                               : 'bg-amber-950/50 border-amber-700/30 text-amber-400'}`}>
              <div className={allUp ? 'dot-green' : 'dot-amber'}
                style={{ width: 6, height: 6, boxShadow: allUp ? '0 0 6px rgba(16,185,129,.8)' : 'none' }} />
              {upCount}/{svcs.length} operational
            </div>
          )}
        </div>
        <div className="flex items-center gap-3">
          <CountdownRing cd={cd} total={REFRESH} />
          <button onClick={load} disabled={loading} className="btn-ghost py-1">
            <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
            <span className="hidden sm:inline">Refresh</span>
          </button>
        </div>
      </div>

      {/* Body */}
      <div className="flex-1 overflow-auto p-5 space-y-4">

        {/* Stat row */}
        {svcs.length > 0 && (
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
            <StatCard label="Services"  value={svcs.length}         sub="registered"       color="#6366f1" icon={Activity} />
            <StatCard label="Healthy"   value={`${upCount}/${svcs.length}`} sub={allUp ? 'all good' : `${svcs.length - upCount} degraded`} color={allUp ? '#10b981' : '#f59e0b'} icon={CheckCircle} />
            <StatCard label="Avg Latency" value={avgMs > 0 ? `${avgMs}ms` : '—'} sub="across services"  color={avgMs < 80 ? '#10b981' : '#f59e0b'} icon={Clock} />
            <StatCard label="Platform"  value={allUp ? 'GO' : 'DEG'} sub={allUp ? 'fully operational' : 'check services'} color={allUp ? '#10b981' : '#ef4444'} icon={Zap} />
          </div>
        )}

        {/* Quick actions */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
          {ACTIONS.map((a, i) => {
            const Icon = a.icon
            return (
              <button
                key={a.id}
                onClick={() => onNavigate(a.id)}
                className="text-left rounded-xl p-4 group transition-all duration-200
                           hover:scale-[1.02] hover:-translate-y-0.5 active:scale-[0.98] overflow-hidden relative"
                style={{
                  background: `linear-gradient(135deg, ${a.from}12 0%, ${a.to}08 100%)`,
                  border: `1px solid ${a.from}20`,
                  animation: `fade-up 0.35s ease-out ${i * 0.06}s both`,
                }}
              >
                {/* Hover shimmer */}
                <div className="absolute inset-0 opacity-0 group-hover:opacity-100 transition-opacity duration-300"
                  style={{ background: `linear-gradient(135deg, ${a.from}08 0%, transparent 100%)` }} />

                <div className="relative z-10">
                  <div className="w-8 h-8 rounded-lg flex items-center justify-center mb-3 transition-transform duration-200 group-hover:scale-110"
                    style={{ background: `${a.from}15`, border: `1px solid ${a.from}25` }}>
                    <Icon size={15} style={{ color: a.from }} />
                  </div>
                  <p className="text-sm font-semibold text-slate-200 leading-tight">{a.label}</p>
                  <p className="text-[11px] text-slate-600 mt-0.5">{a.sub}</p>
                </div>

                {/* Corner dot */}
                <div className="absolute top-3 right-3 w-1.5 h-1.5 rounded-full opacity-30
                                group-hover:opacity-100 transition-opacity duration-200"
                  style={{ backgroundColor: a.from }} />
              </button>
            )
          })}
        </div>

        {/* Lower panels */}
        <div className="grid grid-cols-1 md:grid-cols-5 gap-4 min-h-0" style={{ height: 'calc(100% - 240px)', minHeight: 280 }}>

          {/* Service health */}
          <div className="md:col-span-3 card-flush flex flex-col overflow-hidden">
            <div className="flex items-center justify-between px-4 py-3 border-b border-slate-800/60 flex-shrink-0">
              <div className="flex items-center gap-2">
                <Activity size={12} className="text-indigo-400" />
                <span className="text-xs font-semibold text-slate-400 uppercase tracking-wide">Service Health</span>
              </div>
              {svcs.length > 0 && (
                <div className="flex items-center gap-3 text-[11px] font-mono">
                  <span className="text-emerald-400">{upCount} up</span>
                  {svcs.length - upCount > 0 && <span className="text-red-400">{svcs.length - upCount} down</span>}
                </div>
              )}
            </div>
            <div className="flex-1 overflow-auto px-4">
              {loading && svcs.length === 0
                ? [1,2,3,4,5,6].map(i => <SvcSkeleton key={i} i={i} />)
                : svcs.length > 0
                  ? svcs.map((s, i) => <SvcRow key={s.name} svc={s} index={i} />)
                  : (
                    <div className="flex flex-col items-center justify-center h-full py-8 gap-2">
                      <div className="w-10 h-10 rounded-full bg-slate-800/60 flex items-center justify-center">
                        <Activity size={18} className="text-slate-600" />
                      </div>
                      <p className="text-sm text-slate-600">Backend unreachable</p>
                      <p className="text-xs text-slate-700 mb-1">Is the stack running?</p>
                      <button onClick={load} disabled={loading}
                        className="flex items-center gap-1.5 text-xs text-indigo-400 hover:text-indigo-300
                                   bg-indigo-950/40 border border-indigo-800/40 px-3 py-1.5 rounded-lg
                                   transition-all duration-150 hover:bg-indigo-950/60 disabled:opacity-50">
                        <RefreshCw size={10} className={loading ? 'animate-spin' : ''} />
                        Retry
                      </button>
                    </div>
                  )
              }
            </div>
          </div>

          {/* E2E flow */}
          <div className="md:col-span-2 card-flush flex flex-col overflow-hidden">
            <div className="flex items-center gap-2 px-4 py-3 border-b border-slate-800/60 flex-shrink-0">
              <BookOpen size={12} className="text-indigo-400" />
              <span className="text-xs font-semibold text-slate-400 uppercase tracking-wide">End-to-End Flow</span>
            </div>
            <div className="flex-1 overflow-auto px-3 py-2">
              {FLOW.map((step, i) => (
                <button
                  key={step.n}
                  onClick={() => onNavigate(step.page)}
                  className="w-full flex gap-3 text-left px-2 py-2 rounded-lg
                             hover:bg-slate-800/40 transition-all duration-150 group"
                  style={{ animation: `fade-up 0.3s ease-out ${i * 0.05}s both` }}
                >
                  <div className="flex flex-col items-center flex-shrink-0">
                    <div className="w-5 h-5 rounded-full flex items-center justify-center transition-all duration-200
                                    bg-slate-800/80 border border-slate-700
                                    group-hover:border-indigo-500/50 group-hover:bg-indigo-950/60">
                      <span className="text-[9px] font-bold text-slate-500 group-hover:text-indigo-400 transition-colors">{step.n}</span>
                    </div>
                    {i < FLOW.length - 1 && (
                      <div className="w-px flex-1 mt-1 min-h-[10px]"
                        style={{ background: 'linear-gradient(180deg, rgba(99,102,241,0.15) 0%, transparent 100%)' }} />
                    )}
                  </div>
                  <div className="pb-2 min-w-0 flex-1">
                    <p className="text-xs font-medium text-slate-400 group-hover:text-slate-100 transition-colors leading-tight">{step.title}</p>
                    <p className="text-[10px] text-slate-700 mt-0.5 leading-snug">{step.desc}</p>
                  </div>
                </button>
              ))}
            </div>
          </div>

        </div>
      </div>
    </div>
  )
}
