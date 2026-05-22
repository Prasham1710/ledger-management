import { useEffect, useRef, useState } from 'react'
import { ArrowRight, RefreshCw, Send, Search, CheckCircle, XCircle, Clock, Copy, Zap, Activity } from 'lucide-react'
import { api, genKey } from '../api/client.js'
import Toast from '../components/Toast.jsx'

const CURRENCIES = ['INR', 'USD', 'EUR', 'GBP', 'SGD', 'AED']

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const isUUID = v => UUID_RE.test(v.trim())

const STATUS = {
  TRANSACTION_STATUS_COMPLETED: {
    label: 'Completed', icon: CheckCircle,
    cls: 'text-emerald-400 bg-emerald-950/80 border-emerald-700/40',
    glow: 'rgba(16,185,129,0.08)',
  },
  TRANSACTION_STATUS_FAILED: {
    label: 'Failed', icon: XCircle,
    cls: 'text-red-400 bg-red-950/80 border-red-700/40',
    glow: 'rgba(239,68,68,0.06)',
  },
  TRANSACTION_STATUS_PENDING: {
    label: 'Pending', icon: Clock,
    cls: 'text-amber-400 bg-amber-950/80 border-amber-700/40',
    glow: 'rgba(245,158,11,0.06)',
  },
  TRANSACTION_STATUS_REVERSED: {
    label: 'Reversed', icon: RefreshCw,
    cls: 'text-slate-400 bg-slate-800/80 border-slate-700',
    glow: 'transparent',
  },
}

function Badge({ status }) {
  const m = STATUS[status] ?? { label: status, icon: Clock, cls: 'text-slate-400 bg-slate-800 border-slate-700', glow: 'transparent' }
  const Icon = m.icon
  return (
    <span className={`inline-flex items-center gap-1 text-[11px] px-2 py-0.5 rounded-full font-medium border ${m.cls}`}>
      <Icon size={9} /> {m.label}
    </span>
  )
}

function relativeAge(ts) {
  if (!ts) return ''
  const secs = Math.floor((Date.now() - new Date(ts)) / 1000)
  if (secs < 60) return `${secs}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m`
  return `${Math.floor(secs / 3600)}h`
}

function TxnRow({ txn, onRefresh, onNavigate, index }) {
  const [copied,  setCopied]  = useState(false)
  const [loading, setLoading] = useState(false)
  const isPending = txn.status === 'TRANSACTION_STATUS_PENDING'
  const isCompleted = txn.status === 'TRANSACTION_STATUS_COMPLETED'
  const isFailed = txn.status === 'TRANSACTION_STATUS_FAILED'

  const copy = () => { navigator.clipboard.writeText(txn.id); setCopied(true); setTimeout(() => setCopied(false), 1500) }
  const refresh = async () => { setLoading(true); try { await onRefresh(txn.id) } catch {}; setLoading(false) }
  const viewTrace = () => onNavigate('trace', { txnId: txn.id })

  const statusInfo = STATUS[txn.status] ?? STATUS.TRANSACTION_STATUS_PENDING

  return (
    <tr style={{
      background: isPending ? 'rgba(245,158,11,0.03)' : 'transparent',
      animation: `fade-up 0.25s ease-out ${index * 0.04}s both`,
    }}>
      <td className="pl-4">
        <div className="flex items-center gap-1.5">
          <span className="font-mono text-[11px] text-slate-500">{txn.id?.slice(0, 12)}…</span>
          <button onClick={copy} className="text-slate-700 hover:text-slate-400 transition-colors">
            <Copy size={10} />
          </button>
          {copied && <span className="text-[10px] text-emerald-400 anim-fade-in">✓</span>}
        </div>
        {txn.description && (
          <p className="text-[10px] text-slate-700 mt-0.5 truncate max-w-[160px]">{txn.description}</p>
        )}
        {isPending && txn.created_at && (
          <p className="text-[10px] text-amber-600 mt-0.5">{relativeAge(txn.created_at)} pending</p>
        )}
        {isFailed && (txn.failure_reason || txn.error) && (
          <p className="text-[10px] text-red-500 mt-0.5 truncate max-w-[160px]">{txn.failure_reason || txn.error}</p>
        )}
      </td>

      <td>
        <div className="flex items-center gap-1.5">
          <span className="font-mono text-[11px] text-slate-600 truncate max-w-[70px]">
            {txn.source_account_id?.slice(0, 8)}…
          </span>
          <div className="flex items-center flex-shrink-0">
            {isPending ? (
              <div className="relative">
                <ArrowRight size={12} className="text-amber-500" />
                <div className="absolute inset-0 flex items-center justify-center">
                  <ArrowRight size={12} className="text-amber-400 animate-ping opacity-50" />
                </div>
              </div>
            ) : (
              <ArrowRight size={12} className={isCompleted ? 'text-emerald-600' : 'text-slate-700'} />
            )}
          </div>
          <span className="font-mono text-[11px] text-slate-600 truncate max-w-[70px]">
            {txn.dest_account_id?.slice(0, 8)}…
          </span>
        </div>
      </td>

      <td className="text-right">
        <span className="font-mono font-bold tabular-nums"
          style={{ color: isCompleted ? '#818cf8' : '#64748b' }}>
          {txn.amount?.amount}
        </span>
        <span className="text-[11px] text-slate-700 ml-1">{txn.amount?.currency}</span>
      </td>

      <td><Badge status={txn.status} /></td>

      <td className="hidden xl:table-cell text-[11px] text-slate-700 whitespace-nowrap">
        {txn.created_at
          ? new Date(txn.created_at).toLocaleString('en-IN', { dateStyle: 'short', timeStyle: 'short' })
          : '—'}
      </td>

      <td className="pr-4">
        <div className="flex items-center gap-1">
          {isPending && (
            <button onClick={refresh} disabled={loading}
              className="btn-ghost py-1 px-2 text-[10px]">
              <RefreshCw size={10} className={loading ? 'animate-spin' : ''} />
            </button>
          )}
          <button onClick={viewTrace}
            className="btn-ghost py-1 px-2 text-[10px] flex items-center gap-1 text-indigo-400 hover:text-indigo-300">
            <Activity size={10} /> Trace
          </button>
        </div>
      </td>
    </tr>
  )
}

export default function Transactions({ onNavigate }) {
  const [form, setForm] = useState({
    source_account_id: '', dest_account_id: '', amount: '', currency: 'INR', description: ''
  })
  const [lookupID, setLookupID] = useState('')
  const [txns,     setTxns]     = useState([])
  const [loading,  setLoading]  = useState(false)
  const [toast,    setToast]    = useState(null)
  const pollDelayRef    = useRef(3000)
  const pendingCountRef = useRef(0)

  const refreshTxn = async (id) => {
    const r = await api.getTransaction(id)
    setTxns(prev => prev.map(t => t.id === r.transaction.id ? r.transaction : t))
  }

  useEffect(() => {
    const pending = txns.filter(t => t.status === 'TRANSACTION_STATUS_PENDING')
    if (pending.length === 0) { pollDelayRef.current = 3000; pendingCountRef.current = 0; return }
    // reset backoff if some resolved since last poll
    if (pending.length < pendingCountRef.current) pollDelayRef.current = 3000
    pendingCountRef.current = pending.length
    const delay = pollDelayRef.current
    const t = setTimeout(() => {
      pollDelayRef.current = Math.min(Math.round(pollDelayRef.current * 1.5), 30000)
      Promise.allSettled(pending.map(p => refreshTxn(p.id)))
    }, delay)
    return () => clearTimeout(t)
  }, [txns])

  const send = async (e) => {
    e.preventDefault(); setLoading(true)
    try {
      const r = await api.initiatePayment({
        idempotency_key: genKey(),
        source_account_id: form.source_account_id,
        dest_account_id: form.dest_account_id,
        amount: form.amount,
        currency: form.currency,
        description: form.description,
      })
      setTxns(p => [r.transaction, ...p].slice(0, 20))
      setToast({ msg: 'Payment initiated', type: 'success' })
    } catch (ex) { setToast({ msg: ex.message, type: 'error' }) }
    setLoading(false)
  }

  const lookup = async (e) => {
    e.preventDefault(); if (!lookupID.trim()) return; setLoading(true)
    try {
      const r = await api.getTransaction(lookupID.trim())
      setTxns(prev => {
        const w = prev.filter(x => x.id !== r.transaction.id)
        return [r.transaction, ...w].slice(0, 20)
      })
    } catch (ex) { setToast({ msg: ex.message, type: 'error' }) }
    setLoading(false)
  }

  const hasPending   = txns.some(t => t.status === 'TRANSACTION_STATUS_PENDING')
  const completed    = txns.filter(t => t.status === 'TRANSACTION_STATUS_COMPLETED').length
  const failed       = txns.filter(t => t.status === 'TRANSACTION_STATUS_FAILED').length

  return (
    <div className="page">
      {toast && <Toast message={toast.msg} type={toast.type} onDone={() => setToast(null)} />}

      <div className="ph">
        <div>
          <h1 className="text-sm font-bold text-slate-100">Transactions</h1>
          <p className="text-[11px] text-slate-600 mt-0.5">Idempotent payment sagas · Auto-reversal on failure</p>
        </div>
        <div className="flex items-center gap-2">
          {txns.length > 0 && (
            <div className="hidden sm:flex items-center gap-2 text-[11px]">
              {completed > 0 && (
                <span className="text-emerald-400 bg-emerald-950/40 border border-emerald-800/30 px-2 py-0.5 rounded-full">
                  {completed} done
                </span>
              )}
              {failed > 0 && (
                <span className="text-red-400 bg-red-950/40 border border-red-800/30 px-2 py-0.5 rounded-full">
                  {failed} failed
                </span>
              )}
            </div>
          )}
          {hasPending && (
            <span className="flex items-center gap-1.5 text-[11px] text-amber-400 bg-amber-950/40 border border-amber-700/30 rounded-full px-2.5 py-1">
              <span className="w-1.5 h-1.5 rounded-full bg-amber-500 animate-pulse" />
              Polling…
            </span>
          )}
        </div>
      </div>

      <div className="flex-1 overflow-hidden flex">

        {/* Left — forms */}
        <div className="w-80 flex-shrink-0 border-r border-slate-800/80 overflow-auto">

          {/* Send form */}
          <div className="p-5 border-b border-slate-800/60">
            <p className="text-[10px] font-semibold text-slate-500 uppercase tracking-widest mb-4 flex items-center gap-2">
              <Send size={11} className="text-indigo-400" /> Send Payment
            </p>
            <form onSubmit={send} className="space-y-3">
              <div>
                <label>From Account ID</label>
                <input value={form.source_account_id}
                  onChange={e => setForm({...form, source_account_id: e.target.value})}
                  placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" required
                  className={`font-mono text-xs ${form.source_account_id && !isUUID(form.source_account_id) ? 'border-red-700/60 focus:ring-red-700/30' : ''}`} />
                {form.source_account_id && !isUUID(form.source_account_id) && (
                  <p className="mt-1 text-[10px] text-red-400 flex items-center gap-1">
                    <XCircle size={9} /> Must be a UUID — copy it from the Accounts page
                  </p>
                )}
              </div>
              <div>
                <label>To Account ID</label>
                <input value={form.dest_account_id}
                  onChange={e => setForm({...form, dest_account_id: e.target.value})}
                  placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" required
                  className={`font-mono text-xs ${form.dest_account_id && !isUUID(form.dest_account_id) ? 'border-red-700/60 focus:ring-red-700/30' : ''}`} />
                {form.dest_account_id && !isUUID(form.dest_account_id) && (
                  <p className="mt-1 text-[10px] text-red-400 flex items-center gap-1">
                    <XCircle size={9} /> Must be a UUID — copy it from the Accounts page
                  </p>
                )}
              </div>

              {/* Route preview — only shown when both are valid UUIDs */}
              {isUUID(form.source_account_id) && isUUID(form.dest_account_id) && (
                <div className="flex items-center gap-2 px-3 py-2 rounded-lg border border-indigo-800/30 bg-indigo-950/20 text-[10px] font-mono">
                  <span className="text-indigo-400 truncate flex-1">{form.source_account_id.slice(0, 8)}…</span>
                  <Zap size={9} className="text-indigo-500 flex-shrink-0 animate-pulse" />
                  <span className="text-indigo-400 truncate flex-1 text-right">{form.dest_account_id.slice(0, 8)}…</span>
                </div>
              )}

              <div className="grid grid-cols-3 gap-2">
                <div className="col-span-2">
                  <label>Amount</label>
                  <input value={form.amount}
                    onChange={e => setForm({...form, amount: e.target.value})}
                    placeholder="500.00" required />
                </div>
                <div>
                  <label>Currency</label>
                  <select value={form.currency} onChange={e => setForm({...form, currency: e.target.value})}>
                    {CURRENCIES.map(c => <option key={c}>{c}</option>)}
                  </select>
                </div>
              </div>
              <div>
                <label>Description <span className="text-slate-700 font-normal">(optional)</span></label>
                <input value={form.description}
                  onChange={e => setForm({...form, description: e.target.value})}
                  placeholder="Payment for services" />
              </div>
              <button type="submit"
                disabled={loading || !isUUID(form.source_account_id) || !isUUID(form.dest_account_id)}
                className="btn-primary w-full justify-center">
                {loading ? <RefreshCw size={11} className="animate-spin" /> : <Send size={11} />}
                Send Payment
              </button>
            </form>
          </div>

          {/* Lookup */}
          <div className="p-5">
            <p className="text-[10px] font-semibold text-slate-500 uppercase tracking-widest mb-4 flex items-center gap-2">
              <Search size={11} className="text-indigo-400" /> Look Up
            </p>
            <form onSubmit={lookup} className="space-y-1.5">
              <div className="flex gap-2">
                <input className="flex-1 font-mono text-xs" value={lookupID}
                  onChange={e => setLookupID(e.target.value)}
                  placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" />
                <button type="submit" disabled={loading || !isUUID(lookupID)} className="btn-primary flex-shrink-0">
                  {loading ? <RefreshCw size={11} className="animate-spin" /> : <Search size={11} />}
                </button>
              </div>
              {lookupID && !isUUID(lookupID) && (
                <p className="text-[10px] text-red-400 flex items-center gap-1">
                  <XCircle size={9} /> Enter a valid transaction UUID
                </p>
              )}
            </form>
          </div>
        </div>

        {/* Right — table */}
        <div className="flex-1 overflow-auto">
          {txns.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-full min-h-[300px]">
              <div className="relative mb-5">
                <div className="absolute inset-0 rounded-2xl bg-indigo-500/8 blur-xl" />
                <div className="relative w-16 h-16 rounded-2xl bg-slate-800/80 border border-slate-700/40
                                flex items-center justify-center anim-float">
                  <Send size={24} className="text-slate-600" />
                </div>
              </div>
              <p className="text-sm font-medium text-slate-400 mb-1">Transactions appear here</p>
              <p className="text-xs text-slate-600">Pending transactions auto-refresh every 3 s</p>
            </div>
          ) : (
            <table className="tbl">
              <thead>
                <tr>
                  <th className="pl-4">ID / Note</th>
                  <th>Route</th>
                  <th className="text-right">Amount</th>
                  <th>Status</th>
                  <th className="hidden xl:table-cell">Date</th>
                  <th className="pr-4" />
                </tr>
              </thead>
              <tbody>
                {txns.map((t, i) => (
                  <TxnRow key={t.id} txn={t} index={i} onRefresh={refreshTxn} onNavigate={onNavigate} />
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  )
}
