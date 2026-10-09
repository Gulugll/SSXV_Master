// ssxv-cli —— SSTV/SSDV 解码命令行工具（TECH_SPEC §6）。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ssxv/internal/pipeline"
	"ssxv/internal/source"
	"ssxv/internal/ssdv"
)

const exitOK = 0

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		usage()
		return 1
	}
	switch os.Args[1] {
	case "decode":
		return cmdDecode(os.Args[2:])
	case "info":
		return cmdInfo(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n", os.Args[1])
		usage()
		return 1
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ssxv-cli —— SSTV/SSDV 解码工具

用法:
  ssxv-cli decode <file.wav> [--mode auto|Robot36] [--out dir] [--json]
  ssxv-cli decode <file.cu8|cs16|cf32|iq> [--iq-fs 48000] [--iq-if 0=auto]
  ssxv-cli info <file.wav>

退出码: 0 成功 | 1 用法错误 | 2 输入解析失败 | 3 解码失败
`)
}

type decodeArgs struct {
	file string
	mode string
	out  string
	json bool
	iqFs int
	iqIf float64
}

func parseDecode(args []string) (*decodeArgs, error) {
	a := &decodeArgs{}
	fs := flag.NewFlagSet("decode", flag.ContinueOnError)
	fs.StringVar(&a.mode, "mode", "auto", "解码模式: auto | Robot36")
	fs.StringVar(&a.out, "out", "out", "输出目录")
	fs.BoolVar(&a.json, "json", false, "事件以 NDJSON 输出到 stdout")
	fs.IntVar(&a.iqFs, "iq-fs", 48000, "IQ 记录采样率（.cu8/.cs16/.cf32/.iq 输入时）")
	fs.Float64Var(&a.iqIf, "iq-if", 0, "IQ 记录 IF 偏移 Hz（0=自动估计）")
	if err := fs.Parse(splitFlags(args)); err != nil {
		return nil, err
	}
	if fs.NArg() != 1 {
		return nil, fmt.Errorf("decode 需要恰好一个输入文件")
	}
	a.file = fs.Arg(0)
	return a, nil
}

// splitFlags 把 flags 移到前面、位置参数放到末尾（支持 flag 与文件名混排）。
func splitFlags(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			// 带值 flag：--out dir / --mode auto / --iq-fs 48000 / --iq-if 6000
			switch args[i] {
			case "--out", "-out", "--mode", "-mode", "--iq-fs", "-iq-fs", "--iq-if", "-iq-if":
				if i+1 < len(args) {
					i++
					flags = append(flags, args[i])
				}
			}
			continue
		}
		pos = append(pos, args[i])
	}
	return append(flags, pos...)
}

func cmdDecode(args []string) int {
	a, err := parseDecode(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	raw, err := os.ReadFile(a.file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "输入解析失败:", err)
		return 2
	}
	t0 := time.Now()

	// .ssdv 文件（已解调的包流）：直接包解析 + JPEG 重组
	var res pipeline.DecodeResult
	if strings.EqualFold(filepath.Ext(a.file), ".ssdv") {
		jpgBytes, info, derr := ssdv.DecodeSSDV(raw)
		if derr != nil {
			fmt.Fprintln(os.Stderr, "解码失败:", derr)
			return 3
		}
		img, derr := jpeg.Decode(bytes.NewReader(jpgBytes))
		if derr != nil {
			fmt.Fprintln(os.Stderr, "JPEG 解码失败:", derr)
			return 3
		}
		res = pipeline.DecodeResult{Image: img, Mode: "SSDV " + info.Callsign}
	} else if fmtIQ, isIQ := iqFormat(a.file); isIQ {
		// IQ 基带记录（IF 偏移语义，TECH_SPEC §2.1；仅 SSTV 链路）
		z, derr := pipeline.ParseIQ(raw, fmtIQ)
		if derr != nil {
			fmt.Fprintln(os.Stderr, "输入解析失败:", derr)
			return 2
		}
		res, err = pipeline.DecodeIQResult(z, a.iqFs, a.iqIf)
		if err != nil {
			if a.json {
				emitJSON(map[string]any{"t": "error", "code": "E_DEMOD", "msg": err.Error()})
			}
			fmt.Fprintln(os.Stderr, "解码失败:", err)
			return 3
		}
	} else {
		res, err = pipeline.DecodeWAVResultMode(raw, a.mode)
		if err != nil {
			if a.json {
				emitJSON(map[string]any{"t": "error", "code": "E_DEMOD", "msg": err.Error()})
			}
			fmt.Fprintln(os.Stderr, "解码失败:", err)
			return 3
		}
	}
	elapsed := time.Since(t0)

	if err := os.MkdirAll(a.out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "创建输出目录失败:", err)
		return 2
	}
	base := strings.TrimSuffix(filepath.Base(a.file), filepath.Ext(a.file))
	outPath := filepath.Join(a.out, base+".png")
	f, err := os.Create(outPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "写文件失败:", err)
		return 2
	}
	if err := png.Encode(f, res.Image); err != nil {
		f.Close()
		fmt.Fprintln(os.Stderr, "PNG 编码失败:", err)
		return 2
	}
	f.Close()

	if a.json {
		emitJSON(map[string]any{"t": "done", "file": outPath, "ms": elapsed.Milliseconds()})
	} else {
		b := res.Image.Bounds()
		fmt.Printf("已解码: %s [%s] (%dx%d, %dms)\n%s\n",
			outPath, res.Mode, b.Dx(), b.Dy(), elapsed.Milliseconds(), outPath)
	}
	return exitOK
}

// iqFormat 按扩展名识别 IQ 裸格式。
func iqFormat(file string) (string, bool) {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".cu8":
		return "cu8", true
	case ".cs16":
		return "cs16", true
	case ".cf32", ".iq":
		return "cf32", true
	}
	return "", false
}

func cmdInfo(args []string) int {
	if len(args) != 1 {
		usage()
		return 1
	}
	wav, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	pcm, fs, err := source.ReadWav(wav)
	if err != nil {
		fmt.Fprintln(os.Stderr, "输入解析失败:", err)
		return 2
	}
	fmt.Printf("文件: %s\n采样率: %d Hz\n样本数: %d (%.2fs)\n声道: 已混音单声道\n",
		args[0], fs, len(pcm), float64(len(pcm))/float64(fs))
	return 0
}

func emitJSON(m map[string]any) {
	b, _ := json.Marshal(m)
	fmt.Println(string(b))
}
