package ssdv

import (
	"encoding/binary"
	"fmt"
)

// SSDV 包格式（UKHAS/G4KLA 规范，TECH_SPEC §4.1）。
const (
	PacketSize      = 256
	PacketHeaderLen = 15
	PacketCRCLen    = 4
	PacketRSLen     = 32
	PayloadFEC      = PacketSize - PacketHeaderLen - PacketCRCLen - PacketRSLen // 205
	PayloadNoFEC    = PacketSize - PacketHeaderLen - PacketCRCLen               // 237
	SyncByte        = 0x55
	TypeNormal      = 0x66 // FEC 正常
	TypeNoFEC       = 0x67 // 无 FEC
)

// PacketInfo 解析后的包头。
type PacketInfo struct {
	Type      byte   // 0x66 / 0x67
	Callsign  string // base-40 解码，≤6 字符
	ImageID   byte
	PacketID  uint16
	Width     int // 像素（MCU×16）
	Height    int
	Quality   byte // 0-7
	EOI       bool
	Subsample byte // 0=2x2, 1=1x2, 2=2x1, 3=1x1
	MCUOffset byte
	MCUIndex  uint16
	Payload   []byte // 载荷（不含头/CRC/RS）
}

// decodeCallsign base-40 呼号解码（ssdv_fsphil.c decode_callsign 语义）：
// s=0→'-'，1..10→'0'..'9'，11..13→'-'，14..39→'A'..'Z'；低 40 进制位先输出。
func decodeCallsign(code uint32) string {
	if code > 0xF423FFFF {
		return ""
	}
	var out []byte
	for code != 0 {
		s := code % 40
		var ch byte
		switch {
		case s == 0:
			ch = '-'
		case s < 11:
			ch = '0' + byte(s) - 1
		case s < 14:
			ch = '-'
		default:
			ch = 'A' + byte(s) - 14
		}
		out = append(out, ch)
		code /= 40
	}
	return string(out)
}

// ParsePacket 解析单个 256 字节包（不含 RS 纠错；假设已纠错）。
func ParsePacket(pkt []byte) (*PacketInfo, error) {
	if len(pkt) != PacketSize {
		return nil, fmt.Errorf("ssdv: 包长度 %d != %d", len(pkt), PacketSize)
	}
	if pkt[0] != SyncByte {
		return nil, fmt.Errorf("ssdv: 同步字节错误 %#02x", pkt[0])
	}
	typ := pkt[1]
	var payloadLen int
	switch typ {
	case TypeNormal:
		payloadLen = PayloadFEC
	case TypeNoFEC:
		payloadLen = PayloadNoFEC
	default:
		return nil, fmt.Errorf("ssdv: 未知包类型 %#02x", typ)
	}

	crcData := pkt[1 : 1+PacketHeaderLen-1+payloadLen]
	want := binary.BigEndian.Uint32(pkt[1+PacketHeaderLen-1+payloadLen:])
	if crc32IEEE(crcData) != want {
		return nil, fmt.Errorf("ssdv: CRC 校验失败")
	}

	flags := pkt[11]
	p := &PacketInfo{
		Type:      typ,
		Callsign:  decodeCallsign(uint32(pkt[2])<<24 | uint32(pkt[3])<<16 | uint32(pkt[4])<<8 | uint32(pkt[5])),
		ImageID:   pkt[6],
		PacketID:  binary.BigEndian.Uint16(pkt[7:9]),
		Width:     int(pkt[9]) * 16,
		Height:    int(pkt[10]) * 16,
		Quality:   (flags >> 3) & 7,
		EOI:       flags&4 != 0,
		Subsample: flags & 3,
		MCUOffset: pkt[12],
		MCUIndex:  binary.BigEndian.Uint16(pkt[13:15]),
		Payload:   append([]byte(nil), pkt[PacketHeaderLen:PacketHeaderLen+payloadLen]...),
	}
	// quality 在 flags 中 XOR 4 存储
	p.Quality ^= 4
	return p, nil
}

// ScanPackets 在字节流中扫描 SSDV 包：0x55 同步 + RS 纠错 + CRC 校验。
// 返回按序合法包列表。RS 纠错在原位进行（pkt 为拷贝）。
func ScanPackets(data []byte) []*PacketInfo {
	var out []*PacketInfo
	i := 0
	for i+PacketSize <= len(data) {
		if data[i] != SyncByte || (data[i+1] != TypeNormal && data[i+1] != TypeNoFEC) {
			i++
			continue
		}
		pkt := append([]byte(nil), data[i:i+PacketSize]...)
		if pkt[1] == TypeNormal {
			if DecodeRS8(pkt[1:]) < 0 {
				i++
				continue // 不可纠
			}
		}
		p, err := ParsePacket(pkt)
		if err != nil {
			i++
			continue
		}
		out = append(out, p)
		i += PacketSize
	}
	return out
}
