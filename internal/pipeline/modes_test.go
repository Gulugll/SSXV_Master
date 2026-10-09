package pipeline

import (
	"image"
	"image/color"
	"testing"

	"ssxv/internal/sstv"
	"ssxv/internal/testgen"
)

// pairedGradient 上下成对的渐变图（PD 系色度为两行平均，
// 行成对时平均无歧义，可直接与源图对比）。
func pairedGradient(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		yy := (y / 2 * 255) / (h / 2)
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{
				R: uint8((x * 255) / w),
				G: uint8(yy),
				B: uint8(((x + yy) % 128) * 2),
				A: 255,
			})
		}
	}
	return img
}

func gradientFull(w, h int) *image.NRGBA {
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

func TestModeRoundTripAll(t *testing.T) {
	modes := []sstv.ModeSpec{
		sstv.Robot36(), sstv.MartinM1(), sstv.MartinM2(),
		sstv.ScottieS1(), sstv.ScottieS2(), sstv.ScottieDX(),
		sstv.PD90(), sstv.PD120(), sstv.PD160(), sstv.PD180(), sstv.PD240(), sstv.PD290(),
		sstv.WraaseSC2180(), sstv.WraaseSC2120(),
		sstv.PasokonP3(), sstv.PasokonP5(), sstv.PasokonP7(),
	}
	for _, mode := range modes {
		mode := mode
		t.Run(mode.Name, func(t *testing.T) {
			var src *image.NRGBA
			if mode.Color == sstv.ColorYCbCrPair {
				src = pairedGradient(mode.Width, mode.Height)
			} else {
				// 平滑渐变（避免人为像素级跳变；真实图像不存在 254 级相邻跳变）
				src = gradientFull(mode.Width, mode.Height)
			}
			const fs = 48000
			tone := testgen.ModeTone(mode, src, fs)
			wav := testgen.WriteWav16(tone, fs)

			img, err := DecodeWAVBytes(wav)
			if err != nil {
				t.Fatal(err)
			}
			if got := img.Bounds().Dx(); got != mode.Width {
				t.Fatalf("宽度 %d, 期望 %d", got, mode.Width)
			}
			full := pixelErrRate(img, src, 2)
			interior := pixelErrRateRegion(img, src, 2, true)
			// 阈值按色彩语义分级：RGB 模式每行 3 个扫描边界（边界软化点
			// 比 Robot36 的 2 个多 50%）；PD 系 Cycle 内无 porch 直转。
			// 边界 1-2px 软化为带限解调固有行为（真实解码软件一致）。
			minInterior, minFull := 0.99, 0.95
			if mode.Color != sstv.ColorYCbCrInterleave {
				// RGB 每行 3 个扫描边界、PD 系 Cycle 内无 porch 直转——
				// 边界软化点多于 Robot36，阈值相应放宽（固有行为，非缺陷）
				minInterior = 0.96
			}
			if mode.Name == "Robot36" {
				minInterior = 0.985
			}
			if interior < minInterior {
				t.Errorf("内部命中率 %.4f < %.2f (全图 %.4f)", interior, minInterior, full)
			}
			if full < minFull {
				t.Errorf("全图命中率 %.4f < %.2f", full, minFull)
			}
		})
	}
}

// TestVISNoAmbiguity 干净信号下 VIS 判决唯一性：
// 任意两模式的 VIS 码若汉明距离 ≤2，弱信号下存在互混风险（协议固有），
// 但此测试确保精确 VIS 下总是命中正确模式。
func TestVISNoAmbiguity(t *testing.T) {
	modes := []sstv.ModeSpec{
		sstv.Robot36(), sstv.MartinM1(), sstv.MartinM2(),
		sstv.ScottieS1(), sstv.ScottieS2(), sstv.ScottieDX(),
		sstv.PD90(), sstv.PD120(), sstv.PD160(), sstv.PD180(), sstv.PD240(), sstv.PD290(),
		sstv.WraaseSC2180(), sstv.WraaseSC2120(),
		sstv.PasokonP3(), sstv.PasokonP5(), sstv.PasokonP7(),
	}
	for _, m := range modes {
		got, ok := sstv.ModeByVIS(m.VIS)
		if !ok || got.Name != m.Name {
			t.Errorf("VIS %#02x → %v (ok=%v), 期望 %s", m.VIS, got.Name, ok, m.Name)
		}
	}
}
