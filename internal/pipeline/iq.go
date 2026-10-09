// IQ 基带记录输入（M4-2，TECH_SPEC §2.1）。
//
// 语义：IQ 记录为「IF 偏移」录音——原始 SSTV 音频频谱整体搬移到 IF 处，
// 即 inst_freq(z) = IF + f_audio（f_audio ∈ [1100,2300]）。
// 解码 = 相位差分鉴频 → 减去 IF → 走 SSTV VIS/模式链。
// ponytail: 仅覆盖 SSTV 链路；SSDV-over-IQ（NBFM 真实射频录音、偏差率标定）
// 等有真实语料时再加（IQ 语义不同：载波居中 NBFM 的 inst_freq = (f-1500)·Δk）。
package pipeline

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"ssxv/internal/dsp"
	"ssxv/internal/sstv"
)

// ParseIQ 把裸 IQ 字节流解析为复基带。
// 支持格式：CU8（无符号 8bit，128 偏置）、CS16（LE int16）、CF32（LE float32），
// 均为 I,Q 交错。
func ParseIQ(data []byte, format string) ([]complex64, error) {
	switch format {
	case "cu8":
		if len(data)%2 != 0 {
			return nil, fmt.Errorf("pipeline: CU8 长度非偶数")
		}
		n := len(data) / 2
		z := make([]complex64, n)
		for i := 0; i < n; i++ {
			re := float32(data[2*i]) - 128
			im := float32(data[2*i+1]) - 128
			z[i] = complex(re/127.5, im/127.5)
		}
		return z, nil
	case "cs16":
		if len(data)%4 != 0 {
			return nil, fmt.Errorf("pipeline: CS16 长度非 4 倍数")
		}
		n := len(data) / 4
		z := make([]complex64, n)
		for i := 0; i < n; i++ {
			re := float32(int16(binary.LittleEndian.Uint16(data[4*i:])))
			im := float32(int16(binary.LittleEndian.Uint16(data[4*i+2:])))
			z[i] = complex(re/32767, im/32767)
		}
		return z, nil
	case "cf32":
		if len(data)%8 != 0 {
			return nil, fmt.Errorf("pipeline: CF32 长度非 8 倍数")
		}
		n := len(data) / 8
		z := make([]complex64, n)
		for i := 0; i < n; i++ {
			re := math.Float32frombits(binary.LittleEndian.Uint32(data[8*i:]))
			im := math.Float32frombits(binary.LittleEndian.Uint32(data[8*i+4:]))
			z[i] = complex(re, im)
		}
		return z, nil
	}
	return nil, fmt.Errorf("pipeline: 未知 IQ 格式 %q（支持 cu8/cs16/cf32）", format)
}

// EstimateIF 估计 IQ 记录的 IF 偏移（Hz）。
// 原理：SSTV 频谱占 [1100,2300]+IF，取鉴频分布的双侧稳健分位（0.2%/99.8%）
// 中点减去频带中心 1700。
func EstimateIF(freq []float32) float32 {
	if len(freq) == 0 {
		return 0
	}
	s := make([]float32, len(freq))
	copy(s, freq)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	lo := s[int(float64(len(s))*0.002)]
	hi := s[int(float64(len(s))*0.998)]
	return (lo+hi)/2 - 1700
}

// DecodeIQResult 解码 IQ 基带记录（IF 偏移语义，见包注释）。
// ifHz > 0 时强制使用指定 IF；否则自动估计：
// 分位法粗估（频谱占用不对称会有数十 Hz 偏差）→ VIS 粗同步 →
// 同步脉冲凹槽（恒 1200Hz）逐锚点精校。
func DecodeIQResult(z []complex64, fs int, ifHz float64) (DecodeResult, error) {
	if fs <= 0 {
		return DecodeResult{}, fmt.Errorf("pipeline: 非法采样率 %d", fs)
	}
	// 粗估 IF（分位法，±数十 Hz；仅用于下变频与 VIS 粗同步）
	if ifHz <= 0 {
		f0 := dsp.InstantFreq(z, float32(fs))
		f0 = dsp.Smooth(f0, 0.3, fs)
		ifHz = float64(EstimateIF(f0))
	}
	// 复下变频取实部 ≈ SSTV 音频（残差 δ 为常数偏移，由同步凹槽精校消除），
	// 之后完全复用已校准的实链路（带通降噪 → Hilbert → 鉴频 → VIS → 解码）。
	// ponytail: 不在复域做带通/降噪——实链路行为已被 M0-M2 调准，复用优先。
	x := downshiftRe(z, float64(fs), ifHz)
	x = dsp.Bandpass(x, float32(fs))
	freq := dsp.InstantFreq(dsp.HilbertAt(x, float32(fs)), float32(fs))
	freq = dsp.Smooth(freq, 0.3, fs)

	vis, start, ok := sstv.DetectVIS(freq, float32(fs))
	if !ok {
		return DecodeResult{}, fmt.Errorf("pipeline: IQ 记录未识别到 VIS 头")
	}

	// 同步凹槽精校：锚点窗内均值应为 1200+残差，取中位数抗坏行
	if mode, mok := sstv.ModeByVIS(vis); mok && mode.SyncMs > 0 {
		if deltas := syncDeltas(freq, float32(fs), start, mode.SyncMs); len(deltas) > 0 {
			d := float32(median(deltas))
			for i := range freq {
				freq[i] -= d
			}
			if v2, s2, ok2 := sstv.DetectVIS(freq, float32(fs)); ok2 {
				vis, start = v2, s2
			}
		}
	}

	mode, vok := sstv.ModeByVIS(vis)
	if !vok {
		return DecodeResult{}, fmt.Errorf("pipeline: 未知 VIS %#02x", vis)
	}
	return DecodeResult{Image: sstv.DecodeMode(freq, float32(fs), mode, start), Mode: mode.Name + " (IQ)"}, nil
}

// downshiftRe 复信号频移 -fHz 后取实部。
func downshiftRe(z []complex64, fs, fHz float64) []float32 {
	out := make([]float32, len(z))
	step := -2 * math.Pi * fHz / fs
	c, s := math.Cos(step), math.Sin(step)
	re, im := 1.0, 0.0 // e^{jph} 递推
	for i, v := range z {
		out[i] = float32(float64(real(v))*re - float64(imag(v))*im)
		re, im = re*c-im*s, re*s+im*c
		if re*re+im*im > 1.0001 || re*re+im*im < 0.9999 {
			n := math.Hypot(re, im)
			re, im = re/n, im/n
		}
	}
	return out
}

// syncDeltas 计算各同步锚点窗内频率均值与 1200 的偏差。
// 只取凹槽内侧一半：Smooth 使窗边缘混入前后 porch（Robot36 奇偶行
// 1500/2300 交替，污染可达数十 Hz），内侧半窗远离过渡沿。
func syncDeltas(freq []float32, fs float32, from int, syncMs float64) []float64 {
	anchors := sstv.DebugAnchors(freq, fs, from, syncMs)
	syncSamples := int(syncMs / 1000 * float64(fs))
	lo, hi0 := syncSamples/4, syncSamples*3/4
	var out []float64
	for _, a := range anchors {
		i0, i1 := a+lo, a+hi0
		if i1 > len(freq) || i1 <= i0 {
			continue
		}
		var s float64
		for i := i0; i < i1; i++ {
			s += float64(freq[i])
		}
		out = append(out, s/float64(i1-i0)-float64(sstv.FreqSync))
	}
	return out
}

func median(v []float64) float64 {
	s := make([]float64, len(v))
	copy(s, v)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}
