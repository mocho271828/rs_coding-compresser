package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"syscall/js" // WebAssemblyのため

	"golang.org/x/text/encoding/japanese"
)

// --- グローバル変数, 定数, 構造体定義 ---

const maxCharCount = 9 // 型番1, 漢字モードの最大文字数

// GF(2^8) のためのテーブル
var expTable [256]int
var logTable [256]int

const primitivePolynomial = 0x11D // 原始多項式: x^8 + x^4 + x^3 + x^2 + 1

// マスクパターン (パターン番号3)
const maskPatternHex = "99 99 99 66 66 66 99 99 99 66 66 66 99 99 99 96 66 99 96 66 66 66 99 99 66 99"

var maskPatternBytes []byte

// --- 構造体定義 (JSON出力用にタグを追加) ---

type TemplateData struct {
	KanjiInput   string                   `json:"KanjiInput"`
	Results      []KanjiCompressionResult `json:"Results"`
	Intermediate QRCodeIntermediateData   `json:"Intermediate"`
	Error        string                   `json:"Error"`
	MaxCharCount int                      `json:"MaxCharCount"`
}

type KanjiCompressionResult struct {
	Kanji          string `json:"Kanji"`
	ShiftJISCode   string `json:"ShiftJISCode"`
	SubtractedCode string `json:"SubtractedCode"`
	CompressedHex  string `json:"CompressedHex"`
	Binary13Bit    string `json:"Binary13Bit"`
}

type QRCodeIntermediateData struct {
	ModeIndicator             string `json:"ModeIndicator"`
	CharCountIndicator        string `json:"CharCountIndicator"`
	ConcatenatedBinary        string `json:"ConcatenatedBinary"`
	TerminatedBinary          string `json:"TerminatedBinary"`
	PaddedBinaryBlocks        string `json:"PaddedBinaryBlocks"`
	PaddedHex                 string `json:"PaddedHex"`
	PaddedBinary              string `json:"PaddedBinary"`
	DataPolynomial            string `json:"DataPolynomial"`
	ErrorCorrectionPolynomial string `json:"ErrorCorrectionPolynomial"`
	CodewordPolynomial        string `json:"CodewordPolynomial"`
	CodewordHex               string `json:"CodewordHex"`
	CodewordBinary            string `json:"CodewordBinary"`
	MaskPatternHex            string `json:"MaskPatternHex"`
	MaskedCodewordHex         string `json:"MaskedCodewordHex"`
	MaskedCodewordBinary      string `json:"MaskedCodewordBinary"`
}

// --- main関数 (Wasmエントリーポイント) ---

func main() {
	initGF()
	initMaskPattern()

	js.Global().Set("generateDataCodewords", js.FuncOf(generateDataCodewordsWrapper))
	js.Global().Set("applyEcc", js.FuncOf(applyEccWrapper))
	js.Global().Set("applyMask", js.FuncOf(applyMaskWrapper))

	<-make(chan bool)
}

// generateDataCodewordsWrapper は STEP1-2 を行う
func generateDataCodewordsWrapper(this js.Value, args []js.Value) interface{} {
	if len(args) != 1 {
		return createErrorResponse("Invalid number of arguments")
	}
	data, err := processStep1To2(args[0].String())
	if err != nil {
		return createErrorResponse(err.Error())
	}
	responseBytes, _ := json.Marshal(data)
	return string(responseBytes)
}

// applyEccWrapper は STEP3 を行う
func applyEccWrapper(this js.Value, args []js.Value) interface{} {
	if len(args) != 1 {
		return createErrorResponse("Invalid number of arguments")
	}
	// 引数を2進数文字列として受け取る
	data, err := processStep3(args[0].String())
	if err != nil {
		return createErrorResponse(err.Error())
	}
	responseBytes, _ := json.Marshal(data)
	return string(responseBytes)
}

// applyMaskWrapper は STEP4 を行う
func applyMaskWrapper(this js.Value, args []js.Value) interface{} {
	if len(args) != 1 {
		return createErrorResponse("Invalid number of arguments")
	}
	// 引数を2進数文字列として受け取る
	data, err := processStep4(args[0].String())
	if err != nil {
		return createErrorResponse(err.Error())
	}
	responseBytes, _ := json.Marshal(data)
	return string(responseBytes)
}

// createErrorResponse はエラー情報を含むJSON文字列を作成する
func createErrorResponse(message string) string {
	errorData := TemplateData{Error: message}
	responseBytes, _ := json.Marshal(errorData)
	return string(responseBytes)
}

// processStep1To2 は漢字入力からデータコード語を生成する (STEP 1-2)
func processStep1To2(kanjiInput string) (TemplateData, error) {
	data := TemplateData{MaxCharCount: maxCharCount, KanjiInput: kanjiInput}
	runes := []rune(kanjiInput)

	if len(runes) == 0 {
		return data, fmt.Errorf("漢字が入力されていません.")
	}
	if len(runes) > maxCharCount {
		return data, fmt.Errorf("文字数が多すぎます. %d文字以下で入力してください.", maxCharCount)
	}

	results, err := compressKanjiString(kanjiInput)
	if err != nil {
		return data, fmt.Errorf("圧縮処理中にエラーが発生しました: %v", err)
	}
	data.Results = results

	var binaryBuilder strings.Builder
	for _, res := range results {
		binaryBuilder.WriteString(res.Binary13Bit)
	}

	modeIndicator := "1000"
	charCountIndicator := fmt.Sprintf("%08b", len(runes))
	initialBitStream := modeIndicator + charCountIndicator + binaryBuilder.String()
	terminatedBitStream := initialBitStream

	if len(terminatedBitStream)+4 <= 19*8 {
		terminatedBitStream += "0000"
	}

	data.Intermediate.ModeIndicator = modeIndicator
	data.Intermediate.CharCountIndicator = charCountIndicator
	data.Intermediate.ConcatenatedBinary = binaryBuilder.String()
	data.Intermediate.TerminatedBinary = terminatedBitStream

	paddedStream := terminatedBitStream
	if len(paddedStream)%8 != 0 {
		paddedStream += strings.Repeat("0", 8-len(paddedStream)%8)
	}
	var paddedBinaryBlocks []string
	for i := 0; i < len(paddedStream); i += 8 {
		paddedBinaryBlocks = append(paddedBinaryBlocks, paddedStream[i:i+8])
	}
	data.Intermediate.PaddedBinaryBlocks = strings.Join(paddedBinaryBlocks, " ")

	dataBytes := bitStreamToBytes(paddedStream)
	paddingBytes := []byte{0xEC, 0x11}
	paddingIndex := 0
	for len(dataBytes) < 19 {
		dataBytes = append(dataBytes, paddingBytes[paddingIndex])
		paddingIndex = (paddingIndex + 1) % 2
	}
	data.Intermediate.PaddedHex = formatBytesToHex(dataBytes)
	data.Intermediate.PaddedBinary = formatBytesToBinary(dataBytes)

	return data, nil
}

// processStep3 はデータコード語(2進数)からRS符号化を行う (STEP 3)
func processStep3(dataCodewordsBinary string) (TemplateData, error) {
	dataBytes, err := binaryStringToBytes(dataCodewordsBinary)
	if err != nil {
		return TemplateData{}, fmt.Errorf("データコード語の2進数文字列の解析に失敗しました: %v", err)
	}
	if len(dataBytes) != 19 {
		return TemplateData{}, fmt.Errorf("データコード語は19バイトである必要がありますが, %dバイトでした.", len(dataBytes))
	}

	dataPoly := bytesToInts(dataBytes)
	generatorPoly := getGeneratorPolynomial(7)
	remainderPoly := polyDiv(polyLeftShift(dataPoly, 7), generatorPoly)
	codewordPoly := polyAdd(polyLeftShift(dataPoly, 7), remainderPoly)
	codewordBytes := intsToBytes(codewordPoly)

	var data TemplateData
	data.Intermediate.PaddedHex = formatBytesToHex(dataBytes)
	data.Intermediate.PaddedBinary = formatBytesToBinary(dataBytes)
	data.Intermediate.DataPolynomial = formatPolynomial(dataPoly, "d")
	data.Intermediate.ErrorCorrectionPolynomial = formatPolynomial(remainderPoly, "r")
	data.Intermediate.CodewordPolynomial = formatPolynomial(codewordPoly, "c")
	data.Intermediate.CodewordHex = formatBytesToHex(codewordBytes)
	data.Intermediate.CodewordBinary = formatBytesToBinary(codewordBytes)

	return data, nil
}

// processStep4 は符号語(2進数)にマスク処理を行う (STEP 4)
func processStep4(codewordBinary string) (TemplateData, error) {
	codewordBytes, err := binaryStringToBytes(codewordBinary)
	if err != nil {
		return TemplateData{}, fmt.Errorf("符号語の2進数文字列の解析に失敗しました: %v", err)
	}
	if len(codewordBytes) != 26 {
		return TemplateData{}, fmt.Errorf("符号語は26バイトである必要がありますが, %dバイトでした.", len(codewordBytes))
	}

	maskedBytes := make([]byte, len(codewordBytes))
	for i := range codewordBytes {
		maskedBytes[i] = codewordBytes[i] ^ maskPatternBytes[i]
	}

	var data TemplateData
	// マスク適用前のデータも返す
	data.Intermediate.CodewordHex = formatBytesToHex(codewordBytes)
	data.Intermediate.CodewordBinary = formatBytesToBinary(codewordBytes)
	data.Intermediate.MaskPatternHex = maskPatternHex
	data.Intermediate.MaskedCodewordHex = formatBytesToHex(maskedBytes)
	data.Intermediate.MaskedCodewordBinary = formatBytesToBinary(maskedBytes)

	return data, nil
}

// --- 初期化 ---
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

func initMaskPattern() {
	bytes, err := hexStringToBytes(maskPatternHex)
	if err != nil {
		panic(fmt.Sprintf("固定マスクパターンの初期化に失敗しました: %v", err))
	}
	maskPatternBytes = bytes
}

// --- 漢字圧縮関連 ---
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

// --- GF(2^8)および多項式演算 ---
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

	// p1の係数をコピー
	copy(result, p1)

	// p2の係数を加算(XOR)
	offset := len(result) - len(p2)
	for i := 0; i < len(p2); i++ {
		result[offset+i] ^= p2[i]
	}
	return result
}
func polyLeftShift(p []int, count int) []int {
	result := make([]int, len(p)+count)
	copy(result, p)
	return result
}
func polyDiv(dividend []int, divisor []int) []int {
	result := make([]int, len(dividend))
	copy(result, dividend)
	divLen := len(divisor)
	resLen := len(result)

	for i := 0; i <= resLen-divLen; i++ {
		coeff := result[i]
		if coeff == 0 {
			continue
		}
		// QRコードの生成多項式の最高次係数は常に1なので, 逆元の計算は不要
		factor := coeff
		for j := 0; j < divLen; j++ {
			result[i+j] ^= gfMul(divisor[j], factor)
		}
	}
	// 剰余部分を返す
	return result[resLen-divLen+1:]
}
func getGeneratorPolynomial(degree int) []int {
	// pは計算過程では低次の係数から格納される. 初期値は g(x) = 1.
	p := []int{1}

	for i := 0; i < degree; i++ {
		// p(x) * (x + α^i) を計算する.
		nextP := make([]int, len(p)+1)
		alphaI := expTable[i]

		// p(x) * α^i の項を計算
		for j := 0; j < len(p); j++ {
			nextP[j] = gfMul(p[j], alphaI)
		}
		// p(x) * x の項を加算 (pの各係数を1つ後ろにずらす)
		for j := 0; j < len(p); j++ {
			nextP[j+1] ^= p[j]
		}
		p = nextP
	}

	for i, j := 0, len(p)-1; i < j; i, j = i+1, j-1 {
		p[i], p[j] = p[j], p[i]
	}

	return p
}

// --- ヘルパー関数 ---
func bitStreamToBytes(s string) []byte {
	var b []byte
	for i := 0; i < len(s); i += 8 {
		val, _ := strconv.ParseUint(s[i:i+8], 2, 8)
		b = append(b, byte(val))
	}
	return b
}

func binaryStringToBytes(binaryStr string) ([]byte, error) {
	cleanedBinary := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, binaryStr)

	if len(cleanedBinary)%8 != 0 {
		return nil, fmt.Errorf("2進数文字列の長さが8の倍数ではありません")
	}

	var decoded []byte
	for i := 0; i < len(cleanedBinary); i += 8 {
		val, err := strconv.ParseUint(cleanedBinary[i:i+8], 2, 8)
		if err != nil {
			return nil, fmt.Errorf("2進数文字列のパースに失敗しました: %v", err)
		}
		decoded = append(decoded, byte(val))
	}
	return decoded, nil
}

func hexStringToBytes(hexStr string) ([]byte, error) {
	cleanedHex := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, hexStr)

	if len(cleanedHex)%2 != 0 {
		return nil, fmt.Errorf("16進数文字列の長さが奇数です")
	}
	decoded, err := hex.DecodeString(cleanedHex)
	if err != nil {
		return nil, fmt.Errorf("16進数文字列のデコードに失敗しました: %v", err)
	}
	return decoded, nil
}
func formatBytesToHex(data []byte) string {
	var hexParts []string
	for _, b := range data {
		hexParts = append(hexParts, fmt.Sprintf("%02X", b))
	}
	return strings.Join(hexParts, " ")
}
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
	for i := 0; i < len(p); i++ {
		coeff := p[i]
		power := len(p) - 1 - i
		if coeff == 0 {
			continue
		}

		if !isFirstTerm {
			b.WriteString(" + ")
		}
		isFirstTerm = false

		// 係数が1の場合はαの表記を省略 (ただし定数項を除く)
		if coeff > 1 {
			b.WriteString(fmt.Sprintf("\\alpha^{%d}", logTable[coeff]))
		}

		if power > 0 {
			if coeff > 1 {
				b.WriteString(" \\cdot ") // 係数と変数の間にドットを追加
			}
			b.WriteString(fmt.Sprintf("%s", varName))
			if power > 1 {
				b.WriteString(fmt.Sprintf("^{%d}", power))
			}
		} else { // 定数項
			if coeff == 1 {
				b.WriteString("1")
			}
		}
	}
	if isFirstTerm {
		return "0"
	}
	return b.String()
}
