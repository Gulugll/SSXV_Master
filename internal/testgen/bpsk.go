package testgen

import (
	"math"
	"math/rand"

	"ssxv/internal/dsp"
)

// BPSK 音频调制（SSDV 链路 PHY，TECH_SPEC §4.5）：
//   - 200 波特，载波 1500Hz，位流 MSB 先行；
//   - 矩形脉冲 + 单极点 RC 预滤波（抑制带外溅射）；
//   - bit=1 → 相位 0，bit=0 → 相位 π。
//
// 波特率与载波选择依据：200bd 为业余高空气球 SSDV/BPSK 常用速率；
// 1500Hz 位于 300-2900Hz 语音带内且距奈奎斯特边距充足。

// BPSKBaud 调制波特率。
const BPSKBaud = 200

// BPSKCenter 载波中心频率。
const BPSKCenter = 1500.0

// BPSKModulate 将字节流调制为 BPSK 音频（±0.9 内幅度，float）。
// 每次调用从固定相位开始（确定性）。
func BPSKModulate(data []byte, fs int) []float32 {
	sps := fs / BPSKBaud
	rc := 1 - math.Exp(-2*math.Pi*800/float64(fs)) // 800Hz 单极点低通系数
	total := len(data) * 8 * sps
	out := make([]float32, total)
	base := make([]float32, total)
	w := 2 * math.Pi * BPSKCenter / float64(fs)

	// NRZ 上采样（MSB 先行）
	for bi, b := range data {
		for k := 0; k < 8; k++ {
			bit := (b >> (7 - k)) & 1
			v := float32(-1)
			if bit == 1 {
				v = 1
			}
			for n := 0; n < sps; n++ {
				base[(bi*8+k)*sps+n] = v
			}
		}
	}

	// RC 预滤波 + 载波调制
	var lp float64
	for n := 0; n < total; n++ {
		lp += (float64(base[n]) - lp) * rc
		out[n] = float32(lp * 0.9 * math.Cos(w*float64(n)))
	}
	return out
}

// BPSKAWGN 叠加高斯白噪声。
// snrDb 以 2.7kHz 通信音频声道内的信噪比计（与真实接收机音频口径、
// TECH_SPEC SSTV 验收口径一致）；200Hz 符号带宽内的 SNR 高 11.3dB。
// 若按 200Hz 口径定义 10dB，折算 BER ~1.7%，超出 RS(255,223) 纠错
// 能力（M3 调试记录），不对应真实接收场景。
func BPSKAWGN(x []float32, snrDb float64, seed int64) []float32 {
	var sp float64
	for _, v := range x {
		sp += float64(v) * float64(v)
	}
	sp /= float64(len(x))
	const chanBW = 2700.0 // 通信音频声道带宽
	noiseP := sp / math.Pow(10, snrDb/10) * (48000.0 / chanBW)
	sd := math.Sqrt(noiseP)
	r := rand.New(rand.NewSource(seed))
	out := make([]float32, len(x))
	for i, v := range x {
		out[i] = v + float32(r.NormFloat64()*sd)
	}
	return out
}

// BPSKShiftCFO 模拟接收机本振频偏 ±hz：经解析信号（Hilbert）做单边频移，
// 保持「单载波平移」的物理模型。实信号直接乘 cos 会产生 ±hz 双边分量 +
// 交叉项，平方谱出现 4 根谱线导致 CFO 估计锁错（M3 调试记录）。
func BPSKShiftCFO(x []float32, fs int, hz float64) []float32 {
	z := dsp.HilbertAt(x, float32(fs))
	w := 2 * math.Pi * hz / float64(fs)
	out := make([]float32, len(x))
	for n := range x {
		re := float64(x[n])
		im := float64(imag(z[n]))
		// (re + j·im)·e^{jw n} 的实部
		out[n] = float32(re*math.Cos(w*float64(n)) - im*math.Sin(w*float64(n)))
	}
	return out
}
