import { useEffect, useState } from 'react'
import { Landmark, RefreshCw, Send, Search, CheckCircle, Clock, XCircle, Copy, Zap } from 'lucide-react'
import { api, genKey } from '../api/client.js'
import Toast from '../components/Toast.jsx'

const RAILS      = ['NACH', 'SEPA', 'SWIFT']
const CURRENCIES = ['INR', 'USD', 'EUR', 'GBP', 'SGD', 'AED']

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const IFSC_RE = /^[A-Z]{4}0[A-Z0-9]{6}$/i
const IBAN_RE = /^[A-Z]{2}[0-9]{2}[A-Z0-9]{1,30}$/i
const BIC_RE  = /^[A-Z]{4}[A-Z]{2}[A-Z0-9]{2}([A-Z0-9]{3})?$/i

const RAIL_STYLE = {
  NACH:  { color: '#818cf8', bg: 'rgba(99,102,241,0.1)',  border: 'rgba(99,102,241,0.25)' },
  SEPA:  { color: '#34d399', bg: 'rgba(52,211,153,0.1)',  border: 'rgba(52,211,153,0.25)' },
  SWIFT: { color: '#fbbf24', bg: 'rgba(251,191,36,0.1)',  border: 'rgba(251,191,36,0.25)' },
}

const STATUS = {
  PAYOUT_STATUS_PROCESSING: {
    label: 'Processing', icon: RefreshCw,
    cls: 'text-amber-400 bg-amber-950/80 border-amber-700/40',
  },
  PAYOUT_STATUS_SETTLED: {
    label: 'Settled', icon: CheckCircle,
    cls: 'text-emerald-400 bg-emerald-950/80 border-emerald-700/40',
  },
  PAYOUT_STATUS_FAILED: {
    label: 'Failed', icon: XCircle,
    cls: 'text-red-400 bg-red-950/80 border-red-700/40',
  },
  PAYOUT_STATUS_PENDING: {
    label: 'Pending', icon: Clock,
    cls: 'text-slate-400 bg-slate-800/80 border-slate-700',
  },
}

function Badge({ status }) {
  const m = STATUS[status] ?? { label: status, icon: Clock, cls: 'text-slate-400 bg-slate-800 border-slate-700' }
  const Icon = m.icon
  const isProcessing = status === 'PAYOUT_STATUS_PROCESSING'
  return (
    <span className={`inline-flex items-center gap-1 text-[11px] px-2 py-0.5 rounded-full font-medium border ${m.cls}`}>
      <Icon size={9} className={isProcessing ? 'animate-spin' : ''} /> {m.label}
    </span>
  )
}

function RailBadge({ rail }) {
  const key   = rail?.replace('PAYOUT_RAIL_', '') ?? '—'
  const style = RAIL_STYLE[key] ?? { color: '#64748b', bg: 'rgba(100,116,139,0.1)', border: 'rgba(100,116,139,0.2)' }
  return (
    <span className="text-[11px] font-bold font-mono px-2 py-0.5 rounded"
      style={{ color: style.color, background: style.bg, border: `1px solid ${style.border}` }}>
      {key}
    </span>
  )
}

function PayoutRow({ payout, onRefresh, index }) {
  const [copied,  setCopied]  = useState(false)
  const [loading, setLoading] = useState(false)
  const isActive = payout.status === 'PAYOUT_STATUS_PROCESSING' || payout.status === 'PAYOUT_STATUS_PENDING'
  const isSettled = payout.status === 'PAYOUT_STATUS_SETTLED'

  const copy    = () => { navigator.clipboard.writeText(payout.id); setCopied(true); setTimeout(() => setCopied(false), 1500) }
  const refresh = async () => { setLoading(true); try { await onRefresh(payout.id) } catch {}; setLoading(false) }

  return (
    <tr style={{
      background: isActive ? 'rgba(245,158,11,0.02)' : 'transparent',
      animation: `fade-up 0.25s ease-out ${index * 0.04}s both`,
    }}>
      <td className="pl-4">
        <div className="flex items-center gap-1.5">
          <span className="font-mono text-[11px] text-slate-500">{payout.id?.slice(0, 12)}…</span>
          <button onClick={copy} className="text-slate-700 hover:text-slate-400 transition-colors">
            <Copy size={10} />
          </button>
          {copied && <span className="text-[10px] text-emerald-400 anim-fade-in">✓</span>}
        </div>
        <p className="text-[10px] text-slate-700 font-mono mt-0.5">{payout.account_id?.slice(0, 14)}…</p>
      </td>

      <td><RailBadge rail={payout.rail} /></td>

      <td className="text-right">
        <span className="font-mono font-bold tabular-nums"
          style={{ color: isSettled ? '#818cf8' : '#64748b' }}>
          {payout.amount?.amount}
        </span>
        <span className="text-[11px] text-slate-700 ml-1">{payout.amount?.currency}</span>
      </td>

      <td>
        {payout.destination && (
          <div className="text-[11px] text-slate-600">
            {payout.destination.account_name && (
              <p className="font-medium text-slate-400">{payout.destination.account_name}</p>
            )}
            {payout.destination.account_number && (
              <p className="font-mono">{payout.destination.account_number}</p>
            )}
            {payout.destination.iban && (
              <p className="font-mono">{payout.destination.iban?.slice(0, 16)}…</p>
            )}
          </div>
        )}
      </td>

      <td><Badge status={payout.status} /></td>

      <td className="hidden xl:table-cell text-[11px] text-slate-700 whitespace-nowrap">
        {payout.created_at
          ? new Date(payout.created_at).toLocaleString('en-IN', { dateStyle: 'short', timeStyle: 'short' })
          : '—'}
      </td>

      <td className="pr-4">
        {isActive && (
          <button onClick={refresh} disabled={loading} className="btn-ghost py-1 px-2 text-[10px]">
            <RefreshCw size={10} className={loading ? 'animate-spin' : ''} />
          </button>
        )}
      </td>
    </tr>
  )
}

export default function Payouts() {
  const [form, setForm] = useState({
    account_id: '', amount: '', currency: 'INR', rail: 'NACH',
    account_number: '', ifsc_code: '', iban: '', bic: '', account_name: '',
  })
  const [lookupID, setLookupID] = useState('')
  const [payouts,  setPayouts]  = useState([])
  const [loading,  setLoading]  = useState(false)
  const [toast,    setToast]    = useState(null)

  const refreshPayout = async (id) => {
    const r = await api.getPayout(id)
    setPayouts(prev => prev.map(p => p.id === r.payout.id ? r.payout : p))
  }

  useEffect(() => {
    const active = payouts.filter(p =>
      p.status === 'PAYOUT_STATUS_PROCESSING' || p.status === 'PAYOUT_STATUS_PENDING'
    )
    if (active.length === 0) return
    const t = setTimeout(() => Promise.allSettled(active.map(p => refreshPayout(p.id))), 5000)
    return () => clearTimeout(t)
  }, [payouts])

  const send = async (e) => {
    e.preventDefault(); setLoading(true)
    try {
      const r = await api.initiatePayout({
        idempotency_key: genKey(),
        account_id: form.account_id,
        amount: form.amount,
        currency: form.currency,
        rail: form.rail,
        account_number: form.account_number,
        ifsc_code: form.ifsc_code,
        iban: form.iban,
        bic: form.bic,
        account_name: form.account_name,
      })
      setPayouts(p => [r.payout, ...p].slice(0, 20))
      setToast({ msg: 'Payout initiated', type: 'success' })
    } catch (ex) { setToast({ msg: ex.message, type: 'error' }) }
    setLoading(false)
  }

  const lookup = async (e) => {
    e.preventDefault(); if (!lookupID.trim()) return; setLoading(true)
    try {
      const r = await api.getPayout(lookupID.trim())
      setPayouts(prev => {
        const w = prev.filter(x => x.id !== r.payout.id)
        return [r.payout, ...w].slice(0, 20)
      })
    } catch (ex) { setToast({ msg: ex.message, type: 'error' }) }
    setLoading(false)
  }

  const isNACH = form.rail === 'NACH'
  const isSEPA = form.rail === 'SEPA' || form.rail === 'SWIFT'

  const idValid   = UUID_RE.test(form.account_id)
  const ifscValid = !isNACH || IFSC_RE.test(form.ifsc_code)
  const ibanValid = !isSEPA || IBAN_RE.test(form.iban)
  const bicValid  = !isSEPA || BIC_RE.test(form.bic)
  const formValid = idValid && ifscValid && ibanValid && bicValid
  const hasActive = payouts.some(p =>
    p.status === 'PAYOUT_STATUS_PROCESSING' || p.status === 'PAYOUT_STATUS_PENDING'
  )
  const settled = payouts.filter(p => p.status === 'PAYOUT_STATUS_SETTLED').length

  const currentRailStyle = RAIL_STYLE[form.rail] ?? RAIL_STYLE.NACH

  return (
    <div className="page">
      {toast && <Toast message={toast.msg} type={toast.type} onDone={() => setToast(null)} />}

      <div className="ph">
        <div>
          <h1 className="text-sm font-bold text-slate-100">Payouts</h1>
          <p className="text-[11px] text-slate-600 mt-0.5">Bank withdrawals via NACH · SEPA · SWIFT</p>
        </div>
        <div className="flex items-center gap-2">
          {settled > 0 && (
            <span className="hidden sm:flex items-center gap-1 text-[11px] text-emerald-400 bg-emerald-950/40 border border-emerald-800/30 px-2.5 py-1 rounded-full">
              <CheckCircle size={10} />
              {settled} settled
            </span>
          )}
          {hasActive && (
            <span className="flex items-center gap-1.5 text-[11px] text-amber-400 bg-amber-950/40 border border-amber-700/30 rounded-full px-2.5 py-1">
              <span className="w-1.5 h-1.5 rounded-full bg-amber-500 animate-pulse" />
              Processing…
            </span>
          )}
        </div>
      </div>

      <div className="flex-1 overflow-hidden flex">

        {/* Left — forms */}
        <div className="w-80 flex-shrink-0 border-r border-slate-800/80 overflow-auto">

          {/* Initiate form */}
          <div className="p-5 border-b border-slate-800/60">
            <p className="text-[10px] font-semibold text-slate-500 uppercase tracking-widest mb-4 flex items-center gap-2">
              <Landmark size={11} className="text-indigo-400" /> Initiate Payout
            </p>

            {/* Rail selector — visual */}
            <div className="flex gap-1.5 mb-4">
              {RAILS.map(r => {
                const rs = RAIL_STYLE[r]
                const active = form.rail === r
                return (
                  <button
                    key={r}
                    type="button"
                    onClick={() => setForm({...form, rail: r})}
                    className="flex-1 py-1.5 rounded-lg text-[11px] font-bold transition-all duration-200"
                    style={active ? {
                      color: rs.color, background: rs.bg, border: `1px solid ${rs.border}`,
                    } : {
                      color: '#475569', background: 'rgba(30,33,50,0.4)', border: '1px solid rgba(51,65,85,0.4)',
                    }}
                  >
                    {r}
                  </button>
                )
              })}
            </div>

            <form onSubmit={send} className="space-y-3">
              <div>
                <label>Account ID</label>
                <input value={form.account_id}
                  onChange={e => setForm({...form, account_id: e.target.value})}
                  placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" required
                  className={`font-mono text-xs ${form.account_id && !idValid ? 'border-red-700/60' : ''}`} />
                {form.account_id && !idValid && (
                  <p className="mt-1 text-[10px] text-red-400">Must be a valid account UUID — copy from the Accounts page</p>
                )}
              </div>

              <div className="grid grid-cols-3 gap-2">
                <div className="col-span-2">
                  <label>Amount</label>
                  <input value={form.amount}
                    onChange={e => setForm({...form, amount: e.target.value})}
                    placeholder="1000.00" required />
                </div>
                <div>
                  <label>Currency</label>
                  <select value={form.currency} onChange={e => setForm({...form, currency: e.target.value})}>
                    {CURRENCIES.map(c => <option key={c}>{c}</option>)}
                  </select>
                </div>
              </div>

              <div>
                <label>Beneficiary Name</label>
                <input value={form.account_name}
                  onChange={e => setForm({...form, account_name: e.target.value})}
                  placeholder="John Doe" required />
              </div>

              {isNACH && (
                <>
                  <div>
                    <label>Account Number</label>
                    <input value={form.account_number}
                      onChange={e => setForm({...form, account_number: e.target.value})}
                      placeholder="123456789012" className="font-mono text-xs" />
                  </div>
                  <div>
                    <label>IFSC Code</label>
                    <input value={form.ifsc_code}
                      onChange={e => setForm({...form, ifsc_code: e.target.value.toUpperCase()})}
                      placeholder="HDFC0001234"
                      className={`font-mono text-xs ${form.ifsc_code && !ifscValid ? 'border-red-700/60' : ''}`} />
                    {form.ifsc_code && !ifscValid && (
                      <p className="mt-1 text-[10px] text-red-400">Format: 4 letters + 0 + 6 alphanumeric (e.g. HDFC0001234)</p>
                    )}
                  </div>
                </>
              )}

              {isSEPA && (
                <>
                  <div>
                    <label>IBAN</label>
                    <input value={form.iban}
                      onChange={e => setForm({...form, iban: e.target.value.toUpperCase().replace(/\s/g, '')})}
                      placeholder="DE89370400440532013000"
                      className={`font-mono text-xs ${form.iban && !ibanValid ? 'border-red-700/60' : ''}`} />
                    {form.iban && !ibanValid && (
                      <p className="mt-1 text-[10px] text-red-400">Invalid IBAN — should start with 2-letter country code</p>
                    )}
                  </div>
                  <div>
                    <label>BIC / SWIFT</label>
                    <input value={form.bic}
                      onChange={e => setForm({...form, bic: e.target.value.toUpperCase()})}
                      placeholder="COBADEFFXXX"
                      className={`font-mono text-xs ${form.bic && !bicValid ? 'border-red-700/60' : ''}`} />
                    {form.bic && !bicValid && (
                      <p className="mt-1 text-[10px] text-red-400">BIC must be 8 or 11 characters (e.g. COBADEFFXXX)</p>
                    )}
                  </div>
                </>
              )}

              <button type="submit" disabled={loading || !formValid} className="btn-primary w-full justify-center"
                style={{ background: `linear-gradient(135deg, ${currentRailStyle.color}cc 0%, ${currentRailStyle.color}99 100%)` }}>
                {loading ? <RefreshCw size={11} className="animate-spin" /> : <Send size={11} />}
                Send via {form.rail}
              </button>
            </form>
          </div>

          {/* Lookup */}
          <div className="p-5">
            <p className="text-[10px] font-semibold text-slate-500 uppercase tracking-widest mb-4 flex items-center gap-2">
              <Search size={11} className="text-indigo-400" /> Look Up
            </p>
            <form onSubmit={lookup} className="flex gap-2">
              <input className="flex-1 font-mono text-xs" value={lookupID}
                onChange={e => setLookupID(e.target.value)} placeholder="payout uuid" />
              <button type="submit" disabled={loading} className="btn-primary flex-shrink-0">
                {loading ? <RefreshCw size={11} className="animate-spin" /> : <Search size={11} />}
              </button>
            </form>
          </div>
        </div>

        {/* Right — table */}
        <div className="flex-1 overflow-auto">
          {payouts.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-full min-h-[300px]">
              <div className="relative mb-5">
                <div className="relative w-16 h-16 rounded-2xl bg-slate-800/80 border border-slate-700/40
                                flex items-center justify-center anim-float">
                  <Landmark size={24} className="text-slate-600" />
                </div>
              </div>
              <p className="text-sm font-medium text-slate-400 mb-1">Payouts appear here</p>
              <p className="text-xs text-slate-600">Processing payouts auto-refresh every 5 s</p>
            </div>
          ) : (
            <table className="tbl">
              <thead>
                <tr>
                  <th className="pl-4">ID / Account</th>
                  <th>Rail</th>
                  <th className="text-right">Amount</th>
                  <th>Beneficiary</th>
                  <th>Status</th>
                  <th className="hidden xl:table-cell">Date</th>
                  <th className="pr-4" />
                </tr>
              </thead>
              <tbody>
                {payouts.map((p, i) => (
                  <PayoutRow key={p.id} payout={p} index={i} onRefresh={refreshPayout} />
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  )
}
