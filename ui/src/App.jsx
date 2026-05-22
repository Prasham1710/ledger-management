import { useState } from 'react'
import Sidebar from './components/Sidebar.jsx'
import Dashboard from './pages/Dashboard.jsx'
import Accounts from './pages/Accounts.jsx'
import Ledger from './pages/Ledger.jsx'
import FX from './pages/FX.jsx'
import Transactions from './pages/Transactions.jsx'
import TransactionTrace from './pages/TransactionTrace.jsx'
import Payouts from './pages/Payouts.jsx'
import Tutorial from './pages/Tutorial.jsx'
import Login from './pages/Login.jsx'

const PAGES = {
  dashboard:    Dashboard,
  accounts:     Accounts,
  ledger:       Ledger,
  fx:           FX,
  transactions: Transactions,
  payouts:      Payouts,
  tutorial:     Tutorial,
}

export default function App() {
  const [token, setToken] = useState(() => sessionStorage.getItem('jwt') || '')
  const [page,  setPage]  = useState('dashboard')

  function handleLogin(t) { sessionStorage.setItem('jwt', t); setToken(t) }
  function handleLogout() { sessionStorage.removeItem('jwt'); setToken('') }
  function handleNavigate(id, params) {
    if (id === 'trace' && params?.txnId) sessionStorage.setItem('trace_txn_id', params.txnId)
    setPage(id)
  }

  if (!token) return <Login onLogin={handleLogin} />

  if (page === 'trace') {
    return (
      <div className="flex h-full bg-slate-950 text-slate-100 overflow-hidden">
        <Sidebar current={page} onNavigate={handleNavigate} onLogout={handleLogout} />
        <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
          <TransactionTrace key="trace" onNavigate={handleNavigate} />
        </div>
      </div>
    )
  }

  const Page = PAGES[page] ?? Dashboard
  return (
    <div className="flex h-full bg-slate-950 text-slate-100 overflow-hidden">
      <Sidebar current={page} onNavigate={handleNavigate} onLogout={handleLogout} />
      <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
        <Page key={page} onNavigate={handleNavigate} />
      </div>
    </div>
  )
}
