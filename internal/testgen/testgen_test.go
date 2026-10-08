package testgen

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestToneDurationAndContinuity(t *testing.T) {
	fs := 48000
	segs := []Seg{
		{Freq: 1900, Ms: 300},
		{Freq: 1200, Ms: 10},
		{Freq: 1900, Ms: 300.5},
	}
	x := GenTone(segs, fs)
	want := int(float64(fs) * 610.5 / 1000)
	if abs(x.Len()-want) > 1 {
		t.Errorf("总样本 %d, 期望约 %d（累积舍入）", x.Len(), want)
	}
}

func TestEncodeRobot36Structure(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 320, 240))
	fs := 48000
	segs := EncodeRobot36(img, fs)
	// VIS 头 910ms + 240 行 × 150ms = 36910ms
	var totalMs float64
	for _, s := range segs {
		totalMs += s.Ms
	}
	if math.Abs(totalMs-36910) > 1 {
		t.Errorf("总时长 %.2fms, 期望 36910ms", totalMs)
	}
	syncCount := 0
	for _, s := range segs {
		if s.Freq == 1200 && math.Abs(s.Ms-9) < 0.01 {
			syncCount++
		}
	}
	if syncCount != 240 {
		t.Errorf("行同步数 %d, 期望 240", syncCount)
	}
}

func TestAWGNSnr(t *testing.T) {
	// 常数信号 0.5 → 信号功率 0.25；SNR=20dB → 噪声 rms ≈ 0.05
	x := make([]float32, 48000)
	for i := range x {
		x[i] = 0.5
	}
	noisy := AWGN(x, 20)
	var np float64
	for _, v := range noisy {
		d := float64(v) - 0.5
		np += d * d
	}
	np /= float64(len(noisy))
	db := 10 * math.Log10(0.25/np)
	if math.Abs(db-20) > 0.5 {
		t.Errorf("实测 SNR %.2f dB, 期望 20±0.5", db)
	}
}

func TestWavWriteSize(t *testing.T) {
	fs := 8000
	x := make([]float32, fs) // 1s
	wav := WriteWav16(x, fs)
	if len(wav) != 44+2*fs {
		t.Errorf("WAV 大小 %d, 期望 %d", len(wav), 44+2*fs)
	}
}

func gradientImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{
				R: uint8((x * 255) / w),
				G: uint8((y * 255) / h),
				B: uint8(((x + y) * 255) / (w + h)),
				A: 255,
			})
		}
	}
	return img
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
