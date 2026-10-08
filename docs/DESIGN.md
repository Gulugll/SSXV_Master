# SSXV_Manager 设计文档

> SSTV / SSDV 解码工具 —— Go 解码核心 + React Native (Expo) 前端
>
> 版本：v0.1（设计阶段） · 日期：2026-10-08 · 状态：待用户最终确认

---

## 1. 概述

SSXV_Manager 是一个手机端的业余无线电慢扫描电视（SSTV）与慢扫描数字视频（SSDV）解码工具。解码核心用 Go 实现，通过 gomobile 编译为 Android (.aar) / iOS (.xcframework) 原生库，前端用 React Native（Expo）封装 UI。

### 目标

- 从音频文件、实时麦克风、SDR IQ 原始采样三种来源解码 SSTV 图像
- 解码 SSDV：既支持从音频解调出包，也支持直接读已解调的 .ssdv 包文件
- SSTV 支持主流全模式（参数表驱动）
- 全离线运行，解码全部在手机本地完成

### 非目标（第一版明确不做）

- SSTV **编码**发射功能（编码器仅作为内部测试工具存在）
- SDR 硬件直连（USB OTG 插 RTL-SDR 直收），第一版只处理 IQ **录像文件**
- 云端图库同步 / 社交功能

---

## 2. 已确认的决策记录（ADR 摘要）

| #  | 决策        | 结论                                             | 备选方案（已否决原因）                                                 |
| -- | --------- | ---------------------------------------------- | ----------------------------------------------------------- |
| D1 | 解码核心位置    | **手机本地**（gomobile bind 产物）                     | Go 后端服务：离线不可用、有部署依赖；核心库+双壳：M1 之前先做 CLI 验证，本质兼容，不冲突          |
| D2 | 输入源       | **音频文件 + 实时麦克风 + IQ 录像** 全做                    | 分里程碑交付（见 §9），不做 SDR 硬件直连                                    |
| D3 | SSTV 模式范围 | **全模式**，参数表驱动实现                                | 每模式独立解码器：代码爆炸、维护成本高                                         |
| D4 | SSDV 输入形态 | **音频解调 + 包文件** 双形态，统一包流抽象                      | 只做其一：两形态后端共 90% 代码，没理由砍                                     |
| D5 | 音频采集侧     | **Go 侧编排**：Go 定义采集接口，原生端实现反向注册                 | RN 侧采集推流：更简单，但用户明确要求 Go 侧编排；代价见 §7.2                        |
| D6 | 前端框架      | **Expo** + Expo Modules API 本地模块包住 gomobile 产物 | 裸 RN：接入路径更短，但用户选择 Expo 生态；用 config plugin / local module 解决 |
| D7 | 测试语料      | **自造编码器 + 公开样本**，并加第三方 oracle 交叉验证             | 仅公开样本：边界情况覆盖不足；仅自造：自写编码器与解码器可能同错                            |

---

## 3. 系统架构

```
┌────────────────────────────────────────────────────┐
│  Expo / React Native 层                             │
│  UI：文件选择、实时接收页、图库、模式/参数设置         │
│  通过 Expo 本地模块调用 ↓                            │
└────────────────────┬───────────────────────────────┘
                     │ JSI ← → Kotlin/Swift（Expo Module）
                     │    ↓ 调用 gomobile 生成绑定
│····················· gomobile 桥接面 ··············│
│  约束：仅 []byte / string / int / float / error /   │
│  不透明对象可过桥；map/chan/泛型/函数回调被丢弃      │
│····················································│
┌────────────────────▼───────────────────────────────┐
│  Go 解码核心（纯 Go，可独立测试）                    │
│                                                    │
│  Source 抽象                                        │
│   ├─ 文件源（WAV/MP3/OGG → PCM）                    │
│   ├─ IQ 源（CU8/CS16/WAV-IQ → 下变频抽取 → PCM）    │
│   └─ 实时源（原生采集实现 → 反向注册 → PCM 块流）     │
│        ↓                                            │
│  DSP 链：重采样 → 带通滤波 → 时钟恢复                │
│        ↓                                            │
│   ├─ SSTV：FM 鉴频 → VIS 识别 → 查模式表逐行解码     │
│   │        → 行图像装配 → PNG                       │
│   └─ SSDV：BPSK/AFSK 解调 → 位流 → 找包             │
│            → RS(255,223) 纠错 → CRC32               │
│            → JPEG 扫描数据重组 → JPEG               │
│        ↓                                            │
│  事件/结果：JSON 事件流 + 图像字节                   │
└─────────────────────────────────────────────────────┘
```

### 核心设计原则

1. **桥接面最小化**：gomobile 只绑定一个 `mobile` 包，接口全部是 `[]byte` 进 / `[]byte` 出 + JSON 事件，回避 gobind 的类型限制。
2. **纯 Go 核心**：所有解码逻辑不依赖任何平台特性，`go test` 直接全平台跑；gomobile 只是薄壳。
3. **表驱动扩展**：SSTV 模式、SSDV 调制参数、IQ 格式都是数据表，不是代码分支。
4. **TDD 金标准**：没有失败测试不写实现；解码器对拍第三方 oracle。

---

## 4. Go 代码结构

```
SSXV_Manager/
├── go.mod                          # module github.com/<user>/ssxv
├── cmd/ssxv-cli/main.go            # 开发/调试 CLI（M0 的重要产物）
├── internal/
│   ├── source/                     # 输入源抽象
│   │   ├── source.go               #   Source 接口：块式产出 PCM ([]int16, 采样率)
│   │   ├── wav.go  mp3.go  ogg.go  #   文件源
│   │   └── iq.go                   #   IQ 源：格式表(CU8/CS16/CF32/WAV-IQ) + 用户参数
│   ├── dsp/
│   │   ├── resample.go             #   任意率重采样（多相/线性混合）
│   │   ├── biquad.go               #   带通/去直流
│   │   └── clock.go                #   符号/行时钟恢复
│   ├── sstv/
│   │   ├── modes.go                #   模式表：VIS → 时序参数（§6.2）
│   │   ├── vis.go                  #   VIS 头检测（1200Hz 引导/起止位）
│   │   ├── fm.go                   #   正交鉴频（1500–2300Hz FM 调制）
│   │   └── decode.go               #   通用逐行解码引擎（查表）
│   ├── ssdv/
│   │   ├── packet.go               #   0x55 同步搜索、包类型、CRC32
│   │   ├── rs.go                   #   RS(255,223) GF(256) 纠错（移植 rs8 语义）
│   │   ├── jpeg.go                 #   固定 DQT/DHT 表 + 扫描数据重组（含去/补 0xFF 填充）
│   │   └── demod.go                #   BPSK：Costas 载波环 + Gardner 定时；参数化波特率
│   └── pipeline/
│       ├── pipeline.go             #   Source→DSP→解码器→Sink 的 goroutine 流水线
│       └── events.go               #   事件类型：进度/模式锁定/行完成/图像完成/错误
├── mobile/                         # gomobile 绑定面（唯一可 bind 的包，见 §5）
│   └── mobile.go
├── testdata/                       # 金标准语料（§8）
│   ├── sstv/  ssdv/  iq/
└── internal/testgen/               # 测试专用 SSTV/SSDV 编码器 + 噪声注入（不入产品二进制）
```

---

## 5. gomobile 桥接层

### 5.1 为什么是字节进/字节出

gobind 只支持基本类型、`[]byte`、`string`、`error` 与不透明对象；`map`、`chan`、泛型、函数参数会被**静默跳过**（生成代码里直接消失，不报错）。因此桥接面收敛为一个会话对象 + 字节数组 + 事件回调接口（gobind 支持「Go 定义接口、原生端实现后传入」的反向注册，恰好满足事件推送和 D5 的采集编排需求）。

### 5.2 API 草图

```go
package mobile // gomobile bind 唯一入口

type Options struct {
    SampleRate     int    // 输入 PCM 采样率（实时源）
    Format         string // "sstv" | "ssdv"
    // IQ 源专用
    IQFormat       string // "cu8" | "cs16" | "cf32" | "wav"
    CenterFreqHz   int    // 下变频目标偏移
    IQSampleRate   int
}

type Session struct{ /* opaque */ }

func NewSession(opts *Options) (*Session, error)
func (s *Session) DecodeFile(path string) error      // 文件路径直接传给 Go
func (s *Session) PushAudio(pcmInt16 []byte) error   // 实时源：PCM 块（小端 int16）
func (s *Session) SetEventHandler(h EventHandler)    // 反向注册（gobind 接口）
func (s *Session) Cancel() error

type EventHandler interface { // 原生端实现，事件以 JSON 字节推送
    OnEvent(jsonEvent []byte)
}
```

事件 JSON 示例：`{"t":"vis","vis":8,"mode":"Robot36"}`、`{"t":"progress","p":0.42}`、  
`{"t":"image","fmt":"png","data_len":n}`（图像字节通过 `PullImage()` 分块拉取，避免单次跨桥大缓冲）。

### 5.3 Expo 集成

- `modules/ssxv-core/`：Expo Modules API 本地模块，Android 侧 gradle 引入 `.aar`，iOS 侧 podspec 引入 `.xcframework`，对 JS 暴露 `startDecodeFile() / startLive() / pushAudio() / onEvent` 等 Promise + 事件发射器接口。
- 实时链路（D5）：Go 侧 `Session` 编排采集——原生端实现一个薄采集器（Kotlin `AudioRecord` / Swift `AVAudioEngine`，各约 100 行），拿到 PCM 后回调进 `PushAudio`。**采集权限申请、后台音频模式（iOS `UIBackgroundModes: audio`）由 Expo 模块配置。**

---

## 6. SSTV 解码设计

### 6.1 信号模型

SSTV 是音频 FM：黑=1500Hz、白=2300Hz、同步=1200Hz。链路：带通滤波 → 正交鉴频得瞬时频率 → 行/像素时钟恢复 → 按模式时序把频率积分映射为像素。

### 6.2 模式表（表驱动，D3 的落点）

```go
type ModeSpec struct {
    Name        string        // "Robot36"
    VIS         uint8         // 0x08
    LineTime    time.Duration // 逐行总时长
    SyncPulse   time.Duration // 1200Hz 同步脉冲
    SyncPorch   time.Duration // 1500Hz 分隔
    PixelTime   time.Duration // 单像素时长（按通道可有差异）
    Channels    []ChannelSpec // 每行通道布局：Y / R-Y / B-Y / G，及奇偶交替规则
    Width       int           // 320 / 640
}
```

第一版目标模式集（以 pySSTV/QSSTV 支持集为准，逐模式核对时序后入库）：  
**Robot 36 / 72、Martin M1 / M2 / M3 / M4、Scottie S1 / S2 / DX、PD 90 / 120 / 180 / 290、Wraase SC2-180**。

> 时序参数不凭记忆填表——每个模式入库前必须从 pySSTV 源码或 QSSTV 文档核对出精确值，并配一条该模式的金标准录音测试。

### 6.3 VIS 识别

1200Hz 引导（300ms）→ 起始位（1200Hz, 30ms）→ 8 bit VIS（1100Hz=1 / 1300Hz=0，LSB 先行，每 bit 30ms）→ 停止位。弱信号下采用软判决 + 与模式表已知 VIS 汉明匹配（容错 1–2 bit）提升识别率。

### 6.4 抗噪要点

- 行同步采用滑窗相关而非单点阈值
- 逐像素频率估计用相位增量（对频偏/漂移稳健），配合每行直流校准
- 坏行降级输出（灰条）而不是整图失败，与真实接收软件行为一致

---

## 7. SSDV 解码设计

### 7.1 包格式（已核实，源自 UKHAS/G4KLA 规范）

| 偏移      | 内容                              |
| ------- | ------------------------------- |
| 0       | 同步 0x55                         |
| 1       | 类型：0x66 正常（+32B RS）/ 0x67 无 FEC |
| 2–5     | 呼号（base-40）                     |
| 6       | 图像 ID                           |
| 7–8     | 包序号（大端）                         |
| 9–10    | 宽/高（MCU 块数，像素/16）               |
| 11      | 标志：质量级(XOR 4)、EOI、抽样模式          |
| 12–14   | 首 MCU 偏移/索引                     |
| 15–219  | 载荷 205B（FEC 模式）                 |
| 220–223 | CRC-32                          |
| 224–255 | RS(255,223) 校验，纠 16 字节错         |

### 7.2 解码链

1. **包文件形态**：按 256B 定长扫描（规范允许包间夹杂垃圾数据），0x55 对齐 + 类型字节 + CRC 三重校验。
2. **音频解调形态**：BPSK 解调器（Costas 环载波恢复 + Gardner 符号定时 + 匹配滤波），波特率/采样率参数化；先行支持最常见配置，其他速率靠参数表扩展。
3. **RS 纠错**：GF(256) 上 RS(255,223) 截断码，Berlekamp-Massey；Go 生态没有现成的「RS 纠错」库（klauspost/reedsolomon 是擦除码，不适用），以 Phil Karn `rs8` 语义为准移植并**与 C 参考实现对拍**（同输入同输出）。
4. **丢包处理**：规范定义了 MCU 级补空策略（缺包位置填空 MCU、EOB 截断、按 MCU offset 跳过续接点），完整实现以呈现「花屏但可辨认」的图像为目标。
5. **JPEG 重组**：SSDV 载荷是无头 JPEG 扫描数据（固定 DQT/DHT、baseline DCT、YCbCr）。重组 = 拼标准 JPEG 头（SOI/SOF0/DQT/DHT/SOS）+ 去重排扫描数据（为 0xFF 补 0x00 填充）+ EOI。头模板固定，纯字节操作，可用 C 参考实现输出做金标准对拍。

### 7.3 双形态汇合点

音频解调和包文件读取都产出 `chan []byte`（256B 包流），下游 RS→CRC→重组完全复用（D4 落点）。

---

## 8. 测试策略（TDD）

### 8.1 金标准语料库（`testdata/`）

| 来源                                                   | 用途                          |
| ---------------------------------------------------- | --------------------------- |
| `internal/testgen/` 自造 SSTV 编码器（干净 + 注入高斯噪声/频偏/幅度抖动） | 精确边界控制：VIS 容错、时钟恢复极限、坏行降级   |
| pySSTV 生成的 WAV                                       | **第三方 oracle**：防「编码器和解码器同错」 |
| G4KLA ssdv（C）编码输出 `.bin` 及解码 JPEG                    | SSDV 包重组金标准                 |
| 公开接收样本（ISS Robot36 录音、气球/卫星 SSDV 录音）                 | 真实信道条件回归测试                  |
| IQ 录像：用 testgen 上变频合成 CU8/CS16                       | IQ 链路验证（公开 IQ 样本难找，自造为主）    |

### 8.2 纪律

- 每个 DSP/解码模块先写失败测试（RED）再实现（GREEN）
- RS 解码器：对拍 Karn rs8 —— 随机 1–16 字节错误 × 万次统计必须与 C 实现一致
- SSTV 引擎：同一输入下与 pySSTV 参考图像做像素级容差断言（如 95% 像素误差 < 8/255）
- CLI（M0 产物）是主要手动验证工具：`ssxv-cli decode --mode auto sample.wav -o out.png`

---

## 9. 里程碑

| 阶段            | 内容                                                      | 出口标准                               |
| ------------- | ------------------------------------------------------- | ---------------------------------- |
| **M0 核心链路**   | Source(WAV)→DSP→FM 鉴频→VIS→Robot36 表驱动解码；testgen 编码器；CLI | CLI 从 Robot36 WAV 稳定出图，pySSTV 对拍通过 |
| **M1 手机壳**    | gomobile bind + Expo 本地模块 + RN 页面（选文件→解码→看图→图库）         | 真机上从 WAV 文件解码 Robot36 出图           |
| **M2 模式铺开**   | 模式表补全至 §6.2 全集 + VIS 软判决 + 弱信号调优                        | 公开样本集全模式通过率达标                      |
| **M3 SSDV**   | 包文件解码 + RS 移植对拍 + JPEG 重组 → BPSK 音频解调                   | G4KLA 语料对拍通过；真实卫星录音出图              |
| **M4 实时与 IQ** | Go 编排的原生采集（Android/iOS）+ IQ 源（下变频/抽取）                   | 真机麦克风实时解 Robot36；合成 IQ 出图          |

每个里程碑走完整 superpowers 循环：worktree → 实施计划 → 子代理执行 + TDD → 评审 → 收尾。

---

## 10. 风险与缓解

| 风险                         | 等级 | 缓解                                                            |
| -------------------------- | -- | ------------------------------------------------------------- |
| gobind 类型限制导致接口设计返工        | 中  | 桥接面从第一天就收敛为 bytes-in/out + 反向接口（§5），不心存侥幸                     |
| 模式表时序参数凭记忆填错               | 高  | 铁律：每个模式入库前核对 pySSTV/QSSTV 源码 + 配专属金标准测试                       |
| RS 移植引入细微 bug（GF 运算/生成多项式） | 中  | 与 Karn rs8 万次随机对拍，不一致即 CI 失败                                  |
| iOS 后台实时采集被系统压制            | 中  | M4 单独验证后台音频模式；文档记录音频会话配置                                      |
| IQ 格式动物园（字节序/复数排列五花八门）     | 低  | 格式表化 + 用户显式指定参数，第一版只支持 4 种主流格式                                |
| 实时解码性能不足（低端机）              | 低  | 8kHz 音频 FM 鉴频运算量很小；流水线 goroutine 化；M1 前在 CLI 基准测试实时倍率（目标 ≥4×） |

---

## 11. 全平台扩展策略

用户确认目标为全平台。得益于「纯 Go 核心」原则，核心解码逻辑零改动即可覆盖所有平台；平台差异被压缩到三个点：桥接、采集、打包。策略是在 **JS 层定义统一的 `SSXVCore` 接口**，每平台一个适配实现（适配器模式）：

```ts
// UI 只依赖这个接口，不关心底层是 gomobile 还是 WASM
interface SSXVCore {
  newSession(opts: Options): Promise<Session>;
}
interface Session {
  decodeFile(path: string): Promise<void>;
  pushAudio(pcm: ArrayBuffer): Promise<void>;
  onEvent(cb: (e: CoreEvent) => void): () => void;
  cancel(): Promise<void>;
}
```

### 平台矩阵


| 平台                      | UI 方案                           | 核心接入                                             | 音频采集                                       | 打包           |
| ----------------------- | ------------------------------- | ------------------------------------------------ | ------------------------------------------ | ------------ |
| Android / iOS           | Expo（主线）                        | gomobile `.aar` / `.xcframework`（Expo 本地模块，§5.3） | 原生采集器反向注册（D5）                              | EAS Build    |
| Web                     | react-native-web（复用 Expo UI 代码） | Go → **WASM**，核心直接跑在浏览器里                         | Web Audio API / Worklet 采集 PCM → pushAudio | 静态站点，可发布在线版  |
| Windows / macOS / Linux | 复用 Web 版 UI（Tauri/Electron 壳）   | 同 Web：WASM                                       | 复用 Web Audio                               | Tauri 单二进制分发 |
| 桌面/服务器 CLI              | 无 UI                            | 原生 Go 二进制（M0 产物直接可用）                             | OSS/ALSA/CoreAudio 可后加                     | `go build`   |

### 关键判断

1. **WASM 是全平台的杠杆**：解码链是纯 CPU 字节/信号处理（FM 鉴频、RS、JPEG 重组），无 CGO、无 syscall 依赖，`GOOS=js GOARCH=wasm` 编译即用。桌面端复用 Web 版等于一份 UI 三端跑（浏览器 + 三桌面系统）。
2. **gomobile 桥接与 WASM 桥接实现同一套 JS 接口**：§5.2 的 bytes-in/out 设计正好是两种桥都最舒服的形态（WASM 侧线性内存传 `[]byte` 同样简单），当初的最小桥接面原则直接兑现为多平台红利。
3. **采集仍是每平台独有代码**：Web 用 AudioWorklet，移动端用 D5 的原生采集器——这部分无法通用，但接口一致。
4. **性能预期**：8kHz 音频实时解码在现代设备/WASM 上余量很大（目标 ≥4× 实时倍率同样适用于 WASM，M1 前基准测试一并验证）。
5. **实施顺序不变**：先 M0–M4（移动主线），全平台作为 **M5**（WASM 桥 + react-native-web + Tauri 壳）。WASM 桥甚至可以提前到 M1 顺手做——它比 gomobile 桥简单得多，且能先把核心在浏览器里跑通当演示。

---

## 12. 开放问题（不阻塞开工）

1. MP3/OGG 解码选型：纯 Go（`go-mp3`/`hajimehoshi` 系）够用，但码率覆盖需验证；WAV 优先，MP3/OGG 排 M1+。
2. 实时链路的环形缓冲策略与背压（推流过快/过慢）——M4 实施计划里细化。
3. 图库存储：RN 侧 SQLite 还是 Go 侧管理（经桥操作）——倾向 RN 侧管元数据、图片文件共用 Documents 目录。
4. BPSK 之外的实际卫星下行变体（如 KISS 封装双层 RS，如 DSLWP 案例）是否纳入——M3 时按真实语料决定。
