package testgen

import (
	"image"

	"ssxv/internal/sstv"
)

// channelRows 从图像提取每行各语义通道的亮度值（0-255）。
// 对 ColorYCbCrPair：色度通道为两行平均。
func channelRows(mode sstv.ModeSpec, img *image.NRGBA) map[int][][]int {
	W, H := mode.Width, mode.Height
	yArr := make([][]float64, H)
	cbArr := make([][]float64, H)
	crArr := make([][]float64, H)
	rArr := make([][]float64, H)
	gArr := make([][]float64, H)
	bArr := make([][]float64, H)
	for y := 0; y < H; y++ {
		yArr[y] = make([]float64, W)
		cbArr[y] = make([]float64, W)
		crArr[y] = make([]float64, W)
		rArr[y] = make([]float64, W)
		gArr[y] = make([]float64, W)
		bArr[y] = make([]float64, W)
		for x := 0; x < W; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			r8, g8, b8 := float64(r>>8), float64(g>>8), float64(b>>8)
			rArr[y][x], gArr[y][x], bArr[y][x] = r8, g8, b8
			yy := 0.299*r8 + 0.587*g8 + 0.114*b8
			yArr[y][x] = yy
			cbArr[y][x] = (b8-yy)*0.564 + 128
			crArr[y][x] = (r8-yy)*0.713 + 128
		}
	}

	chans := map[int][][]int{} // channel → row → pixels（按行号索引）
	for ch := 0; ch < 3; ch++ {
		chans[ch] = make([][]int, H)
	}
	addRow := func(ch, y int, vals []float64) {
		out := make([]int, W)
		for x, v := range vals {
			out[x] = int(v + 0.5)
		}
		chans[ch][y] = out
	}

	switch mode.Color {
	case sstv.ColorRGB:
		for y := 0; y < H; y++ {
			addRow(sstv.ChR, y, rArr[y])
			addRow(sstv.ChG, y, gArr[y])
			addRow(sstv.ChB, y, bArr[y])
		}
	case sstv.ColorYCbCrPair:
		for y := 0; y < H; y++ {
			addRow(sstv.ChY, y, yArr[y])
		}
		// 色度为两行平均，行号取对首
		for y := 0; y < H; y += 2 {
			cr := make([]float64, W)
			cb := make([]float64, W)
			y2 := y + 1
			if y2 >= H {
				y2 = y
			}
			for x := 0; x < W; x++ {
				cr[x] = (crArr[y][x] + crArr[y2][x]) / 2
				cb[x] = (cbArr[y][x] + cbArr[y2][x]) / 2
			}
			addRow(sstv.ChCr, y, cr)
			addRow(sstv.ChCb, y, cb)
		}
	case sstv.ColorYCbCrInterleave:
		for y := 0; y < H; y++ {
			addRow(sstv.ChY, y, yArr[y])
		}
		for y := 0; y < H; y++ {
			addRow(sstv.ChCr, y, crArr[y])
			addRow(sstv.ChCb, y, cbArr[y])
		}
	}
	return chans
}

// EncodeMode 通用模式编码器：按 ModeSpec 表生成段序列（含 VIS 头）。
// 与解码共用同一份时序表，保证闭环一致性。
func EncodeMode(mode sstv.ModeSpec, img *image.NRGBA, fs int) []Seg {
	chans := channelRows(mode, img)
	b := &segBuilder{fs: fs}
	b.addVis(mode.VIS)

	rowCursor := 0 // 已发射的行数
	for cycle, emitted := 0, 0; rowCursor < mode.Height; cycle++ {
		cyc := mode.Cycles[cycle%len(mode.Cycles)]
		b.add(sstv.FreqSync, mode.SyncMs)
		for _, seg := range cyc.Segs {
			b.add(sstv.FreqBlack, seg.PorchMs)
			row := rowCursor + seg.RowDelta
			if row >= mode.Height {
				// 尾部越界段（如 Scottie 末行 G/B 的 RowDelta=+1）：
				// 仍需发射（真实发射端会发），用黑电平填充
				b.add(sstv.FreqBlack, seg.ScanMs)
				continue
			}
			pix := chans[seg.Channel][row]
			px := seg.ScanMs / float64(mode.Width)
			for x := 0; x < mode.Width; x++ {
				b.add(ByteToFreq(pix[x]), px)
			}
		}
		rowCursor += cyc.RowsAdv
		emitted++
		if emitted > mode.Height+2 {
			break // 防御
		}
	}
	return b.done()
}

// ModeTone 编码并生成波形。
func ModeTone(mode sstv.ModeSpec, img *image.NRGBA, fs int) []float32 {
	return GenTone(EncodeMode(mode, img, fs), fs).samples
}
