# SSXV_Manager 项目长期备忘

## 项目
- SSTV/SSDV 解码工具。架构（v0.2）：**纯 Go 核心（CLI + WASM 双产物）+ React Web 前端 + Tauri 桌面壳**。无移动端（用户明确不要）。
- 文档：docs/DESIGN.md（架构与 ADR）→ docs/TECH_SPEC.md（可实现规格，关键数值已从 pySSTV/G4KLA ssdv/Karn rs8 源码核实）→ docs/PLAN_M0.md。参考 C/Python 源码存档于 docs/ref/。

## 关键技术事实（勿凭记忆重推）
- pySSTV Robot36 **不发 9ms 行同步**（SYNC 常量未用）→ 不能当 Robot36 oracle；Robot36 分隔脉冲频率与 Barber 规范不一致 → 解码器分隔段只按时间窗不校验频率
- SSDV：CRC32=IEEE 反射 0xEDB88320；RS=GF(256)/0x11D/FCR 112/PRIM 11/NROOTS 32/pad 0，先 RS 后 CRC；JPEG 重组模板 SOI+2×DQT(65B)+SOF0(15B)+4×DHT(29/179/29/179)+SOS(10B)，宽高=MCU×16
- **dsp 教训（M0 调试）**：
  - sincWindow 的 fc 必须除以 fs 归一化（曾退化为 delta 核键，测试因测试音低于奈奎斯特而瞎过）
  - testgen int16 转换必须先钳制再转（越界回绕曾污染全部噪声语料）
  - 窄带 FIR 带通阶跃振铃拖尾 ~10ms 会污染扫描段两端（U 形误差）→ 用温和 IIR（Q=0.75 两级）
  - Hilbert Type-III FIR 有效下限 ≈ 3.3·fs/(2N)——48k 下 127-tap 覆盖不到 1100/1300Hz（VIS 位频率）→ HilbertAt 按 fs 自适应核长
  - 高采样率原生解调（不降到 8k）：Hilbert 建立时间以样本数计，48k 下仅 0.2ms
  - 同步锚点用「频率凹陷质心」而非阈值穿越（对滤波滞后不敏感）+ 校准偏置 fs/5400
  - SNR 口径：以 700-3400Hz 带内计（全带 24kHz 口径对窄带 FM 过于严苛）
- 像素采样：像素窗中心 60-80% 区间均值；E2E 验收双口径：全图 ≥98% + 内部（去两端 2px 过渡列）≥99.3%

## 状态
- M0 已完成并合并 main（2026-10-08）：Robot36 表驱动解码 + VIS 识别 + testgen 编码器 + CLI，65.8× 实时（目标 4×）
- 待做：M1 WASM+React Web 壳 → M2 全 SSTV 模式表 → M3 SSDV（RS 移植对拍 Karn rs8）→ M4 实时麦克风(Web Audio)+IQ+Tauri → M5(可选) Web/Tauri 全平台
- Go 1.26.1 (homebrew)；模块名 `ssxv`；main 分支直开开发，superpowers 流程按里程碑走
