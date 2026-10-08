// ssxv-sample 生成 M0 演示语料：Robot36 测试图 → WAV + 期望 PNG。
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"

	"ssxv/internal/testgen"
)

func main() {
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

	wav, _ := os.Create("sample_robot36.wav")
	wav.Write(testgen.WriteWav16(testgen.Robot36Tone(img, 48000), 48000))
	wav.Close()
	fmt.Println("生成: sample_robot36.wav (48kHz) + sample_want.png")
}
