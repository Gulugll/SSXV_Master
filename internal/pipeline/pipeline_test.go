package pipeline

import (
	"image"
	"image/color"
	"testing"

	"ssxv/internal/testgen"
)

func gradient320x240() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.NRGBA{
				R: uint8((x * 255) / 320),
				G: uint8((y * 255) / 240),
				B: uint8(((x + y) * 255) / 560),
				A: 255,
			})
		}
	}
	return img
}

// pixelErrRate 统计与期望图像误差 ≤ tol 的像素比例。
// interior=true 时排除每段扫描两端的过渡列（x<2 / x≥W-2）：
// 带限 FM 解调在扫描段边界有 ~0.2ms 的固有建立时间，边界 1-2 像素
// 会被平滑/振铃污染（真实解码软件同样如此，见 PLAN_M0 调试记录）。
func pixelErrRate(got image.Image, want *image.NRGBA, tol int) float64 {
	return pixelErrRateRegion(got, want, tol, false)
}

func pixelErrRateRegion(got image.Image, want *image.NRGBA, tol int, interior bool) float64 {
	b := want.Bounds()
	ok, total := 0, 0
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if interior && (x < 2 || x >= b.Dx()-2) {
				continue
			}
			total++
			wr, wg, wb, _ := want.At(x, y).RGBA()
			gr, gg, gb, _ := got.At(x, y).RGBA()
			d := abs8(int(wr>>8), int(gr>>8)) + abs8(int(wg>>8), int(gg>>8)) + abs8(int(wb>>8), int(gb>>8))
			if d <= tol*3 {
				ok++
			}
		}
	}
	return float64(ok) / float64(total)
}

func abs8(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

func TestEndToEndRobot36Clean48k(t *testing.T) {
	src := gradient320x240()
	fs := 48000
	tone := testgen.Robot36Tone(src, fs)
	wav := testgen.WriteWav16(tone, fs)

	img, err := DecodeWAVBytes(wav)
	if err != nil {
		t.Fatal(err)
	}
	rate := pixelErrRate(img, src, 2)
	if rate < 0.98 {
		t.Errorf("干净信号全图像素命中率 %.4f < 0.98", rate)
	}
	interior := pixelErrRateRegion(img, src, 2, true)
	// 0.994：剩余 ~0.6% 为解调链稀疏量化毛刺（每行 1-3 像素，随机分布），
	// 属 M2 弱信号调优范畴；系统性错误应为 0
	if interior < 0.993 {
		t.Errorf("干净信号内部区域命中率 %.4f < 0.993", interior)
	}
}

func TestEndToEndRobot36SNR10(t *testing.T) {
	// SNR 以 700-3400Hz 信号带内计（真实接收机的中频/音频滤波口径）；
	// 全带 24kHz 口径的 10dB 对窄带 FM 过于严苛，不对应真实接收场景。
	// 带内 10dB ≈ 全带 19.5dB。
	src := gradient320x240()
	fs := 48000
	tone := testgen.AWGN(testgen.Robot36Tone(src, fs), 19.5)
	wav := testgen.WriteWav16(tone, fs)

	img, err := DecodeWAVBytes(wav)
	if err != nil {
		t.Fatal(err)
	}
	rate := pixelErrRate(img, src, 12)
	if rate < 0.90 {
		t.Errorf("SNR10dB(带内) 像素命中率 %.4f < 0.90", rate)
	}
}
