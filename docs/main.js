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
    const resultJson = window.generateDataCodewords(kanjiInput);
    renderStep1And2(JSON.parse(resultJson));
});

document.getElementById('eccForm').addEventListener('submit', (e) => {
    e.preventDefault();
    const dataCodewordsBinary = document.getElementById('dataCodewordsBinaryInput').value;
    const resultJson = window.applyEcc(dataCodewordsBinary);
    renderStep3(JSON.parse(resultJson));
});

document.getElementById('maskForm').addEventListener('submit', (e) => {
    e.preventDefault();
    const codewordBinary = document.getElementById('codewordBinaryInput').value;
    const resultJson = window.applyMask(codewordBinary);
    renderStep4(JSON.parse(resultJson));
});

// エラー表示を管理する関数
function displayError(message) {
    const errorContainer = document.getElementById('error-container');
    errorContainer.textContent = message;
    errorContainer.style.display = message ? 'block' : 'none';
}

// STEP1とSTEP2の結果を描画
function renderStep1And2(data) {
    displayError(data.Error);
    if (data.Error) {
        document.getElementById('results-container').style.display = 'none';
        return;
    }
    document.getElementById('results-container').style.display = 'block';
    
    const section = document.getElementById('step1-and-2-section');
    section.style.display = 'block';

    section.querySelector('#step1-results').innerHTML = data.Results.map(res => `
        <div class="result-item">
            <span class="kanji-char">${res.Kanji}</span>
            <div class="step"><strong>1.</strong> Shift JIS: <span class="step-value">${res.ShiftJISCode}</span></div>
            <div class="step"><strong>2.</strong> 減算後: <span class="step-value">${res.SubtractedCode}</span></div>
            <div class="step"><strong>3.</strong> 圧縮後: <span class="step-value">${res.CompressedHex}</span></div>
            <div class="step"><strong>4.</strong> 13bit変換後: <span class="step-value">${res.Binary13Bit}</span></div>
        </div>`).join('');
    
    section.querySelector('#step2-results').innerHTML = `
        <div class="step"><strong>モード指示子 + 文字数指示子 + 連結データ:</strong> <span class="step-value">${data.Intermediate.ModeIndicator} ${data.Intermediate.CharCountIndicator} ${data.Intermediate.ConcatenatedBinary}</span></div>
        <div class="step"><strong>終端パターン付加後:</strong> <span class="step-value">${data.Intermediate.TerminatedBinary}</span></div>
        <div class="step"><strong>8ビット区切り (パディング後):</strong> <span class="step-value">${data.Intermediate.PaddedBinaryBlocks}</span></div>
        <div class="step"><strong>埋め草コード語付加後 (16進数, 19バイト):</strong> <br><span class="step-value">${data.Intermediate.PaddedHex}</span></div>
        <div class="step"><strong>最終データコード語 (2進数, 19バイト):</strong> <br><span class="step-value">${data.Intermediate.PaddedBinary}</span></div>`;

    document.getElementById('dataCodewordsBinaryInput').value = data.Intermediate.PaddedBinary;
    document.getElementById('ecc-form-section').style.display = 'block';
    document.getElementById('step3-section').style.display = 'none';
    document.getElementById('mask-form-section').style.display = 'none';
    document.getElementById('step4-section').style.display = 'none';
}

// STEP3の結果を描画
function renderStep3(data) {
    displayError(data.Error);
    const section = document.getElementById('step3-section');
    section.style.display = data.Error ? 'none' : 'block';
    if(data.Error) return;

    const resultsDiv = document.getElementById('step3-results');
    resultsDiv.innerHTML = `
        <p>データ多項式 $I(x)$:</p>
        <div class="math-block">$$ I(x) = ${data.Intermediate.DataPolynomial} $$</div>
        <p>誤り訂正多項式 $R(x) = [I(x)x^7] \\pmod{G(x)}$:</p>
        <div class="math-block">$$ R(x) = ${data.Intermediate.ErrorCorrectionPolynomial} $$</div>
        <p>符号語多項式 $X(x) = I(x)x^7 + R(x)$:</p>
        <div class="math-block">$$ X(x) = ${data.Intermediate.CodewordPolynomial} $$</div>
        <p><strong>符号語 (16進数, 26バイト):</strong></p>
        <div class="step-value">${data.Intermediate.CodewordHex}</div>
        <p><strong>符号語 (2進数, 26バイト):</strong></p>
        <div class="step-value">${data.Intermediate.CodewordBinary}</div>`;

    document.getElementById('codewordBinaryInput').value = data.Intermediate.CodewordBinary;
    document.getElementById('mask-form-section').style.display = 'block';
    document.getElementById('step4-section').style.display = 'none';

    if (window.MathJax) {
        window.MathJax.typesetPromise([resultsDiv]).catch((err) => console.log('MathJax typeset failed: ', err));
    }
}

// STEP4の結果を描画
function renderStep4(data) {
    displayError(data.Error);
    const section = document.getElementById('step4-section');
    section.style.display = data.Error ? 'none' : 'block';
    if(data.Error) return;

    document.getElementById('step4-results').innerHTML = `
        <p>マスク適用前の符号語 (2進数):</p>
        <div class="step-value">${data.Intermediate.CodewordBinary}</div>
        <p>マスクパターン (パターン番号3):</p>
        <div class="step-value">${data.Intermediate.MaskPatternHex}</div>
        <p><strong>マスク適用後の最終データ (16進数):</strong></p>
        <div class="step-value">${data.Intermediate.MaskedCodewordHex}</div>
        <p><strong>マスク適用後の最終データ (2進数):</strong></p>
        <div class="step-value">${data.Intermediate.MaskedCodewordBinary}</div>`;
}

// Wasmの初期化を実行
initWasm();