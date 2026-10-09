package testgen

// segBuilder 段序列构建器（时间按毫秒累积，与 toneGen 的采样累积一致）。
type segBuilder struct {
	fs   int
	segs []Seg
}

func (b *segBuilder) add(freqHz float64, msec float64) {
	b.segs = append(b.segs, Seg{Freq: freqHz, Ms: msec})
}

func (b *segBuilder) done() []Seg { return b.segs }

// addVis 追加 VIS 头（TECH_SPEC §3.2）：1900×300 → 1200×10 → 1900×300
// → 起始位 1200×30 → 7 数据位（LSB 先行）+ 偶校验 → 停止位 1200×30。
func (b *segBuilder) addVis(vis byte) {
	b.add(tgFreqVisLead, 300)
	b.add(tgFreqSync, 10)
	b.add(tgFreqVisLead, 300)
	b.add(tgFreqSync, 30) // 起始位
	ones := 0
	v := vis
	for i := 0; i < 7; i++ {
		bit := v & 1
		v >>= 1
		ones += int(bit)
		if bit == 1 {
			b.add(tgFreqVisBit1, 30)
		} else {
			b.add(tgFreqVisBit0, 30)
		}
	}
	if ones%2 == 1 {
		b.add(tgFreqVisBit1, 30)
	} else {
		b.add(tgFreqVisBit0, 30)
	}
	b.add(tgFreqSync, 30) // 停止位
}

// ByteToFreq 亮度 → 频率（与 sstv 包公式一致）。
func ByteToFreq(v int) float64 { return tgByteToFreq(v) }
