# SSXV_Manager 技术规格（TECH SPEC）

> 配套 `docs/DESIGN.md`（v0.2）使用。本文档把设计落成可实现的精确规格。
>
> 版本 v0.1 · 2026-10-08 · 核实状态标注：✅=已从参考源码核实原值；⚠️=待实现时从参考源码提取（禁止凭记忆填值）

**已核实的数据来源**
- SSTV 时序 / VIS：`dnet/pysstv`（MIT，`pysstv/sstv.py`、`pysstv/color.py` master 分支，2026-10-08 拉取）
- SSDV 包格式：UKHAS wiki + RadioLib `SSDV.h`
- SSDV RS / CRC / JPEG 重组：`fsphil/ssdv`（`ssdv.c` 1551 行）+ Karn `rs8.c`（bristol-seds fork 持有的 tweaked 版）
- 本地留存：`/tmp/ssdv_fsphil.c`、`/tmp/ssdv_ref.c`、`/tmp/rs8.c`（建议入库 `docs/ref/` 存档）

---

## 1. 音频与 PCM 规格

| 项 | 规格 |
|---|---|
| 内部处理采样率 | 8000 Hz（SSTV 信号带宽 1100–2300Hz，8k 足够） |
| 输入采样率 | 任意（文件头决定 / 麦克风 48k），核心内统一重采样到 8k |
| PCM 格式 | 内部 `[]int16` 单声道；WASM/Worker 传输同为 int16 小端块 |
| 浮点→整型 | 前端 Float32[-1,1] → int16：`clamp(round(f*32767))` |
| 块大小 | 实时链路 1280 样本/块（160ms@8k，Worklet 上游累积后推） |

---

## 2. DSP 规格

### 2.1 重采样（`dsp/resample.go`）

- 整数倍率（如 48000→8000，M=6）：**级联积分梳状（CIC，3 级）+ 半带 FIR 校正**，或等价的多相 FIR 抽取。阻带衰减 ≥ 60dB @ 3.4kHz 以上。
- 非整数倍率（IQ 下变频后任意率）：窗口 sinc 多相 FIR，窗口 Kaiser β≈8，抽头数按倍率自适应（32–128 taps）。
- 验收：扫频 300–3400Hz 正弦重采样，通带纹波 < 0.5dB，混叠分量 < -60dB。

### 2.2 带通滤波（`dsp/biquad.go`）

- 4 阶 Butterworth 带通 800–2900Hz（biquad 级联 ×2），去直流（一阶高通 0.1Hz 或均值抵消）。
- 作用：抑制带外噪声后鉴频，防折叠假频。

### 2.3 FM 鉴频（`sstv/fm.go`）

正交鉴频（相位差分法），对复基带信号 z[n]：

```
phi[n] = atan2(Im(z[n]), Re(z[n]))
inst_freq[n] = wrap(phi[n] - phi[n-1]) * fs / (2π)     // wrap 到 (-π, π]
pixel_value = clamp(round((inst_freq - 1500) * 255 / 800), 0, 255)
```

实信号先经 800–2900Hz 带通后做希尔伯特变换（FIR 解析信号，127 taps）或乘本振构造复信号再鉴频——**实现取希尔伯特方案**（与现有慢速接收软件一致，误差特性已知）。

### 2.4 时钟恢复（`dsp/clock.go`）

- **行级**：滑窗检测 1200Hz 同步脉冲。窗口内瞬时频率 < 1350Hz 的持续时间 ∈ [0.7×SYNC, 1.3×SYNC] 判定命中；以命中点为行起点，行内按模式时序恒速展开。
- **行漂移校正**：每行实际起点与标称起点差 > 0.5ms 时平移游标（防累积倾斜），单行不回退。
- **像素级**：恒速插值（行起点 + k×pixel_time），不做像素级闭环——SSTV 发射端时钟容差约 100ppm，行级校正已足够。

---

## 3. SSTV 规范

### 3.1 频率常量（✅ pySSTV `sstv.py`）

| 常量 | 值 |
|---|---|
| FREQ_SYNC | 1200 Hz |
| FREQ_VIS_BIT1 | 1100 Hz |
| FREQ_VIS_BIT0 | 1300 Hz |
| FREQ_BLACK | 1500 Hz |
| FREQ_VIS_START | 1900 Hz |
| FREQ_WHITE | 2300 Hz |
| 亮度→频率 | `f = 1500 + 800 × v / 255`（v∈[0,255]，线性） |

### 3.2 VIS 头（✅ pySSTV `sstv.py` `gen_freq_bits`）

```
[可选 VOX: 1900/1500/1900/1500/2300/1500/2300/1500 Hz，各 100ms]
1900 Hz × 300 ms        （引导 1）
1200 Hz × 10  ms        （间隔）
1900 Hz × 300 ms        （引导 2）
1200 Hz × 30  ms        （起始位）
7 × 30 ms  数据位，LSB 先行：1100 Hz = 1，1300 Hz = 0
1 × 30 ms  偶校验位（7 位中 1 的个数为奇 → 发 1100，偶 → 发 1300）
1200 Hz × 30 ms         （停止位）
→ 图像行序列
[可选 FSKID：1900=1/2100=0，22ms/位，6bit/字节，载荷 = 0x20,0x2A,文本各字节-0x20,0x01]
```

**解码策略**：不要求完整匹配头部——从任意 1900Hz 引导段或 1200Hz 起始位进入，恢复 7 数据位 + 校验位，与模式表 VIS 求汉明距离，≤2 视为候选、取最小值；校验位仅作参考不硬门限。

### 3.3 模式表（✅ 全部数值取自 pySSTV `color.py`，单位 ms）

通道顺序与结构严格按源码。`px = SCAN / WIDTH` 为单像素时长。

| 模式 | VIS | 尺寸 | 色彩 | 行结构 |
|---|---|---|---|---|
| Martin M1 | 0x2C | 320×256 | G,B,R | 每行：同步 4.862@1200 → [G,B,R] 各 {0.572@1500 + 320px×0.4576 + 0.572@1500} |
| Martin M2 | 0x28 | 160×256 | G,B,R | 同上，px=0.4576，SCAN=73.216 |
| Scottie S1 | 0x3C | 320×256 | G,B,R | **无行同步**；红通道前发 9@1200；各通道 1.5@1500 + 320px×0.42731 |
| Scottie S2 | 0x38 | 160×256 | G,B,R | 同 S1，px=0.54103 |
| Scottie DX | 0x4C | 320×256 | G,B,R | 同 S1，px=1.07531 |
| Robot 36 | 0x08 | 320×240 | YCbCr | 每行：3@1500 → Y 320px×0.275 → 4.5@sep → 1.5@1900 → C 320px×0.1375；**偶行 sep=1500 传 R-Y？否——偶行 sep 频率 1500、奇行 2300（pySSTV 原值）；色彩通道偶行=Cb 索引2、奇行=Cr 索引1（`channel = 2 - line%2`）** |
| PD 90 | 0x63 | 320×256 | YCbCr | 每 2 行一组：同步 20@1200 → 2.08@1500 → Y0 320px×0.532 → Cb(平均) → Cr(平均) → Y1 |
| PD 120 | 0x5F | 640×496 | 同 PD90 结构，px=0.19 |
| PD 160 | 0x62 | 512×400 | 同，px=0.382 |
| PD 180 | 0x60 | 640×496 | 同，px=0.286 |
| PD 240 | 0x61 | 640×496 | 同，px=0.382 |
| PD 290 | 0x5E | 800×616 | 同，px=0.286 |
| Wraase SC2-180 | 0x37 | 320×256 | R,G,B | 每行：同步 5.5225@1200 → [R: 0.5@1500 + 320px×0.734375；G、B 无 porch] |
| Wraase SC2-120 | 0x3F | 320×256 | R,G,B | 同上，SCAN=156.0；**红通道前额外多发一个 0.5ms porch（pySSTV 注释：QSSTV/slowrx 兼容需要）** |
| Pasokon P3 | 0x71 | 640×496 | R,G,B | 时间单位 u=1000/4800ms：同步 25u@1200 → [R,G,B] 各 {5u@1500 + 640px×u + 5u@1500} |
| Pasokon P5 | 0x72 | 640×496 | 同 P3，u=1000/3200 |
| Pasokon P7 | 0xF3 | 640×496 | 同 P3，u=1000/2400 |

灰度模式（Robot8BW / Robot24BW）后置到 M2+，从 `pysstv/grayscale.py` 核实。

### 3.4 pySSTV 偏差警示（实现解码器必须吸收）

1. **Robot36 行同步缺失**：pySSTV `Robot36.encode_line` 未发 9ms/1200Hz 行同步（`SYNC=9` 常量未使用）。⇒ pySSTV **不能**作为 Robot36 的完整 oracle；Robot36 金标准改用：自造编码器（按上表 + Barber 规范补 9ms 同步）+ 公开 ISS 接收录音。M0 第一个任务用本地 Python 实测 pySSTV Robot36 WAV 验证此结论，并把结果写入测试语料 README。
2. **Robot36 分隔脉冲频率与 Barber 规范不一致**（规范 1900Hz，pySSTV 奇偶行分别 2300/1500）。⇒ 解码器对分隔段**只按时间窗定位、不按频率校验**。
3. Scottie 系列行首语义（9ms 同步挂在红通道前、无逐行同步）导致失锁后重同步依赖「1900 引导 + VIS」而非行同步——状态机要支持 VIS-only 重入。

### 3.5 颜色空间

- Robot36/PD 系：YCbCr，**JPEG full-range BT.601**（PIL `convert('YCbCr')` 语义）：`Y=0.299R+0.587G+0.114B`，`Cb=(B-Y)×0.564+128`，`Cr=(R-Y)×0.713+128`。往返精度断言 ±1。
- 其余模式：RGB 直传。

### 3.6 行解码状态机

```
SEARCH_VIS → LOCKED(mode, t0) → 按模式表逐行展开：
  每行：[同步窗] → 通道循环（每通道：porch 窗 + N 像素采样窗）
  同步丢失 → 单行降级（灰条 + 事件 warn）→ 连续 3 行失锁 → 回 SEARCH_VIS
  VIS 码变更（汉明≤2 的新候选且行号=0）→ 判定新图开始
```

坏行策略：单行像素丢失率 > 40% → 整行灰条（128,128,128）+ 事件 `{"t":"badline","line":n}`。

---

## 4. SSDV 规范

### 4.1 包格式（✅ UKHAS + fsphil ssdv.h/ssdv.c）

| 偏移 | 字段 | 说明 |
|---|---|---|
| 0 | 0x55 | 同步（可有多余 0x55 前缀） |
| 1 | 0x66 / 0x67 | 类型：FEC 正常 / 无 FEC |
| 2–5 | 呼号 | base-40 编码 4 字节（≤6 字符） |
| 6 | image ID | 0 起每图 +1（换图判据之一） |
| 7–8 | packet ID | 大端，每图从 0 |
| 9 / 10 | 宽 / 高 | MCU 块数；**像素 = 值 ×16** |
| 11 | flags | `00qqqexx`：质量级 qqq(0-7 XOR 4)、e=EOI、xx=抽样模式(0=2×2,1=1×2,2=2×1,3=1×1) |
| 12 | MCU offset | 包内首 MCU 字节偏移，0xFF=无 |
| 13–14 | MCU index | 大端，0xFFFF=无 |
| 15–219 | 载荷 205B | JPEG 扫描数据（FEC 模式；0x67 为 237B） |
| 220–223 | CRC-32 | 大端 |
| 224–255 | RS 校验 32B | 仅 0x66 模式 |

### 4.2 CRC-32（✅ fsphil ssdv.c:221-232）

标准 IEEE CRC-32：反射、多项式 `0xEDB88320`、init `0xFFFFFFFF`、末值 XOR `0xFFFFFFFF`（= zlib/crc32 等价，Go 标准库 `hash/crc32.ChecksumIEEE` 直接可用）。覆盖：**包字节 1 到载荷末尾**（`pkt_size_crcdata = 15 + payload_len − 1`）。写入大端。

### 4.3 Reed-Solomon（✅ rs8.c 原码核实）

| 参数 | 值 |
|---|---|
| GF(256) 本原多项式 | `0x11D`（ALPHA_TO 表 α^8=0x87 确认） |
| NN / NROOTS | 255 / 32（RS(255,223)，**pad=0，无截断**） |
| FCR / PRIM / IPRIM | 112 / 11 / 116 |
| 调用方式 | `decode_rs_8(&pkt[1], NULL, 0, 0)`：输入为包字节 1–255（含校验），纠错上限 16 字节 |
| 解码顺序 | **先 RS 纠错，再 CRC 校验**（fsphil ssdv.c:564-580） |
| 纠错判定 | Berlekamp-Massey + Chien 搜索；`deg(λ) ≠ 根数` → 不可纠，弃包 |

实现要求：Go 移植以本 rs8.c 为唯一语义基准；GF 表允许运行时生成（α^8=0x11D 展开）+ 与 rs8.c 硬编码 `ALPHA_TO/INDEX_OF/GENPOLY` 三表逐字节一致性测试；随后做 10⁴ 次随机 1–16 字节错误纠正对拍（纠正结果与错误位置必须与 C 版一致——C 版测试用 `go test` 嵌入固定种子用例离线对拍）。

### 4.4 JPEG 重组（✅ fsphil ssdv.c:1151-1184，表体 ⚠️ M3 从源码提取）

重组出的 JPEG 结构（顺序固定）：

```
SOI (FFD8)
DQT  65B   亮度量化表
DQT  65B   色度量化表
SOF0 15B   baseline；高/高 = MCU数×16（大端）；3 分量 Y:01 Cb:02 Cr:03
DHT  29B   DC 亮度   （std_dht00，29 字节含标记与长度）
DHT 179B   AC 亮度   （std_dht10）
DHT  29B   DC 色度   （std_dht01）
DHT 179B   AC 色度   （std_dht11）
SOS  10B   3 分量扫描头
<扫描数据>  按包序拼接 MCU 数据，0xFF 后补 0x00 填充
EOI (FFD9)
```

- DQT 表内容由质量级（flags qqq XOR 4 → 0-7）从标准表缩放生成，DHT 为 JPEG 标准表——**四张 DHT 数组与 DQT 缩放函数在 M3 第一个任务中从 ssdv.c 原样提取为 Go 常量**，不手抄。
- 0xFF 处理：扫描数据写文件时 `0xFF → FF 00` 填充（ssdv.c:386 out_stuff）；读包流时不要求去填充（包内为原始 MCU 字节流）。
- 重组输出用 Go 标准库 `image/jpeg` 解码验证合法性（能解出 = 结构正确）。

### 4.5 包状态机与丢包

```
SEEK_SYNC：字节流滑窗找 0x55 0x66/0x67 → 对齐候选 → RS 纠错 → CRC 过 → ACCEPT
丢包：packet_id 跳号 → 按 4.1 语义补空 MCU（EOB 截断、空 DC/AC、按新包 mcu_offset 跳过续接点）
换图：image_id 变化 → 当前图强制 EOI 输出，开新图
```

目标行为：丢包图像「花屏但结构完整可辨认」，不崩溃、不死等。

### 4.6 BPSK 解调（M3 后半）

| 参数 | 值 |
|---|---|
| 调制 | BPSK；常用下行配置波特率 **200 bps**（参数表可扩） |
| 前端 | IQ/音频 → 复基带（频移 + 抽取到 fs=4×波特率） |
| 载波恢复 | 二阶 Costas 环（α=0.02, β=4α² 上限，按实测调） |
| 符号定时 | Gardner 鉴相器 + 环路滤波 + 插值（Farrow 立方） |
| 判决 | 匹配滤波（RRC α=0.5，4 符号尾）后符号判决 |
| 差分 | 无差分编码（SSDV 直传 NRZ-L）；0x55 同步字哈希匹配作帧同步 + 相位模糊 180° 试探 |
| 输出 | 位流 → 字节（LSB 先行）→ 交给 4.5 包状态机 |

---

## 5. WASM 桥 ABI（精确约定）

### 5.1 导出函数

| 导出名 | 签名（JS 侧） | 语义 |
|---|---|---|
| `ssxv_version` | `() => u32` | ABI 版本（破坏性变更 +1） |
| `ssxv_new_session` | `(optsJsonPtr, optsJsonLen) => i32` | 返回会话句柄，-1=失败（错误经 `ssxv_last_error`） |
| `ssxv_decode_file` | `(h, dataPtr, dataLen) => i32` | 同步喂整文件字节；返回后经事件流取结果 |
| `ssxv_push_audio` | `(h, dataPtr, dataLen) => i32` | 推 int16 PCM 块（实时源） |
| `ssxv_poll_event` | `(h) => u32` | 返回事件指针（0=无）；事件 = `[len:u32][json bytes]`，读后需调 `ssxv_free` |
| `ssxv_pull_image` | `(h, bufPtr, bufLen) => i32` | 拉图像字节，返回实际长度；-1=无 |
| `ssxv_cancel` | `(h) => void` | 取消会话 |
| `ssxv_last_error` | `(h) => u32` | 最近错误 JSON 指针 |
| `ssxv_free` | `(ptr) => void` | 释放 WASM 线性内存中的返回块 |

约定：所有指针为 WASM 线性内存偏移（u32）；字符串一律 UTF-8 JSON；会话句柄由 Go 侧句柄表管理。

### 5.2 事件 JSON Schema

```jsonc
{"t":"vis","vis":8,"mode":"Robot36","conf":0.92}
{"t":"progress","p":0.42}                    // 0..1
{"t":"line","n":128}                          // 行完成（实时模式每行回推）
{"t":"badline","n":128}
{"t":"ssdv_packet","id":42,"ok":true}        // SSDV：包序号 + RS/CRC 结果
{"t":"image","fmt":"png","w":320,"h":240,"bytes":48211}   // 就绪，用 pull_image 取
{"t":"error","code":"E_DEMOD","msg":"..."}
{"t":"done"}
```

错误码表：`E_ARG / E_SRC / E_DEMOD / E_NOMEM / E_CANCELLED / E_INTERNAL`。

### 5.3 Worker 协议

`web/src/core/worker.ts` 与主线程消息：`{cmd:"decode", bytes:Transferable}`、`{cmd:"startLive", sampleRate}`、`{cmd:"pushAudio", pcm}`、`{cmd:"cancel"}`；回推 `{evt: CoreEvent}` 或 `{image: Blob}`。事件节流：`progress` ≥100ms 一次，`line` 直通。

---

## 6. CLI 规格（`cmd/ssxv-cli`）

```
ssxv-cli decode <file>            # SSTV/SSDV 自动识别
  --mode <auto|Robot36|...>       # 默认 auto（VIS 识别）
  --force-ssdv                    # 按 SSDV 解调（--baud 200）
  --out <dir>                     # 输出目录，默认 ./out
  --json                          # 事件流走 stdout（NDJSON），图像写盘
ssxv-cli info <file>              # 打印 WAV/IQ 元数据与信号摘要
ssxv-cli bench <file>             # 实时倍率基准（M1 前性能门禁）
```

退出码：0 成功出图；1 用法错误；2 输入解析失败；3 解码失败（未识别模式/无图像）。
自动识别规则：文件头含 `0x55 0x66` 且 VIS 搜索失败 → 先试 SSDV；WAV 且 VIS 命中 → SSTV。

---

## 7. 测试规格

### 7.1 语料清单（`testdata/`，均入库 + README 标注来源与许可）

| 语料 | 生成方式 | 断言对象 |
|---|---|---|
| `sstv/<mode>_clean.wav` ×16 模式 | testgen 编码器 | 全模式像素级回归 |
| `sstv/<mode>_snr{20,10,6}.wav` | testgen + AWGN | 最低可用 SNR 门限（目标：6dB@Robot36 出可辨认图） |
| `sstv/<mode>_drift.wav` | testgen + ±100ppm 时钟偏差 | 行漂移校正 |
| `sstv/pysstv_<mode>.wav` | 本地跑 pySSTV | 第三方 oracle（Robot36 除外，见 3.4） |
| `ssdv/ref.bin` / `ref.jpeg` | fsphil ssdv -e / -d | 包解析 + RS + JPEG 重组对拍 |
| `ssdv/noise.bin` | ref.bin 注入随机字节错误 | RS 纠错统计 |
| `iq/synth_<fmt>.iq` | testgen 上变频 CU8/CS16/CF32/WAV | IQ 链路 |
| `real/iss_robot36.wav` 等 | 公开接收记录 | 真实信道回归 |

### 7.2 验收指标（CI 门禁）

- 全部单测/对拍测试绿；RS 对拍 10⁴ 用例零不一致
- testgen 干净语料：像素误差 ≥99% 在 ±2 内
- pySSTV oracle：≥95% 像素误差 < 8/255
- bench：≥4× 实时倍率（M1 前在开发机建立基线）
- `go vet` / `gofmt` 干净

### 7.3 TDD 粒度约定

每模块测试文件与实现同包 `internal/<mod>`；表驱动测试优先；DSP 用金数据（golden file）+ 容差断言；禁止 `time.Sleep` 式测试。

---

## 8. 代码规范

- Go：标准 `gofmt`；错误一律 `%w` 包装并带模块前缀（`sstv: ...`）；导出符号必须有注释（revive 基线）。
- 包边界：`internal/*` 不得互相 import 越过 pipeline 层（source/dsp/sstv/ssdv 相互独立，只被 pipeline 引用）。
- 提交：Conventional Commits（`feat(sstv): ...`）；每个 TDD 循环一提交。
- Web：TypeScript strict；`SSXVCore` 接口是 UI 与核心唯一边界，UI 不得 import WASM 胶水。

---

## 9. 与里程碑的映射

| 文档节 | 对应里程碑 |
|---|---|
| §1–§3 | M0（CLI + Robot36） |
| §5, §6, §7.2 bench | M1（Web 壳） |
| §3.3 全表 + 3.6 | M2（全模式） |
| §4 | M3（SSDV） |
| §5.3 实时、§2.1 IQ | M4（实时 + IQ + Tauri） |
