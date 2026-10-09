// SSXVCore 适配器：UI 与核心的唯一边界（TECH_SPEC §5.2）。
import type { DecodeMeta, DecodeMode, WorkerResponse } from './worker';

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

// decodeAudioPCM 用浏览器引擎解码任意容器音频（ogg/mp3/flac/m4a…），
// 下混为单声道 int16。decodeAudioData 会重采样到 AudioContext 采样率。
async function decodeAudioPCM(buf: ArrayBuffer): Promise<{ pcm: Int16Array; sampleRate: number }> {
  const ctx = new AudioContext();
  try {
    const audio = await ctx.decodeAudioData(buf);
    const n = audio.length;
    const chs = audio.numberOfChannels;
    const chans: Float32Array[] = [];
    for (let c = 0; c < chs; c++) chans.push(audio.getChannelData(c));
    const pcm = new Int16Array(n);
    for (let i = 0; i < n; i++) {
      let v = 0;
      for (let c = 0; c < chs; c++) v += chans[c][i];
      v /= chs;
      pcm[i] = Math.round(Math.max(-1, Math.min(1, v)) * 32767);
    }
    return { pcm, sampleRate: audio.sampleRate };
  } finally {
    ctx.close();
  }
}

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

  decodeFile(file: File, mode: DecodeMode = 'auto'): Promise<DecodeResult> {
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
      if (/\.wav$/i.test(file.name)) {
        // WAV：核心内解析（保留原始采样率语义）
        file.arrayBuffer().then((buf) => {
          this.worker.postMessage({ type: 'decode', id, wav: buf, fileName: file.name, mode }, [buf]);
        });
      } else {
        // 其他音频格式（ogg/mp3/flac/m4a…）：交给浏览器引擎解码，
        // 得到 Float32 PCM 后转 int16 推给核心（与实时链路同路径）
        file.arrayBuffer()
          .then((buf) => decodeAudioPCM(buf))
          .then(({ pcm, sampleRate }) => {
            this.worker.postMessage(
              { type: 'decodePCM', id, pcm, sampleRate, fileName: file.name, mode },
              [pcm.buffer as ArrayBuffer],
            );
          })
          .catch((e) => {
            off();
            reject(new Error('音频解码失败: ' + (e instanceof Error ? e.message : String(e))));
          });
      }
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

  liveSnapshot(mode: DecodeMode = 'auto'): Promise<LiveFrame> {
    if (this.livePending) {
      return Promise.resolve({ ok: false, transient: true, seconds: -1 });
    }
    this.livePending = true;
    return new Promise((resolve) => {
      this.pendingLive.set(0, (f) => {
        this.livePending = false;
        resolve(f);
      });
      this.worker.postMessage({ type: 'liveSnapshot', mode });
    });
  }
}

export const core = new SSXVCore();
