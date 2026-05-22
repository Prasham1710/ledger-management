import { useState, useRef } from 'react'
import {
  Wallet, TrendingUp, Lock, Send, BookOpen, Landmark,
  CheckCircle, XCircle, RefreshCw, Play, RotateCcw,
  ArrowRight, Zap, ChevronRight, AlertCircle, User,
  ArrowDownCircle, ArrowUpCircle, Info, Layers, Shield,
  CreditCard, Building2, Globe,
} from 'lucide-react'
import { api, genKey } from '../api/client.js'

/* ─── stable seeds for this session ────────────────────────────── */
function genSeeds() {
  return {
    aliceOwnerId: crypto.randomUUID(),
    bobOwnerId:   crypto.randomUUID(),
    keys: Array.from({ length: 10 }, genKey),
  }
}

/* ─── step definitions ──────────────────────────────────────────── */
const STEPS = [
  {
    id: 'intro',
    n: 0,
    title: 'Platform Overview',
    subtitle: 'What we are building',
    color: '#6366f1',
    icon: Layers,
    actor: null,
    intro: true, // special flag — no API call, just "Continue"
  },
  {
    id: 'create-alice',
    n: 1,
    title: "Create Alice's Wallet",
    subtitle: 'INR account for the sender',
    color: '#818cf8',
    icon: Wallet,
    actor: 'alice',
    what: "Generate a unique owner ID for Alice and open a fresh INR currency wallet. The platform supports multiple wallets per owner across different currencies.",
    why: "Every payer needs an account. The idempotency_key guarantees that retrying this request on network failure never creates a duplicate.",
    method: 'POST',
    endpoint: '/api/accounts',
    fields: [
      { name: 'idempotency_key', type: 'string', required: true,  desc: 'Unique request key. If you resend this exact key the server returns the original response — never creates a duplicate account.' },
      { name: 'owner_id',        type: 'uuid',   required: true,  desc: "UUID of the person or entity who owns this wallet. One owner can have many wallets in different currencies. Alice's ID is auto-generated." },
      { name: 'currency',        type: 'string', required: true,  desc: 'ISO 4217 currency code. This wallet only ever holds balances in this currency. Supported: INR, USD, EUR, GBP, SGD, AED.' },
    ],
    getPayload: (s)    => ({ idempotency_key: s.keys[0], owner_id: s.aliceOwnerId, currency: 'INR' }),
    run: async (s, r)  => {
      const res = await api.createAccount({ idempotency_key: s.keys[0], owner_id: s.aliceOwnerId, currency: 'INR' })
      return { account: res.account }
    },
    pick: r => r.account,
  },
  {
    id: 'create-bob',
    n: 2,
    title: "Create Bob's Wallet",
    subtitle: 'INR account for the receiver',
    color: '#34d399',
    icon: Wallet,
    actor: 'bob',
    what: "Bob also gets a fresh INR wallet. Same endpoint, different owner. The platform can create any number of wallets per owner in different currencies.",
    why: "Bob is the payment recipient — his account must exist before money can be routed to it.",
    method: 'POST',
    endpoint: '/api/accounts',
    fields: [
      { name: 'idempotency_key', type: 'string', required: true,  desc: 'Unique request key. Safe to retry — server deduplicates on this key.' },
      { name: 'owner_id',        type: 'uuid',   required: true,  desc: "Bob's auto-generated UUID. Distinct from Alice's so both accounts have separate owners." },
      { name: 'currency',        type: 'string', required: true,  desc: 'INR — must match Alice to allow direct INR→INR transfers without FX conversion.' },
    ],
    getPayload: (s)    => ({ idempotency_key: s.keys[1], owner_id: s.bobOwnerId, currency: 'INR' }),
    run: async (s, r)  => {
      const res = await api.createAccount({ idempotency_key: s.keys[1], owner_id: s.bobOwnerId, currency: 'INR' })
      return { account: res.account }
    },
    pick: r => r.account,
  },
  {
    id: 'fund-alice',
    n: 3,
    title: 'Fund Alice',
    subtitle: 'Credit ₹50,000 to her wallet',
    color: '#fbbf24',
    icon: ArrowDownCircle,
    actor: 'alice',
    what: "Credit Alice's account with ₹50,000. In production this comes from a bank deposit or card charge. The credit posts a matching double-entry journal record.",
    why: "Alice needs a balance before sending. A credit simultaneously increases her available balance and posts a debit against the funding source account.",
    method: 'POST',
    endpoint: '/api/accounts/{alice_id}/credit',
    fields: [
      { name: 'idempotency_key', type: 'string', required: true,  desc: 'Unique key — prevents double-credit if the same request is retried (e.g. on a network timeout).' },
      { name: 'amount',          type: 'decimal',required: true,  desc: 'Amount to credit as a decimal string. Always send 4 decimal places (e.g. "50000.0000"). The platform rejects ambiguous floats.' },
      { name: 'currency',        type: 'string', required: true,  desc: "Must exactly match the account's currency (INR). A currency mismatch is rejected with 400." },
      { name: 'reference_id',    type: 'string', required: false, desc: 'Your internal reference for this deposit — e.g. an incoming bank transfer ID. Stored for reconciliation; not used for deduplication.' },
    ],
    getPayload: (s, r) => ({
      idempotency_key: s.keys[2],
      amount: '50000.0000',
      currency: 'INR',
      reference_id: 'tut-initial-fund',
    }),
    run: async (s, r)  => {
      await api.credit(r['create-alice'].account.id, {
        idempotency_key: s.keys[2],
        amount: '50000.0000',
        currency: 'INR',
        reference_id: 'tut-initial-fund',
      })
      const bal = await api.getBalance(r['create-alice'].account.id)
      return { balance: bal.balance }
    },
    pick: r => ({ available: r.balance?.amount, currency: r.balance?.currency }),
  },
  {
    id: 'fx-quote',
    n: 4,
    title: 'Get FX Quote',
    subtitle: 'Lock a USD → INR rate for 30 s',
    color: '#60a5fa',
    icon: TrendingUp,
    actor: null,
    what: "Request a live FX quote to convert 100 USD to INR. The server returns a quote with a 30-second validity window during which the rate is guaranteed.",
    why: "FX rates fluctuate. Locking the rate for 30 seconds protects both parties from rate slippage between the time of quoting and actual settlement.",
    method: 'POST',
    endpoint: '/api/fx/quote',
    fields: [
      { name: 'from_currency', type: 'string', required: true, desc: 'ISO 4217 code of the currency you are converting from. The amount you provide is denominated in this currency.' },
      { name: 'to_currency',   type: 'string', required: true, desc: 'ISO 4217 code of the currency you want the rate for. The server returns how much you get in this currency.' },
      { name: 'amount',        type: 'decimal',required: true, desc: 'Amount in from_currency to get a rate quote for. The response shows the equivalent in to_currency at the current market mid-rate.' },
    ],
    getPayload: (s)    => ({ from_currency: 'USD', to_currency: 'INR', amount: '100' }),
    run: async (s, r)  => {
      const res  = await api.getQuote('USD', 'INR', '100')
      const lock = await api.lockQuote(res.quote.id, s.keys[3])
      return { quote: res.quote, locked: lock.locked }
    },
    pick: r => ({
      quote_id:      r.quote?.id,
      rate:          r.quote?.rate,
      source_amount: r.quote?.source_amount?.amount + ' USD',
      target_amount: r.quote?.target_amount?.amount + ' INR',
      locked:        r.locked,
      expires_at:    r.quote?.expires_at,
    }),
  },
  {
    id: 'send-payment',
    n: 5,
    title: 'Send Payment',
    subtitle: 'Alice → Bob · ₹1,000',
    color: '#f472b6',
    icon: Send,
    actor: 'both',
    what: "Alice sends ₹1,000 to Bob. Internally this runs a distributed saga: the platform debits Alice, validates the balance, then credits Bob — atomically. On any failure the saga auto-reverses.",
    why: "The saga pattern ensures exactly-once delivery. The idempotency_key prevents duplicate payments even on client retries. Pending transactions auto-poll every 3 s in the Transactions tab.",
    method: 'POST',
    endpoint: '/api/transactions',
    fields: [
      { name: 'idempotency_key',   type: 'string', required: true,  desc: 'Unique key — prevents duplicate payments on retry. The server returns the same transaction record for any repeat call with this key.' },
      { name: 'source_account_id', type: 'uuid',   required: true,  desc: "Account to debit (Alice's wallet). Must have sufficient available balance. Populated from Step 1's response." },
      { name: 'dest_account_id',   type: 'uuid',   required: true,  desc: "Account to credit (Bob's wallet). Populated from Step 2's response. The platform verifies this account exists before debiting the source." },
      { name: 'amount',            type: 'decimal',required: true,  desc: 'Transfer amount as a decimal string. Must be ≤ source account available balance. Held balance is not eligible.' },
      { name: 'currency',          type: 'string', required: true,  desc: 'Currency of the transfer. Both accounts must share this currency for a direct transfer. Cross-currency requires an FX quote.' },
      { name: 'description',       type: 'string', required: false, desc: 'Human-readable memo. Stored on the transaction record and visible in both accounts\' ledger entries.' },
    ],
    getPayload: (s, r) => ({
      idempotency_key:   s.keys[4],
      source_account_id: r['create-alice']?.account?.id ?? '…pending…',
      dest_account_id:   r['create-bob']?.account?.id   ?? '…pending…',
      amount:     '1000.0000',
      currency:   'INR',
      description: 'Tutorial payment — Alice to Bob',
    }),
    run: async (s, r)  => {
      const res = await api.initiatePayment({
        idempotency_key:   s.keys[4],
        source_account_id: r['create-alice'].account.id,
        dest_account_id:   r['create-bob'].account.id,
        amount: '1000.0000',
        currency: 'INR',
        description: 'Tutorial payment — Alice to Bob',
      })
      let txn = res.transaction
      for (let i = 0; i < 12 && txn.status === 'TRANSACTION_STATUS_PENDING'; i++) {
        await new Promise(res => setTimeout(res, 1500))
        const p = await api.getTransaction(txn.id)
        txn = p.transaction
      }
      return { transaction: txn }
    },
    pick: r => ({
      id:     r.transaction?.id,
      status: r.transaction?.status?.replace('TRANSACTION_STATUS_', ''),
      amount: r.transaction?.amount?.amount + ' ' + r.transaction?.amount?.currency,
    }),
  },
  {
    id: 'check-ledger',
    n: 6,
    title: 'Inspect Ledger',
    subtitle: 'Double-entry journal for both',
    color: '#a78bfa',
    icon: BookOpen,
    actor: 'both',
    what: "Fetch the ledger entries for both Alice and Bob. Every transaction posts two symmetrical journal entries (debit + credit), creating an immutable audit trail that never allows money to appear or disappear.",
    why: "Double-entry invariant: total debits always equal total credits across all accounts. The balance can be proven at any point in time from the append-only journal.",
    method: 'GET',
    endpoint: '/api/ledger/{account_id}/entries',
    fields: [
      { name: 'alice_account_id', type: 'uuid', required: true,  desc: "Alice's account ID (from Step 1). The API returns every journal entry ever posted to this account — credits and debits." },
      { name: 'bob_account_id',   type: 'uuid', required: true,  desc: "Bob's account ID (from Step 2). Fetched in parallel with Alice's entries — both results arrive in a single response." },
      { name: 'page_size',        type: 'int',  required: false, desc: 'Maximum entries to return per account. Entries are ordered newest-first. Default is 20; increase to paginate a high-volume account.' },
    ],
    getPayload: (s, r) => ({
      alice_account_id: r['create-alice']?.account?.id ?? '…pending…',
      bob_account_id:   r['create-bob']?.account?.id   ?? '…pending…',
      page_size: 20,
    }),
    run: async (s, r)  => {
      const [ae, be, ab, bb] = await Promise.all([
        api.listEntries(r['create-alice'].account.id, 20),
        api.listEntries(r['create-bob'].account.id,   20),
        api.getBalance(r['create-alice'].account.id),
        api.getBalance(r['create-bob'].account.id),
      ])
      return {
        alice: { entries: ae.entries ?? [], balance: ab.balance },
        bob:   { entries: be.entries ?? [], balance: bb.balance },
      }
    },
    pick: r => ({
      alice_balance: r.alice?.balance?.amount + ' INR',
      alice_entries: r.alice?.entries?.length,
      bob_balance:   r.bob?.balance?.amount   + ' INR',
      bob_entries:   r.bob?.entries?.length,
    }),
  },
  {
    id: 'payout',
    n: 7,
    title: 'Bank Payout',
    subtitle: 'Alice withdraws ₹500 via NACH',
    color: '#fb923c',
    icon: Landmark,
    actor: 'alice',
    what: "Alice withdraws ₹500 from her wallet to her bank account via NACH (National Automated Clearing House). The payout is queued, validated, and settled asynchronously by the payout rail.",
    why: "Bank rails are async — settlement takes time. The platform tracks the payout lifecycle (pending → processing → settled) and retries on transient failures using the outbox relay pattern.",
    method: 'POST',
    endpoint: '/api/payouts',
    fields: [
      { name: 'idempotency_key', type: 'string', required: true,          desc: 'Unique key — prevents duplicate payout if retried after a timeout. The server returns the same payout record on repeat calls.' },
      { name: 'account_id',      type: 'uuid',   required: true,          desc: "Alice's wallet ID (from Step 1). This balance is debited. Must have at least the requested amount available." },
      { name: 'amount',          type: 'decimal',required: true,          desc: 'Amount to withdraw as a decimal string. The platform locks this amount as a hold, then debits on settlement.' },
      { name: 'currency',        type: 'string', required: true,          desc: "Must match the account's currency. Mismatch is rejected — cross-currency payouts require conversion first." },
      { name: 'rail',            type: 'enum',   required: true,          desc: 'Bank settlement network. NACH = India (needs IFSC). SEPA = EU (needs IBAN). SWIFT = international wires.' },
      { name: 'account_name',    type: 'string', required: true,          desc: "Beneficiary's name exactly as it appears on their bank account. Used by the bank for name-matching validation." },
      { name: 'account_number',  type: 'string', required: true,          desc: 'Destination bank account number. For NACH: 9–18 digit account number. For SEPA/SWIFT: use IBAN instead.' },
      { name: 'ifsc_code',       type: 'string', required: 'NACH only',   desc: '11-character IFSC code identifying the destination branch (e.g. HDFC0001234). Required for NACH rail; not used for SEPA/SWIFT.' },
    ],
    getPayload: (s, r) => ({
      idempotency_key: s.keys[5],
      account_id:      r['create-alice']?.account?.id ?? '…pending…',
      amount:          '500.0000',
      currency:        'INR',
      rail:            'NACH',
      account_name:    'Alice Tutorial',
      account_number:  '9876543210',
      ifsc_code:       'HDFC0001234',
    }),
    run: async (s, r)  => {
      const res = await api.initiatePayout({
        idempotency_key: s.keys[5],
        account_id:      r['create-alice'].account.id,
        amount:          '500.0000',
        currency:        'INR',
        rail:            'NACH',
        account_name:    'Alice Tutorial',
        account_number:  '9876543210',
        ifsc_code:       'HDFC0001234',
      })
      return { payout: res.payout }
    },
    pick: r => ({
      id:     r.payout?.id,
      status: r.payout?.status?.replace('PAYOUT_STATUS_', ''),
      amount: r.payout?.amount?.amount + ' ' + r.payout?.amount?.currency,
      rail:   r.payout?.rail?.replace('PAYOUT_RAIL_', ''),
    }),
  },
]

/* ─── Request fields (structured parameter view) ───────────────── */
const TYPE_COLOR = { string: '#6ee7b7', uuid: '#a78bfa', decimal: '#93c5fd', int: '#fb923c', enum: '#f472b6' }

function RequestFields({ fields, payload }) {
  return (
    <div className="rounded-lg overflow-hidden divide-y divide-slate-800/50"
      style={{ background: 'rgba(0,0,0,0.35)', border: '1px solid rgba(51,65,85,0.4)' }}>
      {/* header */}
      <div className="px-3 py-2 flex items-center gap-3"
        style={{ background: 'rgba(30,33,50,0.6)' }}>
        <span className="text-[9px] font-bold text-slate-600 uppercase tracking-widest">Request body</span>
        <span className="text-[9px] text-slate-700">{fields.filter(f => f.required === true).length} required · {fields.filter(f => f.required !== true).length} optional</span>
      </div>
      {fields.map(f => {
        const raw = payload[f.name]
        const display = raw === undefined ? null
          : typeof raw === 'string' ? `"${raw}"`
          : String(raw)
        const typeColor = TYPE_COLOR[f.type] ?? '#94a3b8'
        const isConditional = typeof f.required === 'string'
        return (
          <div key={f.name} className="px-3 py-3">
            {/* top row */}
            <div className="flex items-center gap-2 mb-1.5 flex-wrap">
              <span className="font-mono text-[12px] font-semibold text-indigo-300">{f.name}</span>
              <span className="text-[8px] font-mono px-1.5 py-0.5 rounded"
                style={{ color: typeColor, background: `${typeColor}12`, border: `1px solid ${typeColor}20` }}>
                {f.type}
              </span>
              {f.required === true && (
                <span className="text-[8px] font-bold uppercase tracking-wider px-1.5 py-0.5 rounded-full
                                 text-rose-400 bg-rose-950/50 border border-rose-800/30">required</span>
              )}
              {f.required === false && (
                <span className="text-[8px] font-bold uppercase tracking-wider px-1.5 py-0.5 rounded-full
                                 text-slate-600 bg-slate-800/40 border border-slate-700/30">optional</span>
              )}
              {isConditional && (
                <span className="text-[8px] font-bold uppercase tracking-wider px-1.5 py-0.5 rounded-full
                                 text-amber-500 bg-amber-950/40 border border-amber-800/30">{f.required}</span>
              )}
            </div>
            {/* value pill */}
            {display !== null && (
              <div className="mb-1.5 inline-flex items-center gap-1.5 px-2 py-1 rounded-md max-w-full"
                style={{ background: 'rgba(99,102,241,0.08)', border: '1px solid rgba(99,102,241,0.15)' }}>
                <span className="text-[9px] text-slate-600 font-mono flex-shrink-0">value</span>
                <span className="text-[11px] font-mono text-indigo-300 truncate">{display}</span>
              </div>
            )}
            {/* desc */}
            <p className="text-[11px] text-slate-600 leading-relaxed">{f.desc}</p>
          </div>
        )
      })}
    </div>
  )
}

/* ─── JSON syntax highlight ─────────────────────────────────────── */
function JsonBlock({ data }) {
  const txt = JSON.stringify(data, null, 2)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  const html = txt
    .replace(/"([^"]+)":/g, '<span style="color:#a78bfa">"$1"</span>:')
    .replace(/: "([^"<]+)"/g, ': <span style="color:#6ee7b7">"$1"</span>')
    .replace(/: (-?\d+\.?\d*)/g, ': <span style="color:#93c5fd">$1</span>')
    .replace(/: (true|false)/g,  ': <span style="color:#fb923c">$1</span>')
    .replace(/: (null)/g,        ': <span style="color:#94a3b8">$1</span>')
  return (
    <pre
      className="text-[11px] font-mono leading-relaxed p-3 rounded-lg overflow-auto max-h-48"
      style={{ background: 'rgba(0,0,0,0.4)', color: '#cbd5e1' }}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}

/* ─── Actor avatar ──────────────────────────────────────────────── */
function Avatar({ name, color, balance }) {
  const initials = name[0].toUpperCase()
  return (
    <div className="flex flex-col items-center gap-1.5">
      <div className="w-10 h-10 rounded-full flex items-center justify-center text-sm font-bold"
        style={{ background: `${color}20`, border: `2px solid ${color}40`, color }}>
        {initials}
      </div>
      <p className="text-[11px] font-medium text-slate-400">{name}</p>
      {balance !== undefined && (
        <p className="text-[10px] font-mono text-slate-600">₹{balance}</p>
      )}
    </div>
  )
}

/* ─── Status icon ───────────────────────────────────────────────── */
function StatusIcon({ status, color }) {
  if (status === 'running')
    return <RefreshCw size={14} className="animate-spin text-indigo-400" />
  if (status === 'success')
    return <CheckCircle size={14} style={{ color: color ?? '#10b981' }} />
  if (status === 'error')
    return <XCircle size={14} className="text-red-400" />
  return null
}

/* ─── Ledger entries mini-table ─────────────────────────────────── */
function EntriesTable({ entries, label }) {
  if (!entries?.length) return (
    <div className="text-center py-3 text-xs text-slate-600">No entries</div>
  )
  return (
    <div>
      <p className="text-[9px] font-semibold text-slate-600 uppercase tracking-widest mb-1.5">{label}</p>
      <div className="space-y-1">
        {entries.map((e, i) => {
          const cr = e.type === 'ENTRY_TYPE_CREDIT'
          return (
            <div key={i} className="flex items-center gap-2 text-[11px]">
              {cr
                ? <ArrowDownCircle size={10} className="text-emerald-500 flex-shrink-0" />
                : <ArrowUpCircle   size={10} className="text-red-500 flex-shrink-0" />}
              <span className={`font-mono font-bold w-24 tabular-nums ${cr ? 'text-emerald-400' : 'text-red-400'}`}>
                {cr ? '+' : '-'}{e.amount?.amount}
              </span>
              <span className="text-slate-700 truncate">{e.referenceId}</span>
            </div>
          )
        })}
      </div>
    </div>
  )
}

/* ─── Main component ────────────────────────────────────────────── */
export default function Tutorial() {
  const [seeds,    setSeeds]    = useState(genSeeds)
  const [statuses, setStatuses] = useState({})
  const [results,  setResults]  = useState({})
  const [errors,   setErrors]   = useState({})
  const [current,  setCurrent]  = useState('intro')
  const [runningAll, setRunningAll] = useState(false)
  const abortRef = useRef(false)

  const currentStep    = STEPS.find(s => s.id === current)
  const apiSteps       = STEPS.filter(s => !s.intro)
  const completedCount = apiSteps.filter(s => statuses[s.id] === 'success').length

  const advanceTo = (stepId) => {
    const idx = STEPS.findIndex(s => s.id === stepId)
    if (idx < STEPS.length - 1) setCurrent(STEPS[idx + 1].id)
  }

  const runStep = async (stepId) => {
    const step = STEPS.find(s => s.id === stepId)
    if (step.intro) { advanceTo(stepId); return true }
    setStatuses(p => ({ ...p, [stepId]: 'running' }))
    setErrors(p => { const n = { ...p }; delete n[stepId]; return n })
    try {
      const result = await step.run(seeds, results)
      setResults(p => ({ ...p, [stepId]: result }))
      setStatuses(p => ({ ...p, [stepId]: 'success' }))
      advanceTo(stepId)
      return true
    } catch (ex) {
      setErrors(p => ({ ...p, [stepId]: ex.message }))
      setStatuses(p => ({ ...p, [stepId]: 'error' }))
      return false
    }
  }

  const runAll = async () => {
    abortRef.current = false
    setRunningAll(true)
    // skip intro, start from first API step
    for (const step of STEPS) {
      if (abortRef.current) break
      if (step.intro) { setCurrent(STEPS[1].id); continue }
      if (statuses[step.id] === 'success') continue
      setCurrent(step.id)
      const ok = await runStep(step.id)
      if (!ok) break
      await new Promise(r => setTimeout(r, 300))
    }
    setRunningAll(false)
  }

  const reset = () => {
    abortRef.current = true
    setRunningAll(false)
    setSeeds(genSeeds())
    setStatuses({})
    setResults({})
    setErrors({})
    setCurrent('intro')
  }

  const stepStatus = statuses[current]
  const stepResult = results[current]
  const stepError  = errors[current]
  // intro step is always runnable; API steps require prior steps done
  const prevsDone  = currentStep.intro
    ? true
    : STEPS.filter(s => !s.intro && s.n < currentStep.n).every(s => statuses[s.id] === 'success')
  const canRun = prevsDone && stepStatus !== 'running' && stepStatus !== 'success'

  /* Alice's and Bob's current balances from results */
  const aliceBal = results['fund-alice']?.balance?.amount
    ?? results['create-alice'] ? '0.0000' : null
  const bobBal   = results['send-payment']
    ? results['check-ledger']?.bob?.balance?.amount ?? '…'
    : results['create-bob'] ? '0.0000' : null

  return (
    <div className="page">

      {/* Header */}
      <div className="ph">
        <div>
          <h1 className="text-sm font-bold text-slate-100">Tutorial</h1>
          <p className="text-[11px] text-slate-600 mt-0.5">Interactive end-to-end payment flow · 7 API steps</p>
        </div>
        <div className="flex items-center gap-2">
          {/* Progress chip */}
          <span className="text-[11px] font-mono text-slate-600 bg-slate-800/60 border border-slate-800 px-2.5 py-1 rounded-full">
            {completedCount} / {apiSteps.length}
          </span>
          <button
            onClick={runAll}
            disabled={runningAll || completedCount === apiSteps.length}
            className="btn-primary"
          >
            {runningAll
              ? <><RefreshCw size={11} className="animate-spin" /> Running…</>
              : <><Zap size={11} /> Run All</>}
          </button>
          <button onClick={reset} className="btn-ghost">
            <RotateCcw size={11} /> Reset
          </button>
        </div>
      </div>

      {/* Overall progress bar */}
      <div className="flex-shrink-0 h-1 bg-slate-800/60">
        <div
          className="h-full transition-all duration-700"
          style={{
            width: `${(completedCount / apiSteps.length) * 100}%`,
            background: completedCount === apiSteps.length
              ? 'linear-gradient(90deg, #10b981, #34d399)'
              : 'linear-gradient(90deg, #6366f1, #818cf8)',
          }}
        />
      </div>

      <div className="flex-1 overflow-hidden flex">

        {/* ── Left: step list ── */}
        <div className="w-60 flex-shrink-0 border-r border-slate-800/80 overflow-auto py-3"
          style={{ background: 'linear-gradient(180deg, #0c0e18 0%, #090b13 100%)' }}>
          {STEPS.map((step, i) => {
            const st     = statuses[step.id]
            const active = step.id === current
            const Icon   = step.icon
            return (
              <button
                key={step.id}
                onClick={() => setCurrent(step.id)}
                className={`w-full flex items-center gap-2.5 px-3 py-2.5 text-left transition-all duration-150 group relative`}
                style={active ? {
                  background: `linear-gradient(90deg, ${step.color}10 0%, transparent 100%)`,
                  borderLeft: `2px solid ${step.color}70`,
                } : { borderLeft: '2px solid transparent' }}
              >
                {/* Step number / status */}
                <div className="w-6 h-6 rounded-full flex items-center justify-center flex-shrink-0"
                  style={st === 'success'
                    ? { background: `${step.color}20`, border: `1px solid ${step.color}40` }
                    : { background: 'rgba(30,33,50,0.8)', border: '1px solid rgba(51,65,85,0.5)' }}>
                  {st === 'running'
                    ? <RefreshCw size={10} className="animate-spin text-indigo-400" />
                    : st === 'success'
                    ? <CheckCircle size={11} style={{ color: step.color }} />
                    : st === 'error'
                    ? <XCircle size={11} className="text-red-400" />
                    : step.intro
                    ? <Layers size={10} style={{ color: step.color }} />
                    : <span className="text-[9px] font-bold text-slate-600">{step.n}</span>}
                </div>

                <div className="min-w-0 flex-1">
                  <p className={`text-[12px] font-medium leading-tight transition-colors
                    ${active ? 'text-slate-100' : st === 'success' ? 'text-slate-400' : 'text-slate-500 group-hover:text-slate-300'}`}>
                    {step.title}
                  </p>
                  <p className="text-[10px] text-slate-700 mt-0.5 leading-tight truncate">{step.subtitle}</p>
                </div>

                {active && <ChevronRight size={11} style={{ color: step.color }} className="flex-shrink-0" />}
              </button>
            )
          })}

          {/* Completion message */}
          {completedCount === apiSteps.length && completedCount > 0 && (
            <div className="mx-3 mt-4 p-3 rounded-xl border border-emerald-800/40 bg-emerald-950/30 anim-fade-up">
              <div className="flex items-center gap-2 mb-1">
                <CheckCircle size={13} className="text-emerald-400" />
                <p className="text-xs font-semibold text-emerald-400">All done!</p>
              </div>
              <p className="text-[10px] text-emerald-700 leading-relaxed">
                Full payment cycle complete. Click Reset to run again with new accounts.
              </p>
            </div>
          )}
        </div>

        {/* ── Right: step detail ── */}
        <div className="flex-1 overflow-auto p-6">
          <div className="max-w-2xl mx-auto space-y-4 anim-fade-up" key={current}>

            {/* ── INTRO PANEL ── */}
            {currentStep.intro && (
              <div className="space-y-5">
                {/* Hero */}
                <div className="rounded-2xl border border-indigo-800/30 overflow-hidden"
                  style={{ background: 'linear-gradient(135deg, #0f1124 0%, #0c0e1a 100%)' }}>

                  {/* Top gradient bar */}
                  <div className="h-1 w-full"
                    style={{ background: 'linear-gradient(90deg, #6366f1, #818cf8, #38bdf8)' }} />

                  <div className="p-7">
                    <div className="flex items-center gap-3 mb-5">
                      <div className="w-10 h-10 rounded-xl flex items-center justify-center"
                        style={{ background: 'rgba(99,102,241,0.15)', border: '1px solid rgba(99,102,241,0.3)' }}>
                        <Zap size={18} className="text-indigo-400" />
                      </div>
                      <div>
                        <h2 className="text-lg font-bold text-slate-50">Ledger Payment Platform</h2>
                        <p className="text-xs text-slate-500">End-to-end tutorial — 7 real API calls</p>
                      </div>
                    </div>

                    <p className="text-sm text-slate-400 leading-relaxed mb-6">
                      This tutorial walks you through the complete payment lifecycle — from opening bank accounts
                      to sending money and withdrawing to a bank — using real API calls against the running backend.
                      Every step shows the exact request payload and live response.
                    </p>

                    {/* Flow diagram */}
                    <div className="rounded-xl border border-slate-800/60 p-5 mb-5"
                      style={{ background: 'rgba(0,0,0,0.25)' }}>
                      <p className="text-[9px] font-bold text-slate-600 uppercase tracking-widest mb-4">
                        What we'll build — step by step
                      </p>
                      <div className="flex items-start gap-1.5 flex-wrap">
                        {[
                          { label: 'Create wallets',  desc: 'Open INR accounts for Alice & Bob', color: '#818cf8', icon: Wallet },
                          { label: 'Fund account',    desc: 'Credit ₹50,000 to Alice',           color: '#fbbf24', icon: CreditCard },
                          { label: 'FX quote',        desc: 'Lock a USD→INR rate for 30 s',      color: '#60a5fa', icon: Globe },
                          { label: 'Send payment',    desc: 'Saga: debit Alice, credit Bob',      color: '#f472b6', icon: Send },
                          { label: 'Audit ledger',    desc: 'Read double-entry journal entries',  color: '#a78bfa', icon: BookOpen },
                          { label: 'Bank payout',     desc: 'Alice withdraws ₹500 via NACH',     color: '#fb923c', icon: Building2 },
                        ].map((item, i, arr) => {
                          const Icon = item.icon
                          return (
                            <div key={item.label} className="flex items-start gap-1.5">
                              <div className="flex flex-col items-center gap-1">
                                <div className="w-8 h-8 rounded-lg flex items-center justify-center flex-shrink-0"
                                  style={{ background: `${item.color}15`, border: `1px solid ${item.color}30` }}>
                                  <Icon size={13} style={{ color: item.color }} />
                                </div>
                                <span className="text-[9px] font-semibold text-center leading-tight max-w-[60px]"
                                  style={{ color: item.color }}>{item.label}</span>
                                <span className="text-[8px] text-slate-700 text-center leading-tight max-w-[60px]">{item.desc}</span>
                              </div>
                              {i < arr.length - 1 && (
                                <ArrowRight size={11} className="text-slate-800 mt-2 flex-shrink-0" />
                              )}
                            </div>
                          )
                        })}
                      </div>
                    </div>

                    {/* How a payment works */}
                    <div className="rounded-xl border border-pink-900/30 p-4 mb-5"
                      style={{ background: 'rgba(244,114,182,0.04)' }}>
                      <div className="flex items-center gap-2 mb-3">
                        <div className="w-5 h-5 rounded-md flex items-center justify-center"
                          style={{ background: 'rgba(244,114,182,0.15)', border: '1px solid rgba(244,114,182,0.25)' }}>
                          <Send size={10} style={{ color: '#f472b6' }} />
                        </div>
                        <p className="text-[10px] font-bold text-pink-400 uppercase tracking-widest">How a payment works — the Transaction saga</p>
                      </div>
                      <p className="text-[11px] text-slate-500 mb-3 leading-relaxed">
                        When you call <span className="font-mono text-pink-300/70">POST /api/transactions</span>, the platform runs a <strong className="text-slate-400">distributed saga</strong> across multiple services. Each step either succeeds and moves forward, or fails and triggers automatic compensation (reversal) of every completed step.
                      </p>
                      <div className="flex items-center gap-1.5 flex-wrap">
                        {[
                          { label: 'PENDING',    color: '#6366f1', desc: 'Transaction record created' },
                          { label: 'PROCESSING', color: '#f59e0b', desc: 'Debit Alice, credit Bob' },
                          { label: 'COMPLETED',  color: '#10b981', desc: 'Both sides settled' },
                        ].map((s, i, arr) => (
                          <div key={s.label} className="flex items-center gap-1.5">
                            <div className="flex flex-col items-center gap-1">
                              <div className="px-2.5 py-1 rounded-full text-[9px] font-bold"
                                style={{ color: s.color, background: `${s.color}15`, border: `1px solid ${s.color}30` }}>
                                {s.label}
                              </div>
                              <span className="text-[8px] text-slate-700 max-w-[80px] text-center leading-tight">{s.desc}</span>
                            </div>
                            {i < arr.length - 1 && <ArrowRight size={10} className="text-slate-800 mb-3 flex-shrink-0" />}
                          </div>
                        ))}
                        <div className="flex items-center gap-1.5">
                          <div className="flex flex-col items-center gap-1">
                            <div className="px-2.5 py-1 rounded-full text-[9px] font-bold text-red-400"
                              style={{ background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.2)' }}>
                              REVERSED
                            </div>
                            <span className="text-[8px] text-slate-700 max-w-[80px] text-center leading-tight">On any failure, auto-compensated</span>
                          </div>
                        </div>
                      </div>
                      <div className="mt-3 flex items-start gap-1.5">
                        <Info size={10} className="text-slate-700 mt-0.5 flex-shrink-0" />
                        <p className="text-[10px] text-slate-700">Step 5 polls the transaction until it leaves PENDING — you'll see the live status update in the tutorial.</p>
                      </div>
                    </div>

                    {/* What the ledger records */}
                    <div className="rounded-xl border border-violet-900/30 p-4 mb-5"
                      style={{ background: 'rgba(167,139,250,0.04)' }}>
                      <div className="flex items-center gap-2 mb-3">
                        <div className="w-5 h-5 rounded-md flex items-center justify-center"
                          style={{ background: 'rgba(167,139,250,0.15)', border: '1px solid rgba(167,139,250,0.25)' }}>
                          <BookOpen size={10} style={{ color: '#a78bfa' }} />
                        </div>
                        <p className="text-[10px] font-bold text-violet-400 uppercase tracking-widest">What the ledger records — double-entry bookkeeping</p>
                      </div>
                      <p className="text-[11px] text-slate-500 mb-3 leading-relaxed">
                        Every payment posts <strong className="text-slate-400">two journal entries</strong> — a debit on the sender and a credit on the receiver. The ledger is <strong className="text-slate-400">append-only</strong>: entries are never modified or deleted, so balances can be proven at any point in time.
                      </p>
                      <div className="rounded-lg overflow-hidden border border-slate-800/60"
                        style={{ background: 'rgba(0,0,0,0.3)' }}>
                        <div className="grid grid-cols-3 gap-0 text-[9px] font-bold text-slate-700 uppercase tracking-widest px-3 py-1.5 border-b border-slate-800/60">
                          <span>Account</span><span>Type</span><span>Amount</span>
                        </div>
                        {[
                          { account: 'Alice', type: 'DEBIT',  amount: '−₹1,000', color: '#ef4444', bg: 'rgba(239,68,68,0.05)' },
                          { account: 'Bob',   type: 'CREDIT', amount: '+₹1,000', color: '#10b981', bg: 'rgba(16,185,129,0.05)' },
                        ].map(row => (
                          <div key={row.account} className="grid grid-cols-3 gap-0 px-3 py-2 text-[11px]"
                            style={{ background: row.bg }}>
                            <span className="font-medium text-slate-400">{row.account}</span>
                            <span className="font-mono font-bold text-[9px] tracking-wider" style={{ color: row.color }}>{row.type}</span>
                            <span className="font-mono font-bold tabular-nums" style={{ color: row.color }}>{row.amount}</span>
                          </div>
                        ))}
                        <div className="px-3 py-1.5 border-t border-slate-800/60 flex items-center gap-1.5">
                          <CheckCircle size={9} className="text-emerald-600" />
                          <span className="text-[9px] text-slate-700">Net change across all accounts = 0 — money is never created or destroyed</span>
                        </div>
                      </div>
                    </div>

                    {/* Key concepts */}
                    <div className="grid grid-cols-1 sm:grid-cols-3 gap-3 mb-6">
                      {[
                        {
                          icon: Shield,
                          color: '#10b981',
                          title: 'Idempotent writes',
                          desc: 'Every API write carries an idempotency_key — safe to retry on network failure. The server returns the same result instead of creating a duplicate.',
                        },
                        {
                          icon: Layers,
                          color: '#818cf8',
                          title: 'Append-only ledger',
                          desc: 'Entries are never updated or deleted. The full balance history is provable by replaying the immutable journal from the beginning.',
                        },
                        {
                          icon: Zap,
                          color: '#f472b6',
                          title: 'Saga auto-reversal',
                          desc: 'If any step of a payment fails, every completed step is automatically compensated — Alice is re-credited, Bob is not paid.',
                        },
                      ].map(c => {
                        const Icon = c.icon
                        return (
                          <div key={c.title} className="rounded-xl p-4 border border-slate-800/50"
                            style={{ background: 'rgba(255,255,255,0.01)' }}>
                            <div className="flex items-center gap-2 mb-2">
                              <div className="w-6 h-6 rounded-md flex items-center justify-center"
                                style={{ background: `${c.color}15`, border: `1px solid ${c.color}25` }}>
                                <Icon size={11} style={{ color: c.color }} />
                              </div>
                              <p className="text-xs font-semibold text-slate-300">{c.title}</p>
                            </div>
                            <p className="text-[11px] text-slate-600 leading-relaxed">{c.desc}</p>
                          </div>
                        )
                      })}
                    </div>

                    {/* Actors */}
                    <div className="rounded-xl p-4 border border-slate-800/50 mb-2"
                      style={{ background: 'rgba(0,0,0,0.2)' }}>
                      <p className="text-[9px] font-bold text-slate-600 uppercase tracking-widest mb-3">Actors in this tutorial</p>
                      <div className="flex items-center gap-6">
                        <div className="flex items-center gap-3">
                          <div className="w-9 h-9 rounded-full flex items-center justify-center text-sm font-bold"
                            style={{ background: 'rgba(129,140,248,0.15)', border: '2px solid rgba(129,140,248,0.3)', color: '#818cf8' }}>
                            A
                          </div>
                          <div>
                            <p className="text-xs font-semibold text-slate-300">Alice</p>
                            <p className="text-[10px] text-slate-600">Sender · gets funded · initiates payment & payout</p>
                          </div>
                        </div>
                        <ArrowRight size={14} className="text-slate-700 flex-shrink-0" />
                        <div className="flex items-center gap-3">
                          <div className="w-9 h-9 rounded-full flex items-center justify-center text-sm font-bold"
                            style={{ background: 'rgba(52,211,153,0.15)', border: '2px solid rgba(52,211,153,0.3)', color: '#34d399' }}>
                            B
                          </div>
                          <div>
                            <p className="text-xs font-semibold text-slate-300">Bob</p>
                            <p className="text-[10px] text-slate-600">Receiver · accepts the ₹1,000 payment</p>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>

                {/* Get Started button */}
                <button
                  onClick={() => setCurrent('create-alice')}
                  className="flex items-center gap-2 px-6 py-2.5 rounded-lg text-sm font-semibold
                             transition-all duration-200 active:scale-95"
                  style={{
                    background: 'linear-gradient(135deg, #6366f1 0%, #4f46e5 100%)',
                    boxShadow: '0 4px 20px rgba(99,102,241,0.35)',
                    color: '#fff',
                  }}
                >
                  <Play size={13} />
                  Get Started
                  <ChevronRight size={13} />
                </button>
              </div>
            )}

            {/* ── REGULAR STEP PANEL ── */}
            {!currentStep.intro && <>

            {/* Step header */}
            <div>
              <div className="flex items-center gap-2 mb-2">
                <span className="text-[10px] font-bold uppercase tracking-widest px-2 py-0.5 rounded-full"
                  style={{ color: currentStep.color, background: `${currentStep.color}15`, border: `1px solid ${currentStep.color}25` }}>
                  Step {currentStep.n} of {apiSteps.length}
                </span>
                {stepStatus && (
                  <span className={`text-[10px] font-bold uppercase tracking-widest px-2 py-0.5 rounded-full
                    ${stepStatus === 'success' ? 'text-emerald-400 bg-emerald-950/60 border border-emerald-800/40'
                    : stepStatus === 'error'   ? 'text-red-400 bg-red-950/60 border border-red-800/40'
                    : stepStatus === 'running' ? 'text-indigo-400 bg-indigo-950/60 border border-indigo-800/40'
                    : ''}`}>
                    {stepStatus}
                  </span>
                )}
              </div>
              <h2 className="text-xl font-bold text-slate-100">{currentStep.title}</h2>
              <p className="text-sm text-slate-500 mt-0.5">{currentStep.subtitle}</p>
            </div>

            {/* Actor diagram */}
            {(currentStep.actor === 'alice' || currentStep.actor === 'bob' || currentStep.actor === 'both') && (
              <div className="flex items-center justify-center gap-4 py-4 rounded-xl border border-slate-800/60"
                style={{ background: 'rgba(255,255,255,0.01)' }}>
                <Avatar
                  name="Alice"
                  color="#818cf8"
                  balance={aliceBal !== null ? aliceBal : undefined}
                />
                {currentStep.actor === 'both' && (
                  <>
                    <div className="flex-1 flex flex-col items-center gap-1 max-w-[120px]">
                      <div className="relative w-full h-0.5 bg-slate-800 rounded-full overflow-visible">
                        {stepStatus === 'running' ? (
                          <div className="absolute inset-y-0 left-0 right-0 rounded-full"
                            style={{ background: `linear-gradient(90deg, transparent, #6366f1, transparent)`, animation: 'shimmer 1.2s linear infinite', backgroundSize: '200% 100%' }} />
                        ) : stepStatus === 'success' ? (
                          <div className="absolute inset-0 rounded-full" style={{ background: '#10b981' }} />
                        ) : (
                          <div className="absolute inset-0 rounded-full bg-slate-700" />
                        )}
                      </div>
                      <Send size={11} className={stepStatus === 'success' ? 'text-emerald-400' : 'text-slate-700'} />
                    </div>
                    <Avatar
                      name="Bob"
                      color="#34d399"
                      balance={bobBal !== null ? bobBal : undefined}
                    />
                  </>
                )}
                {currentStep.actor === 'alice' && (
                  <div className="flex items-center gap-2">
                    <div className="w-px h-8 bg-slate-800" />
                    <div className="w-8 h-8 rounded-lg flex items-center justify-center"
                      style={{ background: `${currentStep.color}15`, border: `1px solid ${currentStep.color}25` }}>
                      <currentStep.icon size={14} style={{ color: currentStep.color }} />
                    </div>
                    <div className="w-px h-8 bg-slate-800" />
                  </div>
                )}
              </div>
            )}

            {/* What & Why */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className="rounded-xl p-4 border border-slate-800/60"
                style={{ background: 'rgba(255,255,255,0.01)' }}>
                <p className="text-[9px] font-bold text-slate-600 uppercase tracking-widest mb-2 flex items-center gap-1.5">
                  <span className="w-1 h-1 rounded-full" style={{ backgroundColor: currentStep.color }} />
                  What happens
                </p>
                <p className="text-xs text-slate-400 leading-relaxed">{currentStep.what}</p>
              </div>
              <div className="rounded-xl p-4 border border-slate-800/60"
                style={{ background: 'rgba(255,255,255,0.01)' }}>
                <p className="text-[9px] font-bold text-slate-600 uppercase tracking-widest mb-2 flex items-center gap-1.5">
                  <span className="w-1 h-1 rounded-full bg-amber-500" />
                  Why it matters
                </p>
                <p className="text-xs text-slate-500 leading-relaxed">{currentStep.why}</p>
              </div>
            </div>

            {/* Request block */}
            <div className="rounded-xl border border-slate-800/60 overflow-hidden">
              {/* endpoint bar */}
              <div className="flex items-center gap-2.5 px-4 py-2.5 border-b border-slate-800/60"
                style={{ background: 'rgba(30,33,50,0.5)' }}>
                <span className="text-[10px] font-bold uppercase tracking-widest px-1.5 py-0.5 rounded font-mono"
                  style={{
                    color: currentStep.method === 'GET' ? '#34d399' : '#818cf8',
                    background: currentStep.method === 'GET' ? 'rgba(52,211,153,0.1)' : 'rgba(129,140,248,0.1)',
                  }}>
                  {currentStep.method}
                </span>
                <span className="text-xs font-mono text-slate-400">{currentStep.endpoint}</span>
              </div>
              {/* fields or json */}
              <div className="p-4" style={{ background: 'rgba(0,0,0,0.2)' }}>
                {currentStep.fields
                  ? <RequestFields fields={currentStep.fields} payload={currentStep.getPayload(seeds, results)} />
                  : <JsonBlock data={currentStep.getPayload(seeds, results)} />}
              </div>
            </div>

            {/* Result block */}
            {stepResult && (
              <div className="rounded-xl border overflow-hidden anim-fade-up"
                style={{ borderColor: `${currentStep.color}30` }}>
                <div className="flex items-center gap-2 px-4 py-2.5 border-b"
                  style={{ background: `${currentStep.color}08`, borderColor: `${currentStep.color}20` }}>
                  <CheckCircle size={12} style={{ color: currentStep.color }} />
                  <span className="text-[11px] font-semibold" style={{ color: currentStep.color }}>Response</span>
                </div>

                {/* Special ledger view */}
                {current === 'check-ledger' ? (
                  <div className="p-4 grid grid-cols-2 gap-4" style={{ background: 'rgba(0,0,0,0.2)' }}>
                    <div>
                      <div className="flex items-center gap-2 mb-3">
                        <Avatar name="Alice" color="#818cf8" balance={stepResult.alice?.balance?.amount} />
                      </div>
                      <EntriesTable entries={stepResult.alice?.entries} label="Alice's journal" />
                    </div>
                    <div>
                      <div className="flex items-center gap-2 mb-3">
                        <Avatar name="Bob" color="#34d399" balance={stepResult.bob?.balance?.amount} />
                      </div>
                      <EntriesTable entries={stepResult.bob?.entries} label="Bob's journal" />
                    </div>
                  </div>
                ) : (
                  <div className="p-4" style={{ background: 'rgba(0,0,0,0.2)' }}>
                    <JsonBlock data={currentStep.pick(stepResult)} />
                  </div>
                )}
              </div>
            )}

            {/* Error block */}
            {stepError && (
              <div className="rounded-xl border border-red-800/40 p-4 flex gap-3 bg-red-950/20 anim-fade-up">
                <AlertCircle size={15} className="text-red-400 flex-shrink-0 mt-0.5" />
                <div>
                  <p className="text-xs font-semibold text-red-400 mb-0.5">Step failed</p>
                  <p className="text-xs text-red-600">{stepError}</p>
                </div>
              </div>
            )}

            {/* Pre-requisite warning */}
            {!prevsDone && (
              <div className="rounded-xl border border-amber-800/30 p-4 flex gap-3 bg-amber-950/20">
                <AlertCircle size={14} className="text-amber-500 flex-shrink-0 mt-0.5" />
                <p className="text-xs text-amber-600">
                  Complete the previous steps first — earlier steps generate the account IDs this step needs.
                </p>
              </div>
            )}

            {/* Action */}
            <div className="flex items-center gap-3 pt-1 flex-wrap">
              {stepStatus === 'success' ? (
                <div className="flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium"
                  style={{ color: currentStep.color, background: `${currentStep.color}10`, border: `1px solid ${currentStep.color}25` }}>
                  <CheckCircle size={14} />
                  Step {currentStep.n} complete
                </div>
              ) : (
                <button
                  onClick={() => runStep(current)}
                  disabled={!canRun || stepStatus === 'running'}
                  className="flex items-center gap-2 px-5 py-2 rounded-lg text-sm font-semibold
                             transition-all duration-200 active:scale-95 disabled:opacity-40 disabled:cursor-not-allowed"
                  style={{
                    background: `linear-gradient(135deg, ${currentStep.color}cc 0%, ${currentStep.color}99 100%)`,
                    boxShadow: `0 4px 20px ${currentStep.color}30`,
                    color: '#fff',
                  }}
                >
                  {stepStatus === 'running'
                    ? <><RefreshCw size={13} className="animate-spin" /> Running…</>
                    : <><Play size={13} /> Run Step {currentStep.n}</>}
                </button>
              )}

              {/* Re-run completed step */}
              {stepStatus === 'success' && (
                <button
                  onClick={() => {
                    setStatuses(p => { const n = {...p}; delete n[current]; return n })
                    setResults(p => { const n = {...p}; delete n[current]; return n })
                    setErrors(p => { const n = {...p}; delete n[current]; return n })
                  }}
                  className="flex items-center gap-1.5 text-xs text-slate-600 hover:text-slate-300
                             border border-slate-800/60 hover:border-slate-700 px-3 py-1.5 rounded-lg
                             transition-all duration-150"
                >
                  <RotateCcw size={10} /> Re-run
                </button>
              )}

              {/* Next step shortcut */}
              {stepStatus === 'success' && currentStep.n < STEPS.length && (
                <button
                  onClick={() => setCurrent(STEPS[currentStep.n].id)}
                  className="flex items-center gap-1.5 text-xs text-slate-500 hover:text-slate-300 transition-colors"
                >
                  Next <ChevronRight size={12} />
                </button>
              )}
            </div>

            {/* Context values (collapsed summary) */}
            {Object.keys(results).length > 0 && (
              <details className="group">
                <summary className="text-[10px] text-slate-700 hover:text-slate-500 cursor-pointer
                                    list-none flex items-center gap-1.5 transition-colors">
                  <ChevronRight size={10} className="group-open:rotate-90 transition-transform" />
                  Session context ({Object.keys(results).length} step{Object.keys(results).length !== 1 ? 's' : ''} completed)
                </summary>
                <div className="mt-2 rounded-lg p-3 text-[10px] font-mono space-y-1"
                  style={{ background: 'rgba(0,0,0,0.3)', border: '1px solid rgba(51,65,85,0.3)' }}>
                  {results['create-alice']?.account?.id && (
                    <div className="flex gap-2">
                      <span className="text-slate-700 w-28 flex-shrink-0">alice_account</span>
                      <span className="text-indigo-400 truncate">{results['create-alice'].account.id}</span>
                    </div>
                  )}
                  {results['create-bob']?.account?.id && (
                    <div className="flex gap-2">
                      <span className="text-slate-700 w-28 flex-shrink-0">bob_account</span>
                      <span className="text-emerald-400 truncate">{results['create-bob'].account.id}</span>
                    </div>
                  )}
                  {results['fund-alice']?.balance && (
                    <div className="flex gap-2">
                      <span className="text-slate-700 w-28 flex-shrink-0">alice_balance</span>
                      <span className="text-amber-400">₹{results['fund-alice'].balance.amount}</span>
                    </div>
                  )}
                  {results['fx-quote']?.quote?.id && (
                    <div className="flex gap-2">
                      <span className="text-slate-700 w-28 flex-shrink-0">fx_quote</span>
                      <span className="text-blue-400 truncate">{results['fx-quote'].quote.id}</span>
                    </div>
                  )}
                  {results['send-payment']?.transaction?.id && (
                    <div className="flex gap-2">
                      <span className="text-slate-700 w-28 flex-shrink-0">txn_id</span>
                      <span className="text-pink-400 truncate">{results['send-payment'].transaction.id}</span>
                    </div>
                  )}
                  {results['payout']?.payout?.id && (
                    <div className="flex gap-2">
                      <span className="text-slate-700 w-28 flex-shrink-0">payout_id</span>
                      <span className="text-orange-400 truncate">{results['payout'].payout.id}</span>
                    </div>
                  )}
                </div>
              </details>
            )}

            </>}

          </div>
        </div>
      </div>
    </div>
  )
}
