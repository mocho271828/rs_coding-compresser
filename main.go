package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"syscall/js" // WebAssemblyのために必要

	"golang.org/x/text/encoding/japanese"
)

// --- グローバル変数, 定数, 構造体定義 ---

const maxCharCount = 9 // 型番1, 漢字モードの最大文字数

// GF(2^8) のためのテーブル
var expTable [256]int
var logTable [256]int

const primitivePolynomial = 0x11D // 原始多項式: x^8 + x^4 + x^3 + x^2 + 1

// RS符号の生成多項式の係数 (alphaのべき乗)
var generatorPolyExponents = []int{87, 229, 146, 149, 238, 102, 21}


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
	DataPolynomial            string `json:"DataPolynomial"`
	ErrorCorrectionPolynomial string `json:"ErrorCorrectionPolynomial"`
	CodewordPolynomial        string `json:"CodewordPolynomial"`
	CodewordHex               string `json:"CodewordHex"`
	CodewordBinary            string `json:"CodewordBinary"`
}

// --- main関数 (Wasmエントリーポイント) ---

func main() {
	// GF(2^8) テーブルを初期化
	initGF()

	// JavaScriptから呼び出すための関数 "encodeQRCode" をグローバルスコープに登録
	js.Global().Set("encodeQRCode", js.FuncOf(encodeQRCodeWrapper))

	// Goのプログラムが終了しないように待機
	<-make(chan bool)
}

// encodeQRCodeWrapper はJavaScriptからの呼び出しをラップし, JSON文字列を返す
func encodeQRCodeWrapper(this js.Value, args []js.Value) interface{} {
	if len(args) != 1 {
		return createErrorResponse("Invalid number of arguments")
	}
	kanjiInput := args[0].String()

	data, err := processKanji(kanjiInput)
	if err != nil {
		errorData := TemplateData{Error: err.Error()}
		responseBytes, _ := json.Marshal(errorData)
		return string(responseBytes)
	}

	responseBytes, err := json.Marshal(data)
	if err != nil {
		return createErrorResponse("Failed to serialize result to JSON")
	}

	return string(responseBytes)
}

// createErrorResponse はエラー情報を含むJSON文字列を作成するヘルパー
func createErrorResponse(message string) string {
	errorData := TemplateData{Error: message}
	responseBytes, _ := json.Marshal(errorData)
	return string(responseBytes)
}

// processKanji はHTTPに依存しないデータ符号化のコアロジック
func processKanji(kanjiInput string) (TemplateData, error) {
	data := TemplateData{MaxCharCount: maxCharCount, KanjiInput: kanjiInput}
	runes := []rune(kanjiInput)

	if len(runes) == 0 {
		return data, fmt.Errorf("漢字が入力されていません.")
	}
	if len(runes) > maxCharCount {
		return data, fmt.Errorf("文字数が多すぎます. %d文字以下で入力してください.", maxCharCount)
	}

	// STEP 1-1
	results, err := compressKanjiString(kanjiInput)
	if err != nil {
		return data, fmt.Errorf("圧縮処理中にエラーが発生しました: %v", err)
	}
	data.Results = results

	var binaryBuilder strings.Builder
	for _, res := range results {
		binaryBuilder.WriteString(res.Binary13Bit)
	}

	// STEP 1-2, 1-3
	modeIndicator := "1000"
	charCountIndicator := fmt.Sprintf("%08b", len(runes))
	initialBitStream := modeIndicator + charCountIndicator + binaryBuilder.String()
	terminatedBitStream := initialBitStream + "0000"
	data.Intermediate.ModeIndicator = modeIndicator
	data.Intermediate.CharCountIndicator = charCountIndicator
	data.Intermediate.ConcatenatedBinary = binaryBuilder.String()
	data.Intermediate.TerminatedBinary = terminatedBitStream

	// STEP 1-4
	paddedStream := terminatedBitStream
	if len(paddedStream)%8 != 0 {
		paddedStream += strings.Repeat("0", 8-len(paddedStream)%8)
	}
	var paddedBinaryBlocks []string
	for i := 0; i < len(paddedStream); i += 8 {
		paddedBinaryBlocks = append(paddedBinaryBlocks, paddedStream[i:i+8])
	}
	data.Intermediate.PaddedBinaryBlocks = strings.Join(paddedBinaryBlocks, " ")

	// STEP 1-5
	dataBytes := bitStreamToBytes(paddedStream)
	// 仕様: データコード語が19個に満たない場合、交互に 11101100 (EC) と 00010001 (11) を追加
	paddingBytes := []byte{0xEC, 0x11}
	paddingIndex := 0
	for len(dataBytes) < 19 {
		dataBytes = append(dataBytes, paddingBytes[paddingIndex])
		paddingIndex = (paddingIndex + 1) % 2
	}
	data.Intermediate.PaddedHex = formatBytesToHex(dataBytes)
	
	// STEP 1-6
	dataPoly := bytesToInts(dataBytes)
	generatorPoly := getGeneratorPolynomial(7)
	remainderPoly := polyDiv(polyLeftShift(dataPoly, 7), generatorPoly)
	
	// STEP 1-7
	codewordPoly := polyAdd(polyLeftShift(dataPoly, 7), remainderPoly)
	codewordBytes := intsToBytes(codewordPoly)
	
	data.Intermediate.DataPolynomial = formatPolynomial(dataPoly, "d")
	data.Intermediate.ErrorCorrectionPolynomial = formatPolynomial(remainderPoly, "r")
	data.Intermediate.CodewordPolynomial = formatPolynomial(codewordPoly, "c")
	data.Intermediate.CodewordHex = formatBytesToHex(codewordBytes)
	data.Intermediate.CodewordBinary = formatBytesToBinary(codewordBytes)

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

	for i := len(result) - len(divisor); i >= 0; i-- {
		coeff := result[i+len(divisor)-1]
		if coeff == 0 {
			continue
		}
		logCoeff := logTable[coeff]
		for j := 0; j < len(divisor); j++ {
			term := gfMul(divisor[j], expTable[logCoeff])
			result[i+j] ^= term
		}
	}
	return result[:len(divisor)-1]
}

func getGeneratorPolynomial(degree int) []int {
	coeffs := make([]int, degree+1)
	coeffs[degree] = 1 // x^7 の係数
	for i, exp := range generatorPolyExponents {
		coeffs[degree-1-i] = expTable[exp]
	}
	return coeffs
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
			// α^0 は 1 なので表示しない
			if logTable[coeff] > 0 {
				b.WriteString(fmt.Sprintf("\\alpha^{%d}", logTable[coeff]))
			}
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