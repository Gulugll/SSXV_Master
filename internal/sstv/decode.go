package sstv

import (
	"image"
	"image/color"
)

// DecodeMode 通用表驱动解码入口（全部模式）。
// visStart 为 VIS 起始位样本索引（DetectVIS 返回值）。
func DecodeMode(freq []float32, fs float32, mode ModeSpec, visStart int) *image.RGBA {
	return decodeGeneric(freq, fs, mode, visStart)
}

// DecodeRobot36 便捷入口（兼容 M0 调用方）。
// 同步策略：优先 syncMs@1200Hz 行同步锚点（质心法）；无同步（pySSTV 类
// 文件）回退到停止位后的标称周期。见 TECH_SPEC §3.4/§3.6。
func DecodeRobot36(freq []float32, fs float32, visStart int) *image.RGBA {
	return DecodeMode(freq, fs, Robot36(), visStart)
}

// decodeGeneric 表驱动逐行解码（全部模式共用）。
// 时间轴：anchor(c) = 第 c 个同步脉冲起点；cycle c 的段偏移自 anchor 累加。
func decodeGeneric(freq []float32, fs float32, mode ModeSpec, visStart int) *image.RGBA {
	W, H := mode.Width, mode.Height

	// VIS 头结束：起始位 + 30ms + 8位×30ms + 停止位 30ms = 300ms
	afterVIS := visStart + int(fs*0.300)

	// 1) 行同步锚点（质心法，对滤波滞后不敏感）
	anchors := findSyncPulses(freq, fs, afterVIS, mode.SyncMs, 1350)
	// 2) 无同步 → 标称周期回退
	if len(anchors) < 2 {
		anchors = anchors[:0]
		step := int(float32(mode.PeriodMs) / 1000 * fs)
		for k := 0; k < cyclesNeeded(mode, H); k++ {
			anchors = append(anchors, afterVIS+k*step)
		}
	}

	// 3) 逐 Cycle 解码：store[row][channel] = 像素
	store := make([]map[int][]int, H)
	for c, anchor := range anchors {
		cyc := mode.Cycles[c%len(mode.Cycles)]
		base := c * cyc.RowsAdv
		pos := anchor + int(float32(mode.SyncMs)/1000*fs)
		var cum float64
		for _, seg := range cyc.Segs {
			cum += seg.PorchMs
			start := pos + int(float32(cum)/1000*fs)
			vals := sampleSegment(freq, fs, start, seg.ScanMs, W)
			row := base + seg.RowDelta
			if 0 <= row && row < H {
				if store[row] == nil {
					store[row] = map[int][]int{}
				}
				store[row][seg.Channel] = vals
			}
			cum += seg.ScanMs
		}
	}

	// 4) 按色彩语义装配
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	switch mode.Color {
	case ColorRGB:
		assembleRGB(img, store, W, H)
	case ColorYCbCrPair:
		assembleYCbCrPair(img, store, W, H)
	case ColorYCbCrInterleave:
		assembleYCbCrInterleave(img, store, W, H)
	}
	return img
}

// cyclesNeeded 覆盖 H 行所需的锚点数。
func cyclesNeeded(mode ModeSpec, h int) int {
	n := 0
	rows := 0
	for rows < h {
		rows += mode.Cycles[n%len(mode.Cycles)].RowsAdv
		n++
	}
	return n
}

func assembleRGB(img *image.RGBA, store []map[int][]int, w, h int) {
	for y := 0; y < h; y++ {
		r, g, b := store[y][ChR], store[y][ChG], store[y][ChB]
		if r == nil || g == nil || b == nil {
			setGrayRow(img, w, y)
			continue
		}
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: u8(r[x]), G: u8(g[x]), B: u8(b[x]), A: 255})
		}
	}
}

func assembleYCbCrPair(img *image.RGBA, store []map[int][]int, w, h int) {
	for y := 0; y < h; y++ {
		base := store[(y/2)*2]
		yPx := store[y][ChY]
		var cb, cr []int
		if base != nil {
			cb, cr = base[ChCb], base[ChCr]
		}
		if yPx == nil || cb == nil || cr == nil {
			setGrayRow(img, w, y)
			continue
		}
		for x := 0; x < w; x++ {
			r, g, b := ycbcrToRGB(u8(yPx[x]), u8(cb[x]), u8(cr[x]))
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
}

func assembleYCbCrInterleave(img *image.RGBA, store []map[int][]int, w, h int) {
	for y := 0; y < h; y++ {
		if store[y] == nil || store[y][ChY] == nil {
			setGrayRow(img, w, y)
			continue
		}
		yPx := store[y][ChY]
		var cb, cr []int
		if y%2 == 0 {
			cr = store[y][ChCr]
			if y+1 < h {
				cb = store[y+1][ChCb]
			}
		} else {
			cb = store[y][ChCb]
			if y-1 >= 0 {
				cr = store[y-1][ChCr]
			}
		}
		for x := 0; x < w; x++ {
			cbV, crV := uint8(128), uint8(128)
			if cb != nil {
				cbV = u8(cb[x])
			}
			if cr != nil {
				crV = u8(cr[x])
			}
			r, g, b := ycbcrToRGB(u8(yPx[x]), cbV, crV)
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
}

func u8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// findSyncPulses 在 from 之后找同步脉冲（<maxFreq 连续 ≥minRatio×syncMs），
// 锚点用「频率凹陷质心」估计：对平滑/滤波造成的边缘滞后不敏感（TECH_SPEC §2.4）。
// 质心 c ≈ 脉冲真实中心，anchor = c - syncMs/2 - 校准偏置。
// 窗口限制在脉冲段 ±(syncMs/2) 内，防止窜入前置 1500Hz porch 或 VIS 位流。
func findSyncPulses(freq []float32, fs float32, from int, syncMs float64, maxFreq float64) []int {
	minLen := int(float32(syncMs) / 1000 * fs * 0.7)
	half := int(float32(syncMs) / 2000 * fs)
	var out []int
	i := from
	for i < len(freq) {
		if float64(freq[i]) < maxFreq {
			j := i
			for j < len(freq) && float64(freq[j]) < maxFreq {
				j++
			}
			if j-i >= minLen {
				lo := i - half
				if lo < from {
					lo = from
				}
				hi := j + half
				if hi > len(freq) {
					hi = len(freq)
				}
				var num, den float64
				for k := lo; k < hi; k++ {
					w := 1350 - float64(freq[k])
					if w <= 0 {
						continue
					}
					if w > 150 {
						w = 150
					}
					num += float64(k) * w
					den += w
				}
				if den > 0 {
					center := num / den
					// 校准偏置：因果滤波使 1350Hz 穿越滞后真实边缘约半个 FM
					// 过渡时间（带宽 2700Hz → fs/5400 样本，随采样率比例缩放；
					// 由干净语料 E2E 校准，见 docs/PLAN_M0.md 调试记录）。
					bias := float64(fs) / 5400
					out = append(out, int(center-float64(syncMs)/2000*float64(fs)-bias+0.5))
				} else {
					out = append(out, i)
				}
			}
			i = j
		} else {
			i++
		}
	}
	return out
}

// DebugAnchors 导出锚点检测供流水线调试（测试用）。
func DebugAnchors(freq []float32, fs float32, from int, syncMs float64) []int {
	return findSyncPulses(freq, fs, from, syncMs, 1350)
}

// sampleSegment 在 [start, start+totalMs) 内取 n 个像素。
// 每像素对窗内中心 60% 区间的瞬时频率取平均：抗噪且抑制过渡沿毛刺；
// 窗口钳制在扫描段内（两端各留 0.5 像素，避开段间过渡区）。
func sampleSegment(freq []float32, fs float32, start int, totalMs float64, n int) []int {
	out := make([]int, n)
	step := totalMs / float64(n)
	winStart := float64(start) + step*0.1*float64(fs)/1000
	winEnd := float64(start) + (totalMs-step*0.1)*float64(fs)/1000
	for k := 0; k < n; k++ {
		lo := float64(start) + (float64(k)+0.1)*step*float64(fs)/1000
		hi := float64(start) + (float64(k)+0.9)*step*float64(fs)/1000
		if lo < winStart {
			lo = winStart
		}
		if hi > winEnd {
			hi = winEnd
		}
		if hi <= lo {
			hi = lo + 1
		}
		var sum float64
		cnt := 0
		for i := int(lo); i <= int(hi) && i < len(freq); i++ {
			sum += float64(freq[i])
			cnt++
		}
		if cnt == 0 {
			out[k] = 128
			continue
		}
		out[k] = FreqToByte(sum / float64(cnt))
	}
	return out
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
