import { IS_MOBILE_HOST } from '../../platform';

type NativeAudio = { postMessage: (action: 'start' | 'stop' | 'cancel', id: string) => void };
type AudioResult = { audio: Blob; filename: string };
type AudioResponse = { id: string; status: string; data?: string; error?: string };

let sequence = 0;

/** One recording owns its listener until completion, cancellation or interruption. */
export class MobileAudioRecording {
  private readonly id = `${Date.now()}-${++sequence}`;
  private readonly bridge: NativeAudio;
  private chunks: Uint8Array<ArrayBuffer>[] = [];
  private size = 0;
  private timer?: number;
  private pending?: { resolve: (result?: AudioResult) => void; reject: (error: Error) => void };
  private finished = false;

  constructor(private readonly signal: AbortSignal, private readonly onInterrupted: (message: string) => void) {
    const bridge = (window as Window & { KlmNativeAudio?: NativeAudio }).KlmNativeAudio;
    if (!IS_MOBILE_HOST || !bridge) throw new Error('Update the KLM Harness app to record audio.');
    this.bridge = bridge;
    window.addEventListener('klm-audio-response', this.receive);
    signal.addEventListener('abort', this.cancel, { once: true });
  }

  async start(): Promise<void> {
    await this.command('start', 90000);
  }

  async stop(): Promise<AudioResult> {
    const result = await this.command('stop', 60000);
    if (!result) throw new Error('No audio was recorded. Retry.');
    return result;
  }

  private command(action: 'start' | 'stop', timeout: number): Promise<AudioResult | undefined> {
    if (this.finished || this.signal.aborted) return Promise.reject(new DOMException('Cancelled', 'AbortError'));
    return new Promise((resolve, reject) => {
      this.pending = { resolve, reject };
      this.timer = window.setTimeout(() => this.fail('Audio recording timed out. Retry.'), timeout);
      try { this.bridge.postMessage(action, this.id); }
      catch { this.fail('Could not connect to the microphone. Retry.'); }
    });
  }

  private receive = (event: Event) => {
    const message = (event as CustomEvent<AudioResponse>).detail;
    if (this.finished || !message || message.id !== this.id) return;
    if (message.status === 'error') { this.fail(message.error || 'Audio recording failed. Retry.'); return; }
    if (message.status === 'data' && typeof message.data === 'string') {
      try {
        const decoded = atob(message.data);
        this.size += decoded.length;
        if (this.size > 25 * 1024 * 1024) { this.fail('Audio exceeds the 25 MiB limit. Record a shorter message.'); return; }
        this.chunks.push(Uint8Array.from(decoded, char => char.charCodeAt(0)));
      } catch { this.fail('Could not read the recorded audio. Retry.'); }
      return;
    }
    if (message.status === 'started') {
      window.clearTimeout(this.timer);
      const pending = this.pending;
      this.pending = undefined;
      pending?.resolve();
    } else if (message.status === 'complete') {
      if (!this.size) { this.fail('No audio was recorded. Retry.'); return; }
      const audio = new Blob(this.chunks, { type: 'audio/mp4' });
      const pending = this.pending;
      this.cleanup();
      pending?.resolve({ audio, filename: 'recording.m4a' });
    }
  };

  private fail(message: string) {
    const pending = this.pending;
    this.cleanup();
    this.releaseNative();
    if (pending) pending.reject(new Error(message));
    else this.onInterrupted(message);
  }

  private cancel = () => {
    if (this.finished) return;
    const pending = this.pending;
    this.cleanup();
    this.releaseNative();
    pending?.reject(new DOMException('Cancelled', 'AbortError'));
  };

  private releaseNative() {
    try { this.bridge.postMessage('cancel', this.id); }
    catch { /* The host also cancels capture when its document/route is disposed. */ }
  }

  private cleanup() {
    this.finished = true;
    window.clearTimeout(this.timer);
    this.pending = undefined;
    this.chunks = [];
    window.removeEventListener('klm-audio-response', this.receive);
    this.signal.removeEventListener('abort', this.cancel);
  }
}
