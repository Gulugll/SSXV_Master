package sstv

import (
	"image"
	"image/color"
	"math"
)

// DecodeRobot36 从瞬时频率序列解码 Robot36 图像。
// visStart 为 VIS 起始位样本索引（DetectVIS 返回值）。
// 同步策略：优先 9ms@1200Hz 行同步锚点；无同步（pySSTV 类文件）回退到
// 停止位后的标称行距（150ms/行）。见 TECH_SPEC §3.4/3.6。
func DecodeRobot36(freq []float32, fs float32, visStart int) *image.RGBA {
	mode := Robot36()
	W, H := mode.Width, mode.Height

	// VIS 头结束：起始位 + 30ms + 8位×30ms + 停止位 30ms = 300ms
	afterVIS := visStart + int(fs*0.300)

	// 1) 行同步锚点
	anchors := findSyncPulses(freq, fs, afterVIS, mode.Lines[0].SyncMs, 1350)
	// 2) 无同步 → 标称行距回退
	nominal := nominalLineSamples(mode, fs)
	if len(anchors) < 2 {
		anchors = anchors[:0]
		for k := 0; k < H; k++ {
			anchors = append(anchors, afterVIS+k*nominal)
		}
	}

	yArr := make([][]int, H)
	cArr := make([][]int, H)
	lineOf := make([]int, H) // 记录每行原始行号（奇偶）
	for i := range lineOf {
		lineOf[i] = -1
	}

	for l := 0; l < H && l < len(anchors); l++ {
		spec := mode.Lines[l%len(mode.Lines)]
		pos := anchors[l] + int(float32(spec.SyncMs)/1000*fs)
		row := make([]int, W)
		var cumMs float64 // 通道窗为累加偏移：锚点+同步+Σ(前序 porch+时长)+本 porch
		for ci, ch := range spec.Channels {
			cumMs += ch.PreMs
			start := pos + int(float32(cumMs)/1000*fs)
			vals := sampleSegment(freq, fs, start, ch.TotalMs, ch.Pixels)
			if ci == 0 {
				copy(row, vals)
			} else {
				cArr[l] = vals
			}
			cumMs += ch.TotalMs
		}
		yArr[l] = row
		lineOf[l] = l
	}

	// 3) 装配：偶行 Y+Cr，奇行 Y+Cb；色度由相邻行共享
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for l := 0; l < H; l++ {
		if yArr[l] == nil {
			setGrayRow(img, W, l)
			continue
		}
		var cb, cr []int
		if l%2 == 0 {
			cr = cArr[l]
			if l+1 < H {
				cb = cArr[l+1]
			}
		} else {
			cb = cArr[l]
			if l-1 >= 0 {
				cr = cArr[l-1]
			}
		}
		for x := 0; x < W; x++ {
			y := uint8(yArr[l][x])
			cbV := uint8(128)
			crV := uint8(128)
			if cb != nil {
				cbV = uint8(cb[x])
			}
			if cr != nil {
				crV = uint8(cr[x])
			}
			r, g, b := ycbcrToRGB(y, cbV, crV)
			img.SetRGBA(x, l, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	return img
}

// findSyncPulses 在 from 之后找连续低于 maxFreq、时长 ≥ minRatio×syncMs 的脉冲段起点。
func findSyncPulses(freq []float32, fs float32, from int, syncMs float64, maxFreq float64) []int {
	minLen := int(float32(syncMs) / 1000 * fs * 0.7)
	var out []int
	i := from
	for i < len(freq) {
		if float64(freq[i]) < maxFreq {
			j := i
			for j < len(freq) && float64(freq[j]) < maxFreq {
				j++
			}
			if j-i >= minLen {
				out = append(out, i)
			}
			i = j
		} else {
			i++
		}
	}
	return out
}

// sampleSegment 在 [start, start+totalMs) 内取 n 个像素（每像素窗中点采样）。
func sampleSegment(freq []float32, fs float32, start int, totalMs float64, n int) []int {
	out := make([]int, n)
	step := totalMs / float64(n)
	for k := 0; k < n; k++ {
		t := float64(start) + (float64(k)+0.5)*step*float64(fs)/1000
		idx := int(t)
		if idx >= len(freq) {
			out[k] = 128
			continue
		}
		out[k] = FreqToByte(float64(freq[idx]))
	}
	return out
}

// nominalLineSamples 计算标称行时长（样本）：行结构序列总时长 / 行结构数。
func nominalLineSamples(mode ModeSpec, fs float32) int {
	var totalMs float64
	for _, ls := range mode.Lines {
		var lineMs float64
		lineMs += ls.SyncMs + ls.AfterSync
		for _, ch := range ls.Channels {
			lineMs += ch.PreMs + ch.TotalMs
		}
		totalMs += lineMs
	}
	avgMs := totalMs / float64(len(mode.Lines))
	return int(float32(avgMs) / 1000 * fs)
}

func setGrayRow(img *image.RGBA, w, y int) {
	for x := 0; x < w; x++ {
		img.SetRGBA(x, y, color.RGBA{R: 128, G: 128, B: 128, A: 255})
	}
}

// ycbcrToRGB JPEG full-range BT.601（TECH_SPEC §3.5）。
func ycbcrToRGB(y, cb, cr uint8) (uint8, uint8, uint8) {
	yy := float64(y)
	cbb := float64(cb) - 128
	crb := float64(cr) - 128
	r := yy + 1.402*crb
	g := yy - 0.344136*cbb - 0.714136*crb
	b := yy + 1.772*cbb
	clamp := func(v float64) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v + 0.5)
	}
	return clamp(r), clamp(g), clamp(b)
}

var _ = math.Pi
