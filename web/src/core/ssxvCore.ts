// SSXVCore 适配器：UI 与核心的唯一边界（TECH_SPEC §5.2）。
import type { DecodeMeta, WorkerResponse } from './worker';

export interface DecodeResult extends DecodeMeta {
  url: string; // blob URL
}

export interface LiveFrame {
  ok: boolean;
  transient: boolean; // 信号未就绪（如 VIS 未检出），非错误
  url?: string; // ok=true 时的 blob URL
  w?: number;
  h?: number;
  mode?: string;
  ms?: number;
  seconds: number;
}

type Listener = (msg: WorkerResponse) => void;

class SSXVCore {
  private worker: Worker;
  private listeners = new Set<Listener>();
  private nextId = 1;
  private pendingFile = new Map<number, string>();
  private pendingLive = new Map<number, (f: LiveFrame) => void>();
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
        if (ev.data.type === 'liveFrame') {
          const cb = this.pendingLive.get(0);
          if (cb) {
            this.pendingLive.delete(0);
            const f = ev.data;
            cb({
              ok: f.ok ?? false,
              transient: f.transient ?? false,
              url: f.png ? URL.createObjectURL(new Blob([f.png as unknown as BlobPart], { type: 'image/png' })) : undefined,
              w: f.w,
              h: f.h,
              mode: f.mode,
              ms: f.ms,
              seconds: f.seconds,
            });
          }
          return;
        }
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

  // ---- 实时链路（M4）----
  // 快照式：主线程推 PCM 块，按需请求整段解码。单飞（同一时刻至多一个快照在途）。
  private livePending = false;

  liveStart(sampleRate: number): void {
    this.worker.postMessage({ type: 'liveStart', sampleRate });
  }

  livePush(pcm: Int16Array): void {
    this.worker.postMessage({ type: 'livePush', pcm }, [pcm.buffer as ArrayBuffer]);
  }

  liveSnapshot(): Promise<LiveFrame> {
    if (this.livePending) {
      return Promise.resolve({ ok: false, transient: true, seconds: -1 });
    }
    this.livePending = true;
    return new Promise((resolve) => {
      this.pendingLive.set(0, (f) => {
        this.livePending = false;
        resolve(f);
      });
      this.worker.postMessage({ type: 'liveSnapshot' });
    });
  }
}

export const core = new SSXVCore();
