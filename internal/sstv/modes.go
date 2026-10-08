// Package sstv 实现 SSTV 解码：VIS 识别、模式表与逐行解码引擎。
// 规格见 docs/TECH_SPEC.md §3（数值已从 pySSTV 源码核实）。
package sstv

// 频率常量（Hz），pySSTV sstv.py 原值。
const (
	FreqSync    = 1200 // 同步/起始/停止位
	FreqVisBit1 = 1100 // VIS 位 = 1
	FreqVisBit0 = 1300 // VIS 位 = 0
	FreqBlack   = 1500 // 黑电平（0）
	FreqVisLead = 1900 // VIS 引导
	FreqWhite   = 2300 // 白电平（255）
)

// ByteToFreq 亮度 0..255 → 1500..2300 Hz 线性映射。
func ByteToFreq(v int) float64 {
	return FreqBlack + 800*float64(v)/255
}

// FreqToByte 瞬时频率 → 亮度，clamp 到 [0,255]。
func FreqToByte(f float64) int {
	v := (f - FreqBlack) * 255 / 800
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	return int(v + 0.5)
}

// ChannelSpec 描述一行中一段扫描窗。
type ChannelSpec struct {
	PreFreq float64 // 窗前 porch 频率（仅注释用途，解码不校验）
	PreMs   float64 // 窗前 porch 时长 ms
	Source  int     // 0=Y,1=Cb,2=Cr（YCbCr 模式）；0=R,1=G,2=B（RGB 模式按 COLOR_SEQ 映射）
	Pixels  int     // 本段像素数
	TotalMs float64 // 本段总时长 ms
}

// ModeSpec 模式时序规格（TECH_SPEC §3.3）。
type ModeSpec struct {
	Name   string
	VIS    uint8
	Width  int
	Height int
	YCbCr  bool // true=YCbCr(Robot36/PD)，false=RGB
	Lines  []LineSpec
}

// LineSpec 一行（或一行组）的结构。
type LineSpec struct {
	SyncMs    float64       // 行同步脉冲时长 @1200Hz（0=无行同步）
	AfterSync float64       // 同步后到首个扫描窗的 porch ms（@1500）
	Channels  []ChannelSpec // 依序扫描段
	Repeat    int           // 本结构覆盖的行数（PD 系 =2；其余 =1）
}

// Robot36 模式（pySSTV 语义：偶行 sep@1500 传 Cr、奇行 sep@2300 传 Cb；
// 分隔段只按时间窗定位，不校验频率）。
func Robot36() ModeSpec {
	y := ChannelSpec{PreFreq: FreqBlack, PreMs: 3, Source: 0, Pixels: 320, TotalMs: 88}
	cEven := ChannelSpec{PreFreq: 1500, PreMs: 4.5 + 1.5, Source: 2, Pixels: 320, TotalMs: 44} // Cr
	cOdd := ChannelSpec{PreFreq: 2300, PreMs: 4.5 + 1.5, Source: 1, Pixels: 320, TotalMs: 44}  // Cb
	return ModeSpec{
		Name: "Robot36", VIS: 0x08, Width: 320, Height: 240, YCbCr: true,
		Lines: []LineSpec{
			{SyncMs: 9, AfterSync: 0, Channels: []ChannelSpec{y, cEven}, Repeat: 1},
			{SyncMs: 9, AfterSync: 0, Channels: []ChannelSpec{y, cOdd}, Repeat: 1},
		},
	}
}
