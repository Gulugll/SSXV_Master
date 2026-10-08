package sstv

import (
	"image"
	"testing"
)

// 按模式规格生成一行的瞬时频率段（testgen 语义，含行同步）
// segBuilder 用累积舍入（pySSTV gen_values 手法）避免像素级整数舍入误差
type segBuilder struct {
	fs    int
	segs  []seg
	carry float64
}

func (b *segBuilder) add(freqHz float64, msec float64) {
	pos := b.carry + float64(b.fs)*msec/1000
	n := int(pos) - int(b.carry)
	b.carry = pos
	if n > 0 {
		b.segs = append(b.segs, seg{freq: freqHz, n: n})
	}
}

func (b *segBuilder) done() []seg { return b.segs }

func robot36LineSegments(fs int, line int, yPx []int, cPx []int) []seg {
	b := &segBuilder{fs: fs}
	b.add(FreqSync, 9) // 行同步
	b.add(FreqBlack, 3)
	for _, p := range yPx { // Y 扫描 88ms / 320px
		b.add(ByteToFreq(p), 0.275)
	}
	// sep（偶行 1500 / 奇行 2300，pySSTV 语义）+ porch 1.5@1900
	if line%2 == 0 {
		b.add(1500, 4.5)
	} else {
		b.add(2300, 4.5)
	}
	b.add(FreqVisLead, 1.5)
	for _, p := range cPx { // C 扫描 44ms / 320px
		b.add(ByteToFreq(p), 0.1375)
	}
	return b.done()
}

type seg struct {
	freq float64
	n    int
}

// visSegs 按秒级精度生成 VIS 头段序列（TECH_SPEC §3.2）
func visSegs(fs int, vis byte) []seg {
	add := func(out []seg, f float64, ms float64) []seg {
		return append(out, seg{freq: f, n: int(float64(fs) * ms / 1000)})
	}
	var out []seg
	out = add(out, FreqVisLead, 300)
	out = add(out, FreqSync, 10)
	out = add(out, FreqVisLead, 300)
	out = add(out, FreqSync, 30) // 起始位
	ones := 0
	v := vis
	for i := 0; i < 7; i++ {
		bit := v & 1
		v >>= 1
		ones += int(bit)
		if bit == 1 {
			out = add(out, FreqVisBit1, 30)
		} else {
			out = add(out, FreqVisBit0, 30)
		}
	}
	if ones%2 == 1 {
		out = add(out, FreqVisBit1, 30)
	} else {
		out = add(out, FreqVisBit0, 30)
	}
	return add(out, FreqSync, 30) // 停止位
}

func concat(segs []seg, fs int) []float32 {
	total := 0
	for _, s := range segs {
		total += s.n
	}
	out := make([]float32, 0, total)
	for _, s := range segs {
		for i := 0; i < s.n; i++ {
			out = append(out, float32(s.freq))
		}
	}
	return out
}

func TestDecodeRobot36Basic(t *testing.T) {
	fs := 8000
	W, H := 320, 240
	// 构造 4 行（2 对），Y=行号相关渐变，偶行 Cr=200，奇行 Cb=100
	var stream []seg
	stream = append(stream, visSegs(fs, 0x08)...)
	for l := 0; l < 4; l++ {
		y := make([]int, W)
		c := make([]int, W)
		for x := 0; x < W; x++ {
			y[x] = (x * 255) / W
			if l%2 == 0 {
				c[x] = 200
			} else {
				c[x] = 100
			}
		}
		stream = append(stream, robot36LineSegments(fs, l, y, c)...)
	}
	// 尾部静默
	for i := 0; i < fs/2; i++ {
		stream = append(stream, seg{freq: FreqBlack, n: 1})
	}
	freq := concat(stream, fs)

	_, start, ok := DetectVIS(freq, float32(fs))
	if !ok {
		t.Fatal("VIS 未识别")
	}
	img := DecodeRobot36(freq, float32(fs), start)
	if img == nil {
		t.Fatal("解码返回 nil")
	}
	b := img.Bounds()
	if b.Dx() != W || b.Dy() != H {
		t.Fatalf("尺寸 %v, 期望 %dx%d", b, W, H)
	}
	// 校验第 2 行（奇数行）的 RGB：Y=(x*255)/320, Cb=100, Cr=200
	okCnt := 0
	for x := 10; x < W-10; x++ {
		y := (x * 255) / W
		r, g, bl := ycbcrToRGB(uint8(y), uint8(100), uint8(200))
		pr, pg, pb, _ := img.At(x, 2).RGBA()
		dr := abs(int(pr>>8)-int(r)) + abs(int(pg>>8)-int(g)) + abs(int(pb>>8)-int(bl))
		if dr <= 6 {
			okCnt++
		}
	}
	if okCnt < W-30 {
		t.Errorf("第 2 行像素匹配 %d/%d", okCnt, W-20)
	}
}

func TestDecodeRobot36NoSyncFallback(t *testing.T) {
	fs := 8000
	W := 320
	// pySSTV 风格：无 9ms 行同步，行结构从停止位后按标称 150ms 排布
	var stream []seg
	stream = append(stream, visSegs(fs, 0x08)...)
	for l := 0; l < 2; l++ {
		y := make([]int, W)
		c := make([]int, W)
		for x := 0; x < W; x++ {
			y[x] = 128
			c[x] = 128
		}
		// 无同步：porch 3 + Y 88 + sep 4.5 + porch 1.5 + C 44 = 141ms（标称 150 含 sync）
		b := &segBuilder{fs: fs}
		b.add(FreqBlack, 3)
		for _, p := range y {
			b.add(ByteToFreq(p), 0.275)
		}
		b.add(1500, 4.5)
		b.add(FreqVisLead, 1.5)
		for _, p := range c {
			b.add(ByteToFreq(p), 0.1375)
		}
		b.add(FreqBlack, 9) // 补齐到 150ms
		stream = append(stream, b.done()...)
	}
	freq := concat(stream, fs)
	_, start, ok := DetectVIS(freq, float32(fs))
	if !ok {
		t.Fatal("VIS 未识别")
	}
	img := DecodeRobot36(freq, float32(fs), start)
	if img == nil {
		t.Fatal("解码返回 nil")
	}
	r, g, b, _ := img.At(160, 1).RGBA()
	// Y=128,Cb=128,Cr=128 → 灰 128
	if abs(int(r>>8)-128) > 8 || abs(int(g>>8)-128) > 8 || abs(int(b>>8)-128) > 8 {
		t.Errorf("无同步回退解码偏差: %d,%d,%d", r>>8, g>>8, b>>8)
	}
}

func TestHamming(t *testing.T) {
	if hamming(0x2C, 0x2C^0x01) != 1 {
		t.Error("hamming 距离错误")
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

var _ = image.Point{}
