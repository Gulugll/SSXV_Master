// Package sstv 实现 SSTV 解码：VIS 识别、模式表与逐行解码引擎。
// 规格见 docs/TECH_SPEC.md §3（数值已从 pySSTV 源码核实）。
package sstv

// ---- SSTV 模式表 v2 —— 全模式通用结构（TECH_SPEC §3.3）----
//
// 模型：解码以「同步脉冲锚点」为周期起点，每个锚点后跟随一个 Cycle（若干
// 扫描段），Cycle 覆盖 1-2 行。三种色彩语义：
//
//	ColorRGB             Martin/Scottie/Pasokon/Wraase：每行 R/G/B 三段扫描
//	ColorYCbCrPair       PD 系：每 Cycle 两行，色度为两行平均（Y0,Cr,Cb,Y1 顺序）
//	ColorYCbCrInterleave Robot36：每 Cycle 一行，色度奇偶交替（偶行 Cr 奇行 Cb）
//
// Scottie 特殊性：无逐行同步，同步脉冲位于 R 段之前（发射序 G,B,sync,R），
// 因此 R 属于本 Cycle 行、G/B 属于下一行（RowDelta=+1）。

type ColorMode int

const (
	ColorRGB             ColorMode = iota // R/G/B 直传
	ColorYCbCrPair                        // PD 系：双行共享平均色度
	ColorYCbCrInterleave                  // Robot36：色度奇偶交替
)

// 语义通道编号
const (
	ChR  = 0
	ChG  = 1
	ChB  = 2
	ChY  = 0
	ChCb = 1
	ChCr = 2
)

// ScanSeg 一个扫描段：前置 porch（@1500Hz）+ 扫描窗。
type ScanSeg struct {
	Channel  int     // 语义通道
	PorchMs  float64 // 扫描窗前的 porch 时长
	ScanMs   float64 // 扫描总时长（Width×像素时长）
	RowDelta int     // 行号 = cycleBase + RowDelta
}

// CycleSpec 一个锚点周期。
type CycleSpec struct {
	RowsAdv int // 本周期推进的行数（PD=2，其余=1）
	Segs    []ScanSeg
}

// ModeSpec 模式规格。
type ModeSpec struct {
	Name     string
	VIS      uint8
	Width    int
	Height   int
	Color    ColorMode
	SyncMs   float64     // 锚点同步脉冲时长 @1200Hz
	PeriodMs float64     // 锚点周期（行/双行时长），用于无同步回退与实时倍率统计
	Cycles   []CycleSpec // 逐锚点轮替（多数模式长度 1；Robot36 为 2）
}

// 频率常量（Hz），pySSTV sstv.py 原值。
const (
	FreqSync    = 1200 // 同步/起始/停止位
	FreqVisBit1 = 1100 // VIS 位 = 1
	FreqVisBit0 = 1300 // VIS 位 = 0
	FreqBlack   = 1500 // 黑电平（亮度 0）
	FreqVisLead = 1900 // VIS 引导 / Robot36 色度 porch
	FreqWhite   = 2300 // 白电平（亮度 255）
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

// ---- 各模式（时序数值：pySSTV color.py 原值；推导见 TECH_SPEC §3.3）----

func martinCycles(scanMs float64) []CycleSpec {
	// 每行：sync(4.862) → [G: 0.572+scan][B: 0.572+scan][R: 0.572+scan]
	return []CycleSpec{{RowsAdv: 1, Segs: []ScanSeg{
		{Channel: ChG, PorchMs: 0.572, ScanMs: scanMs},
		{Channel: ChB, PorchMs: 0.572, ScanMs: scanMs},
		{Channel: ChR, PorchMs: 0.572, ScanMs: scanMs},
	}}}
}

func scottieCycles(scanMs float64) []CycleSpec {
	// 锚点=sync(9ms)：[R: 1.5+scan（本行）][G: 1.5+scan（下一行）][B: 1.5+scan（下一行）]
	return []CycleSpec{{RowsAdv: 1, Segs: []ScanSeg{
		{Channel: ChR, PorchMs: 1.5, ScanMs: scanMs, RowDelta: 0},
		{Channel: ChG, PorchMs: 1.5, ScanMs: scanMs, RowDelta: 1},
		{Channel: ChB, PorchMs: 1.5, ScanMs: scanMs, RowDelta: 1},
	}}}
}

func wraaseCycles(scanMs float64, redPorch float64) []CycleSpec {
	return []CycleSpec{{RowsAdv: 1, Segs: []ScanSeg{
		{Channel: ChR, PorchMs: redPorch, ScanMs: scanMs},
		{Channel: ChG, PorchMs: 0, ScanMs: scanMs},
		{Channel: ChB, PorchMs: 0, ScanMs: scanMs},
	}}}
}

func pasokonCycles(width int, unitMs float64) []CycleSpec {
	scan := float64(width) * unitMs
	return []CycleSpec{{RowsAdv: 1, Segs: []ScanSeg{
		{Channel: ChR, PorchMs: 5 * unitMs, ScanMs: scan},
		{Channel: ChG, PorchMs: 5 * unitMs, ScanMs: scan},
		{Channel: ChB, PorchMs: 5 * unitMs, ScanMs: scan},
	}}}
}

func pdCycles(width int, pixelMs float64) []CycleSpec {
	scan := float64(width) * pixelMs
	// 每 Cycle 两行：sync(20) → porch 2.08 → Y0 → Cr(平均) → Cb(平均) → Y1
	return []CycleSpec{{RowsAdv: 2, Segs: []ScanSeg{
		{Channel: ChY, PorchMs: 2.08, ScanMs: scan, RowDelta: 0},
		{Channel: ChCr, PorchMs: 0, ScanMs: scan, RowDelta: 0},
		{Channel: ChCb, PorchMs: 0, ScanMs: scan, RowDelta: 0},
		{Channel: ChY, PorchMs: 0, ScanMs: scan, RowDelta: 1},
	}}}
}

// Robot36：偶行 sep@1500 传 Cr、奇行 sep@2300 传 Cb（pySSTV 语义）；
// 分隔段只按时间窗定位，不校验频率（TECH_SPEC §3.4）。
func robot36Cycles() []CycleSpec {
	const yScan, cScan = 88.0, 44.0
	return []CycleSpec{
		{RowsAdv: 1, Segs: []ScanSeg{
			{Channel: ChY, PorchMs: 3, ScanMs: yScan},
			{Channel: ChCr, PorchMs: 6, ScanMs: cScan}, // sep 4.5 + porch 1.5
		}},
		{RowsAdv: 1, Segs: []ScanSeg{
			{Channel: ChY, PorchMs: 3, ScanMs: yScan},
			{Channel: ChCb, PorchMs: 6, ScanMs: cScan},
		}},
	}
}

func Robot36() ModeSpec {
	return ModeSpec{
		Name: "Robot36", VIS: 0x08, Width: 320, Height: 240,
		Color: ColorYCbCrInterleave, SyncMs: 9, PeriodMs: 150,
		Cycles: robot36Cycles(),
	}
}

func MartinM1() ModeSpec {
	return ModeSpec{
		Name: "MartinM1", VIS: 0x2C, Width: 320, Height: 256,
		Color: ColorRGB, SyncMs: 4.862, PeriodMs: 446.446,
		Cycles: martinCycles(146.432),
	}
}

func MartinM2() ModeSpec {
	return ModeSpec{
		Name: "MartinM2", VIS: 0x28, Width: 160, Height: 256,
		Color: ColorRGB, SyncMs: 4.862, PeriodMs: 226.196,
		Cycles: martinCycles(73.216),
	}
}

func ScottieS1() ModeSpec {
	return ModeSpec{
		Name: "ScottieS1", VIS: 0x3C, Width: 320, Height: 256,
		Color: ColorRGB, SyncMs: 9, PeriodMs: 428.22,
		Cycles: scottieCycles(136.74),
	}
}

func ScottieS2() ModeSpec {
	return ModeSpec{
		Name: "ScottieS2", VIS: 0x38, Width: 160, Height: 256,
		Color: ColorRGB, SyncMs: 9, PeriodMs: 271.692,
		Cycles: scottieCycles(86.564),
	}
}

func ScottieDX() ModeSpec {
	return ModeSpec{
		Name: "ScottieDX", VIS: 0x4C, Width: 320, Height: 256,
		Color: ColorRGB, SyncMs: 9, PeriodMs: 1050.8,
		Cycles: scottieCycles(344.1),
	}
}

func PD90() ModeSpec {
	return ModeSpec{
		Name: "PD90", VIS: 0x63, Width: 320, Height: 256,
		Color: ColorYCbCrPair, SyncMs: 20, PeriodMs: 733.04,
		Cycles: pdCycles(320, 0.532),
	}
}

func PD120() ModeSpec {
	return ModeSpec{
		Name: "PD120", VIS: 0x5F, Width: 640, Height: 496,
		Color: ColorYCbCrPair, SyncMs: 20, PeriodMs: 528.48,
		Cycles: pdCycles(640, 0.19),
	}
}

func PD160() ModeSpec {
	return ModeSpec{
		Name: "PD160", VIS: 0x62, Width: 512, Height: 400,
		Color: ColorYCbCrPair, SyncMs: 20, PeriodMs: 808.914,
		Cycles: pdCycles(512, 0.382),
	}
}

func PD180() ModeSpec {
	return ModeSpec{
		Name: "PD180", VIS: 0x60, Width: 640, Height: 496,
		Color: ColorYCbCrPair, SyncMs: 20, PeriodMs: 769.36,
		Cycles: pdCycles(640, 0.286),
	}
}

func PD240() ModeSpec {
	return ModeSpec{
		Name: "PD240", VIS: 0x61, Width: 640, Height: 496,
		Color: ColorYCbCrPair, SyncMs: 20, PeriodMs: 1018.92,
		Cycles: pdCycles(640, 0.382),
	}
}

func PD290() ModeSpec {
	return ModeSpec{
		Name: "PD290", VIS: 0x5E, Width: 800, Height: 616,
		Color: ColorYCbCrPair, SyncMs: 20, PeriodMs: 936.22,
		Cycles: pdCycles(800, 0.286),
	}
}

func WraaseSC2180() ModeSpec {
	return ModeSpec{
		Name: "WraaseSC2180", VIS: 0x37, Width: 320, Height: 256,
		Color: ColorRGB, SyncMs: 5.5225, PeriodMs: 711.0225,
		Cycles: wraaseCycles(235.0, 0.5),
	}
}

func WraaseSC2120() ModeSpec {
	return ModeSpec{
		Name: "WraaseSC2120", VIS: 0x3F, Width: 320, Height: 256,
		Color: ColorRGB, SyncMs: 5.5225, PeriodMs: 474.5225,
		Cycles: wraaseCycles(156.0, 1.0),
	}
}

func PasokonP3() ModeSpec {
	return ModeSpec{
		Name: "PasokonP3", VIS: 0x71, Width: 640, Height: 496,
		Color: ColorRGB, SyncMs: 25.0 / 4.8, PeriodMs: 1975.0 / 4.8,
		Cycles: pasokonCycles(640, 1000.0/4800.0),
	}
}

func PasokonP5() ModeSpec {
	return ModeSpec{
		Name: "PasokonP5", VIS: 0x72, Width: 640, Height: 496,
		Color: ColorRGB, SyncMs: 25.0 / 3.2, PeriodMs: 1975.0 / 3.2,
		Cycles: pasokonCycles(640, 1000.0/3200.0),
	}
}

func PasokonP7() ModeSpec {
	return ModeSpec{
		Name: "PasokonP7", VIS: 0xF3, Width: 640, Height: 496,
		Color: ColorRGB, SyncMs: 25.0 / 2.4, PeriodMs: 1975.0 / 2.4,
		Cycles: pasokonCycles(640, 1000.0/2400.0),
	}
}

// allModes 全部已实现模式（ModeByVIS 查询表）。
var allModes = []ModeSpec{
	Robot36(), MartinM1(), MartinM2(),
	ScottieS1(), ScottieS2(), ScottieDX(),
	PD90(), PD120(), PD160(), PD180(), PD240(), PD290(),
	WraaseSC2180(), WraaseSC2120(),
	PasokonP3(), PasokonP5(), PasokonP7(),
}

// ModeByVIS 按 VIS 码查模式，汉明 ≤2 容错（取最近；唯一最近者胜出，
// 平局拒绝）。VIS 空间拥挤，多位模式存在 1bit 相邻（如 MartinM1/M2、
// ScottieS1/S2、PD180/240、Wraase180/120），为协议固有歧义，弱信号下
// 可能解为相邻模式——真实解码软件行为一致。
func ModeByVIS(vis uint8) (ModeSpec, bool) {
	// VIS 只传 7 个数据位 + 1 个奇偶校验位；pySSTV 的 VIS_CODE（如
	// PasokonP7 的 0xF3）把校验位含在 bit7 —— 比较时双侧掩掉 bit7。
	vis &= 0x7F
	best := ModeSpec{}
	bestD := 9
	tie := false
	for _, m := range allModes {
		d := hamming(m.VIS&0x7F, vis)
		if d < bestD {
			bestD, best, tie = d, m, false
		} else if d == bestD {
			tie = true
		}
	}
	if bestD > 2 || tie {
		return ModeSpec{}, false
	}
	return best, true
}

// ModeByName 按名称精确查模式（显式模式解码用，如 "Robot36"）。
func ModeByName(name string) (ModeSpec, bool) {
	for _, m := range allModes {
		if m.Name == name {
			return m, true
		}
	}
	return ModeSpec{}, false
}
