import { useState } from 'react'
import {
  Search, Plus, ArrowDownCircle, ArrowUpCircle, Lock, Unlock,
  RefreshCw, Copy, ChevronDown, BookOpen, Wallet, TrendingUp,
} from 'lucide-react'
import { api, genKey } from '../api/client.js'
import Modal from '../components/Modal.jsx'
import Toast from '../components/Toast.jsx'

const CURRENCIES = ['INR', 'USD', 'EUR', 'GBP', 'SGD', 'AED']

const CUR_COLOR = {
  INR: '#f59e0b', USD: '#10b981', EUR: '#6366f1',
  GBP: '#8b5cf6', SGD: '#06b6d4', AED: '#ec4899',
}

function statusDot(s) {
  const map = {
    ACCOUNT_STATUS_ACTIVE:    { color: '#10b981', label: 'Active',    cls: 'tag-active' },
    ACCOUNT_STATUS_SUSPENDED: { color: '#f59e0b', label: 'Suspended', cls: 'tag-suspended' },
    ACCOUNT_STATUS_CLOSED:    { color: '#ef4444', label: 'Closed',    cls: 'tag-closed' },
  }
  const m = map[s] ?? map.ACCOUNT_STATUS_ACTIVE
  return <span className={m.cls}>{m.label}</span>
}

function CopyVal({ value, mono, short }) {
  const [ok, setOk] = useState(false)
  const copy = () => { navigator.clipboard.writeText(value); setOk(true); setTimeout(() => setOk(false), 1500) }
  const display = short && value?.length > 20 ? value.slice(0, 16) + '…' : value
  return (
    <div className="flex items-center gap-1.5 min-w-0">
      <span className={`text-xs text-slate-400 truncate ${mono ? 'font-mono' : ''}`}>{display}</span>
      <button onClick={copy} className="text-slate-700 hover:text-slate-400 transition-colors flex-shrink-0">
        <Copy size={10} />
      </button>
      {ok && <span className="text-[10px] text-emerald-400 anim-fade-in flex-shrink-0">✓</span>}
    </div>
  )
}

function fmt(ts) {
  if (!ts) return ''
  const d = new Date(ts.seconds ? ts.seconds * 1000 : ts)
  return d.toLocaleString('en-IN', { dateStyle: 'short', timeStyle: 'short' })
}

function AccountCard({ acc, onRefresh, onToast, index }) {
  const [open,        setOpen]        = useState(false)
  const [modal,       setModal]       = useState(null)
  const [showEntries, setShowEntries] = useState(false)
  const [entries,     setEntries]     = useState(null)
  const [loadEnt,     setLoadEnt]     = useState(false)

  const bal  = parseFloat(acc.balance?.amount     ?? 0)
  const hold = parseFloat(acc.holdBalance?.amount ?? 0)
  const cur  = acc.currency ?? ''
  const curColor = CUR_COLOR[cur] ?? '#6366f1'
  const isActive = acc.status === 'ACCOUNT_STATUS_ACTIVE'

  const toggleEntries = async () => {
    if (showEntries) { setShowEntries(false); return }
    if (entries !== null) { setShowEntries(true); return }
    setLoadEnt(true)
    try {
      const r = await api.listEntries(acc.id, 25)
      setEntries(r.entries ?? [])
      setShowEntries(true)
    } catch (ex) { onToast(ex.message, 'error') }
    setLoadEnt(false)
  }

  const refreshEntries = async () => {
    setLoadEnt(true)
    try { const r = await api.listEntries(acc.id, 25); setEntries(r.entries ?? []) }
    catch (ex) { onToast(ex.message, 'error') }
    setLoadEnt(false)
  }

  const doCredit  = async (f) => {
    await api.credit(acc.id, { idempotency_key: genKey(), amount: f.amount, currency: cur, reference_id: f.ref })
    setModal(null); setEntries(null); onRefresh(); onToast('Credit applied', 'success')
  }
  const doDebit   = async (f) => {
    await api.debit(acc.id, { idempotency_key: genKey(), amount: f.amount, currency: cur, hold_id: f.holdId, reference_id: f.ref })
    setModal(null); setEntries(null); onRefresh(); onToast('Debit applied', 'success')
  }
  const doHold    = async (f) => {
    const r = await api.placeHold(acc.id, { idempotency_key: genKey(), amount: f.amount, currency: cur, reference_id: f.ref, ttl_seconds: parseInt(f.ttl) || 172800 })
    setModal(null); onRefresh(); onToast(`Hold placed · ${r.hold?.id?.slice(0,8)}…`, 'success')
  }
  const doRelease = async (f) => {
    await api.releaseHold(acc.id, f.holdId)
    setModal(null); onRefresh(); onToast('Hold released', 'success')
  }

  return (
    <div
      className="card-flush flex flex-col transition-all duration-200 hover:-translate-y-0.5"
      style={{
        animation: `fade-up 0.3s ease-out ${index * 0.06}s both`,
        boxShadow: `0 4px 20px rgba(0,0,0,.4), 0 0 0 1px rgba(${curColor === '#f59e0b' ? '245,158,11' : '99,102,241'},.06)`,
      }}
    >
      {/* Color accent top bar */}
      <div className="h-0.5 w-full" style={{ background: `linear-gradient(90deg, ${curColor}60 0%, transparent 100%)` }} />

      {/* Top row: status + currency + chevron */}
      <div className="flex items-center justify-between px-4 pt-3.5 pb-2">
        <div className="flex items-center gap-2">
          {statusDot(acc.status)}
          <div className="flex items-center gap-1.5 px-2 py-0.5 rounded-md"
            style={{ background: `${curColor}12`, border: `1px solid ${curColor}25` }}>
            <div className="w-1.5 h-1.5 rounded-full" style={{ backgroundColor: curColor }} />
            <span className="text-xs font-bold font-mono" style={{ color: curColor }}>{cur}</span>
          </div>
        </div>
        <button onClick={() => setOpen(o => !o)}
          className="w-6 h-6 flex items-center justify-center rounded-md text-slate-600
                     hover:text-slate-300 hover:bg-slate-800 transition-all duration-150">
          <ChevronDown size={13} className={`transition-transform duration-200 ${open ? 'rotate-180' : ''}`} />
        </button>
      </div>

      {/* Always-visible IDs */}
      <div className="px-4 py-2 grid grid-cols-2 gap-x-4 gap-y-1.5 border-y border-slate-800/50"
        style={{ background: 'rgba(255,255,255,0.01)' }}>
        <div className="min-w-0">
          <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-0.5">Owner ID</p>
          <CopyVal value={acc.ownerId ?? '—'} mono short />
        </div>
        <div className="min-w-0">
          <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-0.5">Account ID</p>
          <CopyVal value={acc.id} mono short />
        </div>
      </div>

      {/* Balances */}
      <div className="grid grid-cols-2">
        <div className="px-4 py-3 border-r border-slate-800/50">
          <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">Available</p>
          <p className="text-xl font-bold font-mono tabular-nums leading-tight"
            style={{
              color: bal > 0 ? '#34d399' : '#ef4444',
              textShadow: bal > 0 ? '0 0 16px rgba(52,211,153,.25)' : 'none',
            }}>
            {acc.balance?.amount ?? '0.0000'}
          </p>
        </div>
        <div className="px-4 py-3">
          <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">On Hold</p>
          <p className={`text-xl font-bold font-mono tabular-nums leading-tight
            ${hold > 0 ? 'text-amber-400' : 'text-slate-800'}`}>
            {acc.holdBalance?.amount ?? '0.0000'}
          </p>
        </div>
      </div>

      {/* Balance utilization bar (hold vs available) */}
      {(bal > 0 || hold > 0) && (
        <div className="px-4 pb-2">
          <div className="h-1 bg-slate-800 rounded-full overflow-hidden">
            <div className="h-full rounded-full flex">
              {/* available portion */}
              <div className="h-full transition-all duration-700"
                style={{
                  width: `${(bal / (bal + hold)) * 100}%`,
                  background: 'linear-gradient(90deg, #10b981 0%, #34d399 100%)',
                }} />
              {/* hold portion */}
              {hold > 0 && (
                <div className="h-full transition-all duration-700"
                  style={{
                    width: `${(hold / (bal + hold)) * 100}%`,
                    background: '#f59e0b',
                  }} />
              )}
            </div>
          </div>
          {hold > 0 && (
            <p className="text-[9px] text-slate-700 mt-1">
              {((hold / (bal + hold)) * 100).toFixed(0)}% on hold
            </p>
          )}
        </div>
      )}

      {/* Expanded details */}
      {open && (
        <div className="px-4 py-3 border-t border-slate-800/50 bg-slate-800/20 space-y-2 anim-fade-up">
          {acc.type && (
            <div>
              <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-0.5">Type</p>
              <p className="text-xs text-slate-400">{acc.type.replace('ACCOUNT_TYPE_', '')}</p>
            </div>
          )}
          <div>
            <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-0.5">Full Owner ID</p>
            <CopyVal value={acc.ownerId ?? '—'} mono />
          </div>
          <div>
            <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-0.5">Full Account ID</p>
            <CopyVal value={acc.id} mono />
          </div>
        </div>
      )}

      {/* Actions */}
      <div className="px-3 py-2.5 border-t border-slate-800/50 flex flex-wrap gap-1.5 mt-auto">
        <button className="btn-success"  onClick={() => setModal('credit')} >
          <ArrowDownCircle size={10} /> Credit
        </button>
        <button className="btn-danger"   onClick={() => setModal('debit')}  >
          <ArrowUpCircle   size={10} /> Debit
        </button>
        <button className="btn-amber"    onClick={() => setModal('hold')}   >
          <Lock            size={10} /> Hold
        </button>
        <button className="btn-ghost"    onClick={() => setModal('release')}>
          <Unlock          size={10} /> Release
        </button>
        <button className="btn-ghost ml-auto" onClick={toggleEntries} disabled={loadEnt}>
          {loadEnt
            ? <RefreshCw size={10} className="animate-spin" />
            : <BookOpen size={10} />}
          {showEntries ? 'Hide' : 'Entries'}
        </button>
      </div>

      {/* Inline ledger entries */}
      {showEntries && entries !== null && (
        <div className="border-t border-slate-800/50 anim-fade-up">
          <div className="flex items-center justify-between px-4 py-2 bg-slate-800/20">
            <span className="text-[9px] font-semibold text-slate-600 uppercase tracking-widest">
              Journal ({entries.length})
            </span>
            <button onClick={refreshEntries} disabled={loadEnt}
              className="text-slate-700 hover:text-slate-400 transition-colors">
              <RefreshCw size={10} className={loadEnt ? 'animate-spin' : ''} />
            </button>
          </div>
          {entries.length === 0 ? (
            <p className="text-[11px] text-slate-600 text-center py-4">No entries yet</p>
          ) : (
            <div className="max-h-44 overflow-y-auto">
              {entries.map((e, i) => {
                const cr = e.type === 'ENTRY_TYPE_CREDIT'
                return (
                  <div key={e.id ?? i}
                    className="flex items-center gap-2 px-4 py-1.5 border-b border-slate-800/20 last:border-0
                               hover:bg-slate-800/20 transition-colors"
                    style={{ animation: `fade-up 0.2s ease-out ${i * 0.02}s both` }}>
                    {cr
                      ? <ArrowDownCircle size={10} className="text-emerald-500 flex-shrink-0" />
                      : <ArrowUpCircle   size={10} className="text-red-500    flex-shrink-0" />}
                    <span className={`text-[11px] font-mono w-24 flex-shrink-0 tabular-nums font-bold
                      ${cr ? 'text-emerald-400' : 'text-red-400'}`}>
                      {cr ? '+' : '-'}{e.amount?.amount}
                    </span>
                    <span className="text-[10px] text-slate-700 flex-1 truncate font-mono">{e.referenceId}</span>
                    <span className="text-[10px] text-slate-800 hidden sm:block flex-shrink-0">{fmt(e.createdAt)}</span>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      )}

      {/* Modals */}
      {modal === 'credit'  && <QuickModal title={`Credit — ${cur}`} btnClass="btn-success" btnLabel="Credit"  fields={[{k:'amount',p:'500.0000'},{k:'ref',l:'Reference ID',p:'ref-001'}]} onClose={() => setModal(null)} onSubmit={doCredit} />}
      {modal === 'debit'   && <DebitModal   cur={cur} onClose={() => setModal(null)} onSubmit={doDebit}   />}
      {modal === 'hold'    && <HoldModal    cur={cur} onClose={() => setModal(null)} onSubmit={doHold}    />}
      {modal === 'release' && <ReleaseModal            onClose={() => setModal(null)} onSubmit={doRelease} />}
    </div>
  )
}

function QuickModal({ title, btnClass, btnLabel, fields, onClose, onSubmit }) {
  const [f, setF]   = useState(Object.fromEntries(fields.map(x => [x.k, ''])))
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async (e) => {
    e.preventDefault(); setErr(''); setBusy(true)
    try { await onSubmit(f) } catch (ex) { setErr(ex.message) }
    setBusy(false)
  }
  return (
    <Modal title={title} onClose={onClose}>
      <form onSubmit={submit} className="space-y-3">
        {fields.map(field => (
          <div key={field.k}>
            <label>{field.l ?? field.k.replace(/_/g,' ')}</label>
            <input value={f[field.k]} onChange={e => setF({...f, [field.k]: e.target.value})}
              placeholder={field.p} required={field.req !== false} />
          </div>
        ))}
        {err && <p className="text-xs text-red-400 bg-red-950/40 border border-red-800/30 rounded px-3 py-2">{err}</p>}
        <div className="flex gap-2 pt-1">
          <button type="submit" disabled={busy} className={`${btnClass} flex-1 justify-center`}>
            {busy ? 'Processing…' : btnLabel}
          </button>
          <button type="button" onClick={onClose} className="btn-ghost flex-1 justify-center">Cancel</button>
        </div>
      </form>
    </Modal>
  )
}

function DebitModal({ cur, onClose, onSubmit }) {
  const [f, setF]     = useState({ amount: '', ref: '', holdId: '' })
  const [err, setErr] = useState(''); const [busy, setBusy] = useState(false)
  const submit = async (e) => {
    e.preventDefault(); setErr(''); setBusy(true)
    try { await onSubmit(f) } catch (ex) { setErr(ex.message) }
    setBusy(false)
  }
  return (
    <Modal title={`Debit — ${cur}`} onClose={onClose}>
      <form onSubmit={submit} className="space-y-3">
        <div><label>Amount</label><input value={f.amount} onChange={e => setF({...f,amount:e.target.value})} placeholder="200.0000" required /></div>
        <div><label>Hold ID <span className="text-slate-700 font-normal text-[10px]">(optional)</span></label>
          <input value={f.holdId} onChange={e => setF({...f,holdId:e.target.value})} placeholder="leave blank for direct debit" className="font-mono text-xs" /></div>
        <div><label>Reference ID</label><input value={f.ref} onChange={e => setF({...f,ref:e.target.value})} placeholder="ref-001" required /></div>
        {err && <p className="text-xs text-red-400 bg-red-950/40 border border-red-800/30 rounded px-3 py-2">{err}</p>}
        <div className="flex gap-2 pt-1">
          <button type="submit" disabled={busy} className="btn-danger flex-1 justify-center">{busy ? 'Processing…' : 'Debit'}</button>
          <button type="button" onClick={onClose} className="btn-ghost flex-1 justify-center">Cancel</button>
        </div>
      </form>
    </Modal>
  )
}

function HoldModal({ cur, onClose, onSubmit }) {
  const [f, setF]     = useState({ amount: '', ref: '', ttl: '172800' })
  const [err, setErr] = useState(''); const [busy, setBusy] = useState(false)
  const submit = async (e) => {
    e.preventDefault(); setErr(''); setBusy(true)
    try { await onSubmit(f) } catch (ex) { setErr(ex.message) }
    setBusy(false)
  }
  return (
    <Modal title={`Place Hold — ${cur}`} onClose={onClose}>
      <form onSubmit={submit} className="space-y-3">
        <div><label>Amount to reserve</label><input value={f.amount} onChange={e => setF({...f,amount:e.target.value})} placeholder="300.0000" required /></div>
        <div><label>Reference ID</label><input value={f.ref} onChange={e => setF({...f,ref:e.target.value})} placeholder="ref-001" required /></div>
        <div><label>TTL seconds <span className="text-slate-700 font-normal text-[10px]">(default 48h)</span></label>
          <input type="number" value={f.ttl} onChange={e => setF({...f,ttl:e.target.value})} /></div>
        {err && <p className="text-xs text-red-400 bg-red-950/40 border border-red-800/30 rounded px-3 py-2">{err}</p>}
        <div className="flex gap-2 pt-1">
          <button type="submit" disabled={busy} className="btn-amber flex-1 justify-center">{busy ? 'Placing…' : 'Place Hold'}</button>
          <button type="button" onClick={onClose} className="btn-ghost flex-1 justify-center">Cancel</button>
        </div>
      </form>
    </Modal>
  )
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

function ReleaseModal({ onClose, onSubmit }) {
  const [holdId, setHoldId] = useState(''); const [err, setErr] = useState(''); const [busy, setBusy] = useState(false)
  const isValid = UUID_RE.test(holdId)
  const submit = async (e) => {
    e.preventDefault(); setErr(''); setBusy(true)
    try { await onSubmit({ holdId }) } catch (ex) { setErr(ex.message) }
    setBusy(false)
  }
  return (
    <Modal title="Release Hold" onClose={onClose}>
      <form onSubmit={submit} className="space-y-3">
        <div>
          <label>Hold ID</label>
          <input value={holdId} onChange={e => setHoldId(e.target.value)}
            placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" required
            className={`font-mono text-xs ${holdId && !isValid ? 'border-red-700/60' : ''}`} />
          {holdId && !isValid && (
            <p className="mt-1 text-[10px] text-red-400">Must be a valid hold UUID — copy it from the hold confirmation toast</p>
          )}
        </div>
        {err && <p className="text-xs text-red-400 bg-red-950/40 border border-red-800/30 rounded px-3 py-2">{err}</p>}
        <div className="flex gap-2 pt-1">
          <button type="submit" disabled={busy || !isValid} className="btn-ghost flex-1 justify-center">{busy ? 'Releasing…' : 'Release'}</button>
          <button type="button" onClick={onClose} className="btn-ghost flex-1 justify-center">Cancel</button>
        </div>
      </form>
    </Modal>
  )
}

export default function Accounts() {
  const [ownerID,    setOwnerID]    = useState('')
  const [query,      setQuery]      = useState('')
  const [accounts,   setAccounts]   = useState(null)
  const [loading,    setLoading]    = useState(false)
  const [showCreate, setShowCreate] = useState(false)
  const [toast,      setToast]      = useState(null)
  const [createForm, setCreateForm] = useState({ owner_id: '', currency: 'INR' })
  const [createErr,  setCreateErr]  = useState('')

  const search = async (e) => {
    e?.preventDefault()
    if (!query.trim()) return
    setLoading(true)
    try {
      const r = await api.listAccounts(query.trim())
      setAccounts(r.accounts ?? [])
      setOwnerID(query.trim())
    } catch (ex) { setToast({ msg: ex.message, type: 'error' }) }
    setLoading(false)
  }

  const refresh = async () => {
    if (!ownerID) return
    try {
      const r = await api.listAccounts(ownerID)
      setAccounts(r.accounts ?? [])
    } catch (ex) { setToast({ msg: ex.message, type: 'error' }) }
  }

  const createAccount = async (e) => {
    e.preventDefault(); setCreateErr('')
    try {
      await api.createAccount({
        idempotency_key: genKey(),
        owner_id: createForm.owner_id,
        currency: createForm.currency,
      })
      setShowCreate(false)
      setQuery(createForm.owner_id)
      setOwnerID(createForm.owner_id)
      const r = await api.listAccounts(createForm.owner_id)
      setAccounts(r.accounts ?? [])
      setToast({ msg: 'Account created', type: 'success' })
    } catch (ex) { setCreateErr(ex.message) }
  }

  const totalBalance = accounts?.reduce((a, acc) => a + parseFloat(acc.balance?.amount ?? 0), 0) ?? 0

  return (
    <div className="page">
      {toast && <Toast message={toast.msg} type={toast.type} onDone={() => setToast(null)} />}

      {/* Header */}
      <div className="ph">
        <div>
          <h1 className="text-sm font-bold text-slate-100">Accounts</h1>
          <p className="text-[11px] text-slate-600 mt-0.5">Multi-currency wallets · Holds · Journal</p>
        </div>
        <div className="flex items-center gap-2">
          {accounts?.length > 0 && (
            <span className="hidden sm:flex items-center gap-1.5 text-[11px] text-slate-500
                             bg-slate-800/40 border border-slate-800 px-2.5 py-1 rounded-full">
              <Wallet size={10} className="text-indigo-400" />
              {accounts.length} wallet{accounts.length !== 1 ? 's' : ''}
            </span>
          )}
          <button className="btn-primary" onClick={() => setShowCreate(true)}>
            <Plus size={12} /> New Account
          </button>
        </div>
      </div>

      {/* Search bar */}
      <div className="flex-shrink-0 px-6 py-3 border-b border-slate-800/80 bg-slate-900/20">
        <form onSubmit={search} className="flex gap-2">
          <div className="relative flex-1">
            <Search size={12} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-600 pointer-events-none" />
            <input className="pl-9 h-9 font-mono text-xs" value={query}
              onChange={e => setQuery(e.target.value)}
              placeholder="Search by owner ID…" />
          </div>
          <button type="submit" disabled={loading} className="btn-primary px-5 h-9">
            {loading ? <RefreshCw size={12} className="animate-spin" /> : <Search size={12} />}
            Search
          </button>
          {ownerID && (
            <button type="button" onClick={refresh} className="btn-ghost h-9">
              <RefreshCw size={12} />
            </button>
          )}
        </form>
      </div>

      {/* Results */}
      <div className="ps">
        {accounts === null && (
          <div className="flex flex-col items-center justify-center h-full min-h-[300px]">
            <div className="relative mb-5">
              <div className="absolute inset-0 rounded-2xl bg-indigo-500/8 blur-xl" />
              <div className="relative w-16 h-16 rounded-2xl bg-slate-800/80 border border-slate-700/40
                              flex items-center justify-center anim-float">
                <Wallet size={24} className="text-slate-600" />
              </div>
            </div>
            <p className="text-sm font-medium text-slate-400 mb-1">Search by owner ID</p>
            <p className="text-xs text-slate-600">Each owner can have multiple currency wallets</p>
          </div>
        )}

        {accounts?.length === 0 && (
          <div className="flex flex-col items-center justify-center h-full min-h-[300px] text-center">
            <p className="text-sm text-slate-500 mb-2">
              No accounts for <span className="font-mono text-slate-400">{ownerID}</span>
            </p>
            <button className="btn-primary" onClick={() => setShowCreate(true)}>
              <Plus size={12} /> Create First Account
            </button>
          </div>
        )}

        {accounts?.length > 0 && (
          <>
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-3">
                <p className="text-xs text-slate-600">
                  <span className="font-medium text-slate-300">{accounts.length}</span>
                  {' '}wallet{accounts.length !== 1 ? 's' : ''} ·{' '}
                  <span className="font-mono text-slate-500">{ownerID.slice(0, 20)}…</span>
                </p>
                <TrendingUp size={11} className="text-slate-700" />
                <span className="text-xs font-mono text-slate-600">
                  Σ {totalBalance.toFixed(2)}
                </span>
              </div>
              <p className="text-[10px] text-slate-700">Click Entries for inline journal</p>
            </div>
            <div className="grid grid-cols-1 lg:grid-cols-2 xl:grid-cols-3 gap-4">
              {accounts.map((acc, i) => (
                <AccountCard
                  key={acc.id}
                  acc={acc}
                  index={i}
                  onRefresh={refresh}
                  onToast={(msg, type) => setToast({ msg, type })}
                />
              ))}
            </div>
          </>
        )}
      </div>

      {showCreate && (
        <Modal title="Create Account" onClose={() => setShowCreate(false)}>
          <form onSubmit={createAccount} className="space-y-3">
            <div>
              <label>Owner ID</label>
              <input value={createForm.owner_id}
                onChange={e => setCreateForm({...createForm, owner_id: e.target.value})}
                placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" required className="font-mono text-xs" />
              <p className="mt-1 text-[10px] text-slate-700">Use a UUID — one owner can have many wallets across currencies</p>
            </div>
            <div>
              <label>Currency</label>
              <div className="grid grid-cols-3 gap-1.5 mt-1">
                {CURRENCIES.map(c => (
                  <button
                    key={c}
                    type="button"
                    onClick={() => setCreateForm({...createForm, currency: c})}
                    className="py-1.5 rounded-lg text-xs font-bold transition-all duration-200"
                    style={createForm.currency === c ? {
                      color: CUR_COLOR[c], background: `${CUR_COLOR[c]}15`,
                      border: `1px solid ${CUR_COLOR[c]}30`,
                    } : {
                      color: '#475569', background: 'rgba(30,33,50,0.4)',
                      border: '1px solid rgba(51,65,85,0.4)',
                    }}
                  >
                    {c}
                  </button>
                ))}
              </div>
            </div>
            {createErr && (
              <p className="text-xs text-red-400 bg-red-950/40 border border-red-800/30 rounded px-3 py-2">
                {createErr}
              </p>
            )}
            <div className="flex gap-2 pt-1">
              <button type="submit" className="btn-primary flex-1 justify-center">Create</button>
              <button type="button" onClick={() => setShowCreate(false)} className="btn-ghost flex-1 justify-center">Cancel</button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  )
}
