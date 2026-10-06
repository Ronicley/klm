import type { Harness, Session } from '../../engine';
import { HarnessIcon } from './HarnessIcon';

export function GeneralHarnessPicker({ session, harnesses, disabled, onChange }: {
  session: Session;
  harnesses: Harness[];
  disabled: boolean;
  onChange: (harness: Harness['id']) => void;
}) {
  if (session.harnessLocked) return null;
  return <fieldset className="harness-picker general-harness-picker" aria-label="General agent harness" disabled={disabled}>
    {harnesses.map(harness => <label key={harness.id} className={`harness-option ${harness.id === session.harness ? 'is-active' : ''}`} title={harness.error}>
      <input type="radio" name={`general-harness-${session.id}`} value={harness.id} checked={harness.id === session.harness} disabled={!harness.available} onChange={() => onChange(harness.id)} />
      <span className="side-harness-icon" aria-hidden="true"><HarnessIcon harness={harness.id} /></span><span>{harness.name}</span>
      {!harness.available && <small>Not installed</small>}
    </label>)}
  </fieldset>;
}
