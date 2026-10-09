import { useEffect, useRef, useState } from 'react';
import { core, type DecodeResult, type LiveFrame } from './core/ssxvCore';

interface GalleryItem extends DecodeResult {
  id: number;
  at: string;
}

export default function App() {
  const [ready, setReady] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const [items, setItems] = useState<GalleryItem[]>([]);
  const [current, setCurrent] = useState<GalleryItem | null>(null);
  const [live, setLive] = useState<LiveFrame | null>(null);
  const [liveOn, setLiveOn] = useState(false);
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
      const r = await core.decodeFile(file);
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
        const fr = await core.liveSnapshot();
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
            <p>拖放 WAV 音频到此处，或</p>
            <label className="btn">
              选择文件
              <input
                type="file"
                accept=".wav,audio/wav"
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  if (f) handleFile(f);
                }}
              />
            </label>
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
