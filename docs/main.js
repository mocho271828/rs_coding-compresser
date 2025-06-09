// main.js

// Wasmモジュールをロードしてインスタンス化する
async function initWasm() {
    const status = document.getElementById('status');
    const encodeForm = document.getElementById('encodeForm');

    if (!WebAssembly.instantiateStreaming) { // Safariなどでのフォールバック
        WebAssembly.instantiateStreaming = async (resp, importObject) => {
            const source = await (await resp).arrayBuffer();
            return await WebAssembly.instantiate(source, importObject);
        };
    }

    const go = new Go();
    try {
        const result = await WebAssembly.instantiateStreaming(fetch('main.wasm'), go.importObject);
        go.run(result.instance);
        status.textContent = 'Wasmモジュールの準備ができました.';
        encodeForm.style.display = 'flex'; // フォームを表示
    } catch (err) {
        console.error(err);
        status.textContent = 'Wasmモジュールの読み込みに失敗しました. コンソールを確認してください.';
        status.style.color = 'red';
    }
}

// フォームの送信イベントを処理
document.getElementById('encodeForm').addEventListener('submit', (e) => {
    e.preventDefault();
    const kanjiInput = document.getElementById('kanjiInput').value;

    // JavaScriptに公開されたGoの関数を呼び出す
    const resultJson = window.encodeQRCode(kanjiInput);
    const data = JSON.parse(resultJson);

    // 結果を描画する
    renderResults(data);
});

// 結果をHTMLに描画する
function renderResults(data) {
    const errorContainer = document.getElementById('error-container');
    const resultsContainer = document.getElementById('results-container');
    
    // エラー表示をリセット
    errorContainer.style.display = 'none';
    errorContainer.textContent = '';

    if (data.Error) {
        errorContainer.textContent = data.Error;
        errorContainer.style.display = 'block';
        resultsContainer.style.display = 'none';
        return;
    }
    
    resultsContainer.style.display = 'block';

    // STEP1: 13ビット圧縮
    const step1Results = document.getElementById('step1-results');
    step1Results.innerHTML = data.Results.map(res => `
        <div class="result-item">
            <span class="kanji-char">${res.Kanji}</span>
            <strong>漢字:</strong> ${res.Kanji}<br>
            <div class="step"><strong>1.</strong> Shift JIS 漢字コードに変換: <span class="step-value">${res.ShiftJISCode}</span></div>
            <div class="step"><strong>2.</strong> 基準値の減算: <span class="step-value">${res.SubtractedCode}</span></div>
            <div class="step"><strong>3.</strong> 上位バイトに C0_16 を乗じ, 下位バイトを加算: <span class="step-value">${res.CompressedHex}</span></div>
            <div class="step"><strong>4.</strong> 13ビットの2進数に変換: <span class="step-value">${res.Binary13Bit}</span></div>
        </div>
    `).join('');

    // STEP2: データコード語の生成
    const step2Results = document.getElementById('step2-results');
    step2Results.innerHTML = `
        <strong>連結ビットストリーム:</strong><br>
        <div class="step"><strong>モード指示子 (漢字: 1000):</strong> <span class="step-value">${data.Intermediate.ModeIndicator}</span></div>
        <div class="step"><strong>文字数指示子 (${data.Results.length}文字):</strong> <span class="step-value">${data.Intermediate.CharCountIndicator}</span></div>
        <div class="step"><strong>連結データ:</strong> <span class="step-value">${data.Intermediate.ConcatenatedBinary}</span></div>
        <div class="step"><strong>終端パターン (0000) 付加後:</strong> <span class="step-value">${data.Intermediate.TerminatedBinary}</span></div>
        <div class="step"><strong>8ビット区切り (パディング後):</strong> <span class="step-value">${data.Intermediate.PaddedBinaryBlocks}</span></div>
        <div class="step"><strong>埋め草コード語付加後 (19バイト):</strong> <br><span class="step-value">${data.Intermediate.PaddedHex}</span></div>
    `;

    // STEP3: リード・ソロモン符号化
    const step3Results = document.getElementById('step3-results');
    step3Results.innerHTML = `
        <p>生成多項式 $G(x)$:</p>
        <div class="math-block">
            $$ G(x) = x^7 + \\alpha^{87}x^6 + \\alpha^{229}x^5 + \\alpha^{146}x^4 + \\alpha^{149}x^3 + \\alpha^{238}x^2 + \\alpha^{102}x + \\alpha^{21} $$
        </div>

        <p>データ多項式 $I(x)$:</p>
        <div class="math-block">
            $$ I(x) = ${data.Intermediate.DataPolynomial} $$
        </div>

        <p>誤り訂正多項式 $R(x) = [I(x)x^7] \\pmod{G(x)}$:</p>
        <div class="math-block">
            $$ R(x) = ${data.Intermediate.ErrorCorrectionPolynomial} $$
        </div>

        <p>符号語多項式 $X(x) = I(x)x^7 + R(x)$:</p>
        <div class="math-block">
            $$ X(x) = ${data.Intermediate.CodewordPolynomial} $$
        </div>

        <p><strong>最終符号語 (16進数ベクトル, 26バイト):</strong></p>
        <div class="step-value" style="white-space: pre-wrap;">${data.Intermediate.CodewordHex}</div>

        <p><strong>最終符号語 (2進数ベクトル):</strong></p>
        <div class="step-value" style="white-space: pre-wrap;">${data.Intermediate.CodewordBinary}</div>
    `;

    // MathJaxに新しい数式をレンダリングさせる
    if (window.MathJax) {
        window.MathJax.typeset();
    }
}

// Wasmの初期化を実行
initWasm();