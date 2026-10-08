package sstv

import "math"

// DetectVIS 在瞬时频率序列中定位 VIS 头并解码 VIS 码。
// 返回：VIS 码、起始位样本索引、是否成功。
// 头结构（TECH_SPEC §3.2）：1900×300ms → 1200×10 → 1900×300 → 1200×30(起始位)
// → 7 数据位 + 偶校验位（1100/1300，各 30ms）→ 1200×30(停止位)。
// 匹配策略：与模式表 VIS 汉明 ≤2 取最近；校验位仅参考不硬门限。
func DetectVIS(freq []float32, fs float32) (uint8, int, bool) {
	type run struct {
		freq       float64
		start, end int
	}
	ms := func(samples int) float64 { return float64(samples) / float64(fs) * 1000 }
	near := func(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

	// 粗分段：相邻样本均值漂移超过 120Hz 切段
	runs := make([]run, 0, 64)
	{
		acc, cnt := float64(freq[0]), 1
		cur := run{start: 0}
		for i := 1; i < len(freq); i++ {
			f := float64(freq[i])
			if math.Abs(f-acc/float64(cnt)) > 120 {
				cur.freq = acc / float64(cnt)
				cur.end = i
				runs = append(runs, cur)
				acc, cnt = 0, 0
				cur = run{start: i}
			}
			acc += f
			cnt++
		}
		cur.freq = acc / float64(cnt)
		cur.end = len(freq)
		runs = append(runs, cur)
	}

	median := func(seg []float32) float64 {
		s := append([]float32(nil), seg...)
		for i := 0; i < len(s); i++ {
			for j := i + 1; j < len(s); j++ {
				if s[j] < s[i] {
					s[i], s[j] = s[j], s[i]
				}
			}
		}
		return float64(s[len(s)/2])
	}

	for _, lead := range runs {
		if !near(lead.freq, FreqVisLead, 100) || ms(lead.end-lead.start) < 200 {
			continue
		}
		// 从引导段后找起始位：1200Hz 且 ≥20ms 的段
		var startIdx = -1
		for _, r := range runs {
			if r.start < lead.end {
				continue
			}
			if near(r.freq, FreqSync, 100) && ms(r.end-r.start) >= 20 {
				startIdx = r.start
				break
			}
		}
		if startIdx < 0 {
			continue
		}
		// 读 8 个 30ms 位（7 数据 + 校验），锚定起始位段起点
		bit0 := startIdx + int(fs*30/1000)
		vis := byte(0)
		dataOnes := 0
		good := true
		for b := 0; b < 7; b++ {
			segStart := bit0 + b*int(fs*30/1000)
			segEnd := segStart + int(fs*30/1000)
			if segEnd >= len(freq) {
				good = false
				break
			}
			mf := median(freq[segStart:segEnd])
			if near(mf, FreqVisBit1, 120) {
				vis |= 1 << b
				dataOnes++
			} else if !near(mf, FreqVisBit0, 120) {
				good = false
				break
			}
		}
		if !good {
			continue
		}
		// 偶校验位（第 8 位）：7 数据位 1 的个数为奇 → 校验=1
		if pStart := bit0 + 7*int(fs*30/1000); pStart+int(fs*30/1000) < len(freq) {
			mf := median(freq[pStart : pStart+int(fs*30/1000)])
			_ = mf // 仅参考，不硬门限（pySSTV/各发射机实现一致，但弱信号下不可靠）
		}
		return vis, startIdx, true
	}
	return 0, 0, false
}

// hamming 8bit 汉明距离。
func hamming(a, b uint8) int {
	d := a ^ b
	n := 0
	for d != 0 {
		n += int(d & 1)
		d >>= 1
	}
	return n
}
