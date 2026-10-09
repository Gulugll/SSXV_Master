package testgen

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

// 幅度谱峰值频率（粗测：Goertzel 单频比较）
func dominantFreq(x []float32, fs int) float64 {
	bestF, bestP := 0.0, -1.0
	for f := 800; f <= 2200; f += 50 {
		w := 2 * math.Pi * float64(f) / float64(fs)
		var re, im float64
		for n, v := range x {
			re += float64(v) * math.Cos(w*float64(n))
			im -= float64(v) * math.Sin(w*float64(n))
		}
		p := re*re + im*im
		if p > bestP {
			bestP, bestF = p, float64(f)
		}
	}
	return bestF
}

func TestBPSKBaudRateAndCarrier(t *testing.T) {
	// 交替位 0x55 序列 @200bd：应产生 1500Hz 附近载波
	bits := make([]byte, 400)
	for i := range bits {
		bits[i] = 0x55
	}
	fs := 48000
	x := BPSKModulate(bits, fs)
	if want := len(bits) * 8 * fs / BPSKBaud; len(x) != want {
		t.Fatalf("长度 %d != %d", len(x), want)
	}
	f := dominantFreq(x[:4800], fs)
	if math.Abs(f-1500) > 100 {
		t.Errorf("主频 %.0f Hz, 期望 ~1500", f)
	}
}

func TestBPSKAmplitude(t *testing.T) {
	bits := bytes.Repeat([]byte{0x00}, 100)
	x := BPSKModulate(bits, 48000)
	peak := 0.0
	for _, v := range x {
		if a := math.Abs(float64(v)); a > peak {
			peak = a
		}
	}
	if peak > 0.95 || peak < 0.5 {
		t.Errorf("峰值幅度 %.2f 超出 (0.5, 0.95)", peak)
	}
}

func TestBPSKDeterministic(t *testing.T) {
	bits := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	a := BPSKModulate(bits, 48000)
	b := BPSKModulate(bits, 48000)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("非确定性 @%d", i)
		}
	}
}

func TestBPSKAddAWGN(t *testing.T) {
	x := []float32{0.5, -0.5, 0.25}
	y := BPSKAWGN(x, 30, 42) // 固定种子
	same := BPSKAWGN(x, 30, 42)
	for i := range y {
		if y[i] != same[i] {
			t.Fatal("同种子噪声应一致")
		}
	}
	// SNR 以 2.7kHz 通信声道计：全带功率比 = 10^(30/10) / (48000/2700) ≈ 56
	var sp, np float64
	for i := range x {
		sp += float64(x[i]) * float64(x[i])
		np += float64(y[i]-x[i]) * float64(y[i]-x[i])
	}
	ratio := sp / np
	want := math.Pow(10, 30.0/10) / (48000.0 / 2700.0)
	if ratio < want/2 || ratio > want*2 {
		t.Errorf("全带功率比 %.2f, 期望 ~%.2f", ratio, want)
	}
	_ = rand.Int
}
