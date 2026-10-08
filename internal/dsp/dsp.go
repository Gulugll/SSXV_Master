// Package dsp 提供音频信号处理原语：重采样、带通滤波、希尔伯特变换与 FM 鉴频。
// 规格见 docs/TECH_SPEC.md §2。
package dsp

import (
	"math"
)

// sincWindow 返回长度 2n+1 的窗口 sinc 低通 FIR（Hamming 窗），截止频率 fcHz。
func sincWindow(fcHz float64, n int) []float32 {
	const pi = math.Pi
	h := make([]float64, 2*n+1)
	fc := fcHz
	var sum float64
	for i := 0; i <= 2*n; i++ {
		m := float64(i - n)
		var v float64
		if m == 0 {
			v = 2 * fc
		} else {
			v = math.Sin(2*pi*fc*m) / (pi * m)
		}
		w := 0.54 - 0.46*math.Cos(2*pi*float64(i)/float64(2*n)) // Hamming
		h[i] = v * w
		sum += h[i]
	}
	out := make([]float32, len(h))
	for i, v := range h {
		out[i] = float32(v / sum)
	}
	return out
}

func convolveValid(x []float32, k []float32) []float32 {
	if len(x) < len(k) {
		return nil
	}
	out := make([]float32, len(x)-len(k)+1)
	for i := range out {
		var s float32
		for j, kv := range k {
			s += x[i+j] * kv
		}
		out[i] = s
	}
	return out
}

// ResampleF32 将信号从 from Hz 重采样到 to Hz。
// 先做抗混叠 FIR（零填充卷积，输出与输入等长）再线性插值；延迟已补偿。
func ResampleF32(in []float32, from, to int) []float32 {
	if from == to || len(in) == 0 {
		return append([]float32(nil), in...)
	}
	ratio := float64(to) / float64(from)
	// 抗混叠低通：截止取输入/输出奈奎斯特中较小者的 0.9（按输入率表示）
	fc := 0.45 * math.Min(float64(from), float64(to))
	taps := sincWindow(fc, 48)
	filtered := convolveFullCompensated(in, taps)

	outLen := int(float64(len(in)) * ratio)
	if outLen < 1 {
		return nil
	}
	out := make([]float32, outLen)
	step := float64(from) / float64(to)
	for i := range out {
		pos := float64(i) * step * float64(len(filtered)) / float64(len(in))
		i0 := int(pos)
		if i0 >= len(filtered)-1 {
			i0 = len(filtered) - 2
		}
		if i0 < 0 {
			i0 = 0
		}
		f := float32(pos - float64(i0))
		out[i] = filtered[i0]*(1-f) + filtered[i0+1]*f
	}
	return out
}

// convolveFullCompensated：全长度卷积（首尾零填充），并把群延迟 (len(k)-1)/2 补偿回来。
func convolveFullCompensated(x []float32, k []float32) []float32 {
	d := len(k) - 1
	pad := make([]float32, len(x)+d)
	copy(pad[d/2:], x) // 延迟补偿：输入右移 d/2，卷积再延迟 d/2，合计 d
	return convolveValid(pad, k)
}

// biquad 直接 II 型，系数 [b0 b1 b2 a1 a2]。
type biquad struct {
	b0, b1, b2, a1, a2 float64
	z1, z2             float64
}

func (b *biquad) process(x float32) float32 {
	// TDF II
	y := b.b0*float64(x) + b.z1
	b.z1 = b.b1*float64(x) - b.a1*y + b.z2
	b.z2 = b.b2*float64(x) - b.a2*y
	return float32(y)
}

// rbjBandpass 按 RBJ Audio EQ Cookbook 计算带通系数（恒定 0dB 峰值增益）。
func rbjBandpass(fs, f0, q float64) biquad {
	w0 := 2 * math.Pi * f0 / fs
	alpha := math.Sin(w0) / (2 * q)
	b0 := alpha
	b1 := 0.0
	b2 := -alpha
	a0 := 1 + alpha
	a1 := -2 * math.Cos(w0)
	a2 := 1 - alpha
	return biquad{b0 / a0, b1 / a0, b2 / a0, a1 / a0, a2 / a0, 0, 0}
}

// Bandpass 对信号做 4 阶带通 800–2900Hz（两级 biquad 级联，中心频率几何平均）
// 并去直流。RBJ 带通单级 Q=中心/带宽 → 每级 Q ≈ f0/BW；两级级联提高矩形度。
func Bandpass(x []float32, fs float32) []float32 {
	f0 := math.Sqrt(800 * 2900) // ≈1523 Hz
	bw := 2900 - 800
	q1 := f0 / (float64(bw) * 0.7)
	q2 := f0 / (float64(bw) * 0.35)
	s1 := rbjBandpass(float64(fs), f0, q1)
	s2 := rbjBandpass(float64(fs), f0, q2)
	// 去直流：一阶高通
	dc := 0.0
	out := make([]float32, len(x))
	for i, v := range x {
		dc = dc*0.9995 + float64(v)*0.0005
		y := s1.process(v - float32(dc))
		out[i] = s2.process(y)
	}
	return out
}

// Hilbert 用 127-tap Type-III FIR（Blackman 窗）构造解析信号。
// 返回 z = x + j*H{x}，其中实部为延迟补偿后的原信号。
func Hilbert(x []float32) []complex64 {
	const n = 63 // 半长，总长 2n+1=127
	h := make([]float64, 2*n+1)
	for i := 0; i <= 2*n; i++ {
		m := i - n
		if m%2 == 0 {
			continue // Type-III 偶系数为 0
		}
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(2*n)) +
			0.08*math.Cos(4*math.Pi*float64(i)/float64(2*n)) // Blackman
		h[i] = w * 2 / (math.Pi * float64(m))
	}
	// 预翻转卷积核便于 convolveValid
	k := make([]float32, len(h))
	for i := range h {
		k[len(h)-1-i] = float32(h[i])
	}
	re := convolveFullCompensated(x, makeDelayKernel(n)) // 延迟补偿后的直通
	im := convolveFullCompensated(x, k)
	if re == nil || im == nil {
		return nil
	}
	z := make([]complex64, len(re))
	for i := range re {
		z[i] = complex(re[i], im[i])
	}
	return z
}

func makeDelayKernel(n int) []float32 {
	k := make([]float32, 2*n+1)
	k[n] = 1
	return k
}

// InstantFreq 计算解析信号的瞬时频率（Hz），相位差分 wrap 到 (-π, π]。
func InstantFreq(z []complex64, fs float32) []float32 {
	if len(z) < 2 {
		return nil
	}
	out := make([]float32, len(z))
	prev := math.Atan2(float64(imag(z[0])), float64(real(z[0])))
	out[0] = 0
	for i := 1; i < len(z); i++ {
		phi := math.Atan2(float64(imag(z[i])), float64(real(z[i])))
		d := phi - prev
		if d > math.Pi {
			d -= 2 * math.Pi
		} else if d <= -math.Pi {
			d += 2 * math.Pi
		}
		out[i] = float32(d * float64(fs) / (2 * math.Pi))
		prev = phi
	}
	return out
}

// Smooth 对信号做 msec 毫秒滑动平均（相位保持：输出与输入等长，窗内均值居中）。
func Smooth(x []float32, msec int, fs int) []float32 {
	w := fs * msec / 1000
	if w < 1 {
		w = 1
	}
	// 前缀和
	sum := make([]float64, len(x)+1)
	for i, v := range x {
		sum[i+1] = sum[i] + float64(v)
	}
	out := make([]float32, len(x))
	for i := range x {
		lo := i - w/2
		hi := i + w/2
		if lo < 0 {
			lo = 0
		}
		if hi > len(x) {
			hi = len(x)
		}
		out[i] = float32((sum[hi] - sum[lo]) / float64(hi-lo))
	}
	return out
}
