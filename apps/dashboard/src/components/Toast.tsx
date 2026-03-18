'use client';

import { useToastContext, Toast, ToastType } from '../context/toast';

const borderColor: Record<ToastType, string> = {
  error: 'border-l-red-500',
  warning: 'border-l-yellow-400',
  success: 'border-l-green-500',
  info: 'border-l-blox-400',
};

const iconColor: Record<ToastType, string> = {
  error: 'text-red-400',
  warning: 'text-yellow-400',
  success: 'text-green-400',
  info: 'text-blox-400',
};

function ToastItem({ toast }: { toast: Toast }) {
  const { dismissToast } = useToastContext();

  return (
    <div
      role={toast.type === 'error' ? 'alert' : 'status'}
      aria-live={toast.type === 'error' ? 'assertive' : 'polite'}
      className={`flex items-start gap-3 px-4 py-3 bg-slate-800/95 backdrop-blur-sm border border-slate-700/50 border-l-4 ${borderColor[toast.type]} rounded-lg shadow-xl w-80 max-w-full`}
    >
      <p className={`flex-1 text-sm font-medium leading-snug ${iconColor[toast.type]}`}>
        {toast.message}
      </p>
      <button
        onClick={() => dismissToast(toast.id)}
        className="text-slate-500 hover:text-white transition-colors mt-0.5 flex-shrink-0"
        aria-label="Dismiss"
      >
        <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>
    </div>
  );
}

export default function ToastContainer() {
  const { toasts } = useToastContext();

  if (toasts.length === 0) return null;

  return (
    <div className="fixed bottom-4 right-4 z-[100] flex flex-col gap-2 items-end pointer-events-none" aria-live="polite">
      {toasts.map((toast) => (
        <div key={toast.id} className="pointer-events-auto animate-slide-in-right">
          <ToastItem toast={toast} />
        </div>
      ))}
    </div>
  );
}
