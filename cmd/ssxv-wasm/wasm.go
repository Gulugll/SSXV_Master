//go:build js && wasm

// ssxv-wasm 把解码核心暴露给 JS（GOOS=js GOARCH=wasm）。
// ABI：bytes-in / bytes-out + JSON 事件（TECH_SPEC §5）。
// M1 范围：文件解码（同步，运行于 Web Worker 内）；实时流为 M4。
//
// 注册的全局函数：
//
//	ssxvVersion() → number                      // ABI 版本
//	ssxvDecodeWav(Uint8Array wav) → object      // {ok, w, h, ms, pngLen} 或 {ok:false, error}
//	ssxvGetImage(Uint8Array dst) → number       // 拷贝最近一次解码的 PNG 字节，返回实际长度
//	ssxvDecodePCM(Int16Array pcm, sampleRate) → object  // 实时链路：int16 PCM 快照解码（M4）
package main

import (
	"bytes"
	"image/png"
	"syscall/js"

	"ssxv/internal/pipeline"
)

const abiVersion = 1

var lastPNG []byte

func init() {
	g := js.Global()
	g.Set("ssxvVersion", js.FuncOf(func(this js.Value, args []js.Value) any {
		return abiVersion
	}))
	g.Set("ssxvDecodeWav", js.FuncOf(decodeWav))
	g.Set("ssxvGetImage", js.FuncOf(getImage))
	g.Set("ssxvDecodePCM", js.FuncOf(decodePCM))
}

// encodeResult 把解码结果编码为 PNG 并填 lastPNG，返回给 JS 的结果对象。
func encodeResult(res pipeline.DecodeResult) map[string]any {
	var buf bytes.Buffer
	if err := png.Encode(&buf, res.Image); err != nil {
		return map[string]any{"ok": false, "error": "E_INTERNAL: png encode: " + err.Error()}
	}
	lastPNG = buf.Bytes()
	b := res.Image.Bounds()
	return map[string]any{
		"ok":     true,
		"w":      b.Dx(),
		"h":      b.Dy(),
		"mode":   res.Mode,
		"pngLen": len(lastPNG),
	}
}

func decodeWav(this js.Value, args []js.Value) any {
	fail := func(msg string) any {
		return map[string]any{"ok": false, "error": msg}
	}
	if len(args) < 1 || args[0].Type() != js.TypeObject {
		return fail("E_ARG: 需要 Uint8Array 参数")
	}
	wav := make([]byte, args[0].Get("byteLength").Int())
	js.CopyBytesToGo(wav, args[0])

	res, err := pipeline.DecodeWAVResult(wav)
	if err != nil {
		return fail("E_DEMOD: " + err.Error())
	}
	return encodeResult(res)
}

// decodePCM 实时链路：int16 PCM + 采样率快照解码（输入在原生采样率处理，
// 见 pipeline.DecodePCM 注释；48k 麦克风直推即可，无需前端重采样）。
func decodePCM(this js.Value, args []js.Value) any {
	fail := func(msg string) any {
		return map[string]any{"ok": false, "error": msg}
	}
	if len(args) < 2 || args[0].Type() != js.TypeObject {
		return fail("E_ARG: 需要 Int16Array 与 sampleRate 参数")
	}
	src := args[0]
	n := src.Get("length").Int()
	pcm := make([]int16, n)
	// 经 Uint8Array 视图拷出原始小端字节，再拼 int16
	u8 := js.Global().Get("Uint8Array").New(src.Get("buffer"), src.Get("byteOffset"), src.Get("byteLength"))
	buf := make([]byte, u8.Get("byteLength").Int())
	js.CopyBytesToGo(buf, u8)
	for i := 0; i < n; i++ {
		pcm[i] = int16(uint16(buf[2*i]) | uint16(buf[2*i+1])<<8)
	}
	fs := args[1].Int()

	res, err := pipeline.DecodePCMResult(pcm, fs)
	if err != nil {
		return fail("E_DEMOD: " + err.Error())
	}
	return encodeResult(res)
}

func getImage(this js.Value, args []js.Value) any {
	if len(args) < 1 || args[0].Type() != js.TypeObject {
		return 0
	}
	dst := args[0]
	n := dst.Get("byteLength").Int()
	if n > len(lastPNG) {
		n = len(lastPNG)
	}
	js.CopyBytesToJS(dst, lastPNG[:n])
	return n
}

// main 注册全局函数后阻塞；解码状态保持在本包变量中。
func main() {
	select {} // 常驻：JS 回调经 js.FuncOf 进入
}
