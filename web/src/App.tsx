import { useEffect, useState } from 'react';
import { core, type DecodeResult } from './core/ssxvCore';

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

  return (
    <div className="app">
      <header>
        <h1>SSXV_Manager</h1>
        <span className="sub">SSTV / SSDV 解码 · Robot36 已支持</span>
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

      {current && (
        <section className="result" data-testid="result">
          <h2>解码结果</h2>
          <p className="meta" data-testid="meta">
            {current.fileName} · {current.w}×{current.h} · {current.ms}ms
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
