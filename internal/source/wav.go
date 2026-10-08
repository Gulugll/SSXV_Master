// Package source 提供音频输入源抽象（TECH_SPEC §1/§4）。
package source

import (
	"encoding/binary"
	"fmt"
)

// ReadWav 解析 PCM16 WAV（单/双声道，任意采样率）。
// 双声道取平均混为单声道。返回 int16 PCM 与采样率。
func ReadWav(b []byte) ([]int16, int, error) {
	if len(b) < 44 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("source: 不是 RIFF/WAVE 文件")
	}
	u16 := binary.LittleEndian.Uint16
	u32 := binary.LittleEndian.Uint32

	// 遍历 chunk 找 fmt 与 data
	var audioFormat, channels, bits uint16
	var sampleRate uint32
	var data []byte
	pos := 12
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := int(u32(b[pos+4 : pos+8]))
		body := pos + 8
		if body+size > len(b) {
			size = len(b) - body
		}
		switch id {
		case "fmt ":
			audioFormat = u16(b[body : body+2])
			channels = u16(b[body+2 : body+4])
			sampleRate = u32(b[body+4 : body+8])
			bits = u16(b[body+14 : body+16])
		case "data":
			data = b[body : body+size]
		}
		pos = body + size + (size & 1) // chunk 按 2 字节对齐
	}
	if audioFormat != 1 || len(data) == 0 {
		return nil, 0, fmt.Errorf("source: 仅支持 PCM WAV")
	}
	if bits != 16 {
		return nil, 0, fmt.Errorf("source: 仅支持 16bit，得到 %dbit", bits)
	}
	if channels < 1 || channels > 2 {
		return nil, 0, fmt.Errorf("source: 不支持 %d 声道", channels)
	}
	bytesPerFrame := int(channels) * 2
	n := len(data) / bytesPerFrame
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		if channels == 1 {
			out[i] = int16(u16(data[i*2:]))
		} else {
			l := int32(int16(u16(data[i*4:])))
			r := int32(int16(u16(data[i*4+2:])))
			out[i] = int16((l + r) / 2)
		}
	}
	return out, int(sampleRate), nil
}
