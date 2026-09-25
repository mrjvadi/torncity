import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';
import { Icon } from './Icon.tsx';

type Tone = 'ok' | 'bad' | 'warn' | 'info';
interface Toast {
  id: number;
  tone: Tone;
  text: string;
}

const Ctx = createContext<(tone: Tone, text: string) => void>(() => {});

let next = 1;

const ICON: Record<Tone, string> = { ok: 'check', bad: 'alert', warn: 'bell', info: 'info' };

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const dismiss = useCallback((id: number) => setToasts((l) => l.filter((x) => x.id !== id)), []);
  const push = useCallback(
    (tone: Tone, text: string) => {
      const id = next++;
      setToasts((l) => [...l.slice(-3), { id, tone, text }]);
      window.setTimeout(() => dismiss(id), tone === 'ok' || tone === 'info' ? 4000 : 8000);
    },
    [dismiss],
  );
  const value = useMemo(() => push, [push]);
  return (
    <Ctx.Provider value={value}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {toasts.map((x) => (
          <div key={x.id} className={`toast ${x.tone}`}>
            <Icon name={ICON[x.tone]} />
            <span>{x.text}</span>
            <button type="button" className="icon-btn" onClick={() => dismiss(x.id)} aria-label="×">
              <Icon name="x" size={14} />
            </button>
          </div>
        ))}
      </div>
    </Ctx.Provider>
  );
}

export function useToast(): (tone: Tone, text: string) => void {
  return useContext(Ctx);
}
