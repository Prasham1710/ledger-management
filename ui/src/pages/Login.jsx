import { useState } from 'react'
import { Zap, Lock, ArrowRight, ShieldCheck, Globe, Layers, BarChart3, Cpu } from 'lucide-react'

const FEATURES = [
  { icon: Layers,     color: '#818cf8', label: 'Double-entry bookkeeping',     desc: 'Append-only journal' },
  { icon: Globe,      color: '#34d399', label: 'Multi-currency wallets',       desc: 'INR · USD · EUR · GBP' },
  { icon: Cpu,        color: '#60a5fa', label: 'Idempotent payment sagas',     desc: 'At-least-once delivery' },
  { icon: BarChart3,  color: '#f59e0b', label: 'FX exchange & rate lock',      desc: '30-second rate window' },
  { icon: ShieldCheck,color: '#a78bfa', label: 'NACH · SEPA · SWIFT payouts', desc: 'Bank withdrawals' },
]

export default function Login({ onLogin }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error,    setError]    = useState('')
  const [loading,  setLoading]  = useState(false)

  async function handleSubmit(e) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res  = await fetch('/auth/login', {
        method:  'POST',
        headers: { 'Content-Type': 'application/json' },
        body:    JSON.stringify({ username, password }),
      })
      const data = await res.json()
      if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`)
      onLogin(data.token)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="h-full flex bg-[#07080f] overflow-hidden">

      {/* ── Left panel ── */}
      <div className="hidden lg:flex flex-col w-[440px] flex-shrink-0 relative overflow-hidden"
        style={{ background: 'linear-gradient(145deg, #0c0e1a 0%, #080a13 100%)' }}>

        {/* Animated dot grid */}
        <div className="absolute inset-0 pointer-events-none" style={{
          backgroundImage: 'radial-gradient(circle, rgba(99,102,241,0.12) 1px, transparent 1px)',
          backgroundSize: '28px 28px',
        }} />

        {/* Glow blobs */}
        <div className="absolute top-0 left-0 w-96 h-96 rounded-full opacity-[0.07] pointer-events-none"
          style={{ background: 'radial-gradient(circle, #6366f1 0%, transparent 70%)', transform: 'translate(-30%, -30%)' }} />
        <div className="absolute bottom-0 right-0 w-80 h-80 rounded-full opacity-[0.06] pointer-events-none"
          style={{ background: 'radial-gradient(circle, #8b5cf6 0%, transparent 70%)', transform: 'translate(20%, 20%)' }} />

        {/* Border right line */}
        <div className="absolute right-0 inset-y-0 w-px"
          style={{ background: 'linear-gradient(180deg, transparent 0%, rgba(99,102,241,.2) 30%, rgba(99,102,241,.2) 70%, transparent 100%)' }} />

        <div className="relative z-10 flex flex-col h-full p-10">

          {/* Logo */}
          <div className="flex items-center gap-3 mb-12">
            <div className="relative">
              <div className="absolute inset-0 rounded-xl bg-indigo-500 blur-md opacity-40" />
              <div className="relative w-10 h-10 rounded-xl bg-gradient-to-br from-indigo-500 to-violet-600
                              flex items-center justify-center shadow-xl">
                <Zap size={18} className="text-white" />
              </div>
            </div>
            <div>
              <p className="text-lg font-bold text-slate-50 leading-tight">Ledger</p>
              <p className="text-[10px] text-slate-600 tracking-widest uppercase">Payment Platform</p>
            </div>
          </div>

          {/* Headline */}
          <div className="mb-10">
            <h2 className="text-3xl font-bold text-slate-50 leading-tight mb-3 text-balance">
              Multi-currency<br />
              <span style={{
                background: 'linear-gradient(135deg, #818cf8 0%, #38bdf8 100%)',
                WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent',
              }}>payment infrastructure</span>
            </h2>
            <p className="text-sm text-slate-500 leading-relaxed">
              Event-driven, saga-based payments with double-entry
              bookkeeping and real-time FX settlement.
            </p>
          </div>

          {/* Features */}
          <div className="space-y-3 flex-1">
            {FEATURES.map((f, i) => {
              const Icon = f.icon
              return (
                <div
                  key={f.label}
                  className="flex items-center gap-3 group"
                  style={{ animation: `fade-up 0.4s ease-out ${i * 0.07}s both` }}
                >
                  <div className="w-7 h-7 rounded-lg flex items-center justify-center flex-shrink-0"
                    style={{ background: `${f.color}18`, border: `1px solid ${f.color}30` }}>
                    <Icon size={13} style={{ color: f.color }} />
                  </div>
                  <div>
                    <p className="text-xs font-medium text-slate-300">{f.label}</p>
                    <p className="text-[10px] text-slate-600">{f.desc}</p>
                  </div>
                </div>
              )
            })}
          </div>

          {/* Footer */}
          <div className="flex items-center gap-2 mt-8">
            <div className="flex gap-1.5">
              {['JWT', 'Rate Limited', 'Audit Log'].map(t => (
                <span key={t} className="text-[9px] text-slate-600 bg-slate-800/60 border border-slate-700/40
                                         px-1.5 py-0.5 rounded tracking-wide uppercase">
                  {t}
                </span>
              ))}
            </div>
          </div>
        </div>
      </div>

      {/* ── Right panel — form ── */}
      <div className="flex-1 flex items-center justify-center p-8 relative">

        {/* Subtle bg pattern */}
        <div className="absolute inset-0 pointer-events-none opacity-30" style={{
          backgroundImage: 'radial-gradient(circle, rgba(99,102,241,0.06) 1px, transparent 1px)',
          backgroundSize: '40px 40px',
        }} />

        <div className="relative w-full max-w-[340px] anim-scale-in">

          {/* Mobile logo */}
          <div className="flex items-center gap-3 mb-8 lg:hidden">
            <div className="w-9 h-9 rounded-xl bg-gradient-to-br from-indigo-500 to-violet-600
                            flex items-center justify-center shadow-lg shadow-indigo-900/50">
              <Zap size={16} className="text-white" />
            </div>
            <div>
              <p className="text-base font-bold text-slate-100">Ledger</p>
              <p className="text-xs text-slate-500">Payment Platform</p>
            </div>
          </div>

          <div className="mb-7">
            <h1 className="text-2xl font-bold text-slate-50 mb-1">Welcome back</h1>
            <p className="text-sm text-slate-500">Sign in to your dashboard</p>
          </div>

          {/* Form card */}
          <div className="rounded-2xl border border-slate-800/60 p-6"
            style={{
              background: 'linear-gradient(145deg, #111420 0%, #0d1018 100%)',
              boxShadow: '0 20px 60px rgba(0,0,0,.5), 0 0 0 1px rgba(99,102,241,.06)',
            }}>

            <form onSubmit={handleSubmit} className="space-y-4">
              {error && (
                <div className="flex items-center gap-2 text-sm bg-red-950/40 border border-red-800/40
                                rounded-lg px-3 py-2.5 anim-fade-up">
                  <div className="w-1.5 h-1.5 rounded-full bg-red-500 flex-shrink-0" />
                  <span className="text-red-300 text-xs">{error}</span>
                </div>
              )}

              <div>
                <label>Username</label>
                <div className="relative">
                  <input
                    type="text"
                    required
                    autoFocus
                    autoComplete="username"
                    value={username}
                    onChange={e => setUsername(e.target.value)}
                    placeholder="admin"
                    className="pl-9"
                  />
                  <div className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-600">
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                      <circle cx="12" cy="8" r="4"/><path d="M4 20c0-4 3.6-7 8-7s8 3 8 7"/>
                    </svg>
                  </div>
                </div>
              </div>

              <div>
                <div className="mb-1.5">
                  <label>Password</label>
                </div>
                <div className="relative">
                  <input
                    type="password"
                    required
                    autoComplete="current-password"
                    value={password}
                    onChange={e => setPassword(e.target.value)}
                    placeholder="••••••••"
                    className="pl-9"
                  />
                  <div className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-600">
                    <Lock size={13} />
                  </div>
                </div>
              </div>

              <button
                type="submit"
                disabled={loading}
                className="w-full flex items-center justify-center gap-2 py-2.5 rounded-lg
                           text-sm font-semibold transition-all duration-200
                           disabled:opacity-50 mt-1"
                style={{
                  background: loading ? '#3730a3' : 'linear-gradient(135deg, #6366f1 0%, #4f46e5 100%)',
                  boxShadow: '0 4px 20px rgba(99,102,241,0.35)',
                }}
              >
                {loading ? (
                  <>
                    <span className="w-3.5 h-3.5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    <span>Signing in…</span>
                  </>
                ) : (
                  <>
                    <Lock size={13} />
                    <span>Sign in</span>
                    <ArrowRight size={13} />
                  </>
                )}
              </button>
            </form>
          </div>

          <p className="text-center text-[11px] text-slate-700 mt-5">
            Secured by JWT · TLS in production
          </p>
        </div>
      </div>
    </div>
  )
}
