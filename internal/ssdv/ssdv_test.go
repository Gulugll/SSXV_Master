package ssdv

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"os"
	"testing"
)

// TestGFTablesMatchCReference 运行时生成的 GF(256) 表必须与 Karn rs8.c
// 的硬编码表逐字节一致（表提取自 docs/ref/rs8.c）。
func TestGFTablesMatchCReference(t *testing.T) {
	for i := 0; i < 256; i++ {
		if gf.alphaTo[i] != cAlphaTo[i] {
			t.Fatalf("ALPHA_TO[%d] = %#02x, C 参考 %#02x", i, gf.alphaTo[i], cAlphaTo[i])
		}
		if gf.indexOf[i] != cIndexOf[i] {
			t.Fatalf("INDEX_OF[%d] = %#02x, C 参考 %#02x", i, gf.indexOf[i], cIndexOf[i])
		}
	}
	for i := 0; i < len(cGenPoly); i++ {
		if gf.genPoly[i] != cGenPoly[i] {
			t.Fatalf("GENPOLY[%d] = %#02x, C 参考 %#02x", i, gf.genPoly[i], cGenPoly[i])
		}
	}
}

// TestRS8RoundTripWithErrors 随机数据 + 1..16 字节随机错误 → 必须完全恢复。
func TestRS8RoundTripWithErrors(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	codeword := make([]byte, rsNN)
	orig := make([]byte, rsNN-rsNRoots)
	for trial := 0; trial < 2000; trial++ {
		for i := range orig {
			orig[i] = byte(rng.Intn(256))
		}
		copy(codeword, orig)
		EncodeRS8(codeword[:rsNN-rsNRoots], codeword[rsNN-rsNRoots:])

		nErr := 1 + rng.Intn(16)
		posSet := map[int]bool{}
		for nErr > len(posSet) {
			posSet[rng.Intn(rsNN)] = true
		}
		corrupt := append([]byte(nil), codeword...)
		for pos := range posSet {
			corrupt[pos] ^= byte(1 + rng.Intn(255))
		}

		if n := DecodeRS8(corrupt); n < 0 {
			t.Fatalf("trial %d: 不可纠（%d 错）", trial, nErr)
		}
		if !bytes.Equal(corrupt, codeword) {
			t.Fatalf("trial %d: 纠错后不一致", trial)
		}
	}
}

// TestCRC32IEEE 已知向量：CRC-32("123456789") = 0xCBF43926。
func TestCRC32IEEE(t *testing.T) {
	if got := crc32IEEE([]byte("123456789")); got != 0xCBF43926 {
		t.Errorf("CRC32 = %#08x, 期望 0xCBF43926", got)
	}
}

// TestGoldenReferenceDecode 与 G4KLA ssdv -d 的输出做字节级对拍。
func TestGoldenReferenceDecode(t *testing.T) {
	packets, err := os.ReadFile("../../testdata/ssdv/packets.bin")
	if err != nil {
		t.Skip("语料缺失:", err)
	}
	want, err := os.ReadFile("../../testdata/ssdv/ref.jpeg")
	if err != nil {
		t.Skip("语料缺失:", err)
	}
	got, info, err := DecodeSSDV(packets)
	if err != nil {
		t.Fatal(err)
	}
	if info.Callsign != "TEST01" {
		t.Errorf("呼号 %q, 期望 TEST01", info.Callsign)
	}
	if info.Quality != 4 {
		t.Errorf("质量级 %d, 期望 4", info.Quality)
	}
	if !bytes.Equal(got, want) {
		// 找第一个差异位置辅助定位
		n := len(got)
		if len(want) < n {
			n = len(want)
		}
		for i := 0; i < n; i++ {
			if got[i] != want[i] {
				t.Fatalf("输出与 C 参考不一致 @%d: got %#02x want %#02x (长度 got=%d want=%d)",
					i, got[i], want[i], len(got), len(want))
			}
		}
		t.Fatalf("输出长度不一致: got %d, want %d", len(got), len(want))
	}
}

// TestScanPacketsCorruption 注入字节错误后仍应恢复全部包（RS 纠错路径）。
func TestScanPacketsCorruption(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/ssdv/packets.bin")
	if err != nil {
		t.Skip("语料缺失:", err)
	}
	clean := ScanPackets(raw)
	if len(clean) != 14 {
		t.Fatalf("干净包数 %d, 期望 14", len(clean))
	}
	rng := rand.New(rand.NewSource(7))
	corrupt := append([]byte(nil), raw...)
	// 每包注入 ≤8 字节错误（RS 纠 16 上限内），保留同步与类型字节
	for p := 0; p < 14; p++ {
		base := p * 256
		for n := 0; n < 8; n++ {
			off := base + 2 + rng.Intn(250)
			corrupt[off] ^= byte(1 + rng.Intn(255))
		}
	}
	got := ScanPackets(corrupt)
	if len(got) != 14 {
		t.Fatalf("纠错后包数 %d, 期望 14", len(got))
	}
	for i := range got {
		if binary.BigEndian.Uint16([]byte{0, byte(i)}) != got[i].PacketID {
			t.Errorf("包 %d 顺序异常: id=%d", i, got[i].PacketID)
		}
	}
}
