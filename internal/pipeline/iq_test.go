package pipeline

import (
	"math"
	"math/rand"
	"testing"

	"ssxv/internal/dsp"
	"ssxv/internal/testgen"
)

// instantFreq 便捷包装。
func instantFreq(z []complex64, fs int) []float32 { return dsp.InstantFreq(z, float32(fs)) }

// addComplexNoise 复高斯白噪声（SNR 相对信号均方功率）。
func addComplexNoise(z []complex64, snrDb float64, seed int64) {
	var sp float64
	for _, v := range z {
		re, im := float64(real(v)), float64(imag(v))
		sp += re*re + im*im
	}
	np := sp / float64(len(z)) / math.Pow(10, snrDb/10)
	sigma := math.Sqrt(np / 2)
	rng := rand.New(rand.NewSource(seed))
	for i := range z {
		z[i] += complex(float32(rng.NormFloat64()*sigma), float32(rng.NormFloat64()*sigma))
	}
}

// TestIQRoundTripAllFormats IQ 链路（M4-2）：段序列 → IF 偏移复基带（3 种裸格式）
// → ParseIQ → 自动估 IF → 解码，与源图像素对比。 Robot36 全段含 VIS/同步，
// 频谱占 [1100,2300]+IF，分位估计应准确到数十 Hz。
func TestIQRoundTripAllFormats(t *testing.T) {
	const fs = 48000
	const ifHz = 6000.0
	src := pairedGradient(320, 240)
	segs := testgen.EncodeRobot36(src, fs)

	for _, tc := range []struct {
		name   string
		format testgen.IQFormat
		str    string
	}{
		{"CU8", testgen.IQCU8, "cu8"},
		{"CS16", testgen.IQCS16, "cs16"},
		{"CF32", testgen.IQCF32, "cf32"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := testgen.IQUpconvertSegs(segs, fs, ifHz, tc.format)
			z, err := ParseIQ(raw, tc.str)
			if err != nil {
				t.Fatal(err)
			}
			res, err := DecodeIQResult(z, fs, 0)
			if err != nil {
				t.Fatal(err)
			}
			if res.Mode != "Robot36 (IQ)" {
				t.Fatalf("模式 %q, 期望 Robot36 (IQ)", res.Mode)
			}
			full := pixelErrRate(res.Image, src, 2)
			interior := pixelErrRateRegion(res.Image, src, 2, true)
			// IQ 路径阈值低于实链路（Robot36 0.985/0.95）：合成 IQ 为瞬时频率
			// 过渡 + EMA 平滑，与实链路带限过渡形状不同，扫描边界像素质差异
			// 属固有行为（干净信号解码图像肉眼无差异）。
			if interior < 0.94 || full < 0.93 {
				t.Errorf("内部 %.4f / 全图 %.4f 低于阈值", interior, full)
			}
		})
	}
}

// TestIQEstimateIF 自动估 IF 精度：干净信号下应准确到 ±50Hz。
func TestIQEstimateIF(t *testing.T) {
	const fs = 48000
	src := pairedGradient(320, 240)
	segs := testgen.EncodeRobot36(src, fs)
	for _, ifHz := range []float64{0, 3000, 6000, 12000} {
		raw := testgen.IQUpconvertSegs(segs, fs, ifHz, testgen.IQCS16)
		z, _ := ParseIQ(raw, "cs16")
		freq := instantFreq(z, fs)
		got := float64(EstimateIF(freq))
		if d := got - ifHz; d > 50 || d < -50 {
			t.Errorf("IF=%v: 估计 %.1f Hz（偏差 %.1f 超 ±50）", ifHz, got, d)
		}
	}
}

// TestIQNoiseMargin 噪声余量：与实链路语料同口径（带内 SNR10 = 全带 19.5dB，
// 见 pipeline_test.TestEndToEndRobot36SNR10），tol 与阈值一致。
func TestIQNoiseMargin(t *testing.T) {
	const fs = 48000
	src := pairedGradient(320, 240)
	segs := testgen.EncodeRobot36(src, fs)
	raw := testgen.IQUpconvertSegs(segs, fs, 6000, testgen.IQCS16)
	z, _ := ParseIQ(raw, "cs16")
	addComplexNoise(z, 19.5, 7)
	res, err := DecodeIQResult(z, fs, 0)
	if err != nil {
		t.Fatal(err)
	}
	full := pixelErrRate(res.Image, src, 12)
	if full < 0.90 {
		t.Errorf("带内 SNR10 全图命中率 %.4f < 0.90", full)
	}
}

// TestIQBadFormat 非法输入快速失败。
func TestIQBadFormat(t *testing.T) {
	if _, err := ParseIQ([]byte{1, 2, 3}, "cu8"); err == nil {
		t.Error("奇数长度 CU8 应报错")
	}
	if _, err := ParseIQ(nil, "wav9"); err == nil {
		t.Error("未知格式应报错")
	}
	// 纯直流（无信号）应报未识别 VIS
	z, _ := ParseIQ([]byte{128, 128, 128, 128}, "cu8")
	if _, err := DecodeIQResult(z, 48000, 0); err == nil {
		t.Error("无信号应报未识别 VIS")
	}
}
