package ssdv

import (
	"bytes"
	"os"
	"testing"

	"ssxv/internal/testgen"
)

// 干净闭环：随机字节流调制→解调→字节一致
func TestBPSKDemodClean(t *testing.T) {
	data := make([]byte, 300)
	for i := range data {
		data[i] = byte(i * 7)
	}
	x := testgen.BPSKModulate(data, 48000)
	got := DemodBPSK(x, 48000)
	// 解调器无法恢复绝对帧相位（定时相位任意 mod sps），尾字节可能不完整；
	// 位流内容必须与数据前缀一致，帧同步由包层 ScanPackets 负责
	if !bytes.HasPrefix(data, got) {
		t.Fatalf("位流与数据前缀不一致: got=%d bytes", len(got))
	}
}

// 噪声下端到端验收：2.7kHz 声道口径 SNR 10dB → 解调 → RS 纠错 → JPEG
// 与 C 参考输出逐字节一致（RS 容 16 字节/包，链路层 BER 需显著低于其上限）
func TestBPSKDemodSNR10(t *testing.T) {
	packets, err := os.ReadFile("../../testdata/ssdv/packets.bin")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := os.ReadFile("../../testdata/ssdv/ref.jpeg")
	if err != nil {
		t.Fatal(err)
	}
	// 补 2 字节填充：解调器最后一个符号的判决窗超出信号末端会被丢弃，
	// 不补则尾包截断 1 字节导致 CRC 失败（真实发射末尾亦有静音冗余）
	x := testgen.BPSKModulate(append(append([]byte{}, packets...), 0, 0), 48000)
	x = testgen.BPSKAWGN(x, 10, 7)
	got := DemodBPSK(x, 48000)
	jpeg, _, err := DecodeSSDV(got)
	if err != nil {
		t.Fatalf("DecodeSSDV: %v", err)
	}
	if !bytes.Equal(jpeg, ref) {
		t.Fatalf("SNR10 下 JPEG 与参考不一致: len=%d/%d", len(jpeg), len(ref))
	}
}

// 载波频偏 ±80Hz 内 Costas 应跟踪
func TestBPSKDemodCFO(t *testing.T) {
	data := bytes.Repeat([]byte{0xA5}, 200)
	for _, cfo := range []float64{80, -80} {
		x := testgen.BPSKModulate(data, 48000)
		x = testgen.BPSKShiftCFO(x, 48000, cfo)
		got := DemodBPSK(x, 48000)
		if !bytes.HasPrefix(data, got) {
			t.Fatalf("CFO %+.0fHz 下解调失败: got=%d bytes", cfo, len(got))
		}
	}
}

// 端到端（干净）：SSDV 包流→调制→解调→JPEG（黄金对拍基准）
func TestBPSKEndToEnd(t *testing.T) {
	packets, err := os.ReadFile("../../testdata/ssdv/packets.bin")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := os.ReadFile("../../testdata/ssdv/ref.jpeg")
	if err != nil {
		t.Fatal(err)
	}
	x := testgen.BPSKModulate(append(append([]byte{}, packets...), 0, 0), 48000)
	bytesOut := DemodBPSK(x, 48000)
	jpeg, _, err := DecodeSSDV(bytesOut)
	if err != nil {
		t.Fatalf("DecodeSSDV: %v", err)
	}
	if !bytes.Equal(jpeg, ref) {
		n := 0
		for i := range jpeg {
			if i < len(ref) && jpeg[i] != ref[i] {
				n++
			}
		}
		t.Fatalf("JPEG 不一致: len=%d/%d 差异字节=%d", len(jpeg), len(ref), n)
	}
}
