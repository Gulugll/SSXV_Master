// ssxv-sample 生成 M0 演示语料：Robot36 测试图 → WAV + 期望 PNG。
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"

	"ssxv/internal/sstv"
	"ssxv/internal/testgen"
)

func main() {
	modeName := flag.String("mode", "Robot36", "模式名")
	flag.Parse()

	modes := map[string]sstv.ModeSpec{
		"Robot36": sstv.Robot36(), "MartinM1": sstv.MartinM1(), "MartinM2": sstv.MartinM2(),
		"ScottieS1": sstv.ScottieS1(), "ScottieS2": sstv.ScottieS2(), "ScottieDX": sstv.ScottieDX(),
		"PD90": sstv.PD90(), "PD120": sstv.PD120(), "PD160": sstv.PD160(),
		"PD180": sstv.PD180(), "PD240": sstv.PD240(), "PD290": sstv.PD290(),
		"WraaseSC2180": sstv.WraaseSC2180(), "WraaseSC2120": sstv.WraaseSC2120(),
		"PasokonP3": sstv.PasokonP3(), "PasokonP5": sstv.PasokonP5(), "PasokonP7": sstv.PasokonP7(),
	}
	mode, ok := modes[*modeName]
	if !ok {
		fmt.Fprintln(os.Stderr, "未知模式:", *modeName)
		os.Exit(1)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.NRGBA{
				R: uint8((x * 255) / 320),
				G: uint8((y * 255) / 240),
				B: uint8(((x + y) % 128) * 2),
				A: 255,
			})
		}
	}
	pngWant, _ := os.Create("sample_want.png")
	png.Encode(pngWant, img)
	pngWant.Close()

	wav, _ := os.Create("sample_" + *modeName + ".wav")
	wav.Write(testgen.WriteWav16(testgen.ModeTone(mode, img, 48000), 48000))
	wav.Close()
	fmt.Printf("生成: sample_%s.wav (%dHz, %dx%d) + sample_want.png\n", *modeName, 48000, mode.Width, mode.Height)
}
