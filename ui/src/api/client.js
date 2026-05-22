const BASE = '/api'

function authHeader() {
  const token = sessionStorage.getItem('jwt')
  return token ? { Authorization: `Bearer ${token}` } : {}
}

async function req(method, path, body) {
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 30000)
  try {
    const res = await fetch(BASE + path, {
      method,
      signal: controller.signal,
      headers: {
        ...authHeader(),
        ...(body ? { 'Content-Type': 'application/json' } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
    })
    if (res.status === 204) return {}
    if (res.status === 401) {
      sessionStorage.removeItem('jwt')
      window.location.reload()
      return {}
    }
    const data = await res.json().catch(() => ({}))
    if (!res.ok) throw new Error(data.error || data.message || data.detail || `HTTP ${res.status}`)
    return data
  } catch (err) {
    if (err.name === 'AbortError') throw new Error('Request timed out — is the backend running?')
    throw err
  } finally {
    clearTimeout(timeout)
  }
}

export const api = {
  health: () => req('GET', '/health'),

  listAccounts:  (ownerID)      => req('GET',    `/accounts?owner_id=${encodeURIComponent(ownerID)}`),
  createAccount: (body)          => req('POST',   '/accounts', body),
  getAccount:    (id)            => req('GET',    `/accounts/${id}`),
  credit:        (id, body)      => req('POST',   `/accounts/${id}/credit`, body),
  debit:         (id, body)      => req('POST',   `/accounts/${id}/debit`, body),
  placeHold:     (id, body)      => req('POST',   `/accounts/${id}/holds`, body),
  releaseHold:   (id, holdId)    => req('DELETE', `/accounts/${id}/holds/${holdId}`),

  getBalance:  (accountId)       => req('GET',  `/ledger/${accountId}/balance`),
  listEntries: (accountId, ps)   => req('GET',  `/ledger/${accountId}/entries?page_size=${ps ?? 20}`),
  postEntry:   (body)            => req('POST', '/ledger/entries', body),

  getQuote:  (from, to, amount) => req('POST', '/fx/quote', { from_currency: from, to_currency: to, amount }),
  lockQuote: (id, idemKey)      => req('POST', `/fx/quote/${id}/lock`, { idempotency_key: idemKey }),

  initiatePayment:      (body) => req('POST', '/transactions', body),
  getTransaction:       (id)   => req('GET',  `/transactions/${id}`),
  getTransactionTrace:  (id)   => req('GET',  `/transactions/${id}/trace`),

  initiatePayout: (body) => req('POST', '/payouts', body),
  getPayout:      (id)   => req('GET',  `/payouts/${id}`),
}

export function genKey() {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 7)
}
