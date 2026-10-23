import { invoke } from '@tauri-apps/api/core';
import { IS_DESKTOP, IS_MOBILE_HOST } from '../../platform';

type FileResult = { id: string; cancelled?: boolean; content?: string; error?: string };

function mobileFile(request: { action: 'open' | 'save'; name?: string; content?: string }): Promise<FileResult> {
  const bridge = (window as Window & { KlmFiles?: { postMessage: (message: string) => void } }).KlmFiles;
  if (!bridge) return Promise.reject(new Error('Update KLM Harness to use graph files.'));
  const id = crypto.randomUUID();
  const message = JSON.stringify({ ...request, id });
  if (message.length > 4 * 1024 * 1024) return Promise.reject(new Error('Graph file is too large.'));
  return new Promise((resolve, reject) => {
    const receive = (event: Event) => {
      const result = (event as CustomEvent<FileResult>).detail;
      if (result?.id !== id) return;
      window.removeEventListener('klm-file-result', receive);
      if (result.error) reject(new Error(result.error)); else resolve(result);
    };
    window.addEventListener('klm-file-result', receive);
    try { bridge.postMessage(message); }
    catch (error) { window.removeEventListener('klm-file-result', receive); reject(error); }
  });
}

export async function openMobileGraphFile() {
  const result = await mobileFile({ action: 'open' });
  return result.cancelled ? null : result.content ?? '';
}

export async function saveGraphFile(name: string, content: string, type: 'json' | 'svg'): Promise<boolean> {
  if (new TextEncoder().encode(content).length > 2 * 1024 * 1024) throw new Error('Graph file exceeds 2 MiB.');
  if (IS_MOBILE_HOST) return !(await mobileFile({ action: 'save', name, content })).cancelled;
  if (IS_DESKTOP) return invoke<boolean>('save_graph_file', { name, content });
  const url = URL.createObjectURL(new Blob([content], { type: type === 'json' ? 'application/json' : 'image/svg+xml' }));
  const link = document.createElement('a'); link.href = url; link.download = name;
  document.body.append(link); link.click(); link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
  return true;
}

export async function copyGraphJSON(text: string) {
  if (navigator.clipboard && window.isSecureContext) { await navigator.clipboard.writeText(text); return; }
  // LAN Focus may use HTTP, where the Clipboard API is unavailable.
  const focused = document.activeElement;
  const area = document.createElement('textarea'); area.value = text;
  area.style.cssText = 'position:fixed;left:-9999px;top:0';
  document.body.append(area); area.select();
  try { if (!document.execCommand('copy')) throw new Error('Could not copy JSON. Export the JSON file instead.'); }
  finally { area.remove(); if (focused instanceof HTMLElement) focused.focus(); }
}
