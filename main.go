package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/japanese"
)

// --- グローバル変数と定数 ---

var tmpl *template.Template

const maxCharCount = 9 // 型番1, 漢字モードの最大文字数

// GF(2^8) のためのテーブル
var expTable [256]int
var logTable [256]int

const primitivePolynomial = 0x11D // 原始多項式: x^8 + x^4 + x^3 + x^2 + 1

// RS符号の生成多項式の係数 (alphaのべき乗)
var generatorPolyExponents = []int{87, 229, 146, 149, 238, 102, 21}

// --- 初期化 ---

func init() {
	// テンプレートのロード
	tmpl = template.Must(template.ParseFiles("templates/index.html"))
	// GF(2^8) テーブルの初期化
	initGF()
}

// initGF はGF(2^8)の乗算・除算のための対数テーブルと指数テーブルを生成します.
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
	// 仕様により expTable[255] は 1 とする.
	expTable[255] = 1
}

// --- 構造体定義 ---

// TemplateData はテンプレートに渡すデータ全体の構造体です.
type TemplateData struct {
	KanjiInput   string
	Results      []KanjiCompressionResult
	Intermediate QRCodeIntermediateData
	Error        string
	MaxCharCount int
}

// KanjiCompressionResult は個々の漢字の圧縮結果を保持します.
type KanjiCompressionResult struct {
	Kanji          string
	ShiftJISCode   string
	SubtractedCode string
	CompressedHex  string
	Binary13Bit    string
}

// QRCodeIntermediateData はRS符号化までの中間結果を保持します.
type QRCodeIntermediateData struct {
	ModeIndicator             string
	CharCountIndicator        string
	ConcatenatedBinary        string
	TerminatedBinary          string
	PaddedBinaryBlocks        string
	PaddedHex                 string
	DataPolynomial            string
	ErrorCorrectionPolynomial string
	CodewordPolynomial        string
	CodewordHex               string
	CodewordBinary            string
}

// --- HTTPハンドラ ---

func main() {
	http.HandleFunc("/", mainHandler)
	fmt.Println("サーバーを起動しました: http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// mainHandler はリクエストに応じてフォーム表示または処理実行を行います.
func mainHandler(w http.ResponseWriter, r *http.Request) {
	data := TemplateData{MaxCharCount: maxCharCount}
	if r.Method == http.MethodPost {
		processKanjiHandler(r, &data)
	}
	renderTemplate(w, "index.html", data)
}

// renderTemplate は指定されたテンプレートを描画します.
func renderTemplate(w http.ResponseWriter, tmplName string, data TemplateData) {
	err := tmpl.ExecuteTemplate(w, tmplName, data)
	if err != nil {
		http.Error(w, "テンプレートのレンダリングに失敗しました", http.StatusInternalServerError)
		log.Printf("テンプレートのレンダリングエラー: %v", err)
	}
}

// processKanjiHandler は入力された漢字を処理し, 符号化結果を生成します.
func processKanjiHandler(r *http.Request, data *TemplateData) {
	kanjiInput := r.FormValue("kanjiInput")
	data.KanjiInput = kanjiInput
	runes := []rune(kanjiInput)

	if len(runes) == 0 {
		data.Error = "漢字が入力されていません."
		return
	}
	if len(runes) > maxCharCount {
		data.Error = fmt.Sprintf("文字数が多すぎます. %d文字以下で入力してください.", maxCharCount)
		return
	}

	// STEP 1-1: 13ビット圧縮
	results, err := compressKanjiString(kanjiInput)
	if err != nil {
		data.Error = fmt.Sprintf("圧縮処理中にエラーが発生しました: %v", err)
		return
	}
	data.Results = results

	// 13ビットバイナリデータを連結
	var binaryBuilder strings.Builder
	for _, res := range results {
		binaryBuilder.WriteString(res.Binary13Bit)
	}

	// STEP 1-2, 1-3: モード指示子, 文字数指示子, 終端パターンの追加
	modeIndicator := "1000"
	charCountIndicator := fmt.Sprintf("%08b", len(runes))
	initialBitStream := modeIndicator + charCountIndicator + binaryBuilder.String()
	terminatedBitStream := initialBitStream + "0000"
	data.Intermediate.ModeIndicator = modeIndicator
	data.Intermediate.CharCountIndicator = charCountIndicator
	data.Intermediate.ConcatenatedBinary = binaryBuilder.String()
	data.Intermediate.TerminatedBinary = terminatedBitStream

	// STEP 1-4: 8ビット区切りとパディング
	paddedStream := terminatedBitStream
	if len(paddedStream)%8 != 0 {
		paddedStream += strings.Repeat("0", 8-len(paddedStream)%8)
	}
	var paddedBinaryBlocks []string
	for i := 0; i < len(paddedStream); i += 8 {
		paddedBinaryBlocks = append(paddedBinaryBlocks, paddedStream[i:i+8])
	}
	data.Intermediate.PaddedBinaryBlocks = strings.Join(paddedBinaryBlocks, " ")

	// STEP 1-5: 埋め草コード語の追加
	dataBytes := bitStreamToBytes(paddedStream)
	for len(dataBytes) < 19 {
		if len(dataBytes)%2 == 1 { // 奇数番目 (0-indexed)
			dataBytes = append(dataBytes, 0xEC) // 11101100
		} else {
			dataBytes = append(dataBytes, 0x11) // 00010001
		}
	}
	data.Intermediate.PaddedHex = formatBytesToHex(dataBytes)

	// STEP 1-6: RS符号の計算
	dataPoly := bytesToInts(dataBytes)
	generatorPoly := getGeneratorPolynomial(7)
	remainderPoly := polyDiv(polyLeftShift(dataPoly, 7), generatorPoly)

	// STEP 1-7: 符号語の生成
	codewordPoly := polyAdd(polyLeftShift(dataPoly, 7), remainderPoly)
	codewordBytes := intsToBytes(codewordPoly)

	data.Intermediate.DataPolynomial = formatPolynomial(dataPoly, "d")
	data.Intermediate.ErrorCorrectionPolynomial = formatPolynomial(remainderPoly, "r")
	data.Intermediate.CodewordPolynomial = formatPolynomial(codewordPoly, "c")
	data.Intermediate.CodewordHex = formatBytesToHex(codewordBytes)
	data.Intermediate.CodewordBinary = formatBytesToBinary(codewordBytes)
}

// --- 漢字圧縮関連 ---

// compressKanjiString は漢字文字列を13ビット形式に圧縮します.
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

// convertToShiftJIS はUTF-8文字列をShift-JISバイト列に変換します.
func convertToShiftJIS(s string) ([]byte, error) {
	encoder := japanese.ShiftJIS.NewEncoder()
	return encoder.Bytes([]byte(s))
}

// --- GF(2^8)および多項式演算 ---

// gfMul はGF(2^8)上の乗算を行います.
func gfMul(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	return expTable[(logTable[a]+logTable[b])%255]
}

// polyAdd はGF(2^8)上の多項式加算 (XOR) を行います.
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

// polyLeftShift は多項式を`count`回左シフトします (x^countを乗じる).
func polyLeftShift(p []int, count int) []int {
	result := make([]int, len(p)+count)
	copy(result[count:], p)
	return result
}

// polyDiv はGF(2^8)上の多項式除算を行い, 剰余を返します.
func polyDiv(dividend []int, divisor []int) []int { // 修正: 引数dividendの型を []int に変更
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

	// 剰余部分を返す
	return result[:len(divisor)-1]
}

// getGeneratorPolynomial は指定された次数(誤り訂正コード語の数)の生成多項式を計算します.
func getGeneratorPolynomial(degree int) []int {
	// G(x) = (x-a^0)(x-a^1)...(x-a^(degree-1))
	// QRコードの仕様では (x-a^0) からではなく、特殊な係数になる.
	// PDFの仕様 G(x)=x^7 + a^87x^6 + ... + a^21 に基づき係数を直接設定
	coeffs := make([]int, degree+1)
	coeffs[degree] = 1 // x^7 の係数
	for i, exp := range generatorPolyExponents {
		coeffs[degree-1-i] = expTable[exp]
	}
	return coeffs
}

// --- ヘルパー関数 ---

// bitStreamToBytes はビット文字列をバイトスライスに変換します.
func bitStreamToBytes(s string) []byte {
	var b []byte
	for i := 0; i < len(s); i += 8 {
		val, _ := strconv.ParseUint(s[i:i+8], 2, 8)
		b = append(b, byte(val))
	}
	return b
}

// formatBytesToHex はバイトスライスを16進数文字列にフォーマットします.
func formatBytesToHex(data []byte) string {
	var hexParts []string
	for _, b := range data {
		hexParts = append(hexParts, fmt.Sprintf("%02X", b))
	}
	return strings.Join(hexParts, " ")
}

// formatBytesToBinary はバイトスライスを2進数文字列にフォーマットします.
func formatBytesToBinary(data []byte) string {
	var binParts []string
	for _, b := range data {
		binParts = append(binParts, fmt.Sprintf("%08b", b))
	}
	return strings.Join(binParts, " ")
}

// bytesToInts は []byte を []int に変換します.
func bytesToInts(b []byte) []int {
	ints := make([]int, len(b))
	for i, v := range b {
		ints[i] = int(v)
	}
	return ints
}

// intsToBytes は []int を []byte に変換します.
func intsToBytes(i []int) []byte {
	bytes := make([]byte, len(i))
	for j, v := range i {
		bytes[j] = byte(v)
	}
	return bytes
}

// formatPolynomial は多項式の係数スライスをMathJax形式の文字列に変換します.
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
			// 定数項が1の場合
			b.WriteString("1")
		}
	}
	if isFirstTerm {
		return "0"
	}
	return b.String()
}
