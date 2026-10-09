package sstv

import (
	"sort"
)

// DetectVIS 在瞬时频率序列中定位 VIS 头并解码 VIS 码。
// 返回：VIS 码、起始位样本索引、是否成功。
// 头结构（TECH_SPEC §3.2）：1900×300ms → 1200×10 → 1900×300 → 1200×30(起始位)
// → 7 数据位 + 偶校验位（1100/1300，各 30ms）→ 1200×30(停止位)。
//
// 鲁棒策略：1ms 桶中位数网格 + 结构搜索。中位数对逐样本抖动稳健；
// 噪声下不再依赖连续 run 不被打碎。校验位仅参考不硬门限。
func DetectVIS(freq []float32, fs float32) (uint8, int, bool) {
	const bucketMs = 1.0
	nB := int(float64(len(freq)) / (float64(fs) * bucketMs / 1000))
	if nB < 900 {
		return 0, 0, false
	}
	// 1ms 桶中位数
	bucket := make([]float32, nB)
	samplesPerBucket := int(fs * bucketMs / 1000)
	for b := 0; b < nB; b++ {
		seg := freq[b*samplesPerBucket : (b+1)*samplesPerBucket]
		s := append([]float32(nil), seg...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		bucket[b] = s[len(s)/2]
	}
	// 结构搜索：分类扫描状态机（L=引导 1900±，S=同步/位 1100-1400，O=其他）。
	// 允许连续同类桶之间夹杂 ≤2 个异类桶（毛刺容差），不依赖平滑。
	// 结构分类前做 3 桶滑动平均（噪声下桶值 σ ~130Hz 会打碎长 run；
	// 3-MA 把 σ 降到 ~75，且 10ms break 在 48k/1ms 桶下仍可分辨）
	sm := make([]float32, nB)
	for i := 0; i < nB; i++ {
		lo, hi := i-1, i+2
		if lo < 0 {
			lo = 0
		}
		if hi > nB {
			hi = nB
		}
		var sum float64
		for k := lo; k < hi; k++ {
			sum += float64(bucket[k])
		}
		sm[i] = float32(sum / float64(hi-lo))
	}
	isLead := func(v float32) bool { return v >= 1650 && v <= 2200 }
	isSync := func(v float32) bool { return v >= 1050 && v <= 1450 }

	// 生成行程表：[class, start, end)（class: 0=L, 1=S）
	type run struct {
		cls        int
		start, end int
	}
	var runs []run
	{
		clsOf := func(v float32) int {
			if isLead(v) {
				return 0
			}
			if isSync(v) {
				return 1
			}
			return 2
		}
		cur := run{clsOf(sm[0]), 0, 1}
		for i := 1; i < nB; i++ {
			c := clsOf(sm[i])
			if c == cur.cls || (c == 2 && i-cur.end <= 2) { // O 容差并入
				cur.end = i + 1
				continue
			}
			// 换类：回退检查尾部 O 桶
			runs = append(runs, cur)
			cur = run{c, i, i + 1}
		}
		runs = append(runs, cur)
	}
	// 丢弃 O 行程，合并相邻同类
	var rs []run
	for _, r := range runs {
		if r.cls == 2 {
			continue
		}
		if len(rs) > 0 && rs[len(rs)-1].cls == r.cls {
			rs[len(rs)-1].end = r.end
			continue
		}
		rs = append(rs, r)
	}

	// 模式匹配：Lead(≥200) Sync(4..60) Lead(≥200) Sync(15..60)
	for i := 0; i+3 < len(rs); i++ {
		r1, r2, r3, r4 := rs[i], rs[i+1], rs[i+2], rs[i+3]
		if r1.cls != 0 || r2.cls != 1 || r3.cls != 0 || r4.cls != 1 {
			continue
		}
		if r1.end-r1.start < 200 || r3.end-r3.start < 200 {
			continue
		}
		if bl := r2.end - r2.start; bl < 4 || bl > 60 {
			continue
		}
		if sl := r4.end - r4.start; sl < 15 {
			// 起始位(30)与后续数据位(1100-1400)同属 S 类，会合并成长行程——
			// 只要求 ≥15 保证含完整起始位，不设上限
			continue
		}
		startBitBucket := r4.start
		// 读 7 数据位 + 校验位（每位 30ms，取中 24ms 的均值——
		// 高斯噪声下均值比中位数效率高 25%，SNR 10dB 时 σ≈13Hz）
		vis := byte(0)
		good := true
		for bit := 0; bit < 8; bit++ {
			// 窗口取每位中段 18ms（±6ms 余量）：结构搜索的 run 起点受
			// 3 桶 MA 平滑影响会早偏 1-2 桶，余量防止蹭到相邻位
			lo := startBitBucket + 30*(bit+1) + 6
			hi := startBitBucket + 30*(bit+2) - 6
			if hi >= nB {
				good = false
				break
			}
			var sum float64
			for k := lo; k < hi; k++ {
				sum += float64(bucket[k])
			}
			mean := sum / float64(hi-lo)
			if mean >= 1000 && mean < 1200 {
				if bit < 7 {
					vis |= 1 << bit
				}
			} else if mean < 1200 || mean > 1400 {
				good = false
				break
			}
		}
		if !good {
			continue
		}
		return vis, startBitBucket * samplesPerBucket, true
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
