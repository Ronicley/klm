import { EllipsisVertical } from 'lucide-react';
import { useCallback, useState, useSyncExternalStore } from 'react';
import { IconButton } from '../../design-system/Button';
import { Menu } from '../../design-system/Menu';
import { Toggle } from '../../design-system/Toggle';
import { ENGINE_URL, request, type Session, type SessionResponse } from '../../engine';

type YoloIntent = { value?: boolean; saving: boolean; error: string };
const idleIntent: YoloIntent = { saving: false, error: '' };
const intents = new Map<string, YoloIntent>();
const listeners = new Map<string, Set<() => void>>();
function publishIntent(key: string, intent: YoloIntent) {
  intents.set(key, intent);
  listeners.get(key)?.forEach(listener => listener());
}

export function SessionOptions({ session, disabled, onSnapshot }: {
  session: Session; disabled: boolean; onSnapshot: (snapshot: SessionResponse) => void;
}) {
  const [open, setOpen] = useState(false);
  const key = `${ENGINE_URL}/${session.id}`;
  const subscribe = useCallback((listener: () => void) => {
    let set = listeners.get(key);
    if (!set) { set = new Set(); listeners.set(key, set); }
    set.add(listener);
    return () => { set.delete(listener); if (!set.size) listeners.delete(key); };
  }, [key]);
  const { value: preview, saving, error } = useSyncExternalStore(subscribe, () => intents.get(key) ?? idleIntent);
  const locked = disabled || saving || session.status === 'running';
  async function save(yolo: boolean) {
    if (intents.get(key)?.saving || locked) return;
    publishIntent(key, { value: yolo, saving: true, error: '' });
    let failure = '';
    try {
      onSnapshot(await request<SessionResponse>(`/api/sessions/${encodeURIComponent(session.id)}/permissions`, 'PATCH', { yolo }));
    } catch (error) {
      failure = error instanceof Error ? error.message : 'Could not change YOLO mode.';
    } finally { publishIntent(key, { saving: false, error: failure }); }
  }
  return <div className="session-options">
    {(preview ?? session.yolo) && <span className="yolo-indicator" title="YOLO mode is active for this session">YOLO</span>}
    <Menu label="Session options" className="session-options-menu" open={open} onOpenChange={setOpen} trigger={props => <IconButton {...props} label="Session options" size="sm"><EllipsisVertical /></IconButton>}>
      <Toggle label="YOLO mode" checked={preview ?? !!session.yolo} disabled={locked} onCheckedChange={value => void save(value)} title={session.status === 'running' ? 'Stop or finish the current turn to change YOLO mode' : 'Skip permission approvals and saved restrictions for this session'} />
      {error && <p className="form-error menu-feedback" role="alert">{error}</p>}
    </Menu>
  </div>;
}
