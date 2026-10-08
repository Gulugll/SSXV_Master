package dsp

import (
	"math"
	"testing"
)

// 合成纯音：fs 采样率、freq 频率、msec 时长，返回实信号
func tone(fs int, freq float64, msec int) []float32 {
	n := fs * msec / 1000
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / float64(fs)))
	}
	return x
}

func medianFloat(xs []float32) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float32(nil), xs...)
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return float64(s[len(s)/2])
}

func TestInstantFreqPureTone(t *testing.T) {
	fs := 8000
	for _, f := range []float64{1200, 1500, 1900, 2300} {
		x := tone(fs, f, 50)
		z := Hilbert(x)
		freq := InstantFreq(z, float32(fs))
		if len(freq) != len(x) {
			t.Fatalf("InstantFreq 长度 %d != 输入 %d", len(freq), len(x))
		}
		// 去掉 FIR 暂态两端各 2ms
		edge := fs * 2 / 1000
		core := freq[edge : len(freq)-edge]
		got := medianFloat(core)
		if math.Abs(got-f) > 5 {
			t.Errorf("freq=%.0f: 鉴频中位值 %.2f，误差 > 5Hz", f, got)
		}
	}
}

func TestBandpassRejectsOutfBand(t *testing.T) {
	fs := 8000
	// 带内 1500Hz 应通过，带外 400Hz 应被强烈衰减
	inBand := Bandpass(tone(fs, 1500, 100), float32(fs))
	outBand := Bandpass(tone(fs, 400, 100), float32(fs))

	rms := func(x []float32) float64 {
		var s float64
		edge := fs * 5 / 1000
		for _, v := range x[edge : len(x)-edge] {
			s += float64(v) * float64(v)
		}
		return math.Sqrt(s / float64(len(x)-2*edge))
	}
	if r := rms(inBand); r < 0.3 {
		t.Errorf("带内 1500Hz 衰减过大: rms=%.4f", r)
	}
	// 温和 IIR 带通（Q=0.57，低振铃优先）对 400Hz 抑制有限，
	// 断言相对衰减 ≥ 12dB（Q 权衡见 PLAN_M0 调试记录）
	if r := rms(outBand); r > 0.25*rms(inBand) {
		t.Errorf("带外 400Hz 抑制不足: out=%.4f in=%.4f (需 <0.25 倍)", r, rms(inBand))
	}
}

func TestResample48kTo8k(t *testing.T) {
	fsIn, fsOut := 48000, 8000
	x := tone(fsIn, 1500, 100)
	y := ResampleF32(x, fsIn, fsOut)
	if want := len(x) * fsOut / fsIn; absDiff(len(y), want) > 2 {
		t.Fatalf("输出长度 %d, 期望约 %d", len(y), want)
	}
	z := Hilbert(y)
	freq := InstantFreq(z, float32(fsOut))
	edge := fsOut * 5 / 1000
	core := freq[edge : len(freq)-edge]
	if got := medianFloat(core); math.Abs(got-1500) > 10 {
		t.Errorf("重采样后 1500Hz 鉴频中位值 %.2f", got)
	}
}

func TestSmoothPreservesMean(t *testing.T) {
	x := tone(8000, 1500, 20)
	y := Smooth(x, 1, 8000)
	if len(y) != len(x) {
		t.Fatalf("Smooth 改变了长度")
	}
	z := Hilbert(y)
	freq := InstantFreq(z, 8000)
	edge := 8000 * 2 / 1000
	if got := medianFloat(freq[edge : len(freq)-edge]); math.Abs(got-1500) > 5 {
		t.Errorf("平滑后鉴频偏移: %.2f", got)
	}
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
