// Package testgen 生成测试语料：SSTV 编码器、噪声注入与 WAV 写出。
// 仅用于测试，不进入产品二进制（DESIGN.md §4）。
package testgen

import (
	"encoding/binary"
	"image"
	"math"
	"math/rand"
)

// Seg 一个恒定频率正弦段。
type Seg struct {
	Freq float64 // Hz
	Ms   float64 // 时长（毫秒，可为小数）
}

// toneGen 相位连续正弦发生器（累积舍入，pySSTV gen_values 语义）。
type toneGen struct {
	fs      int
	phase   float64 // 当前相位（弧度）
	samples []float32
	carry   float64 // 已排入的样本位置（浮点，累积舍入）
}

func (g *toneGen) emit(freqHz float64, msec float64) {
	pos := g.carry + float64(g.fs)*msec/1000
	n := int(pos) - int(g.carry)
	g.carry = pos
	step := 2 * math.Pi * freqHz / float64(g.fs)
	for i := 0; i < n; i++ {
		g.phase += step
		for g.phase >= 2*math.Pi {
			g.phase -= 2 * math.Pi
		}
		g.samples = append(g.samples, float32(math.Sin(g.phase)))
	}
}

// GenTone 按段序列生成相位连续正弦波形（幅度 ±1）。
func GenTone(segs []Seg, fs int) *toneGen {
	g := &toneGen{fs: fs}
	for _, s := range segs {
		g.emit(s.Freq, s.Ms)
	}
	return g
}

// Len 已生成样本数。
func (g *toneGen) Len() int { return len(g.samples) }

// ---- SSTV 频率常量（与 sstv 包一致，复制以保持 testgen 独立）----

const (
	tgFreqSync    = 1200
	tgFreqVisBit1 = 1100
	tgFreqVisBit0 = 1300
	tgFreqBlack   = 1500
	tgFreqVisLead = 1900
	tgFreqWhite   = 2300
)

func tgByteToFreq(v int) float64 { return 1500 + 800*float64(v)/255 }

// ---- VIS 头 ----

func visSegments(vis byte) []Seg {
	var out []Seg
	out = append(out, Seg{tgFreqVisLead, 300}, Seg{tgFreqSync, 10},
		Seg{tgFreqVisLead, 300}, Seg{tgFreqSync, 30}) // 起始位
	ones := 0
	v := vis
	for i := 0; i < 7; i++ {
		bit := v & 1
		v >>= 1
		ones += int(bit)
		if bit == 1 {
			out = append(out, Seg{tgFreqVisBit1, 30})
		} else {
			out = append(out, Seg{tgFreqVisBit0, 30})
		}
	}
	if ones%2 == 1 {
		out = append(out, Seg{tgFreqVisBit1, 30})
	} else {
		out = append(out, Seg{tgFreqVisBit0, 30})
	}
	return append(out, Seg{tgFreqSync, 30}) // 停止位
}

// ---- Robot36 编码 ----

// EncodeRobot36 将 320×240 图像编码为 Robot36 段序列。
// 含 9ms 行同步（Barber 规范；pySSTV 缺同步的偏差见 TECH_SPEC §3.4）。
// 分隔脉冲频率随奇偶行 1500/2300（pySSTV 语义），porch 1.5ms@1900。
func EncodeRobot36(img *image.NRGBA, fs int) []Seg {
	const W, H = 320, 240
	b := img.Bounds()
	if b.Dx() != W || b.Dy() != H {
		panic("EncodeRobot36: 需要 320x240 图像")
	}
	// 预转 YCbCr（JPEG full-range BT.601）
	yArr := make([][]float64, H)
	cbArr := make([][]float64, H)
	crArr := make([][]float64, H)
	for y := 0; y < H; y++ {
		yArr[y] = make([]float64, W)
		cbArr[y] = make([]float64, W)
		crArr[y] = make([]float64, W)
		for x := 0; x < W; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			r8, g8, b8 := float64(r>>8), float64(g>>8), float64(bl>>8)
			yArr[y][x] = 0.299*r8 + 0.587*g8 + 0.114*b8
			cbArr[y][x] = (b8-yArr[y][x])*0.564 + 128
			crArr[y][x] = (r8-yArr[y][x])*0.713 + 128
		}
	}

	segs := visSegments(0x08)
	pxY := 88.0 / W // 0.275 ms/px
	pxC := 44.0 / W // 0.1375 ms/px
	for line := 0; line < H; line++ {
		segs = append(segs, Seg{tgFreqSync, 9})
		segs = append(segs, Seg{tgFreqBlack, 3})
		for x := 0; x < W; x++ {
			segs = append(segs, Seg{tgByteToFreq(int(yArr[line][x] + 0.5)), pxY})
		}
		// 色度：偶行 Cr，奇行 Cb（PIL 语义：channel = 2 - line%2）
		cArr := cbArr
		sepFreq := tgFreqWhite // 奇行 Cb
		if line%2 == 0 {
			cArr = crArr
			sepFreq = tgFreqBlack // 偶行
		}
		segs = append(segs, Seg{float64(sepFreq), 4.5}, Seg{tgFreqVisLead, 1.5})
		for x := 0; x < W; x++ {
			segs = append(segs, Seg{tgByteToFreq(int(cArr[line][x] + 0.5)), pxC})
		}
	}
	return segs
}

// Robot36Tone 编码并生成波形。
func Robot36Tone(img *image.NRGBA, fs int) []float32 {
	return GenTone(EncodeRobot36(img, fs), fs).samples
}

// ---- AWGN ----

// AWGN 按目标 SNR(dB，相对信号均方功率) 注入加性高斯白噪声。
func AWGN(x []float32, snrDb float64) []float32 {
	var sp float64
	for _, v := range x {
		sp += float64(v) * float64(v)
	}
	sp /= float64(len(x))
	np := sp / math.Pow(10, snrDb/10)
	sigma := math.Sqrt(np)
	out := make([]float32, len(x))
	rng := rand.New(rand.NewSource(1))
	for i, v := range x {
		out[i] = v + float32(rng.NormFloat64()*sigma)
	}
	return out
}

// ---- WAV ----

// WriteWav16 将 ±1 浮点波形写为单声道 PCM16 WAV 字节流。
func WriteWav16(x []float32, fs int) []byte {
	dataLen := len(x) * 2
	buf := make([]byte, 44+dataLen)
	copy(buf[0:], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], uint32(36+dataLen))
	copy(buf[8:], "WAVE")
	copy(buf[12:], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:], 1) // mono
	binary.LittleEndian.PutUint32(buf[24:], uint32(fs))
	binary.LittleEndian.PutUint32(buf[28:], uint32(fs*2))
	binary.LittleEndian.PutUint16(buf[32:], 2)
	binary.LittleEndian.PutUint16(buf[34:], 16)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], uint32(dataLen))
	for i, v := range x {
		// 先钳制再转换：float→int16 越界回绕会把峰值样本变成反号尖峰
		// （曾导致全部噪声语料被尖峰污染）
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		s := int16(math.Round(float64(v) * 32767))
		binary.LittleEndian.PutUint16(buf[44+i*2:], uint16(s))
	}
	return buf
}
