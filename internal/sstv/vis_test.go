package sstv

import (
	"math"
	"testing"
)

// 构造恒定频率段
func segF(freqHz float64, msec int, fs int) []float32 {
	n := fs * msec / 1000
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(freqHz)
	}
	return out
}

// 用 testgen 语义构造 VIS 头的瞬时频率序列
func visHeaderSamples(fs int, vis byte) []float32 {
	var f []float32
	f = append(f, segF(1900, 300, fs)...)
	f = append(f, segF(1200, 10, fs)...)
	f = append(f, segF(1900, 300, fs)...)
	f = append(f, segF(1200, 30, fs)...) // 起始位
	ones := 0
	v := vis
	for i := 0; i < 7; i++ {
		bit := v & 1
		v >>= 1
		ones += int(bit)
		if bit == 1 {
			f = append(f, segF(1100, 30, fs)...)
		} else {
			f = append(f, segF(1300, 30, fs)...)
		}
	}
	// 偶校验
	if ones%2 == 1 {
		f = append(f, segF(1100, 30, fs)...)
	} else {
		f = append(f, segF(1300, 30, fs)...)
	}
	f = append(f, segF(1200, 30, fs)...) // 停止位
	return f
}

func TestDetectVISRobot36(t *testing.T) {
	fs := 8000
	freq := visHeaderSamples(fs, 0x08)
	// 头部前加 1s 静默、后加 200ms 静默
	freq = append(append(make([]float32, fs), freq...), segF(1500, 200, fs)...)

	got, idx, ok := DetectVIS(freq, float32(fs))
	if !ok {
		t.Fatal("未识别出 VIS")
	}
	if got != 0x08 {
		t.Errorf("VIS=%#02x, 期望 0x08", got)
	}
	// idx 应落在起始位（1s 静默 + 300+10+300 ms）附近，容差 ±5ms
	want := float64(fs) + float64(fs)*(300.0+10.0+300.0)/1000.0
	if math.Abs(float64(idx)-want) > float64(fs)*5/1000 {
		t.Errorf("起始位索引 %d, 期望约 %.0f", idx, want)
	}
}

func TestDetectVISHammingTolerance(t *testing.T) {
	fs := 8000
	// 0x2C (MartinM1) 的位流翻转 1 位 → 应仍识别成功（模式表匹配在 Decode 阶段做）
	freq := visHeaderSamples(fs, 0x2C^0x01)
	_, _, ok := DetectVIS(freq, float32(fs))
	if !ok {
		t.Fatal("汉明容错 1bit 未识别")
	}
}

func TestByteToFreq(t *testing.T) {
	if got := ByteToFreq(0); got != 1500 {
		t.Errorf("ByteToFreq(0)=%f", got)
	}
	if got := ByteToFreq(255); math.Abs(got-2300) > 0.01 {
		t.Errorf("ByteToFreq(255)=%f", got)
	}
}
