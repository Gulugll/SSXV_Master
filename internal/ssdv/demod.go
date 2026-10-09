package ssdv

import "math"

// DemodBPSK 200bd BPSK 音频解调（TECH_SPEC §4.5）。
//
// 链路：
//  1. 平方环粗频估：BPSK 信号平方消去调制 → 2·fc 处连续单音，
//     Goertzel 扫描取峰值 → CFO（分辨率 5Hz，残余 ≤2.5Hz）；
//  2. 固定混频 + 盒式匹配滤波（矩形脉冲最优 MF）→ 眼图峰值搜索定时
//     （闭环语料无采样钟漂，固定定时；Gardner 环留待 M4 实时链路）；
//  3. Costas 内联混频（判决引导，跟踪残余相位/频差）+ 硬判决；
//  4. 位流 MSB 先行 → 字节；极性模糊（Costas 180° 锁定）用 ScanPackets 试探。
func DemodBPSK(x []float32, fs int) []byte {
	sps := fs / 200
	fc := 1500.0

	// 1) 平方环 CFO 估计
	cfo := estimateCFO(x, fs, fc)

	// 2) 定时：固定混频 + MF + 眼图搜索（只看前 1s）
	scanLen := fs + sps
	if scanLen > len(x) {
		scanLen = len(x)
	}
	head := make([]float32, scanLen)
	w0 := 2 * math.Pi * (fc + cfo) / float64(fs)
	var ph float64
	for n := 0; n < scanLen; n++ {
		head[n] = x[n] * float32(math.Cos(ph))
		ph += w0
	}
	mfHead := boxcar(head, sps)
	bestP := timingSearch(mfHead, sps, fs)

	// 3) Costas 内联混频 + 判决
	// 增益权衡：平方环已把 CFO 估到 ±2.5Hz，环路只需跟踪慢漂；
	// 大增益会让转换沿渗漏的系统性误差把 wCorr 推成随机游走
	// （实测 17 符号内走到 -30Hz 导致相位螺旋脱锁，M3 调试记录）。
	const (
		alpha = 0.02
		beta  = 0.0001
	)
	var bits []byte
	var cur byte
	var nbit uint
	theta := 0.0
	wCorr := 0.0 // 残余频差（rad/sample）
	// 盒式运行和
	bufI := make([]float32, sps)
	bufQ := make([]float32, sps)
	var sumI, sumQ float64
	ptr := 0
	for n, v := range x {
		i := float64(v) * math.Cos(theta)
		q := -float64(v) * math.Sin(theta)
		sumI += i - float64(bufI[ptr])
		sumQ += q - float64(bufQ[ptr])
		bufI[ptr] = float32(i)
		bufQ[ptr] = float32(q)
		ptr++
		if ptr == sps {
			ptr = 0
		}
		// 盒式窗填满后才判决（首个 strobe 可能落在部分窗上，
		// 会引入伪位导致整帧字节错位——M3 调试记录）
		if n >= sps-1 && n >= bestP && (n-bestP)%sps == 0 {
			In := sumI / float64(sps)
			Qn := sumQ / float64(sps)
			bit := byte(0)
			if In >= 0 {
				bit = 1
			}
			cur = cur<<1 | bit
			nbit++
			if nbit == 8 {
				bits = append(bits, cur)
				cur, nbit = 0, 0
			}
			// 判决引导 Costas
			e := Qn
			if In < 0 {
				e = -Qn
			}
			theta += alpha * e
			wCorr += beta * e
		}
		theta += w0 + wCorr
		if theta > 2*math.Pi {
			theta -= 2 * math.Pi
		} else if theta < -2*math.Pi {
			theta += 2 * math.Pi
		}
	}

	// bits 在 strobe 循环中已按 MSB 先行聚成字节（勿再二次打包）
	data := bits

	// 4) 极性试探（Costas 可能锁到 180°）
	if len(ScanPackets(data)) == 0 {
		inv := make([]byte, len(data))
		for i, b := range data {
			inv[i] = ^b
		}
		if len(ScanPackets(inv)) > 0 {
			return inv
		}
	}
	return data
}

// estimateCFO 平方环：y=x² 消去 BPSK 调制，2·fc 处出现连续单音
// （频率 2(fc+Δ)），Goertzel 两级扫描取峰 → Δ。
// 粗扫 ±400Hz 步进 5Hz，细扫峰值 ±6Hz 步进 0.5Hz——残余 CFO ≤0.25Hz，
// 否则慢速 Costas 的 pull-in 期间相位会漂过判决门限（M3 调试记录）。
func estimateCFO(x []float32, fs int, fc float64) float64 {
	n := fs // 1s 窗口
	if n > len(x) {
		n = len(x)
	}
	y := make([]float32, n)
	for i := 0; i < n; i++ {
		y[i] = x[i] * x[i]
	}
	goertzel := func(f float64) float64 {
		w := 2 * math.Pi * f / float64(fs)
		coef := 2 * math.Cos(w)
		var s1, s2 float64
		for _, v := range y {
			s0 := float64(v) + coef*s1 - s2
			s2, s1 = s1, s0
		}
		return s1*s1 + s2*s2 - coef*s1*s2
	}
	// 粗扫
	bestP := -1.0
	peak := 2 * fc
	for f := 2*fc - 400; f <= 2*fc+400; f += 5 {
		if p := goertzel(f); p > bestP {
			bestP = p
			peak = f
		}
	}
	// 细扫
	for f := peak - 6; f <= peak+6; f += 0.5 {
		if p := goertzel(f); p > bestP {
			bestP = p
			peak = f
		}
	}
	return (peak - 2*fc) / 2
}

// boxcar 宽度 w 的滑动平均（边界处截断窗口）。
func boxcar(x []float32, w int) []float32 {
	out := make([]float32, len(x))
	var sum float64
	lo := 0
	for n, v := range x {
		sum += float64(v)
		if n >= w {
			sum -= float64(x[lo])
			lo++
		}
		cnt := n - lo + 1
		out[n] = float32(sum / float64(cnt))
	}
	return out
}

// timingSearch 眼图峰值定时：选 |MF| 均值最大的抽取相位。
func timingSearch(mfI []float32, sps int, scanLen int) int {
	if scanLen > len(mfI) {
		scanLen = len(mfI)
	}
	bestP, bestScore := 0, -1.0
	for p := 0; p < sps; p++ {
		var score float64
		for n := p; n < scanLen; n += sps {
			v := float64(mfI[n])
			if v < 0 {
				v = -v
			}
			score += v
		}
		if score > bestScore {
			bestScore, bestP = score, p
		}
	}
	return bestP
}
