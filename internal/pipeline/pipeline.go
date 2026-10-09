// Package pipeline 串联 source → dsp → sstv 的解码流水线。
package pipeline

import (
	"fmt"
	"image"

	"ssxv/internal/dsp"
	"ssxv/internal/source"
	"ssxv/internal/sstv"
)

// DecodeResult 解码结果。
type DecodeResult struct {
	Image image.Image
	Mode  string // 检测到的模式名（如 "Robot36"）
}

// DecodeWAVBytes 解码 WAV 字节流中的 SSTV 图像。
func DecodeWAVBytes(wav []byte) (image.Image, error) {
	r, err := DecodeWAVResult(wav)
	if err != nil {
		return nil, err
	}
	return r.Image, nil
}

// DecodeWAVResult 同上，附带模式名。
func DecodeWAVResult(wav []byte) (DecodeResult, error) {
	pcm, fs, err := source.ReadWav(wav)
	if err != nil {
		return DecodeResult{}, fmt.Errorf("pipeline: %w", err)
	}
	return DecodePCMResult(pcm, fs)
}

// DecodePCM 解码 int16 PCM。在输入原生采样率上处理——Hilbert 建立时间
// 以样本数计（~10 样本），采样率越高时间上越窄（48k ≈ 0.2ms），
// 低采样率（8k）会污染扫描段两端 1-4 像素（M0 调试记录，PLAN_M0.md）。
func DecodePCM(pcm []int16, fs int) (image.Image, error) {
	r, err := DecodePCMResult(pcm, fs)
	if err != nil {
		return nil, err
	}
	return r.Image, nil
}

// DecodePCMResult 同 DecodePCM，附带模式名。
func DecodePCMResult(pcm []int16, fs int) (DecodeResult, error) {
	x := make([]float32, len(pcm))
	for i, v := range pcm {
		x[i] = float32(v) / 32767
	}
	// 温和 IIR 带通（2 阶 Butterworth 700-3400，Q≈0.57）：把解调噪声带宽从
	// 24kHz 压到 ~2.7kHz（-9.5dB），振铃仅 ~6 样本（0.12ms），不污染像素窗。
	// 窄带 FIR 方案已被实测否决（阶跃振铃拖尾 ~10ms，U 形误差，见 PLAN_M0）。
	x = dsp.Bandpass(x, float32(fs))
	z := dsp.HilbertAt(x, float32(fs))
	freq := dsp.InstantFreq(z, float32(fs))
	freq = dsp.Smooth(freq, 0.3, fs)

	vis, start, ok := sstv.DetectVIS(freq, float32(fs))
	if !ok {
		return DecodeResult{}, fmt.Errorf("pipeline: 未识别到 VIS 头")
	}
	mode, vok := sstv.ModeByVIS(vis)
	if !vok {
		return DecodeResult{}, fmt.Errorf("pipeline: 未知 VIS %#02x", vis)
	}
	return DecodeResult{Image: sstv.DecodeMode(freq, float32(fs), mode, start), Mode: mode.Name}, nil
}
