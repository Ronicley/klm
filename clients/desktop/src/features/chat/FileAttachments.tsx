import { FileText, X } from 'lucide-react';
import { IconButton } from '../../design-system/Button';
import { useEffect, useState } from 'react';
import { ENGINE_URL } from '../../engine';

export type FileInfo = { name: string; size: number; truncated?: boolean; type?: string; url?: string };

function AttachmentPreview({ file }: { file: FileInfo }) {
  const [localURL, setLocalURL] = useState('');
  const [failed, setFailed] = useState(false);
  const [retry, setRetry] = useState(0);
  const localFile = file instanceof File ? file : null;
  const remotePath = file.url?.startsWith('/api/sessions/') || file.url?.startsWith('/api/images/') ? file.url : '';
  useEffect(() => {
    const controller = new AbortController();
    let url = '';
    setLocalURL('');
    setFailed(false);
    if (localFile?.type.startsWith('image/')) {
      url = URL.createObjectURL(localFile);
      setLocalURL(url);
    } else if (remotePath) {
      // Desktop CSP allows blob images, not direct engine-origin image URLs.
      void fetch(`${ENGINE_URL}${remotePath}`, { credentials: 'omit', signal: controller.signal }).then(async response => {
        if (!response.ok) throw new Error('Image unavailable');
        const blob = await response.blob();
        if (controller.signal.aborted) return;
        url = URL.createObjectURL(blob);
        setLocalURL(url);
      }).catch(() => { if (!controller.signal.aborted) setFailed(true); });
    }
    return () => { controller.abort(); if (url) URL.revokeObjectURL(url); };
  }, [localFile, remotePath, retry]);
  const src = localURL;
  return failed ? <IconButton label={`Retry image preview ${file.name}`} onClick={() => setRetry(current => current + 1)}><FileText /></IconButton> : src ? <a href={src} target="_blank" rel="noreferrer"><img className="file-attachment-preview" src={src} alt={file.name} loading="lazy" onError={() => setFailed(true)} /></a> : <FileText aria-hidden="true" />;
}

export function FileAttachments({ files = [], onRemove, missing = false }: { files?: FileInfo[]; onRemove?: (index: number) => void; missing?: boolean }) {
  if (!files.length) return null;
  return <ul className="file-attachments" aria-label={missing ? 'Files to reselect' : 'Attached files'}>{files.map((file, index) => <li key={index}>
    {missing ? <FileText aria-hidden="true" /> : <AttachmentPreview file={file} />}<span className="file-attachment-name" title={file.name}>{file.name}</span>
    <small>{missing ? 'Reselect file' : file.size < 1024 ? `${file.size} B` : file.size < 1024 * 1024 ? `${(file.size / 1024).toFixed(1)} KiB` : `${(file.size / (1024 * 1024)).toFixed(1)} MiB`}{file.truncated && ' · Truncated'}</small>
    {onRemove && <IconButton label={`Remove attachment ${file.name}`} onClick={() => onRemove(index)}><X /></IconButton>}
  </li>)}</ul>;
}
