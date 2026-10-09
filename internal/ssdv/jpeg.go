package ssdv

import "fmt"

var traceInBytes int

// JPEG 头重组 + 熵流转码（MCU 级游走）。
// 语义严格对齐 fsphil ssdv.c 的解码路径：
//   - DC：每包首个 MCU（packet.MCUIndex）的 DC 为绝对值，需转为差分；
//     其余为差分直通（ssdv_fsphil.c:509-527）。
//   - AC：量化表相同时为符号直通（含 RLE/EOB/ZRL）。
//   - 包丢失：缺失 MCU 以「空块」（DC size0 + AC EOB）填充（fill_gap 语义）。
//   - 每包 mcu_offset 处冲刷未处理位（编码端在 MCU 边界做了字节对齐填充，
//     ssdv_fsphil.c:1352-1360）。
//   - 输出熵流按 JPEG 规则做 0xFF→FF 00 填充（ssdv_fsphil.c:386）。

// huffCodec 霍夫曼编解码器（由 DHT 表构建，规范码）。
type huffCodec struct {
	lengths [16]int
	symbols []byte
	decode  map[uint32]byte // (len<<16|code) → symbol
	encode  map[byte]uint32 // symbol → (len<<16|code)
}

func newHuffCodec(dht []byte) *huffCodec {
	h := &huffCodec{decode: map[uint32]byte{}, encode: map[byte]uint32{}}
	for i := 0; i < 16; i++ {
		h.lengths[i] = int(dht[1+i])
	}
	h.symbols = append(h.symbols, dht[17:]...)
	code := 0
	idx := 0
	for cw := 1; cw <= 16; cw++ {
		for n := 0; n < h.lengths[cw-1]; n++ {
			key := uint32(cw)<<16 | uint32(code)
			h.decode[key] = h.symbols[idx]
			h.encode[h.symbols[idx]] = key
			idx++
			code++
		}
		code <<= 1
	}
	return h
}

// jpegIntExpand JPEG 整数值扩展：size 位原码 → 有符号值。
func jpegIntExpand(raw uint32, size int) int {
	if size == 0 {
		return 0
	}
	v := int(raw)
	if v < 1<<(size-1) {
		v -= (1 << size) - 1
	}
	return v
}

// jpegIntEncode JPEG 整数编码：有符号值 → (size, raw)。
// 严格对齐 C 参考的 jpeg_encode_int：正数 raw=v；负数 raw=|v|^(2^s-1)
// （即 v+2^s-1）。正数套用负数公式是 M3 调试记录中的单 bit 偏差根源。
func jpegIntEncode(v int) (size int, raw uint32) {
	if v == 0 {
		return 0, 0
	}
	abs := v
	if abs < 0 {
		abs = -abs
	}
	for size = 0; abs != 0; abs >>= 1 {
		size++
	}
	if v < 0 {
		raw = uint32((-v) ^ ((1 << uint(size)) - 1))
	} else {
		raw = uint32(v)
	}
	return size, raw
}

// jpegIntSize 值 → 类别（位数）。
func jpegIntSize(v int) int {
	if v < 0 {
		v = -v
	}
	size := 0
	for v != 0 {
		size++
		v >>= 1
	}
	return size
}

// reassembler 熵流转码器状态。
type reassembler struct {
	out        []byte // 输出（熵流含 0xFF 填充）
	bitBuf     uint32
	bitLen     uint
	outStuffOn bool

	dcAbs  [3]int // 各分量 DC 绝对值
	dcHuff [2]*huffCodec
	acHuff [2]*huffCodec

	ycParts  int
	mcuCount int
	width    int
	height   int
	quality  byte
	mcuMode  byte
	callsign string
	imageID  byte
}

func (r *reassembler) writeByte(b byte) {
	r.out = append(r.out, b)
	if r.outStuffOn && b == 0xFF {
		r.out = append(r.out, 0x00)
	}
}

func (r *reassembler) writeMarker(id uint16, payload []byte) {
	r.writeByte(0xFF)
	r.writeByte(byte(id))
	if len(payload) > 0 {
		l := len(payload) + 2
		r.writeByte(byte(l >> 8))
		r.writeByte(byte(l))
		for _, b := range payload {
			r.writeByte(b)
		}
	}
}

func (r *reassembler) writeHeaders(p *PacketInfo) {
	r.outStuffOn = false
	r.writeMarker(0xD8, nil) // SOI
	r.writeMarker(0xE0, stdApp0[:])
	r.writeMarker(0xDB, scaledDQT(stdDqt0[:], p.Quality))
	r.writeMarker(0xDB, scaledDQT(stdDqt1[:], p.Quality))

	sof := make([]byte, 15)
	sof[0] = 8
	sof[1] = byte(p.Height >> 8)
	sof[2] = byte(p.Height)
	sof[3] = byte(p.Width >> 8)
	sof[4] = byte(p.Width)
	sof[5] = 3
	sof[6] = 1
	switch p.Subsample {
	case 0:
		sof[7] = 0x22
	case 1:
		sof[7] = 0x12
	case 2:
		sof[7] = 0x21
	case 3:
		sof[7] = 0x11
	}
	sof[8] = 0
	sof[9] = 2
	sof[10] = 0x11
	sof[11] = 1
	sof[12] = 3
	sof[13] = 0x11
	sof[14] = 1
	r.writeMarker(0xC0, sof)

	r.writeMarker(0xC4, stdDht00[:])
	r.writeMarker(0xC4, stdDht10[:])
	r.writeMarker(0xC4, stdDht01[:])
	r.writeMarker(0xC4, stdDht11[:])
	r.writeMarker(0xDA, stdSOS[:])
	r.outStuffOn = true
}

// scaledDQT 质量缩放（load_standard_dqt 语义）。
func scaledDQT(std []byte, quality byte) []byte {
	if quality > 7 {
		quality = 7
	}
	scale := uint32(dqtScales[quality])
	out := make([]byte, 65)
	out[0] = std[0]
	for i := 0; i < 64; i++ {
		temp := (uint32(std[i+1])*scale + 50) / 100
		if temp == 0 {
			temp = 1
		}
		if temp > 255 {
			temp = 255
		}
		out[i+1] = byte(temp)
	}
	return out
}

// emitInt 输出 (run, size) 霍夫曼符号 + size 位原码值。
func (r *reassembler) emitInt(dc bool, comp int, run, size int, raw uint32) {
	tbl := 0
	if comp != 0 {
		tbl = 1
	}
	var key uint32
	if dc {
		key = r.dcHuff[tbl].encode[byte(size)]
	} else {
		key = r.acHuff[tbl].encode[byte(run<<4|size)]
	}
	length := int(key >> 16)
	code := int(key & 0xFFFF)
	r.emitBits(uint32(code), length)
	for i := size - 1; i >= 0; i-- {
		r.emitBit(int(raw>>uint(i)) & 1)
	}
}

func (r *reassembler) emitBit(b int) {
	r.bitBuf = r.bitBuf<<1 | uint32(b&1)
	r.bitLen++
	if r.bitLen == 8 {
		r.writeByte(byte(r.bitBuf))
		r.bitBuf = 0
		r.bitLen = 0
	}
}

func (r *reassembler) emitBits(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		r.emitBit(int(v>>uint(i)) & 1)
	}
}

// emitEOB DC 的 size0 符号 / AC 的 EOB 符号。
func (r *reassembler) emitDCZero(comp int) { r.emitInt(true, comp, 0, 0, 0) }
func (r *reassembler) emitACZero(comp int) { r.emitInt(false, comp, 0, 0, 0) }
func (r *reassembler) emitACZRL(comp int)  { r.emitInt(false, comp, 15, 0, 0) }

// lookupHuff 原子查表：返回 (符号, 码长, ok) 但不消费位。
// 调用方确认值位也足够后一次性消费——避免「码已消费、值位未到」时
// 把值位误当下一码字（M3 调试记录）。
func (h *huffCodec) lookupHuff(b *bitFeed) (byte, int, bool) {
	for cw := 1; cw <= 16; cw++ {
		if b.len < cw {
			return 0, 0, false
		}
		v := (b.bits >> uint(b.len-cw)) & ((1 << uint(cw)) - 1)
		if sym, ok := h.decode[uint32(cw)<<16|uint32(v)]; ok {
			return sym, cw, true
		}
	}
	return 0, 0, false
}

// decodeHuffFeed 从位缓冲解码一个霍夫曼符号（不越界预读）。
func (h *huffCodec) decodeHuffFeed(b *bitFeed) (byte, bool) {
	for cw := 1; cw <= 16; cw++ {
		v, ok := b.peek(cw)
		if !ok {
			return 0, false
		}
		if sym, ok := h.decode[uint32(cw)<<16|uint32(v)]; ok {
			b.consume(cw)
			return sym, true
		}
	}
	return 0, false
}

// walkState 连续熵游走状态（跨包延续）。
type walkState struct {
	mcu  int // 当前 MCU id
	part int // 部件序号（0..ycParts-1 = Y，ycParts = Cb，+1 = Cr）
	ac   int // AC 系数序号（0=DC 阶段标记，1..63 = AC）
}

// bitFeed 字节逐喂位缓冲（MSB 先行），严格模拟 C 的 workbits/worklen：
// 霍夫曼解码永远不会越过已喂的字节边界——这是 mcu_offset 冲刷能正确
// 丢弃填充位的关键（填充 1 位在 ≤7 位内不构成完整码字）。
type bitFeed struct {
	bits uint64
	len  int
}

func (b *bitFeed) feed(v byte) {
	b.bits = b.bits<<8 | uint64(v)
	b.len += 8
	if b.len > 40 { // 防御：正常流不会积压这么多
		b.bits &= (1 << uint(b.len)) - 1
	}
}

func (b *bitFeed) peek(n int) (uint64, bool) {
	if b.len < n {
		return 0, false
	}
	return (b.bits >> uint(b.len-n)) & ((1 << uint(n)) - 1), true
}

func (b *bitFeed) consume(n int) {
	b.bits &= (1 << uint(b.len-n)) - 1
	b.len -= n
}

func (b *bitFeed) reset() { b.bits, b.len = 0, 0 }

// walkPacket 消费单包载荷（喂字节 + 熵转码）。状态跨包延续。
// skipTo：从该字节偏移开始消费（丢包后 = mcu_offset；否则 0）。
// 返回 (EOI, false)。
func (r *reassembler) walkPacket(p *PacketInfo, st *walkState, skipTo int, feed *bitFeed) (eoi bool) {
	for i := skipTo; i < len(p.Payload); i++ {
		// mcu_offset 边界：丢弃未处理位（填充位 ≤7 个 1，不构成码字）
		if i == int(p.MCUOffset) && i > 0 {
			feed.reset()
		}
		feed.feed(p.Payload[i])
		traceInBytes++

		for {
			comp := 0
			if st.part >= r.ycParts {
				comp = st.part - r.ycParts + 1
			}
			tbl := 0
			if comp != 0 {
				tbl = 1
			}
			reset := st.mcu == int(p.MCUIndex) && (st.part == 0 || st.part >= r.ycParts)

			if st.ac == 0 {
				// DC 阶段：原子查表（码 + 值位齐备才消费）——
				// 否则「码已消费、值位未到」时值位会被误当下一码字
				sym, hlen, ok := r.dcHuff[tbl].lookupHuff(feed)
				if !ok {
					break // 等更多位
				}
				if feed.len < hlen+int(sym) {
					break // 值位不足
				}
				raw64, _ := feed.peek(hlen + int(sym))
				feed.consume(hlen + int(sym))
				raw := uint32(raw64 & ((1 << uint(sym)) - 1))
				if sym == 0 {
					if reset {
						sz, rawv := jpegIntEncode(-r.dcAbs[comp])
						r.emitInt(true, comp, 0, sz, rawv)
						r.dcAbs[comp] = 0
					} else {
						r.emitDCZero(comp)
					}
					st.ac = 1
					continue
				}
				val := jpegIntExpand(raw, int(sym))
				if reset {
					// 输入为绝对值 → 输出差分
					sz, rawOut := jpegIntEncode(val - r.dcAbs[comp])
					r.emitInt(true, comp, 0, sz, rawOut)
					r.dcAbs[comp] = val
				} else {
					// 差分直通
					r.dcAbs[comp] += val
					r.emitInt(true, comp, 0, int(sym), raw)
				}
				st.ac = 1
				continue
			}

			// AC 阶段：原子查表
			sym, hlen, ok := r.acHuff[tbl].lookupHuff(feed)
			if !ok {
				break
			}
			rle, size := int(sym>>4), int(sym&0xF)
			if feed.len < hlen+size {
				break // 值位不足
			}
			raw64, _ := feed.peek(hlen + size)
			feed.consume(hlen + size)
			raw := uint32(raw64 & ((1 << uint(size)) - 1))
			if size == 0 {
				if rle == 15 {
					r.emitACZRL(comp)
					st.ac += 16
					if st.ac >= 64 {
						st.part++
						if st.part >= r.ycParts+2 {
							st.part = 0
							st.mcu++
						}
						st.ac = 0
					}
					continue
				}
				r.emitACZero(comp)
				st.part++
				if st.part >= r.ycParts+2 {
					st.part = 0
					st.mcu++
				}
				st.ac = 0
				continue
			}
			r.emitInt(false, comp, rle, size, raw)
			// rle 个零系数在符号解码时即计入序号（C: acpart += acrle），
			// 加上本系数共前进 rle+1——漏加会导致位流错位（M3 调试记录）
			st.ac += rle + 1
			if st.ac >= 64 {
				st.part++
				if st.part >= r.ycParts+2 {
					st.part = 0
					st.mcu++
				}
				st.ac = 0
			}
		}

		if st.mcu >= r.mcuCount {
			return true
		}
	}
	return false
}

// DecodeSSDV 从 .ssdv 包字节流解码出 JPEG。
// 连续熵游走：包边界的未完成 MCU 由下一包载荷延续；DC 在每包首个 MCU
// 处为绝对值（编码端已转换），需转回差分。
func DecodeSSDV(data []byte) ([]byte, *PacketInfo, error) {
	packets := ScanPackets(data)
	if len(packets) == 0 {
		return nil, nil, fmt.Errorf("ssdv: 未找到有效包")
	}
	first := packets[0]
	if first.ImageID != packets[len(packets)-1].ImageID {
		var filtered []*PacketInfo
		for _, p := range packets {
			if p.ImageID == first.ImageID {
				filtered = append(filtered, p)
			}
		}
		packets = filtered
	}

	r := &reassembler{
		dcHuff: [2]*huffCodec{newHuffCodec(stdDht00[:]), newHuffCodec(stdDht01[:])},
		acHuff: [2]*huffCodec{newHuffCodec(stdDht10[:]), newHuffCodec(stdDht11[:])},
	}
	r.writeHeaders(first)

	ycParts := 1
	switch first.Subsample {
	case 0:
		ycParts = 4
	case 1, 2:
		ycParts = 2
	}
	r.ycParts = ycParts
	r.mcuCount = first.Width / 16 * first.Height / 16
	switch first.Subsample {
	case 1, 2:
		r.mcuCount *= 2
	case 3:
		r.mcuCount *= 4
	}
	r.width, r.height = first.Width, first.Height
	r.quality, r.mcuMode = first.Quality, first.Subsample
	r.callsign, r.imageID = first.Callsign, first.ImageID

	st := &walkState{}
	feed := &bitFeed{}
	traceInBytes = 0
	lastID := -1
	eoi := false
	for _, p := range packets {
		pid := int(p.PacketID)
		if pid <= lastID {
			continue
		}
		skipTo := 0
		if pid > lastID+1 {
			// 丢包：未完成 MCU 作废，填充空 MCU
			st.part, st.ac = 0, 0
			for mcu := st.mcu; mcu < int(p.MCUIndex) && mcu < r.mcuCount; mcu++ {
				for part := 0; part < ycParts+2; part++ {
					comp := 0
					if part >= ycParts {
						comp = part - ycParts + 1
					}
					r.emitDCZero(comp)
					r.emitACZero(comp)
				}
			}
			st.mcu = int(p.MCUIndex)
			skipTo = int(p.MCUOffset) // 跳过丢失 MCU 的残余字节
		}
		lastID = pid

		if eoi = r.walkPacket(p, st, skipTo, feed); eoi {
			break
		}
	}

	// 位同步（填充 1 位到字节边界）+ EOI
	for r.bitLen != 0 {
		r.emitBit(1)
	}
	r.outStuffOn = false
	r.writeMarker(0xD9, nil)

	return r.out, first, nil
}
