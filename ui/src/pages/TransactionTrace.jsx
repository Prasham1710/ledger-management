import { useEffect, useRef, useState } from 'react'
import {
  ArrowLeft, CheckCircle, XCircle, Clock, RefreshCw,
  Layers, Shield, BookOpen, ArrowDownCircle, ArrowUpCircle, Flag,
} from 'lucide-react'
import { api } from '../api/client.js'

const STEP_META = {
  PlaceHold:        { icon: Shield,          label: 'Place Hold',         desc: 'Reserve funds on source account' },
  PostEntry:        { icon: BookOpen,         label: 'Post Ledger Entry',  desc: 'Write double-entry accounting record' },
  DebitAccount:     { icon: ArrowUpCircle,    label: 'Debit Account',      desc: 'Consume hold — debit source account' },
  CreditAccount:    { icon: ArrowDownCircle,  label: 'Credit Account',     desc: 'Credit destination account' },
  FinishTransaction:{ icon: Flag,             label: 'Finish Transaction', desc: 'Commit status + publish outbox event' },
}

const ALL_STEPS = ['PlaceHold', 'PostEntry', 'DebitAccount', 'CreditAccount', 'FinishTransaction']

const TXN_STATUS = {
  COMPLETED: { label: 'Completed', cls: 'text-emerald-400 bg-emerald-950/80 border-emerald-700/40' },
  FAILED:    { label: 'Failed',    cls: 'text-red-400 bg-red-950/80 border-red-700/40' },
  PENDING:   { label: 'Pending',   cls: 'text-amber-400 bg-amber-950/80 border-amber-700/40' },
  REVERSED:  { label: 'Reversed',  cls: 'text-slate-400 bg-slate-800/80 border-slate-700' },
}

function statusKey(raw) {
  if (!raw) return 'PENDING'
  return raw.replace('TRANSACTION_STATUS_', '')
}

function fmt(ts) {
  if (!ts) return '—'
  return new Date(ts).toLocaleString('en-IN', { dateStyle: 'short', timeStyle: 'medium' })
}

function StepCard({ name, step, index, isPending }) {
  const meta   = STEP_META[name] ?? { icon: Layers, label: name, desc: '' }
  const Icon   = meta.icon
  const done   = !!step
  const failed = step?.status === 'failed'

  let stateClass = 'border-slate-800/60 bg-slate-900/30'
  let iconClass  = 'text-slate-700'
  let dot        = <div className="w-2 h-2 rounded-full bg-slate-800 flex-shrink-0" />

  if (done && !failed) {
    stateClass = 'border-emerald-800/40 bg-emerald-950/20'
    iconClass  = 'text-emerald-400'
    dot        = <div className="w-2 h-2 rounded-full bg-emerald-500 flex-shrink-0" />
  } else if (done && failed) {
    stateClass = 'border-red-800/40 bg-red-950/20'
    iconClass  = 'text-red-400'
    dot        = <div className="w-2 h-2 rounded-full bg-red-500 flex-shrink-0" />
  } else if (isPending && !done) {
    dot = <div className="w-2 h-2 rounded-full bg-amber-500/60 animate-pulse flex-shrink-0" />
  }

  return (
    <div
      className={`flex gap-4 rounded-xl border p-4 ${stateClass}`}
      style={{ animation: `fade-up 0.2s ease-out ${index * 0.06}s both` }}
    >
      {/* connector dot + icon */}
      <div className="flex flex-col items-center gap-1 pt-0.5">
        <div className={`w-9 h-9 rounded-xl flex items-center justify-center flex-shrink-0
                         border ${done ? (failed ? 'border-red-800/50 bg-red-950/40' : 'border-emerald-800/50 bg-emerald-950/40') : 'border-slate-800 bg-slate-900'}`}>
          <Icon size={16} className={iconClass} />
        </div>
        {index < ALL_STEPS.length - 1 && (
          <div className={`w-px flex-1 min-h-[16px] ${done && !failed ? 'bg-emerald-800/40' : 'bg-slate-800/60'}`} />
        )}
      </div>

      {/* body */}
      <div className="flex-1 min-w-0 pb-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-xs font-semibold text-slate-200">{meta.label}</span>
          {done && !failed && (
            <span className="inline-flex items-center gap-1 text-[10px] text-emerald-400 bg-emerald-950/60 border border-emerald-800/40 px-1.5 py-0.5 rounded-full">
              <CheckCircle size={9} /> success
            </span>
          )}
          {done && failed && (
            <span className="inline-flex items-center gap-1 text-[10px] text-red-400 bg-red-950/60 border border-red-800/40 px-1.5 py-0.5 rounded-full">
              <XCircle size={9} /> failed
            </span>
          )}
          {!done && isPending && (
            <span className="inline-flex items-center gap-1 text-[10px] text-amber-400 bg-amber-950/60 border border-amber-700/40 px-1.5 py-0.5 rounded-full">
              <Clock size={9} /> waiting
            </span>
          )}
          {!done && !isPending && (
            <span className="text-[10px] text-slate-700 bg-slate-800/40 border border-slate-800 px-1.5 py-0.5 rounded-full">
              not reached
            </span>
          )}
          {done && (
            <span className="text-[10px] text-slate-600 font-mono ml-auto">{step.duration_ms} ms</span>
          )}
        </div>
        <p className="text-[11px] text-slate-600 mt-0.5">{meta.desc}</p>
        {done && step.started_at && (
          <p className="text-[10px] text-slate-700 mt-1 font-mono">{fmt(step.started_at)}</p>
        )}
        {done && failed && step.error_msg && (
          <p className="mt-2 text-[11px] text-red-400 bg-red-950/40 border border-red-800/30 rounded-lg px-3 py-2 font-mono break-all">
            {step.error_msg}
          </p>
        )}
        {/* connector dot on left side of step number badge */}
        <div className="mt-1.5 flex items-center gap-1.5">
          {dot}
          <span className="text-[9px] text-slate-700 uppercase tracking-widest">step {index + 1}</span>
        </div>
      </div>
    </div>
  )
}

export default function TransactionTrace({ onNavigate }) {
  const txnId = sessionStorage.getItem('trace_txn_id') ?? ''
  const [data,    setData]    = useState(null)
  const [loading, setLoading] = useState(false)
  const [error,   setError]   = useState('')
  const pollRef = useRef(null)

  const load = async () => {
    if (!txnId) return
    setLoading(true)
    try {
      const r = await api.getTransactionTrace(txnId)
      setData(r)
      setError('')
    } catch (ex) {
      setError(ex.message)
    }
    setLoading(false)
  }

  // poll while transaction is still pending
  useEffect(() => {
    load()
  }, [])

  useEffect(() => {
    const st = statusKey(data?.transaction?.status)
    if (st !== 'PENDING' && st !== 'PROCESSING') {
      clearInterval(pollRef.current)
      return
    }
    pollRef.current = setInterval(load, 2000)
    return () => clearInterval(pollRef.current)
  }, [data?.transaction?.status])

  const txn     = data?.transaction
  const steps   = data?.steps ?? []
  const stepMap = Object.fromEntries(steps.map(s => [s.step_name, s]))
  const sk      = statusKey(txn?.status)
  const stInfo  = TXN_STATUS[sk] ?? TXN_STATUS.PENDING
  const isPending = sk === 'PENDING' || sk === 'PROCESSING'
  const totalMs = steps.reduce((a, s) => a + (s.duration_ms ?? 0), 0)

  if (!txnId) {
    return (
      <div className="page">
        <div className="ph">
          <button onClick={() => onNavigate('transactions')} className="btn-ghost">
            <ArrowLeft size={12} /> Back
          </button>
        </div>
        <div className="flex flex-col items-center justify-center flex-1 min-h-[300px]">
          <p className="text-sm text-slate-500">No transaction selected.</p>
          <p className="text-xs text-slate-700 mt-1">Click "View Trace" on a transaction first.</p>
        </div>
      </div>
    )
  }

  return (
    <div className="page">
      {/* Header */}
      <div className="ph">
        <div className="flex items-center gap-3">
          <button onClick={() => onNavigate('transactions')} className="btn-ghost">
            <ArrowLeft size={12} /> Back
          </button>
          <div>
            <h1 className="text-sm font-bold text-slate-100 flex items-center gap-2">
              Saga Trace
              {isPending && (
                <span className="flex items-center gap-1 text-[10px] text-amber-400 bg-amber-950/40 border border-amber-700/30 px-2 py-0.5 rounded-full">
                  <span className="w-1.5 h-1.5 rounded-full bg-amber-500 animate-pulse" /> Live
                </span>
              )}
            </h1>
            <p className="text-[11px] text-slate-600 font-mono mt-0.5">{txnId}</p>
          </div>
        </div>
        <button onClick={load} disabled={loading} className="btn-ghost">
          <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
        </button>
      </div>

      <div className="flex-1 overflow-auto px-6 py-5 space-y-5 max-w-2xl mx-auto w-full">

        {error && (
          <div className="text-xs text-red-400 bg-red-950/40 border border-red-800/30 rounded-lg px-4 py-3">
            {error}
          </div>
        )}

        {/* Transaction summary card */}
        {txn && (
          <div className="rounded-xl border border-slate-800/60 bg-slate-900/40 overflow-hidden"
               style={{ animation: 'fade-up 0.2s ease-out both' }}>
            <div className="px-4 py-3 border-b border-slate-800/60 flex items-center justify-between">
              <span className="text-[10px] font-semibold text-slate-500 uppercase tracking-widest">Transaction</span>
              <span className={`inline-flex items-center gap-1 text-[11px] px-2 py-0.5 rounded-full font-medium border ${stInfo.cls}`}>
                {stInfo.label}
              </span>
            </div>
            <div className="grid grid-cols-2 sm:grid-cols-3 divide-x divide-y divide-slate-800/40">
              <div className="px-4 py-3">
                <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">Amount</p>
                <p className="text-sm font-bold font-mono text-indigo-300 tabular-nums">
                  {txn.amount} <span className="text-xs text-slate-600">{txn.currency}</span>
                </p>
              </div>
              <div className="px-4 py-3">
                <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">From</p>
                <p className="text-[11px] font-mono text-slate-500 truncate">{txn.source_account_id?.slice(0, 16)}…</p>
              </div>
              <div className="px-4 py-3">
                <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">To</p>
                <p className="text-[11px] font-mono text-slate-500 truncate">{txn.dest_account_id?.slice(0, 16)}…</p>
              </div>
              <div className="px-4 py-3">
                <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">Started</p>
                <p className="text-[11px] text-slate-500">{fmt(txn.created_at)}</p>
              </div>
              <div className="px-4 py-3">
                <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">Steps done</p>
                <p className="text-sm font-bold font-mono text-slate-300">{steps.length} / {ALL_STEPS.length}</p>
              </div>
              {steps.length > 0 && (
                <div className="px-4 py-3">
                  <p className="text-[9px] text-slate-700 uppercase tracking-widest mb-1">Total time</p>
                  <p className="text-sm font-bold font-mono text-slate-300 tabular-nums">{totalMs} ms</p>
                </div>
              )}
            </div>
          </div>
        )}

        {/* Skeleton while first load */}
        {!data && loading && (
          <div className="space-y-3">
            {ALL_STEPS.map((_, i) => (
              <div key={i} className="rounded-xl border border-slate-800/40 bg-slate-900/20 p-4 flex gap-4">
                <div className="w-9 h-9 rounded-xl skeleton flex-shrink-0" style={{ animationDelay: `${i * 0.07}s` }} />
                <div className="flex-1 space-y-2">
                  <div className="h-3 skeleton w-32 rounded" style={{ animationDelay: `${i * 0.07 + 0.05}s` }} />
                  <div className="h-2.5 skeleton w-48 rounded" style={{ animationDelay: `${i * 0.07 + 0.1}s` }} />
                </div>
              </div>
            ))}
          </div>
        )}

        {/* Step timeline */}
        {data && (
          <div className="space-y-2">
            <p className="text-[10px] font-semibold text-slate-600 uppercase tracking-widest mb-3">Saga Steps</p>
            {ALL_STEPS.map((name, i) => (
              <StepCard
                key={name}
                name={name}
                step={stepMap[name] ?? null}
                index={i}
                isPending={isPending}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
