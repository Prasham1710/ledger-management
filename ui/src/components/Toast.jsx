import { useEffect, useState } from 'react'
import { CheckCircle, XCircle, Info, X } from 'lucide-react'

const CONFIG = {
  success: {
    icon: CheckCircle,
    bar: 'bg-emerald-500',
    bg: 'bg-[#0d2118] border-emerald-700/50',
    icon_cls: 'text-emerald-400',
    text: 'text-emerald-100',
  },
  error: {
    icon: XCircle,
    bar: 'bg-red-500',
    bg: 'bg-[#1f0d0d] border-red-700/50',
    icon_cls: 'text-red-400',
    text: 'text-red-100',
  },
  info: {
    icon: Info,
    bar: 'bg-indigo-500',
    bg: 'bg-[#0d1020] border-indigo-700/50',
    icon_cls: 'text-indigo-400',
    text: 'text-indigo-100',
  },
}

const DURATION = 3500

export default function Toast({ message, type = 'success', onDone }) {
  const [leaving, setLeaving] = useState(false)
  const cfg = CONFIG[type] ?? CONFIG.info
  const Icon = cfg.icon

  useEffect(() => {
    const leave = setTimeout(() => setLeaving(true), DURATION - 350)
    const done  = setTimeout(onDone, DURATION)
    return () => { clearTimeout(leave); clearTimeout(done) }
  }, [onDone])

  return (
    <div
      style={{ animation: leaving ? 'slide-out-toast 0.35s ease-in forwards' : 'slide-in-toast 0.35s ease-out both' }}
      className={`fixed bottom-5 right-5 z-50 min-w-[260px] max-w-xs overflow-hidden
        rounded-xl border shadow-2xl ${cfg.bg}`}
    >
      {/* progress drain bar */}
      <div
        className={`h-0.5 ${cfg.bar} origin-left`}
        style={{ animation: `progress-drain ${DURATION}ms linear forwards` }}
      />
      <div className="flex items-start gap-3 px-4 py-3">
        <Icon size={16} className={`${cfg.icon_cls} flex-shrink-0 mt-0.5`} />
        <span className={`text-sm font-medium flex-1 leading-snug ${cfg.text}`}>{message}</span>
        <button
          onClick={() => { setLeaving(true); setTimeout(onDone, 350) }}
          className="text-slate-600 hover:text-slate-400 transition-colors flex-shrink-0 mt-0.5"
        >
          <X size={13} />
        </button>
      </div>
      <style>{`
        @keyframes slide-in-toast {
          from { opacity:0; transform:translateX(100%) scale(.9); }
          to   { opacity:1; transform:translateX(0)  scale(1);   }
        }
        @keyframes slide-out-toast {
          from { opacity:1; transform:translateX(0)   scale(1);   }
          to   { opacity:0; transform:translateX(100%) scale(.9); }
        }
        @keyframes progress-drain {
          from { transform: scaleX(1); }
          to   { transform: scaleX(0); }
        }
      `}</style>
    </div>
  )
}
