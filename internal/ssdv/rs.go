// Package ssdv 实现 SSDV 解码：RS(255,223) 纠错、包解析、JPEG 重组与 BPSK 解调。
// 规格见 docs/TECH_SPEC.md §4；RS 语义以 Phil Karn rs8.c（docs/ref/rs8.c）为唯一基准。
package ssdv

// gfTable GF(256) 运算表，运行时生成本原多项式 0x11D（与 rs8.c 硬编码表一致，
// 一致性由 rs_test.go TestGFTablesMatchCReference 保证）。
type gfTable struct {
	alphaTo [256]byte
	indexOf [256]byte
	genPoly [33]byte
}

var gf = newGF()

func newGF() *gfTable {
	// 重要：rs8.c（SSDV 所用变体）为 CCSDS 对偶基表示——ALPHA_TO/INDEX_OF
	// 并非常规本原幂次表（INDEX_OF[3]=0x63 而非 0x19），FCR=112/PRIM=11
	// 的组合只在对偶基下成立。因此直接采用从 docs/ref/rs8.c 提取的
	// 硬编码表（rs_tables.go），与 C 实现逐字节一致，不做运行时生成。
	return &gfTable{alphaTo: cAlphaTo, indexOf: cIndexOf, genPoly: cGenPoly}
}

func mod255(x int) int {
	for x >= 255 {
		x -= 255
		x = (x >> 8) + (x & 0xFF)
	}
	return x
}

func (g *gfTable) mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return g.alphaTo[mod255(int(g.indexOf[a])+int(g.indexOf[b]))]
}

func (g *gfTable) div(a, b byte) byte {
	if a == 0 {
		return 0
	}
	return g.alphaTo[mod255(int(g.indexOf[a])+rsNN-int(g.indexOf[b]))]
}

// EncodeRS8 计算 RS(255,223) 校验：输入 223 字节数据，输出 32 字节校验
// （rs8.c encode_rs_8 的移植，pad=0）。
func EncodeRS8(data []byte, parity []byte) {
	g := gf
	for i := range parity {
		parity[i] = 0
	}
	for i := 0; i < rsNN-rsNRoots; i++ {
		feedback := g.indexOf[data[i]^parity[0]]
		if feedback != rsA0 {
			for j := 1; j < rsNRoots; j++ {
				parity[j] ^= g.alphaTo[mod255(int(feedback)+int(g.genPoly[rsNRoots-j]))]
			}
		}
		// 移位
		copy(parity, parity[1:])
		if feedback != rsA0 {
			parity[rsNRoots-1] = g.alphaTo[mod255(int(feedback)+int(g.genPoly[0]))]
		} else {
			parity[rsNRoots-1] = 0
		}
	}
}

// DecodeRS8 纠错：data 为 255 字节（223 数据 + 32 校验，原位修正），
// 返回纠正的错误数；-1 表示不可纠（deg(λ) ≠ 根数）。
// rs8.c decode_rs_8 的移植（无擦除，pad=0）。
func DecodeRS8(data []byte) int {
	g := gf
	var s [rsNRoots]byte // 症状
	var lambda [rsNRoots + 1]byte
	var b [rsNRoots + 1]byte
	var t [rsNRoots + 1]byte
	var omega [rsNRoots + 1]byte
	var root [rsNRoots]byte
	var reg [rsNRoots + 1]byte
	var loc [rsNRoots]byte

	// 症状：S[i] = C(α^(FCR+i·PRIM))
	for i := 0; i < rsNRoots; i++ {
		s[i] = data[0]
	}
	for j := 1; j < rsNN; j++ {
		for i := 0; i < rsNRoots; i++ {
			if s[i] == 0 {
				s[i] = data[j]
			} else {
				s[i] = data[j] ^ g.alphaTo[mod255(int(g.indexOf[s[i]])+(rsFCR+i)*rsPrim)]
			}
		}
	}

	synError := 0
	for i := 0; i < rsNRoots; i++ {
		synError |= int(s[i])
		s[i] = g.indexOf[s[i]]
	}
	if synError == 0 {
		return 0 // 无错
	}

	// Berlekamp-Massey
	lambda[0] = 1
	for i := 1; i <= rsNRoots; i++ {
		lambda[i] = 0
		b[i] = g.indexOf[lambda[i]]
	}
	b[0] = g.indexOf[lambda[0]]

	el := 0
	for r := 1; r <= rsNRoots; r++ {
		discr := 0
		for i := 0; i < r; i++ {
			if lambda[i] != 0 && s[r-i-1] != rsA0 {
				discr ^= int(g.alphaTo[mod255(int(g.indexOf[lambda[i]])+int(s[r-i-1]))])
			}
		}
		discr = int(g.indexOf[byte(discr)])
		if discr == rsA0 {
			// B(x) ← x·B(x)
			copy(b[1:], b[:rsNRoots])
			b[0] = rsA0
		} else {
			// T(x) ← λ(x) - discr·x·B(x)
			t[0] = lambda[0]
			for i := 0; i < rsNRoots; i++ {
				if b[i] != rsA0 {
					t[i+1] = lambda[i+1] ^ g.alphaTo[mod255(discr+int(b[i]))]
				} else {
					t[i+1] = lambda[i+1]
				}
			}
			if 2*el <= r-1 {
				el = r - el
				// B(x) ← inv(discr)·λ(x)
				for i := 0; i <= rsNRoots; i++ {
					if lambda[i] == 0 {
						b[i] = rsA0
					} else {
						b[i] = byte(mod255(int(g.indexOf[lambda[i]]) - discr + rsNN))
					}
				}
			} else {
				copy(b[1:], b[:rsNRoots])
				b[0] = rsA0
			}
			copy(lambda[:], t[:])
		}
	}

	// λ 转指数形式，求 deg
	degLambda := 0
	for i := 0; i <= rsNRoots; i++ {
		lambda[i] = g.indexOf[lambda[i]]
		if lambda[i] != rsA0 {
			degLambda = i
		}
	}

	// Chien 搜索
	copy(reg[1:], lambda[1:rsNRoots+1])
	count := 0
	k := rsIPrim - 1
	for i := 1; i <= rsNN; i, k = i+1, mod255(k+rsIPrim) {
		q := byte(1)
		for j := degLambda; j > 0; j-- {
			if reg[j] != rsA0 {
				reg[j] = byte(mod255(int(reg[j]) + j))
				q ^= g.alphaTo[int(reg[j])]
			}
		}
		if q != 0 {
			continue
		}
		root[count] = byte(i)
		loc[count] = byte(k)
		count++
		if count == degLambda {
			break
		}
	}
	if degLambda != count {
		return -1 // 不可纠
	}

	// ω(x) = s(x)·λ(x) mod x^NROOTS
	degOmega := degLambda - 1
	for i := 0; i <= degOmega; i++ {
		tmp := 0
		for j := i; j >= 0; j-- {
			if s[i-j] != rsA0 && lambda[j] != rsA0 {
				tmp ^= int(g.alphaTo[mod255(int(s[i-j])+int(lambda[j]))])
			}
		}
		omega[i] = g.indexOf[byte(tmp)]
	}

	// Forney：错误值
	for j := count - 1; j >= 0; j-- {
		num1 := 0
		for i := degOmega; i >= 0; i-- {
			if omega[i] != rsA0 {
				num1 ^= int(g.alphaTo[mod255(int(omega[i])+i*int(root[j]))])
			}
		}
		num2 := g.alphaTo[mod255(int(root[j])*(rsFCR-1)+rsNN)]
		den := 0
		for i := minInt(degLambda, rsNRoots-1) &^ 1; i >= 0; i -= 2 {
			if lambda[i+1] != rsA0 {
				den ^= int(g.alphaTo[mod255(int(lambda[i+1])+i*int(root[j]))])
			}
		}
		// 应用错误值
		if num1 != 0 {
			data[loc[j]] ^= g.alphaTo[mod255(int(g.indexOf[byte(num1)])+int(g.indexOf[num2])+rsNN-int(g.indexOf[byte(den)]))]
		}
	}
	return count
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// crc32IEEE 标准 IEEE CRC-32（反射 0xEDB88320，init/final 0xFFFFFFFF）。
// 与 zlib/crc32 等价；Go 标准库 hash/crc32.ChecksumIEEE 同实现。
var crc32Table = func() [256]uint32 {
	var t [256]uint32
	for i := 0; i < 256; i++ {
		c := uint32(i)
		for j := 0; j < 8; j++ {
			if c&1 != 0 {
				c = (c >> 1) ^ 0xEDB88320
			} else {
				c >>= 1
			}
		}
		t[i] = c
	}
	return t
}()

func crc32IEEE(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc = (crc >> 8) ^ crc32Table[byte(crc)^b]
	}
	return crc ^ 0xFFFFFFFF
}
