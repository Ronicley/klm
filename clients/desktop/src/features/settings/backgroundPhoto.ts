export type BackgroundPhotoSettings = {
  mode: 'gradient' | 'image';
  blur: number;
  mask: number;
  color: string;
};
export const backgroundPhotoSettingsKey = 'klm.background-photo.v1';
const defaults: BackgroundPhotoSettings = { mode: 'gradient', blur: 0, mask: 0, color: '#000000' };

export function readBackgroundPhotoSettings(): BackgroundPhotoSettings {
  try {
    const value = JSON.parse(localStorage.getItem(backgroundPhotoSettingsKey) ?? 'null') as Partial<BackgroundPhotoSettings> | null;
    if (!value || typeof value !== 'object') return defaults;
    return {
      mode: value.mode === 'image' ? 'image' : 'gradient',
      blur: typeof value.blur === 'number' && Number.isFinite(value.blur) ? Math.max(0, Math.min(80, value.blur)) : 0,
      mask: typeof value.mask === 'number' && Number.isFinite(value.mask) ? Math.max(0, Math.min(100, value.mask)) : 0,
      color: typeof value.color === 'string' && /^#[\da-f]{6}$/i.test(value.color) ? value.color : defaults.color,
    };
  } catch { return defaults; }
}

// Keep the photo out of localStorage's small synchronous string quota.
function photoStore(action: 'read' | 'write' | 'delete', photo?: Blob): Promise<Blob | undefined> {
  return new Promise((resolve, reject) => {
    const opening = indexedDB.open('klm-backgrounds', 1);
    let blocked = false;
    opening.onupgradeneeded = () => {
      if (!opening.result.objectStoreNames.contains('images')) opening.result.createObjectStore('images');
    };
    opening.onerror = () => reject(opening.error);
    opening.onblocked = () => { blocked = true; reject(new Error('Close other KLM tabs and try again.')); };
    opening.onsuccess = () => {
      const db = opening.result;
      if (blocked) { db.close(); return; }
      db.onversionchange = () => db.close();
      try {
        const transaction = db.transaction('images', action === 'read' ? 'readonly' : 'readwrite');
        const store = transaction.objectStore('images');
        const operation = action === 'read' ? store.get('focus') : action === 'delete' ? store.delete('focus') : store.put(photo, 'focus');
        let result: Blob | undefined;
        operation.onsuccess = () => { if (action === 'read' && operation.result instanceof Blob) result = operation.result; };
        transaction.oncomplete = () => { db.close(); resolve(result); };
        transaction.onabort = () => { db.close(); reject(transaction.error ?? new Error('Image storage failed.')); };
        transaction.onerror = () => { /* The transaction abort handler reports the failure. */ };
      } catch (error) { db.close(); reject(error); }
    };
  });
}

export const readBackgroundPhoto = () => photoStore('read');
export const saveBackgroundPhoto = (photo: Blob) => photoStore('write', photo);
export const removeBackgroundPhoto = () => photoStore('delete');
