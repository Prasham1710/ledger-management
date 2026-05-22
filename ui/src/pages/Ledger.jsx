import { useState } from 'react'
import { Search, Plus, ArrowDownCircle, ArrowUpCircle, RefreshCw, BookOpen, XCircle } from 'lucide-react'
import { api, genKey } from '../api/client.js'
import Modal from '../components/Modal.jsx'
import Toast from '../components/Toast.jsx'

function fmt(ts) {
  if (!ts) return '—'
  const d = new Date(ts.seconds ? ts.seconds * 1000 : ts)
  return d.toLocaleString('en-IN', { dateStyle: 'short', timeStyle: 'short' })
}

function AmountBar({ amount, max, isCredit }) {
  const val = parseFloat(amount ?? 0)
  const pct = max > 0 ? Math.min(100, (val / max) * 100) : 0
  return (
    <div className="flex items-center gap-2">
      <div className="w-12 h-1 bg-slate-800 rounded-full overflow-hidden flex-shrink-0">
        <div
          className="h-full rounded-full"
          style={{
            width: `${pct}%`,
            background: isCredit ? '#10b981' : '#ef4444',
            transition: 'width 0.5s ease',
          }}
        />
      </div>
    </div>
  )
}

export default function Ledger() {
  const [accountId, setAccountId] = useState('')
  const [query,     setQuery]     = useState('')
  const [balance,   setBalance]   = useState(null)
  const [entries,   setEntries]   = useState(null)
  const [loading,   setLoading]   = useState(false)
  const [showPost,  setShowPost]  = useState(false)
  const [toast,     setToast]     = useState(null)
  const [pf, setPf]               = useState({ debit: '', credit: '', amount: '', currency: 'INR', ref: '', desc: '' })
  const [postErr,   setPostErr]   = useState('')

  const load = async (e) => {
    e?.preventDefault()
    if (!query.trim()) return
    setLoading(true)
    try {
      const [bal, ent] = await Promise.all([
        api.getBalance(query.trim()),
        api.listEntries(query.trim(), 100),
      ])
      setBalance(bal)
      setEntries(ent.entries ?? [])
      setAccountId(query.trim())
    } catch (ex) { setToast({ msg: ex.message, type: 'error' }) }
    setLoading(false)
  }

  const refresh = async () => {
    if (!accountId) return
    try {
      const [bal, ent] = await Promise.all([
        api.getBalance(accountId),
        api.listEntries(accountId, 100),
      ])
      setBalance(bal)
      setEntries(ent.entries ?? [])
    } catch (ex) { setToast({ msg: ex.message, type: 'error' }) }
  }

  const postEntry = async (e) => {
    e.preventDefault(); setPostErr('')
    try {
      await api.postEntry({
        idempotency_key: genKey(),
        debit_account_id:  pf.debit,
        credit_account_id: pf.credit,
        amount:       pf.amount,
        currency:     pf.currency,
        reference_id: pf.ref,
        description:  pf.desc,
      })
      setShowPost(false)
      setToast({ msg: 'Entry posted', type: 'success' })
      if (accountId) refresh()
    } catch (ex) { setPostErr(ex.message) }
  }

  const balAmt = balance?.balance?.amount
  const balCur = balance?.balance?.currency
  const isPos  = parseFloat(balAmt ?? 0) >= 0

  const maxAmt = entries?.length
    ? Math.max(...entries.map(e => parseFloat(e.amount?.amount ?? 0)))
    : 0

  const totalCredits = entries?.filter(e => e.type === 'ENTRY_TYPE_CREDIT')
    .reduce((a, e) => a + parseFloat(e.amount?.amount ?? 0), 0) ?? 0
  const totalDebits = entries?.filter(e => e.type !== 'ENTRY_TYPE_CREDIT')
    .reduce((a, e) => a + parseFloat(e.amount?.amount ?? 0), 0) ?? 0

  return (
    <div className="page">
      {toast && <Toast message={toast.msg} type={toast.type} onDone={() => setToast(null)} />}

      {/* Header */}
      <div className="ph">
        <div>
          <h1 className="text-sm font-bold text-slate-100">Ledger</h1>
          <p className="text-[11px] text-slate-600 mt-0.5">Append-only double-entry bookkeeping</p>
        </div>
        <button className="btn-primary" onClick={() => setShowPost(true)}>
          <Plus size={12} /> Post Entry
        </button>
      </div>

      {/* Search bar */}
      <div className="flex-shrink-0 border-b border-slate-800/80 bg-slate-900/20">
        <div className="px-6 py-3">
          <form onSubmit={load} className="flex gap-2">
            <div className="relative flex-1">
              <Search size={12} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-600 pointer-events-none" />
              <input className="pl-9 font-mono text-xs h-9" value={query}
                onChange={e => setQuery(e.target.value)} placeholder="Account ID…" />
            </div>
            <button type="submit" disabled={loading} className="btn-primary px-5 h-9">
              {loading ? <RefreshCw size={12} className="animate-spin" /> : <Search size={12} />}
              Load
            </button>
            {accountId && (
              <button type="button" onClick={refresh} className="btn-ghost h-9">
                <RefreshCw size={12} />
              </button>
            )}
          </form>
        </div>

        {/* Balance strip */}
        {balance && (
          <div className="flex items-stretch border-t border-slate-800/60">
            <div className="px-6 py-3 border-r border-slate-800/60">
              <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">Balance</p>
              <p className={`text-2xl font-bold font-mono tabular-nums ${isPos ? 'text-emerald-400' : 'text-red-400'}`}
                style={{ textShadow: isPos ? '0 0 20px rgba(16,185,129,0.3)' : '0 0 20px rgba(239,68,68,0.3)' }}>
                {balAmt}
              </p>
            </div>
            <div className="px-6 py-3 border-r border-slate-800/60">
              <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">Currency</p>
              <p className="text-sm font-bold text-slate-300">{balCur}</p>
            </div>
            <div className="px-6 py-3 border-r border-slate-800/60">
              <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">Entries</p>
              <p className="text-sm font-bold text-slate-300 font-mono">{entries?.length ?? '—'}</p>
            </div>
            {entries?.length > 0 && (
              <>
                <div className="px-6 py-3 border-r border-slate-800/60">
                  <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">Total Credits</p>
                  <p className="text-sm font-bold font-mono text-emerald-500 tabular-nums">
                    +{totalCredits.toFixed(4)}
                  </p>
                </div>
                <div className="px-6 py-3 border-r border-slate-800/60">
                  <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">Total Debits</p>
                  <p className="text-sm font-bold font-mono text-red-500 tabular-nums">
                    -{totalDebits.toFixed(4)}
                  </p>
                </div>
              </>
            )}
            <div className="px-6 py-3 ml-auto">
              <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">Account</p>
              <p className="text-[11px] font-mono text-slate-600 max-w-[180px] truncate">{accountId}</p>
            </div>
          </div>
        )}
      </div>

      {/* Table */}
      <div className="flex-1 overflow-auto">
        {entries === null && loading && (
          <div className="px-6 py-4 space-y-2">
            {[1,2,3,4,5,6,7].map(i => (
              <div key={i} className="flex items-center gap-4 py-2.5 border-b border-slate-800/20">
                <div className="w-4 h-4 rounded skeleton flex-shrink-0" style={{ animationDelay: `${i*0.06}s` }} />
                <div className="h-3 skeleton w-14 rounded" style={{ animationDelay: `${i*0.06+0.05}s` }} />
                <div className="h-3 skeleton w-20 rounded ml-auto tabular-nums" style={{ animationDelay: `${i*0.06+0.1}s` }} />
                <div className="h-1 skeleton w-12 rounded" style={{ animationDelay: `${i*0.06+0.15}s` }} />
                <div className="h-3 skeleton w-28 rounded" style={{ animationDelay: `${i*0.06+0.2}s` }} />
                <div className="h-3 skeleton w-20 rounded hidden lg:block" style={{ animationDelay: `${i*0.06+0.25}s` }} />
              </div>
            ))}
          </div>
        )}

        {entries === null && !loading && (
          <div className="flex flex-col items-center justify-center h-full min-h-[300px]">
            <div className="relative mb-5">
              <div className="relative w-16 h-16 rounded-2xl bg-slate-800/80 border border-slate-700/40
                              flex items-center justify-center anim-float">
                <BookOpen size={24} className="text-slate-600" />
              </div>
            </div>
            <p className="text-sm font-medium text-slate-400 mb-1">Enter an account ID</p>
            <p className="text-xs text-slate-600">to load double-entry journal</p>
          </div>
        )}

        {entries?.length === 0 && (
          <div className="flex flex-col items-center justify-center h-full min-h-[200px]">
            <p className="text-sm text-slate-500 mb-1">No entries yet</p>
            <p className="text-xs text-slate-600">Post the first entry to get started</p>
          </div>
        )}

        {entries?.length > 0 && (
          <table className="tbl">
            <thead>
              <tr>
                <th className="w-8 pl-4" />
                <th>Type</th>
                <th className="text-right">Amount</th>
                <th>Magnitude</th>
                <th>Reference</th>
                <th className="hidden lg:table-cell">Description</th>
                <th className="hidden xl:table-cell">Date</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e, i) => {
                const cr = e.type === 'ENTRY_TYPE_CREDIT'
                return (
                  <tr key={e.id ?? i} style={{ animation: `fade-up 0.25s ease-out ${i * 0.02}s both` }}>
                    <td className="pl-4 pr-2">
                      {cr
                        ? <ArrowDownCircle size={13} className="text-emerald-500" />
                        : <ArrowUpCircle   size={13} className="text-red-500" />}
                    </td>
                    <td>
                      <span className={`text-xs font-semibold ${cr ? 'text-emerald-400' : 'text-red-400'}`}>
                        {cr ? 'Credit' : 'Debit'}
                      </span>
                    </td>
                    <td className="text-right">
                      <span className={`font-mono tabular-nums text-sm font-bold ${cr ? 'text-emerald-400' : 'text-red-400'}`}>
                        {cr ? '+' : '-'}{e.amount?.amount}
                      </span>
                      <span className="text-slate-700 text-xs ml-1">{e.amount?.currency}</span>
                    </td>
                    <td>
                      <AmountBar amount={e.amount?.amount} max={maxAmt} isCredit={cr} />
                    </td>
                    <td>
                      <span className="font-mono text-[11px] text-slate-500">{e.referenceId}</span>
                    </td>
                    <td className="hidden lg:table-cell text-slate-600 text-xs">{e.description || '—'}</td>
                    <td className="hidden xl:table-cell text-slate-700 text-xs whitespace-nowrap">{fmt(e.createdAt)}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </div>

      {/* Post entry modal */}
      {showPost && (
        <Modal title="Post Double Entry" onClose={() => setShowPost(false)}>
          <form onSubmit={postEntry} className="space-y-3">
            <div>
              <label className="flex items-center gap-1.5 mb-1.5">
                <ArrowUpCircle size={11} className="text-red-400" />
                <span className="text-red-400">Debit Account ID</span>
                <span className="text-slate-700 text-[10px] font-normal">— money leaves this account</span>
              </label>
              <input className="font-mono text-xs" value={pf.debit}
                onChange={e => setPf({...pf, debit: e.target.value})}
                placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" required
                style={{ borderColor: pf.debit ? 'rgba(239,68,68,0.3)' : undefined }} />
              {pf.debit && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(pf.debit) && (
                <p className="mt-1 text-[10px] text-red-400 flex items-center gap-1"><XCircle size={9} /> Must be a valid account UUID</p>
              )}
            </div>
            <div>
              <label className="flex items-center gap-1.5 mb-1.5">
                <ArrowDownCircle size={11} className="text-emerald-400" />
                <span className="text-emerald-400">Credit Account ID</span>
                <span className="text-slate-700 text-[10px] font-normal">— money arrives here</span>
              </label>
              <input className="font-mono text-xs" value={pf.credit}
                onChange={e => setPf({...pf, credit: e.target.value})}
                placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" required
                style={{ borderColor: pf.credit ? 'rgba(16,185,129,0.3)' : undefined }} />
              {pf.credit && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(pf.credit) && (
                <p className="mt-1 text-[10px] text-red-400 flex items-center gap-1"><XCircle size={9} /> Must be a valid account UUID</p>
              )}
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div>
                <label>Amount</label>
                <input value={pf.amount}
                  onChange={e => setPf({...pf, amount: e.target.value})}
                  placeholder="500.0000" required />
              </div>
              <div>
                <label>Currency</label>
                <select value={pf.currency} onChange={e => setPf({...pf, currency: e.target.value})}>
                  {['INR','USD','EUR','GBP','SGD','AED'].map(c => <option key={c}>{c}</option>)}
                </select>
              </div>
            </div>
            <div>
              <label>Reference ID</label>
              <input value={pf.ref}
                onChange={e => setPf({...pf, ref: e.target.value})}
                placeholder="ref-001" required />
            </div>
            <div>
              <label>Description</label>
              <input value={pf.desc}
                onChange={e => setPf({...pf, desc: e.target.value})}
                placeholder="payment" />
            </div>
            {postErr && (
              <p className="text-xs text-red-400 bg-red-950/40 border border-red-800/30 rounded px-3 py-2">
                {postErr}
              </p>
            )}
            <div className="flex gap-2 pt-1">
              <button type="submit" className="btn-primary flex-1">Post Entry</button>
              <button type="button" onClick={() => setShowPost(false)} className="btn-ghost flex-1">Cancel</button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  )
}
