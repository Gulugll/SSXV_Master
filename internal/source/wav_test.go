package source

import (
	"testing"

	"ssxv/internal/testgen"
)

func TestReadWav16Mono(t *testing.T) {
	fs := 8000
	x := make([]float32, fs)
	for i := range x {
		x[i] = 0.25
	}
	wav := testgen.WriteWav16(x, fs)
	pcm, gotFs, err := ReadWav(wav)
	if err != nil {
		t.Fatal(err)
	}
	if gotFs != fs {
		t.Errorf("采样率 %d, 期望 %d", gotFs, fs)
	}
	if len(pcm) != fs {
		t.Errorf("样本数 %d, 期望 %d", len(pcm), fs)
	}
	if pcm[100] != 8192 { // round(0.25 * 32767)
		t.Errorf("样本值 %d, 期望 8192", pcm[100])
	}
}

func TestReadWav16Stereo(t *testing.T) {
	fs := 48000
	// 手工构造双声道：每帧 4 字节
	n := 100
	data := make([]byte, 44+n*4)
	copy(data[0:], "RIFF")
	put32 := func(off int, v uint32) {
		data[off] = byte(v)
		data[off+1] = byte(v >> 8)
		data[off+2] = byte(v >> 16)
		data[off+3] = byte(v >> 24)
	}
	put16 := func(off int, v uint16) { data[off] = byte(v); data[off+1] = byte(v >> 8) }
	put32(4, uint32(36+n*4))
	copy(data[8:], "WAVE")
	copy(data[12:], "fmt ")
	put32(16, 16)
	put16(20, 1)
	put16(22, 2) // stereo
	put32(24, uint32(fs))
	put32(28, uint32(fs*4))
	put16(32, 4)
	put16(34, 16)
	copy(data[36:], "data")
	put32(40, uint32(n*4))
	// 左声道 100, 右声道 -100
	neg := int16(-100)
	for i := 0; i < n; i++ {
		put16(44+i*4, 100)
		put16(44+i*4+2, uint16(neg))
	}
	pcm, gotFs, err := ReadWav(data)
	if err != nil {
		t.Fatal(err)
	}
	if gotFs != fs || len(pcm) != n {
		t.Fatalf("fs=%d n=%d", gotFs, len(pcm))
	}
	if pcm[0] != 0 { // (100 + -100) / 2 = 0
		t.Errorf("混音期望 0, 得到 %d", pcm[0])
	}
}

func TestReadWavRejectsGarbage(t *testing.T) {
	if _, _, err := ReadWav([]byte("not a wav")); err == nil {
		t.Fatal("垃圾输入应报错")
	}
}
