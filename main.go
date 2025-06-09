// main.go

package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall/js" // WebAssemblyのために必須

	"golang.org/x/text/encoding/japanese"
)

// --- グローバル変数と構造体定義 (変更なし) ---
var expTable [256]int
var logTable [256]int

const primitivePolynomial = 0x11D

var generatorPolyExponents = []int{87, 229, 146, 149, 238, 102, 21}

const maxCharCount = 9

type KanjiCompressionResult struct {
	Kanji          string `json:"kanji"`
	ShiftJISCode   string `json:"shiftJisCode"`
	SubtractedCode string `json:"subtractedCode"`
	CompressedHex  string `json:"compressedHex"`
	Binary13Bit    string `json:"binary13Bit"`
}

type QRCodeIntermediateData struct {
	ModeIndicator             string `json:"modeIndicator"`
	CharCountIndicator        string `json:"charCountIndicator"`
	ConcatenatedBinary        string `json:"concatenatedBinary"`
	TerminatedBinary          string `json:"terminatedBinary"`
	PaddedBinaryBlocks        string `json:"paddedBinaryBlocks"`
	PaddedHex                 string `json:"paddedHex"`
	DataPolynomial            string `json:"dataPolynomial"`
	ErrorCorrectionPolynomial string `json:"errorCorrectionPolynomial"`
	CodewordPolynomial        string `json:"codewordPolynomial"`
	CodewordHex               string `json:"codewordHex"`
	CodewordBinary            string `json:"codewordBinary"`
}

// --- メインロジック ---

func main() {
	// GF(2^8) テーブルの初期化
	initGF()

	// "processKanji"という名前でGoの関数をJavaScript側に公開
	js.Global().Set("processKanji", js.FuncOf(processKanji))

	// Goのプログラムが終了しないように待機
	<-make(chan struct{})
}

// processKanji はJavaScriptから呼び出されるラッパー関数です.
// 引数として入力文字列を受け取り、結果をJavaScriptオブジェクトとして返します.
func processKanji(this js.Value, args []js.Value) any {
	// 引数が1つでない場合はエラーを返す
	if len(args) != 1 {
		return map[string]any{
			"error": "Invalid number of arguments passed",
		}
	}
	// 文字列入力を取得
	kanjiInput := args[0].String()
	runes := []rune(kanjiInput)

	// 入力チェック
	if len(runes) == 0 {
		return map[string]any{"error": "漢字が入力されていません."}
	}
	if len(runes) > maxCharCount {
		return map[string]any{"error": fmt.Sprintf("文字数が多すぎます. %d文字以下で入力してください.", maxCharCount)}
	}

	// 以前のprocessKanjiHandlerのロジックをここに移植
	// STEP 1-1: 13ビット圧縮
	results, err := compressKanjiString(kanjiInput)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("圧縮処理中にエラーが発生しました: %v", err)}
	}

	var binaryBuilder strings.Builder
	for _, res := range results {
		binaryBuilder.WriteString(res.Binary13Bit)
	}

	var intermediate QRCodeIntermediateData
	intermediate.ModeIndicator = "1000"
	intermediate.CharCountIndicator = fmt.Sprintf("%08b", len(runes))
	intermediate.ConcatenatedBinary = binaryBuilder.String()

	initialBitStream := intermediate.ModeIndicator + intermediate.CharCountIndicator + intermediate.ConcatenatedBinary
	terminatedBitStream := initialBitStream + "0000"
	intermediate.TerminatedBinary = terminatedBitStream

	paddedStream := terminatedBitStream
	if len(paddedStream)%8 != 0 {
		paddedStream += strings.Repeat("0", 8-len(paddedStream)%8)
	}
	var paddedBinaryBlocks []string
	for i := 0; i < len(paddedStream); i += 8 {
		paddedBinaryBlocks = append(paddedBinaryBlocks, paddedStream[i:i+8])
	}
	intermediate.PaddedBinaryBlocks = strings.Join(paddedBinaryBlocks, " ")

	dataBytes := bitStreamToBytes(paddedStream)
	for len(dataBytes) < 19 {
		if len(dataBytes)%2 == 1 {
			dataBytes = append(dataBytes, 0xEC)
		} else {
			dataBytes = append(dataBytes, 0x11)
		}
	}
	intermediate.PaddedHex = formatBytesToHex(dataBytes)

	dataPoly := bytesToInts(dataBytes)
	generatorPoly := getGeneratorPolynomial(7)
	remainderPoly := polyDiv(polyLeftShift(dataPoly, 7), generatorPoly)

	codewordPoly := polyAdd(polyLeftShift(dataPoly, 7), remainderPoly)
	codewordBytes := intsToBytes(codewordPoly)

	intermediate.DataPolynomial = formatPolynomial(dataPoly, "d")
	intermediate.ErrorCorrectionPolynomial = formatPolynomial(remainderPoly, "r")
	intermediate.CodewordPolynomial = formatPolynomial(codewordPoly, "c")
	intermediate.CodewordHex = formatBytesToHex(codewordBytes)
	intermediate.CodewordBinary = formatBytesToBinary(codewordBytes)

	// ★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★
	// 修正点：js.ValueOfを削除し、Goのネイティブなマップを返す
	// ★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★★
	return map[string]any{
		"results":      results,
		"intermediate": intermediate,
	}
}

// --- 以下のヘルパー関数群は変更なし (ただし、HTTP関連は不要になったため削除) ---

func initGF() {
	x := 1
	for i := 0; i < 255; i++ {
		expTable[i] = x
		logTable[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= primitivePolynomial
		}
	}
	expTable[255] = 1
}

func compressKanjiString(kanjiInput string) ([]KanjiCompressionResult, error) {
	shiftJISBytes, err := convertToShiftJIS(kanjiInput)
	if err != nil {
		return nil, fmt.Errorf("Shift-JISへの変換に失敗しました: %v", err)
	}

	var results []KanjiCompressionResult
	runes := []rune(kanjiInput)
	runeIndex := 0

	for i := 0; i < len(shiftJISBytes); i += 2 {
		if i+1 >= len(shiftJISBytes) {
			break
		}
		shiftJISCode := uint16(shiftJISBytes[i])<<8 | uint16(shiftJISBytes[i+1])
		currentKanji := string(runes[runeIndex])
		result := KanjiCompressionResult{
			Kanji:        currentKanji,
			ShiftJISCode: fmt.Sprintf("%04X", shiftJISCode),
		}
		var subtractedCode uint16
		if shiftJISCode >= 0x8140 && shiftJISCode <= 0x9FFC {
			subtractedCode = shiftJISCode - 0x8140
			result.SubtractedCode = fmt.Sprintf("%04X - 8140 = %04X", shiftJISCode, subtractedCode)
		} else if shiftJISCode >= 0xE040 && shiftJISCode <= 0xEBBF {
			subtractedCode = shiftJISCode - 0xC140
			result.SubtractedCode = fmt.Sprintf("%04X - C140 = %04X", shiftJISCode, subtractedCode)
		} else {
			return nil, fmt.Errorf("'%s' (%04X) はサポート外のShift-JISコード範囲です", currentKanji, shiftJISCode)
		}
		upperByte := (subtractedCode >> 8) & 0xFF
		lowerByte := subtractedCode & 0xFF
		compressedValue := uint16(upperByte)*0xC0 + lowerByte
		result.CompressedHex = fmt.Sprintf("%04X", compressedValue)
		result.Binary13Bit = fmt.Sprintf("%013b", compressedValue)
		results = append(results, result)
		runeIndex++
	}
	return results, nil
}

func convertToShiftJIS(s string) ([]byte, error) {
	encoder := japanese.ShiftJIS.NewEncoder()
	return encoder.Bytes([]byte(s))
}

func gfMul(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	return expTable[(logTable[a]+logTable[b])%255]
}

func polyAdd(p1, p2 []int) []int {
	maxLen := len(p1)
	if len(p2) > maxLen {
		maxLen = len(p2)
	}
	result := make([]int, maxLen)
	for i := 0; i < maxLen; i++ {
		val1, val2 := 0, 0
		if i < len(p1) {
			val1 = p1[i]
		}
		if i < len(p2) {
			val2 = p2[i]
		}
		result[i] = val1 ^ val2
	}
	return result
}

func polyLeftShift(p []int, count int) []int {
	result := make([]int, len(p)+count)
	copy(result[count:], p)
	return result
}

func polyDiv(dividend []int, divisor []int) []int {
	result := make([]int, len(dividend))
	copy(result, dividend)
	for i := len(result) - 1; i >= len(divisor)-1; i-- {
		coeff := result[i]
		if coeff == 0 {
			continue
		}
		logCoeff := logTable[coeff]
		for j := 0; j < len(divisor); j++ {
			term := gfMul(expTable[(logCoeff-logTable[divisor[len(divisor)-1-j]]+255)%255], divisor[len(divisor)-1-j])
			result[i-j] ^= term
		}
	}
	return result[:len(divisor)-1]
}

func getGeneratorPolynomial(degree int) []int {
	coeffs := make([]int, degree+1)
	coeffs[degree] = 1
	for i, exp := range generatorPolyExponents {
		coeffs[degree-1-i] = expTable[exp]
	}
	return coeffs
}

func bitStreamToBytes(s string) []byte {
	var b []byte
	for i := 0; i < len(s); i += 8 {
		val, _ := strconv.ParseUint(s[i:i+8], 2, 8)
		b = append(b, byte(val))
	}
	return b
}

func formatBytesToHex(data []byte) string {
	var hexParts []string
	for _, b := range data {
		hexParts = append(hexParts, fmt.Sprintf("%02X", b))
	}
	return strings.Join(hexParts, " ")
}

// formatBytesToBinary はバイトスライスを2進数文字列にフォーマットします。
func formatBytesToBinary(data []byte) string {
	var binParts []string
	for _, b := range data {
		binParts = append(binParts, fmt.Sprintf("%08b", b))
	}
	return strings.Join(binParts, " ")
}

func bytesToInts(b []byte) []int {
	ints := make([]int, len(b))
	for i, v := range b {
		ints[i] = int(v)
	}
	return ints
}

func intsToBytes(i []int) []byte {
	bytes := make([]byte, len(i))
	for j, v := range i {
		bytes[j] = byte(v)
	}
	return bytes
}

func formatPolynomial(p []int, varName string) string {
	var b strings.Builder
	isFirstTerm := true
	for i := len(p) - 1; i >= 0; i-- {
		coeff := p[i]
		if coeff == 0 {
			continue
		}
		if !isFirstTerm {
			b.WriteString(" + ")
		}
		isFirstTerm = false
		if coeff > 1 {
			b.WriteString(fmt.Sprintf("\\alpha^{%d}", logTable[coeff]))
		}
		if i > 0 {
			b.WriteString(fmt.Sprintf("%s", varName))
			if i > 1 {
				b.WriteString(fmt.Sprintf("^{%d}", i))
			}
		} else if coeff == 1 {
			b.WriteString("1")
		}
	}
	if isFirstTerm {
		return "0"
	}
	return b.String()
}
