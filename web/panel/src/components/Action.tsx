import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { useI18n } from '../i18n/index.tsx';
import { newKey } from '../lib/api.ts';
import { checkReason, MAX_REASON } from '../lib/validate.ts';
import { Icon } from './Icon.tsx';
import { useToast } from './Toast.tsx';
import { useErrorText } from './ui.tsx';

// Action is a button that opens a confirmation dialog for one change: the
// change's own fields (children), a required reason, and a confirm. The
// request's idempotency key is made when the dialog opens and kept across
// retries, so a double click or a retry after a timeout acts once. A
// destructive change names what it acts on (typed): the confirm button waits
// until the operator has typed it, and the typed text is sent along.
export function Action<R>({ label, title, lead, danger, children, check, run, done, disabled, typed, icon, small }: {
  label: string;
  title?: string;
  lead?: ReactNode;
  danger?: boolean;
  children?: ReactNode;
  // check returns a message when the fields are not ready, else null.
  check?: () => string | null;
  run: (reason: string, key: string, confirm: string) => Promise<R>;
  done: (result: R) => string;
  disabled?: boolean;
  typed?: string;
  icon?: string;
  small?: boolean;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const errorText = useErrorText();
  const ref = useRef<HTMLDialogElement>(null);
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [confirm, setConfirm] = useState('');
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [key, setKey] = useState('');
  const reasonId = useId();
  const typedId = useId();
  const errId = useId();

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);

  const start = () => {
    setReason('');
    setConfirm('');
    setProblem(null);
    setKey(newKey());
    setOpen(true);
  };

  const typedOK = !typed || confirm.trim().toUpperCase() === typed.toUpperCase();

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const fieldProblem = check?.() ?? null;
    if (fieldProblem) {
      setProblem(fieldProblem);
      return;
    }
    const r = checkReason(reason);
    if (r) {
      setProblem(r === 'reason_required' ? t('err.reason_required') : t('common.reason_too_long'));
      return;
    }
    if (!typedOK) {
      setProblem(t('confirm.typed_mismatch'));
      return;
    }
    setBusy(true);
    setProblem(null);
    try {
      const result = await run(reason.trim(), key, confirm.trim());
      setOpen(false);
      toast('ok', done(result));
    } catch (err) {
      setProblem(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <button type="button" className={`btn ${small ? 'small' : ''} ${danger ? 'danger' : 'primary'}`} onClick={start} disabled={disabled}>
        {icon && <Icon name={icon} size={16} />}
        {label}
      </button>
      <dialog ref={ref} className="dialog" onClose={() => setOpen(false)} aria-labelledby={`${reasonId}-title`}>
        {open && (
          <form onSubmit={submit} noValidate>
            <div className="dialog-head">
              <h2 id={`${reasonId}-title`}>{title ?? label}</h2>
            </div>
            <p className={danger ? 'warning-box' : 'muted'}>
              {danger && <Icon name="alert" />}
              <span>{lead ?? t('confirm.lead')}</span>
            </p>
            {children}
            <label htmlFor={reasonId} className="field-label">
              {t('common.reason')} <span className="req">*</span>
            </label>
            <textarea
              id={reasonId}
              value={reason}
              maxLength={MAX_REASON * 2}
              rows={3}
              required
              aria-required="true"
              aria-describedby={problem ? errId : undefined}
              aria-invalid={problem !== null}
              onChange={(e) => setReason(e.target.value)}
            />
            <p className="hint">{t('common.reason_hint')}</p>
            {typed && (
              <div className="field">
                <label htmlFor={typedId} className="field-label">
                  {t('confirm.type_to_confirm', { code: typed })}
                </label>
                <input id={typedId} dir="ltr" value={confirm} autoComplete="off" onChange={(e) => setConfirm(e.target.value)} />
              </div>
            )}
            {problem && (
              <p id={errId} className="error" role="alert">
                {problem}
              </p>
            )}
            <div className="dialog-actions">
              <button type="button" className="btn" onClick={() => setOpen(false)} disabled={busy}>
                {t('common.cancel')}
              </button>
              <button type="submit" className={`btn ${danger ? 'danger' : 'primary'}`} disabled={busy || !typedOK}>
                {busy ? t('common.loading') : t('common.confirm')}
              </button>
            </div>
          </form>
        )}
      </dialog>
    </>
  );
}

// Field is a labelled input inside an Action's dialog or a form.
export function Field({ label, value, onChange, type, placeholder, hint, dir, inputMode, autoComplete }: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  type?: string;
  placeholder?: string;
  hint?: string;
  dir?: 'ltr' | 'rtl' | 'auto';
  inputMode?: 'text' | 'numeric' | 'decimal';
  autoComplete?: string;
}) {
  const id = useId();
  return (
    <div className="field">
      <label htmlFor={id} className="field-label">
        {label}
      </label>
      <input
        id={id}
        type={type ?? 'text'}
        value={value}
        placeholder={placeholder}
        dir={dir}
        inputMode={inputMode}
        autoComplete={autoComplete ?? 'off'}
        onChange={(e) => onChange(e.target.value)}
      />
      {hint && <p className="hint">{hint}</p>}
    </div>
  );
}

export function TextArea({ label, value, onChange, dir, hint }: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  dir?: 'ltr' | 'rtl' | 'auto';
  hint?: string;
}) {
  const id = useId();
  return (
    <div className="field">
      <label htmlFor={id} className="field-label">
        {label}
      </label>
      <textarea id={id} value={value} rows={4} dir={dir} onChange={(e) => onChange(e.target.value)} />
      {hint && <p className="hint">{hint}</p>}
    </div>
  );
}

export function Select({ label, value, onChange, options }: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: [string, string][];
}) {
  const id = useId();
  return (
    <div className="field">
      <label htmlFor={id} className="field-label">
        {label}
      </label>
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
        {options.map(([v, l]) => (
          <option key={v} value={v}>
            {l}
          </option>
        ))}
      </select>
    </div>
  );
}
