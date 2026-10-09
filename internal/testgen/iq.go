// IQ 语料生成（M4-2）：SSTV 段序列 → IF 偏移复基带（FM 语义）→ CU8/CS16/CF32 裸字节。
// 与 pipeline.ParseIQ / DecodeIQResult 严格互逆（IF 偏移语义见 pipeline/iq.go）。
package testgen

import (
	"encoding/binary"
	"math"
)

// IQFormat IQ 裸格式。
type IQFormat int

const (
	IQCU8 IQFormat = iota
	IQCS16
	IQCF32
)

// IQUpconvertSegs 把段序列生成为 IF 偏移复基带：inst_freq = IF + Seg.Freq，
// 相位连续、恒定包络 0.9（模拟真实 FM 信号）。
func IQUpconvertSegs(segs []Seg, fs int, ifHz float64, f IQFormat) []byte {
	iq := make([]float32, 0, len(segs)*int(float64(fs)*0.01)*2)

	idx := 0
	phase := 0.0
	carry := 0.0 // 小数样本进位（与 toneGen 一致；15 万个像素段无进位会累积漂移 1s+）
	for _, s := range segs {
		pos := carry + float64(fs)*s.Ms/1000
		n := int(pos) - int(carry)
		carry = pos
		step := 2 * math.Pi * (ifHz + s.Freq) / float64(fs)
		for i := 0; i < n; i++ {
			phase += step
			if phase >= 2*math.Pi {
				phase -= 2 * math.Pi
			}
			re := float32(0.9 * math.Cos(phase))
			im := float32(0.9 * math.Sin(phase))
			iq = append(iq, re, im)
			idx++
		}
	}
	return packIQ(iq, f)
}

// packIQ 交错复数对 → 裸字节。
func packIQ(iq []float32, f IQFormat) []byte {
	n := len(iq) / 2
	switch f {
	case IQCU8:
		out := make([]byte, n*2)
		for i, v := range iq {
			out[i] = clampU8(v*127.5 + 128)
		}
		return out
	case IQCS16:
		out := make([]byte, n*4)
		for i, v := range iq {
			binary.LittleEndian.PutUint16(out[2*i:], uint16(clampS16(v*32767)))
		}
		return out
	case IQCF32:
		out := make([]byte, n*8)
		for i, v := range iq {
			binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(v))
		}
		return out
	}
	panic("unreachable")
}

func clampU8(v float32) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v + 0.5)
}

func clampS16(v float32) int16 {
	if v < -32768 {
		v = -32768
	}
	if v > 32767 {
		v = 32767
	}
	return int16(math.Round(float64(v)))
}
