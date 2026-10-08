import { FileText, X } from 'lucide-react';
import { IconButton } from '../../design-system/Button';

export type FileInfo = { name: string; size: number; truncated?: boolean };

export function FileAttachments({ files = [], onRemove, missing = false }: { files?: FileInfo[]; onRemove?: (index: number) => void; missing?: boolean }) {
  if (!files.length) return null;
  return <ul className="file-attachments" aria-label={missing ? 'Files to reselect' : 'Attached files'}>{files.map((file, index) => <li key={index}>
    <FileText aria-hidden="true" /><span className="file-attachment-name" title={file.name}>{file.name}</span>
    <small>{missing ? 'Reselect file' : file.size < 1024 ? `${file.size} B` : file.size < 1024 * 1024 ? `${(file.size / 1024).toFixed(1)} KiB` : `${(file.size / (1024 * 1024)).toFixed(1)} MiB`}{file.truncated && ' · Truncated'}</small>
    {onRemove && <IconButton label={`Remove attachment ${file.name}`} onClick={() => onRemove(index)}><X /></IconButton>}
  </li>)}</ul>;
}
