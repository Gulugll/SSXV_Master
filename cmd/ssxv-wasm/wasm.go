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

	img, err := pipeline.DecodeWAVBytes(wav)
	if err != nil {
		return fail("E_DEMOD: " + err.Error())
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return fail("E_INTERNAL: png encode: " + err.Error())
	}
	lastPNG = buf.Bytes()
	b := img.Bounds()
	return map[string]any{
		"ok":     true,
		"w":      b.Dx(),
		"h":      b.Dy(),
		"pngLen": len(lastPNG),
	}
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
