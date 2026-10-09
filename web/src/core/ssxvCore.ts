// SSXVCore 适配器：UI 与核心的唯一边界（TECH_SPEC §5.2）。
import type { DecodeMeta, WorkerResponse } from './worker';

export interface DecodeResult extends DecodeMeta {
  url: string; // blob URL
}

type Listener = (msg: WorkerResponse) => void;

class SSXVCore {
  private worker: Worker;
  private listeners = new Set<Listener>();
  private nextId = 1;
  private pendingFile = new Map<number, string>();
  ready: Promise<void>;

  constructor() {
    this.worker = new Worker(new URL('./worker.ts', import.meta.url), { type: 'module' });
    this.ready = new Promise((resolve, reject) => {
      const onFirst = (ev: MessageEvent<WorkerResponse>) => {
        if (ev.data.type === 'ready') {
          resolve();
        } else if (ev.data.type === 'error') {
          reject(new Error(ev.data.message));
        }
      };
      this.worker.addEventListener('message', onFirst, { once: true });
      this.worker.postMessage({ type: 'init' });
      // ready 之后切到常驻监听
      this.worker.addEventListener('message', (ev: MessageEvent<WorkerResponse>) => {
        if (ev.data.type === 'ready') return;
        this.listeners.forEach((l) => l(ev.data));
      });
      // 超时保护
      setTimeout(() => reject(new Error('WASM 初始化超时（10s）')), 10_000);
    });
  }

  onMessage(l: Listener): () => void {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  }

  decodeFile(file: File): Promise<DecodeResult> {
    const id = this.nextId++;
    this.pendingFile.set(id, file.name);
    return new Promise((resolve, reject) => {
      const off = this.onMessage((msg) => {
        if (msg.type === 'decoded' && msg.id === id) {
          off();
          const blob = new Blob([msg.png as unknown as BlobPart], { type: 'image/png' });
          resolve({
            url: URL.createObjectURL(blob),
            w: msg.w,
            h: msg.h,
            ms: msg.ms,
            mode: msg.mode,
            fileName: this.pendingFile.get(id) ?? file.name,
          });
        } else if (msg.type === 'error') {
          off();
          reject(new Error(msg.message));
        }
      });
      file.arrayBuffer().then((buf) => {
        this.worker.postMessage({ type: 'decode', id, wav: buf, fileName: file.name }, [buf]);
      });
    });
  }
}

export const core = new SSXVCore();
