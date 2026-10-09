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
  | { type: 'decode'; id: number; wav: ArrayBuffer; fileName: string };

export type WorkerResponse =
  | { type: 'ready'; abi: number }
  | { type: 'error'; message: string }
  | ({ type: 'decoded'; id: number; png: Uint8Array } & DecodeMeta);

let goReady: Promise<void> | null = null;

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

function decodeWavSync(wav: ArrayBuffer): { png: Uint8Array; meta: Omit<DecodeMeta, 'fileName'> } {
  const g = globalThis as any;
  const bytes = new Uint8Array(wav);
  const t0 = performance.now();
  const res: DecodeResult = g.ssxvDecodeWav(bytes);
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
      const { png, meta } = decodeWavSync(req.wav);
      self.postMessage(
        { type: 'decoded', id: req.id, png, ms: meta.ms, w: meta.w, h: meta.h, mode: meta.mode, fileName: req.fileName },
        // Transferable：避免大数组拷贝
        [png.buffer as ArrayBuffer],
      );
      return;
    }
  } catch (e) {
    self.postMessage({ type: 'error', message: e instanceof Error ? e.message : String(e) });
  }
};

export {};
