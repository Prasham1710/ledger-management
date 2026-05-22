import { X } from 'lucide-react'
import { useEffect, useRef } from 'react'

export default function Modal({ title, onClose, children, size = 'md' }) {
  const overlayRef = useRef(null)

  useEffect(() => {
    const esc = (e) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', esc)
    return () => window.removeEventListener('keydown', esc)
  }, [onClose])

  const maxW = size === 'lg' ? 'max-w-lg' : size === 'sm' ? 'max-w-sm' : 'max-w-md'

  return (
    <div
      ref={overlayRef}
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
      style={{ animation: 'fade-in-overlay 0.2s ease-out both' }}
    >
      <div
        className="absolute inset-0 bg-black/70 backdrop-blur-md"
        onClick={onClose}
      />
      <div
        className={`relative z-10 w-full ${maxW} rounded-2xl border border-slate-700/60 shadow-2xl overflow-hidden`}
        style={{
          background: 'linear-gradient(145deg, #131620 0%, #0e1017 100%)',
          animation: 'modal-scale-in 0.22s cubic-bezier(.175,.885,.32,1.275) both',
          boxShadow: '0 25px 80px rgba(0,0,0,.7), 0 0 0 1px rgba(99,102,241,.1)',
        }}
      >
        {/* Top accent line */}
        <div className="h-px w-full bg-gradient-to-r from-transparent via-indigo-500/50 to-transparent" />

        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-slate-800/60">
          <h2 className="text-sm font-semibold text-slate-100">{title}</h2>
          <button
            onClick={onClose}
            className="w-6 h-6 flex items-center justify-center rounded-md text-slate-500
                       hover:text-slate-200 hover:bg-slate-800 transition-all duration-150"
          >
            <X size={14} />
          </button>
        </div>

        {/* Body */}
        <div className="px-5 py-4">{children}</div>
      </div>

      <style>{`
        @keyframes fade-in-overlay {
          from { opacity: 0; }
          to   { opacity: 1; }
        }
        @keyframes modal-scale-in {
          from { opacity: 0; transform: scale(.94) translateY(8px); }
          to   { opacity: 1; transform: scale(1)   translateY(0);   }
        }
      `}</style>
    </div>
  )
}
