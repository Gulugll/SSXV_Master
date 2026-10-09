// SSXVCore Worker 协议（TECH_SPEC §5.2/§5.3）。
// Worker 内实例化 WASM，主线程只管 UI。

export interface DecodeMeta {
  w: number;
  h: number;
  ms: number;
  fileName: string;
  mode: string;
}

export type WorkerRequest =
  | { type: 'init' }
  | { type: 'decode'; id: number; wav: ArrayBuffer; fileName: string; mode?: DecodeMode }
  | { type: 'decodePCM'; id: number; pcm: Int16Array; sampleRate: number; fileName: string; mode?: DecodeMode }
  // 实时链路（M4）：主线程推 int16 PCM 块，快照式整体解码
  | { type: 'liveStart'; sampleRate: number }
  | { type: 'livePush'; pcm: Int16Array }
  | { type: 'liveSnapshot'; mode?: DecodeMode };

// 解码链路选择："auto" VIS 自动识别 / "sstv" 强制 SSTV（VIS 判模式）/
// "sstv:<Name>" 强制指定 SSTV 模式 / "ssdv" 强制 SSDV
export type DecodeMode = 'auto' | 'ssdv' | 'sstv' | `sstv:${string}`;

export type WorkerResponse =
  | { type: 'ready'; abi: number }
  | { type: 'error'; message: string }
  | ({ type: 'decoded'; id: number; png: Uint8Array } & DecodeMeta)
  // 实时快照结果：ok=false 且 transient=true 表示信号尚未就绪（如未检出 VIS），不算错误
  | {
      type: 'liveFrame';
      ok: boolean;
      transient?: boolean;
      png?: Uint8Array;
      w?: number;
      h?: number;
      mode?: string;
      ms?: number;
      seconds: number;
    };

let goReady: Promise<void> | null = null;

// 实时累积缓冲（worker 内，主线程只推块）
let liveBuf: Int16Array | null = null;
let liveLen = 0;
let liveFs = 48000;

async function loadWasm(): Promise<void> {
  // Go 官方胶水（IIFE，执行后挂 globalThis.Go），由 Vite 打包进 worker
  await import('../vendor/go_wasm_exec.js');
  if (typeof (globalThis as any).Go !== 'function') {
    throw new Error('E_INTERNAL: wasm_exec.js 加载失败');
  }
  const go = new (globalThis as any).Go();
  const res = await WebAssembly.instantiateStreaming(
    fetch('/wasm/ssxv.wasm'),
    go.importObject,
  );
  go.run(res.instance);
}

function ensureReady(): Promise<void> {
  if (!goReady) {
    goReady = loadWasm();
  }
  return goReady;
}

interface DecodeResult {
  ok: boolean;
  w?: number;
  h?: number;
  mode?: string;
  pngLen?: number;
  error?: string;
}

function appendLive(pcm: Int16Array): void {
  if (!liveBuf || liveBuf.length < liveLen + pcm.length) {
    // 倍增扩容
    const cap = Math.max(48000 * 10, liveLen + pcm.length, liveBuf ? liveBuf.length * 2 : 0);
    const next = new Int16Array(cap);
    if (liveBuf && liveLen > 0) next.set(liveBuf.subarray(0, liveLen));
    liveBuf = next;
  }
  liveBuf.set(pcm, liveLen);
  liveLen += pcm.length;
}

function liveSnapshot(mode: DecodeMode): WorkerResponse {
  const seconds = liveLen / liveFs;
  if (!liveBuf || liveLen === 0) {
    return { type: 'liveFrame', ok: false, transient: true, seconds: 0 };
  }
  const g = globalThis as any;
  const t0 = performance.now();
  const res: DecodeResult = g.ssxvDecodePCM(
    liveBuf.subarray(0, liveLen) as unknown as Uint8Array,
    liveFs,
    mode,
  );
  const ms = Math.round(performance.now() - t0);
  if (!res.ok) {
    // 接收初期常态：VIS 未检出 / 信号不足
    return { type: 'liveFrame', ok: false, transient: true, seconds, ms };
  }
  const png = new Uint8Array(res.pngLen!);
  const n = g.ssxvGetImage(png);
  if (n !== res.pngLen) {
    return { type: 'liveFrame', ok: false, seconds, ms };
  }
  return { type: 'liveFrame', ok: true, png, w: res.w, h: res.h, mode: res.mode, ms, seconds };
}

function decodeWavSync(wav: ArrayBuffer, mode: DecodeMode): { png: Uint8Array; meta: Omit<DecodeMeta, 'fileName'> } {
  const g = globalThis as any;
  const bytes = new Uint8Array(wav);
  const t0 = performance.now();
  const res: DecodeResult = g.ssxvDecodeWav(bytes, mode);
  const ms = Math.round(performance.now() - t0);
  if (!res.ok) {
    throw new Error(res.error ?? '未知解码错误');
  }
  const png = new Uint8Array(res.pngLen!);
  const n = g.ssxvGetImage(png);
  if (n !== res.pngLen) {
    throw new Error('E_INTERNAL: 图像字节长度不匹配');
  }
  return { png, meta: { w: res.w!, h: res.h!, ms, mode: res.mode ?? '?' } };
}

function decodePcmSync(pcm: Int16Array, fs: number, mode: DecodeMode): { png: Uint8Array; meta: Omit<DecodeMeta, 'fileName'> } {
  const g = globalThis as any;
  const t0 = performance.now();
  const res: DecodeResult = g.ssxvDecodePCM(pcm as unknown as Uint8Array, fs, mode);
  const ms = Math.round(performance.now() - t0);
  if (!res.ok) {
    throw new Error(res.error ?? '未知解码错误');
  }
  const png = new Uint8Array(res.pngLen!);
  const n = g.ssxvGetImage(png);
  if (n !== res.pngLen) {
    throw new Error('E_INTERNAL: 图像字节长度不匹配');
  }
  return { png, meta: { w: res.w!, h: res.h!, ms, mode: res.mode ?? '?' } };
}

self.onmessage = async (ev: MessageEvent<WorkerRequest>) => {
  const req = ev.data;
  try {
    if (req.type === 'init') {
      await ensureReady();
      self.postMessage({ type: 'ready', abi: (globalThis as any).ssxvVersion() });
      return;
    }
    if (req.type === 'decode') {
      await ensureReady();
      const { png, meta } = decodeWavSync(req.wav, req.mode ?? 'auto');
      self.postMessage(
        { type: 'decoded', id: req.id, png, ms: meta.ms, w: meta.w, h: meta.h, mode: meta.mode, fileName: req.fileName },
        // Transferable：避免大数组拷贝
        [png.buffer as ArrayBuffer],
      );
      return;
    }
    if (req.type === 'decodePCM') {
      await ensureReady();
      const { png, meta } = decodePcmSync(req.pcm, req.sampleRate, req.mode ?? 'auto');
      self.postMessage(
        { type: 'decoded', id: req.id, png, ms: meta.ms, w: meta.w, h: meta.h, mode: meta.mode, fileName: req.fileName },
        [png.buffer as ArrayBuffer],
      );
      return;
    }
    if (req.type === 'liveStart') {
      await ensureReady();
      liveBuf = null;
      liveLen = 0;
      if ('sampleRate' in req) liveFs = req.sampleRate;
      return;
    }
    if (req.type === 'livePush') {
      appendLive(req.pcm);
      return;
    }
    if (req.type === 'liveSnapshot') {
      const resp = liveSnapshot(req.mode ?? 'auto');
      if (resp.type === 'liveFrame' && resp.png) {
        self.postMessage(resp, [resp.png.buffer as ArrayBuffer]);
      } else {
        self.postMessage(resp);
      }
      return;
    }
  } catch (e) {
    self.postMessage({ type: 'error', message: e instanceof Error ? e.message : String(e) });
  }
};

export {};
