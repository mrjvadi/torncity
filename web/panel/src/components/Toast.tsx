import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';

type Tone = 'ok' | 'bad';
interface Toast {
  id: number;
  tone: Tone;
  text: string;
}

const Ctx = createContext<(tone: Tone, text: string) => void>(() => {});

let next = 1;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const push = useCallback((tone: Tone, text: string) => {
    const id = next++;
    setToasts((l) => [...l, { id, tone, text }]);
    window.setTimeout(() => setToasts((l) => l.filter((x) => x.id !== id)), tone === 'ok' ? 4000 : 8000);
  }, []);
  const value = useMemo(() => push, [push]);
  return (
    <Ctx.Provider value={value}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {toasts.map((x) => (
          <div key={x.id} className={`toast ${x.tone}`}>
            {x.text}
          </div>
        ))}
      </div>
    </Ctx.Provider>
  );
}

export function useToast(): (tone: Tone, text: string) => void {
  return useContext(Ctx);
}
