import { useEffect, useState } from 'react'
import { TrendingUp, ArrowRight, Lock, RefreshCw, Copy, Zap } from 'lucide-react'
import { api, genKey } from '../api/client.js'
import Toast from '../components/Toast.jsx'

const CURRENCIES = ['INR', 'USD', 'EUR', 'GBP', 'SGD', 'AED']

const PAIRS = [
  { from: 'USD', to: 'INR', rate: '83.5000' },
  { from: 'INR', to: 'USD', rate: '0.01198' },
  { from: 'USD', to: 'EUR', rate: '0.9200'  },
  { from: 'EUR', to: 'USD', rate: '1.0870'  },
  { from: 'USD', to: 'GBP', rate: '0.7860'  },
  { from: 'GBP', to: 'USD', rate: '1.2722'  },
  { from: 'EUR', to: 'INR', rate: '90.7200' },
  { from: 'GBP', to: 'INR', rate: '106.100' },
  { from: 'USD', to: 'SGD', rate: '1.3510'  },
  { from: 'USD', to: 'AED', rate: '3.6730'  },
  { from: 'AED', to: 'USD', rate: '0.2722'  },
  { from: 'INR', to: 'EUR', rate: '0.01102' },
  { from: 'INR', to: 'GBP', rate: '0.00943' },
  { from: 'SGD', to: 'USD', rate: '0.7402'  },
]

const TTL = 30

function CountdownRing({ expiresAt }) {
  const [remaining, setRemaining] = useState(TTL)

  useEffect(() => {
    const update = () => {
      const diff = Math.max(0, Math.ceil((new Date(expiresAt) - Date.now()) / 1000))
      setRemaining(diff)
    }
    update()
    const t = setInterval(update, 500)
    return () => clearInterval(t)
  }, [expiresAt])

  const pct   = remaining / TTL
  const r     = 13
  const circ  = 2 * Math.PI * r
  const dash  = circ * pct
  const color = pct > 0.5 ? '#10b981' : pct > 0.2 ? '#f59e0b' : '#ef4444'

  return (
    <div className="relative flex items-center justify-center w-9 h-9">
      <svg className="absolute" width="36" height="36" style={{ transform: 'rotate(-90deg)' }}>
        <circle cx="18" cy="18" r={r} fill="none" stroke="rgba(255,255,255,0.05)" strokeWidth="2.5" />
        <circle cx="18" cy="18" r={r} fill="none" stroke={color} strokeWidth="2.5"
          strokeLinecap="round"
          strokeDasharray={`${dash} ${circ}`}
          style={{ transition: 'stroke-dasharray 0.5s linear, stroke 0.5s ease' }} />
      </svg>
      <span className="text-[11px] font-bold font-mono tabular-nums" style={{ color }}>
        {remaining}
      </span>
    </div>
  )
}

function QuoteCard({ quote, onLock, onToast, index }) {
  const [loading, setLoading] = useState(false)
  const [copied,  setCopied]  = useState(false)
  const [justLocked, setJustLocked] = useState(false)
  const expired = new Date(quote.expires_at) < new Date()

  const copy = () => {
    navigator.clipboard.writeText(quote.id)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  const lock = async () => {
    setLoading(true)
    try {
      const res = await api.lockQuote(quote.id, genKey())
      if (res.locked) {
        setJustLocked(true)
        onToast('Rate locked successfully', 'success')
        setTimeout(() => onLock(quote.id), 600)
      } else {
        onToast('Quote expired or already locked', 'error')
      }
    } catch (ex) { onToast(ex.message, 'error') }
    setLoading(false)
  }

  const statusColor = quote.locked
    ? { border: 'rgba(16,185,129,0.3)', glow: 'rgba(16,185,129,0.06)', bar: '#10b981', badge: 'bg-emerald-950/80 border-emerald-700/40 text-emerald-400' }
    : expired
    ? { border: 'rgba(100,116,139,0.2)', glow: 'transparent', bar: '#334155', badge: 'bg-slate-800 border-slate-700 text-slate-500' }
    : { border: 'rgba(99,102,241,0.25)', glow: 'rgba(99,102,241,0.05)', bar: '#6366f1', badge: 'bg-indigo-950/80 border-indigo-700/40 text-indigo-400' }

  return (
    <div
      className={`relative rounded-xl border overflow-hidden transition-all duration-500
                  ${justLocked ? 'scale-[0.98]' : 'hover:scale-[1.01] hover:-translate-y-0.5'}`}
      style={{
        borderColor: statusColor.border,
        background: `linear-gradient(145deg, #111420 0%, #0d1018 100%)`,
        boxShadow: `0 8px 32px rgba(0,0,0,.4), 0 0 0 1px ${statusColor.border}`,
        animation: `fade-up 0.35s ease-out ${index * 0.08}s both`,
      }}
    >
      {/* Top status bar */}
      <div className="h-0.5 w-full" style={{ backgroundColor: statusColor.bar }} />

      {/* Glow bg */}
      <div className="absolute inset-0 pointer-events-none"
        style={{ background: `radial-gradient(circle at 50% -20%, ${statusColor.glow} 0%, transparent 60%)` }} />

      <div className="relative z-10 p-4">
        {/* Header row */}
        <div className="flex items-center justify-between mb-3">
          <div className="flex items-center gap-2">
            <span className="text-base font-bold text-slate-100">{quote.from_currency}</span>
            <ArrowRight size={13} className="text-slate-600" />
            <span className="text-base font-bold text-slate-100">{quote.to_currency}</span>
          </div>
          <div className="flex items-center gap-2">
            {!quote.locked && !expired && <CountdownRing expiresAt={quote.expires_at} />}
            <span className={`text-[10px] px-2 py-0.5 rounded-full border font-medium ${statusColor.badge}`}>
              {quote.locked ? '🔒 Locked' : expired ? 'Expired' : 'Live'}
            </span>
          </div>
        </div>

        {/* Amounts */}
        <div className="grid grid-cols-2 gap-2 mb-3">
          <div className="rounded-lg p-3" style={{ background: 'rgba(30,33,50,0.6)', border: '1px solid rgba(255,255,255,0.04)' }}>
            <p className="text-[10px] text-slate-600 mb-1">You send</p>
            <p className="text-lg font-bold font-mono tabular-nums text-slate-200">
              {quote.source_amount?.amount}
            </p>
            <p className="text-xs text-slate-600 mt-0.5">{quote.from_currency}</p>
          </div>
          <div className="rounded-lg p-3 relative overflow-hidden"
            style={{ background: 'rgba(30,33,50,0.6)', border: '1px solid rgba(99,102,241,.12)' }}>
            <div className="absolute inset-0 opacity-[0.04]"
              style={{ background: 'radial-gradient(circle at 80% 50%, #6366f1 0%, transparent 70%)' }} />
            <p className="text-[10px] text-slate-600 mb-1 relative z-10">They receive</p>
            <p className="text-lg font-bold font-mono tabular-nums text-indigo-400 relative z-10">
              {quote.target_amount?.amount}
            </p>
            <p className="text-xs text-slate-600 mt-0.5 relative z-10">{quote.to_currency}</p>
          </div>
        </div>

        {/* Meta */}
        <div className="space-y-1.5 text-xs mb-3">
          <div className="flex justify-between items-center">
            <span className="text-slate-600">Rate</span>
            <span className="font-mono text-slate-300 text-[11px]">
              1 {quote.from_currency} = <strong className="text-indigo-300">
                {parseFloat(quote.rate ?? 0).toFixed(4)}
              </strong> {quote.to_currency}
            </span>
          </div>
          <div className="flex justify-between items-center">
            <span className="text-slate-600">Quote ID</span>
            <div className="flex items-center gap-1.5">
              <span className="font-mono text-slate-500 text-[10px]">{quote.id?.slice(0, 10)}…</span>
              <button onClick={copy} className="text-slate-700 hover:text-slate-400 transition-colors">
                <Copy size={10} />
              </button>
              {copied && <span className="text-[10px] text-emerald-400 anim-fade-in">✓</span>}
            </div>
          </div>
        </div>

        {/* Lock button */}
        {!quote.locked && !expired && (
          <button onClick={lock} disabled={loading}
            className="w-full flex items-center justify-center gap-2 py-2 rounded-lg text-xs font-semibold
                       transition-all duration-200 active:scale-95 disabled:opacity-50"
            style={{
              background: 'linear-gradient(135deg, #4f46e5 0%, #6366f1 100%)',
              boxShadow: '0 4px 16px rgba(99,102,241,0.3)',
            }}>
            {loading
              ? <RefreshCw size={11} className="animate-spin" />
              : <Lock size={11} />}
            {loading ? 'Locking…' : 'Lock Rate'}
          </button>
        )}

        {quote.locked && (
          <div className="flex items-center justify-center gap-1.5 py-2 rounded-lg text-xs font-semibold
                          text-emerald-400 border border-emerald-800/40 bg-emerald-950/30">
            <Zap size={11} />
            Rate Locked
          </div>
        )}
      </div>
    </div>
  )
}

export default function FX() {
  const [form,    setForm]    = useState({ from: 'USD', to: 'INR', amount: '' })
  const [quotes,  setQuotes]  = useState([])
  const [loading, setLoading] = useState(false)
  const [toast,   setToast]   = useState(null)

  // auto-remove non-locked quotes that have been expired for >8 seconds
  useEffect(() => {
    if (quotes.length === 0) return
    const t = setInterval(() => {
      const cutoff = Date.now() - 8000
      setQuotes(q => q.filter(x => x.locked || new Date(x.expires_at).getTime() > cutoff))
    }, 2000)
    return () => clearInterval(t)
  }, [quotes.length])

  const getQuote = async (e) => {
    e.preventDefault()
    if (form.from === form.to) { setToast({ msg: 'Currencies must differ', type: 'error' }); return }
    const amt = parseFloat(form.amount)
    if (!amt || amt <= 0) { setToast({ msg: 'Amount must be a positive number', type: 'error' }); return }
    setLoading(true)
    try {
      const r = await api.getQuote(form.from, form.to, form.amount)
      setQuotes(q => [r.quote, ...q].slice(0, 9))
    } catch (ex) { setToast({ msg: ex.message, type: 'error' }) }
    setLoading(false)
  }

  const markLocked = (id) => setQuotes(q => q.map(x => x.id === id ? { ...x, locked: true } : x))

  const activeCount  = quotes.filter(q => !q.locked && new Date(q.expires_at) > new Date()).length
  const lockedCount  = quotes.filter(q => q.locked).length

  return (
    <div className="page">
      {toast && <Toast message={toast.msg} type={toast.type} onDone={() => setToast(null)} />}

      <div className="ph">
        <div>
          <h1 className="text-sm font-bold text-slate-100">FX Exchange</h1>
          <p className="text-[11px] text-slate-600 mt-0.5">Live quotes · 30-second rate lock</p>
        </div>
        {quotes.length > 0 && (
          <div className="flex items-center gap-3 text-[11px]">
            {activeCount > 0 && (
              <span className="flex items-center gap-1.5 text-indigo-400 bg-indigo-950/40 border border-indigo-700/30 px-2.5 py-1 rounded-full">
                <span className="w-1.5 h-1.5 rounded-full bg-indigo-400 animate-pulse" />
                {activeCount} live
              </span>
            )}
            {lockedCount > 0 && (
              <span className="flex items-center gap-1.5 text-emerald-400 bg-emerald-950/40 border border-emerald-700/30 px-2.5 py-1 rounded-full">
                <Lock size={10} />
                {lockedCount} locked
              </span>
            )}
          </div>
        )}
      </div>

      <div className="flex-1 overflow-hidden flex">

        {/* Left panel */}
        <div className="w-68 flex-shrink-0 border-r border-slate-800/80 flex flex-col overflow-hidden"
          style={{ width: 272 }}>

          {/* Quote form */}
          <div className="p-5 border-b border-slate-800/60 flex-shrink-0">
            <p className="text-[10px] font-semibold text-slate-500 uppercase tracking-widest mb-4 flex items-center gap-2">
              <TrendingUp size={11} className="text-indigo-400" /> Get Quote
            </p>
            <form onSubmit={getQuote} className="space-y-3">
              <div className="grid grid-cols-2 gap-2">
                <div>
                  <label>From</label>
                  <select value={form.from} onChange={e => setForm({...form, from: e.target.value})}>
                    {CURRENCIES.map(c => <option key={c}>{c}</option>)}
                  </select>
                </div>
                <div>
                  <label>To</label>
                  <select value={form.to} onChange={e => setForm({...form, to: e.target.value})}>
                    {CURRENCIES.map(c => <option key={c}>{c}</option>)}
                  </select>
                </div>
              </div>
              <div>
                <label>Amount</label>
                <input value={form.amount} onChange={e => setForm({...form, amount: e.target.value})}
                  placeholder="1000.00" required />
              </div>
              <button type="submit" disabled={loading} className="btn-primary w-full justify-center">
                {loading ? <RefreshCw size={11} className="animate-spin" /> : <TrendingUp size={11} />}
                Get Quote
              </button>
            </form>
          </div>

          {/* Reference rates */}
          <div className="flex-1 overflow-auto p-4">
            <p className="text-[9px] font-semibold text-slate-700 uppercase tracking-widest mb-3">
              Reference Rates
            </p>
            <div className="space-y-0">
              {PAIRS.map(p => (
                <div key={`${p.from}-${p.to}`}
                  className="flex items-center justify-between py-1.5 border-b border-slate-800/30 last:border-0
                             hover:bg-slate-800/20 px-1 rounded transition-colors cursor-default">
                  <span className="text-[11px] text-slate-600">
                    {p.from}
                    <ArrowRight size={9} className="inline text-slate-800 mx-1" />
                    {p.to}
                  </span>
                  <span className="text-[11px] font-mono tabular-nums text-slate-500">{p.rate}</span>
                </div>
              ))}
            </div>
          </div>
        </div>

        {/* Right panel — quotes */}
        <div className="flex-1 overflow-auto p-5">
          {quotes.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-full min-h-[300px]">
              <div className="relative mb-5">
                <div className="absolute inset-0 rounded-2xl bg-indigo-500/10 blur-xl" />
                <div className="relative w-16 h-16 rounded-2xl bg-slate-800/80 border border-slate-700/40
                                flex items-center justify-center anim-float">
                  <TrendingUp size={26} className="text-slate-600" />
                </div>
              </div>
              <p className="text-sm font-medium text-slate-400 mb-1">Quotes appear here</p>
              <p className="text-xs text-slate-600">Each quote is valid for 30 seconds</p>
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4 items-start">
              {quotes.map((q, i) => (
                <QuoteCard
                  key={q.id}
                  quote={q}
                  index={i}
                  onLock={markLocked}
                  onToast={(msg, type) => setToast({ msg, type })}
                />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
