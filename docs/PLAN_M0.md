# M0 实施计划：核心链路（CLI + Robot36）

> 规格：`docs/TECH_SPEC.md`。出口标准：`ssxv-cli decode robot36.wav` 稳定出图；testgen→decode 闭环像素误差 ≥99% 在 ±2；SNR 10dB 可解。

### 任务 1：隔离分支 + 骨架
- 分支 `m0-core`；`go.mod`（module ssxv, go 1.26）；目录 `internal/{dsp,sstv,source,testgen,pipeline}`、`cmd/ssxv-cli`。
- 验证：`go build ./...` 通过（空包）。

### 任务 2：DSP（TDD，internal/dsp）
- `resample.go`：`ResampleF32(in []float32, from, to int) []float32`——先 FIR 低通（Hamming sinc，截止 min(fs_in,fs_out)×0.45/2 归一）再线性插值重采样。
- `filter.go`：`Bandpass(x []float32, fs float32) []float32`——RBJ biquad 带通 800–2900Hz ×2 级联 + 去直流。
- `fm.go`：`Hilbert(x []float32) []complex64`（127-tap Type-III FIR，Blackman 窗）+ `InstantFreq(z []complex64, fs float32) []float32`（相位差分，wrap (-π,π]）+ `Smooth(x, ms, fs)` 滑动平均。
- 验证：1kHz 正弦@8k 鉴频输出中位误差 < 5Hz；48k→8k 重采样后扫频纹波 < 0.5dB。

### 任务 3：SSTV（TDD，internal/sstv）
- `freqs.go`：频率常量（TECH_SPEC §3.1）+ `ByteToFreq`。
- `vis.go`：`DetectVIS(freq []float32, fs float32) (vis uint8, sampleIdx int, ok bool)`——1900Hz 引导（≥200ms，±100Hz）→ 1200Hz 10ms → 1900Hz 300ms → 起始位 1200Hz 30ms → 7 数据位 + 校验（1100/1300，30ms，窗中位频率判决）→ 与模式表汉明 ≤2 匹配。
- `modes.go`：`ModeSpec` 结构 + Robot36 表（TECH_SPEC §3.3）+ `ModeByVIS`。
- `decode.go`：`Decode(freq []float32, fs float32, mode ModeSpec) *image.NRGBA`——同步脉冲搜索（<1350Hz 连续 ≥0.7×9ms）→ 行锚点 → 恒速采样（ porch/Y/sep/C 四窗）→ 无同步回退（VIS 锚点 + 标称行距）→ YCbCr→RGB 装配（偶行 Cr 偶+奇共享，奇行 Cb）。
- 验证：合成 VIS 段 → DetectVIS 返回 0x08；行锚点定位误差 < 1ms。

### 任务 4：testgen（TDD，internal/testgen）
- `gen.go`：相位连续正弦 `GenTone(segments []Seg, fs int) []float32`（Seg={FreqHz, Msec}）；VIS 头段生成（TECH_SPEC §3.2，含偶校验）。
- `robot36.go`：`EncodeRobot36(img *image.NRGBA, fs int) []Seg`——含 9ms 行同步；分隔频率偶行 1500 / 奇行 2300（pySSTV 语义，TECH_SPEC §3.3）。
- `noise.go`：`AWGN(x, snrDb)`。`wav.go`：写 PCM16 WAV（单声道）。
- 验证：段总时长 = VIS 770ms + 240 行 ×150ms；AWGN 能量比断言。

### 任务 5：WAV 读取 + E2E（TDD）
- `internal/source/wav.go`：解析 RIFF/WAVE PCM16（单/双声道，任意采样率）→ `[]int16` + 采样率。
- `internal/pipeline/pipeline.go`：`DecodeWAVFile(path, modeName) (image.Image, error)`——串联 source→dsp→sstv。
- E2E 测试：320×240 渐变图 → EncodeRobot36@48k → decode → 像素误差 ≥99% 在 ±2；SNR=10dB 版本 ≥90% 在 ±12。

### 任务 6：CLI + 收尾
- `cmd/ssxv-cli/main.go`：`decode <file> --mode auto --out dir`、`--json` 事件 NDJSON（M0 仅 progress/done/error）。退出码按 TECH_SPEC §6。
- 验证：`go vet ./...`、`gofmt -l .` 空、全测试绿、CLI 跑 testgen 样例出 PNG；合并 main。

### YAGNI 边界（M0 明确不做）
MP3/OGG、IQ 源、SSDV、WASM、其余 SSTV 模式、FSKID 解码、实时流源。
