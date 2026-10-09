import { useEffect, useRef, useState } from 'react';
import { core, type DecodeResult, type LiveFrame } from './core/ssxvCore';
import type { DecodeMode } from './core/worker';

interface GalleryItem extends DecodeResult {
  id: number;
  at: string;
}

// SSTV 具体模式（与 sstv 包 ModeByName 名称一致）
const SSTV_MODES: { name: string; label: string }[] = [
  { name: 'Robot36', label: 'Robot 36' },
  { name: 'MartinM1', label: 'Martin M1' },
  { name: 'MartinM2', label: 'Martin M2' },
  { name: 'ScottieS1', label: 'Scottie S1' },
  { name: 'ScottieS2', label: 'Scottie S2' },
  { name: 'ScottieDX', label: 'Scottie DX' },
  { name: 'PD90', label: 'PD 90' },
  { name: 'PD120', label: 'PD 120' },
  { name: 'PD160', label: 'PD 160' },
  { name: 'PD180', label: 'PD 180' },
  { name: 'PD240', label: 'PD 240' },
  { name: 'PD290', label: 'PD 290' },
  { name: 'WraaseSC2180', label: 'Wraase SC2-180' },
  { name: 'WraaseSC2120', label: 'Wraase SC2-120' },
  { name: 'PasokonP3', label: 'Pasokon P3' },
  { name: 'PasokonP5', label: 'Pasokon P5' },
  { name: 'PasokonP7', label: 'Pasokon P7' },
];

type LinkType = 'auto' | 'sstv' | 'ssdv';

export default function App() {
  const [ready, setReady] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const [items, setItems] = useState<GalleryItem[]>([]);
  const [current, setCurrent] = useState<GalleryItem | null>(null);
  const [live, setLive] = useState<LiveFrame | null>(null);
  const [liveOn, setLiveOn] = useState(false);
  const [linkType, setLinkType] = useState<LinkType>('auto');
  const [sstvMode, setSstvMode] = useState('');
  // 传给核心的 mode 串："auto" | "sstv" | "sstv:<Name>" | "ssdv"
  const modeRef = useRef<DecodeMode>('auto');
  const setMode = (t: LinkType, sm: string) => {
    modeRef.current = t === 'auto' ? 'auto' : t === 'ssdv' ? 'ssdv' : sm ? `sstv:${sm}` : 'sstv';
  };
  const audioRef = useRef<{
    ctx: AudioContext;
    stream: MediaStream;
    timer: number;
  } | null>(null);
  const lastLiveRef = useRef<(LiveFrame & { url: string }) | null>(null);

  useEffect(() => {
    core.ready.then(
      () => setReady(true),
      (e) => setErr(e.message),
    );
  }, []);

  const handleFile = async (file: File) => {
    setBusy(true);
    setErr(null);
    try {
      const r = await core.decodeFile(file, modeRef.current);
      const item: GalleryItem = { ...r, id: Date.now(), at: new Date().toLocaleTimeString() };
      setItems((prev) => [item, ...prev]);
      setCurrent(item);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const onDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    const f = e.dataTransfer.files[0];
    if (f) handleFile(f);
  };

  // ---- 实时接收（M4）：麦克风 → AudioWorklet → int16 → worker 快照解码 ----
  const stopLive = () => {
    const a = audioRef.current;
    audioRef.current = null;
    setLiveOn(false);
    if (!a) return;
    clearInterval(a.timer);
    a.stream.getTracks().forEach((t) => t.stop());
    a.ctx.close();
    // 收尾：把最后一张有效图入图库
    const last = lastLiveRef.current;
    if (last) {
      const item: GalleryItem = {
        id: Date.now(),
        at: new Date().toLocaleTimeString(),
        url: last.url,
        w: last.w ?? 0,
        h: last.h ?? 0,
        ms: last.ms ?? 0,
        mode: last.mode ?? '?',
        fileName: '实时接收',
      };
      setItems((prev) => [item, ...prev]);
      setCurrent(item);
      lastLiveRef.current = null;
    }
  };

  const startLive = async () => {
    setErr(null);
    setLive(null);
    lastLiveRef.current = null;
    try {
      // SSTV 音频不能被浏览器降噪/AGC 破坏，全部关掉
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: false, noiseSuppression: false, autoGainControl: false },
      });
      const ctx = new AudioContext();
      await ctx.resume();
      const fs = ctx.sampleRate;
      core.liveStart(fs);

      // 内联 AudioWorklet：累积 2048 样本上抛（避免每 128 样本一条消息）
      const code =
        'class Cap extends AudioWorkletProcessor{constructor(){super();this.buf=new Float32Array(2048);this.n=0}' +
        'process(inputs){const ch=inputs[0]&&inputs[0][0];if(ch){for(let i=0;i<ch.length;i++){this.buf[this.n++]=ch[i];' +
        'if(this.n>=2048){this.port.postMessage(this.buf.slice(0));this.n=0}}}return true}}' +
        "registerProcessor('ssxv-cap',Cap);";
      const url = URL.createObjectURL(new Blob([code], { type: 'application/javascript' }));
      await ctx.audioWorklet.addModule(url);
      const node = new AudioWorkletNode(ctx, 'ssxv-cap');
      node.port.onmessage = (ev: MessageEvent<Float32Array>) => {
        if (!audioRef.current) return;
        const f = ev.data;
        const pcm = new Int16Array(f.length);
        for (let i = 0; i < f.length; i++) {
          const v = Math.round(Math.max(-1, Math.min(1, f[i])) * 32767);
          pcm[i] = v;
        }
        core.livePush(pcm);
      };
      ctx.createMediaStreamSource(stream).connect(node);
      // 不连 destination，避免回授

      // 周期快照解码（整段重解，60s 音频 ~1s 内，可接受）
      const tick = async () => {
        const fr = await core.liveSnapshot(modeRef.current);
        if (fr.seconds < 0) return; // 单飞去重
        setLive(fr);
        if (fr.ok && fr.url) lastLiveRef.current = fr as LiveFrame & { url: string };
      };
      const timer = window.setInterval(tick, 2000);

      audioRef.current = { ctx, stream, timer };
      setLiveOn(true);
    } catch (e) {
      setErr('实时接收启动失败: ' + (e instanceof Error ? e.message : String(e)));
      stopLive();
    }
  };

  useEffect(() => {
    return () => {
      const a = audioRef.current;
      if (a) {
        clearInterval(a.timer);
        a.stream.getTracks().forEach((t) => t.stop());
        a.ctx.close();
      }
    };
  }, []);

  return (
    <div className="app">
      <header>
        <h1>SSXV_Manager</h1>
        <span className="sub">SSTV 17 模式 / SSDV 解码 · 实时接收</span>
        <span className={'status ' + (ready ? 'ok' : err ? 'bad' : '')}>
          {ready ? '核心就绪' : err ? '核心加载失败' : '核心加载中…'}
        </span>
      </header>

      <section
        className={'dropzone' + (dragOver ? ' over' : '') + (busy ? ' busy' : '')}
        onDragOver={(e) => {
          e.preventDefault();
          setDragOver(true);
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={onDrop}
        data-testid="dropzone"
      >
        {busy ? (
          <p>解码中…</p>
        ) : (
          <>
            <p>拖放音频文件到此处（WAV / OGG / MP3 / FLAC / M4A…），或</p>
            <div className="moderow">
              <label className="btn">
                选择文件
                <input
                  type="file"
                  accept=".wav,.ogg,.oga,.mp3,.flac,.m4a,.aac,.opus,.webm,audio/*"
                  onChange={(e) => {
                    const f = e.target.files?.[0];
                    if (f) handleFile(f);
                  }}
                />
              </label>
              <select
                className="modeselect"
                data-testid="link-select"
                value={linkType}
                onChange={(e) => {
                  const t = e.target.value as LinkType;
                  setLinkType(t);
                  setMode(t, sstvMode);
                }}
              >
                <option value="auto">信号：自动识别</option>
                <option value="sstv">信号：SSTV</option>
                <option value="ssdv">信号：SSDV</option>
              </select>
              <select
                className="modeselect"
                data-testid="mode-select"
                value={linkType === 'auto' ? '' : linkType === 'ssdv' ? '200' : sstvMode}
                disabled={linkType !== 'sstv'}
                onChange={(e) => {
                  const m = e.target.value;
                  setSstvMode(m);
                  setMode(linkType, m);
                }}
              >
                {linkType === 'auto' && <option value="">—</option>}
                {linkType === 'ssdv' && <option value="200">SSDV 200 bps</option>}
                {linkType === 'sstv' && (
                  <>
                    <option value="">模式自动(VIS)</option>
                    {SSTV_MODES.map((m) => (
                      <option key={m.name} value={m.name}>
                        {m.label}
                      </option>
                    ))}
                  </>
                )}
              </select>
            </div>
          </>
        )}
      </section>

      {err && <div className="error" data-testid="error">{err}</div>}

      <section className="live" data-testid="live">
        <h2>实时接收</h2>
        <p className="hint">打开麦克风接收 SSTV/SSDV 信号（建议直接对准解调器音频输出）</p>
        <button
          className="btn"
          data-testid="live-btn"
          disabled={!ready}
          onClick={() => (liveOn ? stopLive() : startLive())}
        >
          {liveOn ? '停止接收' : '开始接收'}
        </button>
        {liveOn && (
          <span className="meta" data-testid="live-status">
            接收中 · {live ? `${live.seconds.toFixed(0)}s` : '…'}
            {live?.transient && ' · 等待信号'}
            {live?.ok && ` · ${live.mode} ${live.ms}ms`}
          </span>
        )}
        {live?.ok && live.url && (
          <div className="result">
            <img src={live.url} alt="实时接收" width={(live.w ?? 320) * 2} data-testid="live-img" />
          </div>
        )}
      </section>


      {current && (
        <section className="result" data-testid="result">
          <h2>解码结果</h2>
          <p className="meta" data-testid="meta">
            {current.fileName} · {current.mode} · {current.w}×{current.h} · {current.ms}ms
          </p>
          <img src={current.url} alt="解码结果" width={current.w * 2} data-testid="decoded-img" />
        </section>
      )}

      {items.length > 0 && (
        <section className="gallery">
          <h2>图库（本次会话）</h2>
          <ul>
            {items.map((it) => (
              <li key={it.id} className={current?.id === it.id ? 'sel' : ''} onClick={() => setCurrent(it)}>
                <img src={it.url} alt={it.fileName} width={96} />
                <span>
                  {it.fileName}
                  <br />
                  <small>
                    {it.w}×{it.h} · {it.at}
                  </small>
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}
