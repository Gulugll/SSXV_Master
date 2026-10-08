package sstv

import (
	"image"
	"image/color"
	"math"
)

// DecodeMode 按模式规格分发的通用入口（M0 仅 Robot36 有实现）。
func DecodeMode(freq []float32, fs float32, mode ModeSpec, visStart int) *image.RGBA {
	switch mode.Name {
	case "Robot36":
		return decodeWithSpec(freq, fs, mode, visStart)
	default:
		return nil
	}
}

// DecodeRobot36 便捷入口：解码 Robot36（visStart 为 VIS 起始位样本索引）。
// 同步策略：优先 9ms@1200Hz 行同步锚点；无同步（pySSTV 类文件）回退到
// 停止位后的标称行距（150ms/行）。见 TECH_SPEC §3.4/3.6。
func DecodeRobot36(freq []float32, fs float32, visStart int) *image.RGBA {
	return decodeWithSpec(freq, fs, Robot36(), visStart)
}

// decodeWithSpec 表驱动逐行解码（Robot36 的通用实现路径）。
func decodeWithSpec(freq []float32, fs float32, mode ModeSpec, visStart int) *image.RGBA {
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

// findSyncPulses 在 from 之后找同步脉冲（<maxFreq 连续 ≥minRatio×syncMs），
// 锚点用「频率凹陷质心」估计：对平滑/滤波造成的边缘滞后不敏感（TECH_SPEC §2.4）。
// 质心 c ≈ 脉冲真实中心，anchor = c - syncMs/2。
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

// sampleSegment 在 [start, start+totalMs) 内取 n 个像素。
// 每像素对窗内中心 60% 区间的瞬时频率取平均：抗噪且抑制过渡沿毛刺；
// 窗口钳制在扫描段内（两端各留 0.5 像素，避开段间过渡区）。
func sampleSegment(freq []float32, fs float32, start int, totalMs float64, n int) []int {
	out := make([]int, n)
	step := totalMs / float64(n)
	winStart := float64(start) + step*0.2*float64(fs)/1000
	winEnd := float64(start) + (totalMs-step*0.2)*float64(fs)/1000
	for k := 0; k < n; k++ {
		lo := float64(start) + (float64(k)+0.2)*step*float64(fs)/1000
		hi := float64(start) + (float64(k)+0.8)*step*float64(fs)/1000
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

// DebugAnchors 导出锚点检测供流水线调试（测试用）。
func DebugAnchors(freq []float32, fs float32, from int, syncMs float64) []int {
	return findSyncPulses(freq, fs, from, syncMs, 1350)
}
