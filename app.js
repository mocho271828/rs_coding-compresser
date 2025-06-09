// app.js

// WASMの読み込みとインスタンス化
async function initWasm() {
    const go = new Go();
    const response = await fetch("main.wasm");
    const buffer = await response.arrayBuffer();
    const result = await WebAssembly.instantiate(buffer, go.importObject);
    go.run(result.instance);
    return true;
}

// 結果をDOMに描画する関数
function displayResults(data) {
    // エラー表示をクリア
    document.getElementById('error-display').textContent = '';
    
    // 圧縮結果テーブル
    const tableBody = document.querySelector("#compression-table tbody");
    tableBody.innerHTML = ''; // テーブルをクリア
    data.results.forEach(res => {
        const row = tableBody.insertRow();
        row.insertCell(0).textContent = res.kanji;
        row.insertCell(1).textContent = res.shiftJisCode;
        row.insertCell(2).textContent = res.subtractedCode;
        row.insertCell(3).textContent = res.compressedHex;
        row.insertCell(4).textContent = res.binary13Bit;
    });

    // 中間データ
    const i = data.intermediate;
    document.getElementById('mode-indicator').textContent = i.modeIndicator;
    document.getElementById('char-count-indicator').textContent = i.charCountIndicator;
    document.getElementById('concatenated-binary').textContent = i.concatenatedBinary;
    document.getElementById('terminated-binary').textContent = i.terminatedBinary;
    document.getElementById('padded-binary-blocks').textContent = i.paddedBinaryBlocks;
    document.getElementById('padded-hex').textContent = i.paddedHex;
    document.getElementById('data-polynomial').textContent = `$${i.dataPolynomial}$`;
    document.getElementById('error-correction-polynomial').textContent = `$${i.errorCorrectionPolynomial}$`;
    document.getElementById('codeword-polynomial').textContent = `$${i.codewordPolynomial}$`;
    document.getElementById('codeword-hex').textContent = i.codewordHex;
    document.getElementById('codeword-binary').textContent = i.codewordBinary;
    
    // MathJaxに数式を再レンダリングさせる
    if (window.MathJax) {
        window.MathJax.typeset();
    }

    // 結果コンテナを表示
    document.getElementById('results-container').style.display = 'block';
}

// エラーを表示する関数
function displayError(message) {
    document.getElementById('results-container').style.display = 'none';
    const errorDisplay = document.getElementById('error-display');
    errorDisplay.textContent = `エラー: ${message}`;
}

// メイン処理
document.addEventListener('DOMContentLoaded', async () => {
    const runButton = document.getElementById('runButton');
    const kanjiInput = document.getElementById('kanjiInput');
    const status = document.getElementById('status');

    runButton.disabled = true;

    try {
        await initWasm();
        status.textContent = 'WASMのロード完了. 実行可能です.';
        runButton.disabled = false;

        // ボタンクリック時のイベントリスナー
        runButton.addEventListener('click', () => {
            const inputValue = kanjiInput.value;
            // Go側でグローバルに登録した processKanji 関数を呼び出す
            const result = window.processKanji(inputValue);

            if (result.error) {
                displayError(result.error);
            } else {
                displayResults(result);
            }
        });

    } catch (err) {
        status.textContent = 'WASMのロードに失敗しました.';
        displayError(err);
        console.error(err);
    }
});