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
        if (!data.Error) {
            renderStep5(data.Intermediate.MaskedCodewordBinary);
            document.getElementById('step5-section').style.display = 'block';
        } else {
            document.getElementById('step5-section').style.display = 'none';
        }
}

// STEP5(最終QRコード)結果

function renderStep5(maskedBinaryString) {
    const canvas = document.getElementById('qrCanvas');
    const listElement = document.getElementById('black-modules-list');
    if (!canvas || !listElement) return;

    const ctx = canvas.getContext('2d');
    const qrSize = 21;
    const quietZone = 2;
    const totalModules = qrSize + (quietZone * 2);
    const moduleSize = canvas.width / totalModules;

    const qrMatrix = generateFinalQrMatrix(maskedBinaryString);

    // --- Canvasへの描画 ---
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.fillStyle = 'white';
    ctx.fillRect(0, 0, canvas.width, canvas.height);

    const blackDataModules = [];
    const dataPositions = getQrDataPositions();

    for (let r = 0; r < qrSize; r++) {
        for (let c = 0; c < qrSize; c++) {
            if (qrMatrix[r][c] === 1) {
                ctx.fillStyle = 'black';
                ctx.fillRect(
                    (c + quietZone) * moduleSize,
                    (r + quietZone) * moduleSize,
                    moduleSize,
                    moduleSize
                );
                const dataModule = dataPositions.find(p => p.r === r && p.c === c);
                if (dataModule) {
                    blackDataModules.push(dataModule.num);
                }
            }
        }
    }
    
    // --- 塗りつぶし位置を番号でリストに表示 ---
    listElement.textContent = blackDataModules.sort((a, b) => a - b).join(', ');
}


function generateFinalQrMatrix(binaryString) {
    const qrSize = 21;
    const matrix = Array.from({ length: qrSize }, () => Array(qrSize).fill(null));

    placeStaticPatterns(matrix);

    const dataPositions = getQrDataPositions();
    for (let i = 0; i < dataPositions.length; i++) {
        if (i < binaryString.length) {
            const pos = dataPositions[i];
            matrix[pos.r][pos.c] = parseInt(binaryString[i], 10);
        }
    }

    const formatInfo = "111011110001001".split('').map(Number);
    placeFormatInformation(matrix, formatInfo);

    return matrix;
}

function placeStaticPatterns(matrix) {
    const qrSize = 21;
    const placeFinder = (row, col) => {
        for (let r = -1; r <= 7; r++) {
            for (let c = -1; c <= 7; c++) {
                if (row + r >= 0 && row + r < qrSize && col + c >= 0 && col + c < qrSize) {
                    if (r >= 0 && r < 7 && c >= 0 && c < 7 && (r === 0 || r === 6 || c === 0 || c === 6 || (r > 1 && r < 5 && c > 1 && c < 5))) {
                        matrix[row + r][col + c] = 1;
                    } else {
                        matrix[row + r][col + c] = 0; // セパレータ含む
                    }
                }
            }
        }
    };
    placeFinder(0, 0);
    placeFinder(0, qrSize - 7);
    placeFinder(qrSize - 7, 0);

    // タイミング
    for (let i = 8; i < qrSize - 8; i++) {
        matrix[6][i] = (i % 2 === 0) ? 1 : 0;
        matrix[i][6] = (i % 2 === 0) ? 1 : 0;
    }

    matrix[13][8] = 1;
}

function placeFormatInformation(matrix, formatBits) {
    const qrSize = 21;
    let bitIndex = 0;
    const positions = [
        [8,0], [8,1], [8,2], [8,3], [8,4], [8,5], [8,7],
        [7,8], [5,8], [4,8], [3,8], [2,8], [1,8], [0,8],
    ];
    for(const pos of positions) {
        matrix[pos[0]][pos[1]] = formatBits[bitIndex++];
    }
    bitIndex = 0;
    const positions2 = [
        [qrSize-1, 8], [qrSize-2, 8], [qrSize-3, 8], [qrSize-4, 8], [qrSize-5, 8], [qrSize-6, 8], [qrSize-7, 8],
        [8, qrSize-8], [8, qrSize-7], [8, qrSize-6], [8, qrSize-5], [8, qrSize-4], [8, qrSize-3], [8, qrSize-2], [8, qrSize-1],
    ];
    const formatBitsMasked = "101010000010010".split('').map(Number);
    for(let i = 0; i < 15; i++) {
        const p = positions2[i];
        matrix[p[0]][p[1]] = formatBits[i] ^ formatBitsMasked[i];
    }
}

function getQrDataPositions() {
    const qrSize = 21;
    const matrix = Array.from({ length: qrSize }, () => Array(qrSize).fill(null));
    placeStaticPatterns(matrix);
    placeFormatInformation(matrix, Array(15).fill(0));

    const positions = [];
    let number = 1;
    let direction = -1;

    for (let c = qrSize - 1; c >= 0; c -= 2) {
        if (c === 6) c--;

        for (let r_offset = 0; r_offset < qrSize; r_offset++) {
            const r = (direction === -1) ? (qrSize - 1 - r_offset) : r_offset;
            for (let col_offset = 0; col_offset < 2; col_offset++) {
                const current_col = c - col_offset;
                if (matrix[r][current_col] === null) {
                    positions.push({ num: number++, r, c: current_col });
                }
            }
        }
        direction *= -1;
    }
    return positions;
}

// Wasmの初期化を実行
initWasm();