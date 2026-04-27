package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/joho/godotenv"
	"net/http"
	"os"
	"sync"
	"time"
)

// Result フロントエンドにJSONとして送るため、タグ（`json:"..."`）を追加します
type Result struct {
	URL          string `json:"url"`
	Status       int    `json:"status"`
	Latency      int64  `json:"latency"`         // ミリ秒
	ErrorMessage string `json:"error,omitempty"` // エラーがない時は省略される
}

// 監視対象を管理する構造体
type Monitor struct {
	urls []string
	mu   sync.RWMutex // 読み書きを安全に行うための鍵
	// 各URLの「前回落ちていたか」を記録する (true = 落ちている)
	isDown map[string]bool
	// 追加：連続失敗回数を記録するマップ
	failCounts map[string]int
}

// データを保存するファイル名
const dataFile = "urls.json"

// グローバル変数としてインスタンス化
var monitor = &Monitor{
	urls:       []string{},
	isDown:     make(map[string]bool),
	failCounts: make(map[string]int), // 初期化
}

// === 新機能: ファイルからURLリストを読み込む ===
func (m *Monitor) Load() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. ファイルを読み込む
	data, err := os.ReadFile(dataFile)
	if err != nil {
		// ファイルが存在しない場合は、デフォルト値を入れてファイルを作成する
		if os.IsNotExist(err) {
			fmt.Println("urls.json が見つからないため、新規作成します。")
			m.urls = []string{"https://go.dev", "https://google.com"}
			m.saveToFile() // ファイルに書き出す
			return
		}
		fmt.Println("ファイル読み込みエラー:", err)
		return
	}

	// 2. 読み込んだJSONデータ(バイト配列)をスライスに変換する
	if err := json.Unmarshal(data, &m.urls); err != nil {
		fmt.Println("JSONパースエラー:", err)
	} else {
		fmt.Println("ファイルからURLリストを読み込みました:", m.urls)
	}
}

// === 新機能: 現在のURLリストをファイルに保存する (内部用) ===
// ※注意: この関数を呼ぶときは、すでにLockがかかっている前提とします
func (m *Monitor) saveToFile() {
	// スライスをきれいなJSON文字列に変換
	data, err := json.MarshalIndent(m.urls, "", "  ")
	if err != nil {
		fmt.Println("JSON変換エラー:", err)
		return
	}

	// ファイルに書き込む (0644は一般的なファイルの権限)
	if err := os.WriteFile(dataFile, data, 0644); err != nil {
		fmt.Println("ファイル書き込みエラー:", err)
	}
}

// URLを追加するメソッド
func (m *Monitor) Add(url string) {
	m.mu.Lock()         // muに対してロックをかける
	defer m.mu.Unlock() // 関数が終わったらアンロック
	m.urls = append(m.urls, url)
	m.saveToFile()
}

func (m *Monitor) Remove(target string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	newUrls := []string{}
	for _, u := range m.urls {
		if u != target {
			newUrls = append(newUrls, u)
		}
	}
	m.urls = newUrls
	m.saveToFile()
}

func (m *Monitor) GetURLs() []string {
	m.mu.RLock() // 読み取り専用ロック
	defer m.mu.RUnlock()
	return append([]string{}, m.urls...)
}

func main() {
	// .envの読み込み
	if err := godotenv.Load(); err != nil {
		fmt.Println(".envファイルが見つかりません。環境変数から直接読み込みます。")
	}

	// === ここを追加: サーバー起動前にファイルを読み込む ===
	monitor.Load()

	// "/stream" というURLにアクセスが来たら、streamHandler関数を実行する
	http.HandleFunc("/stream", streamHandler)

	// --- ここを追加: フロントエンドから Add/Remove を呼べるようにする ---
	http.HandleFunc("/urls", func(w http.ResponseWriter, r *http.Request) {
		// CORS対策
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			return
		}

		switch r.Method {
		case http.MethodPost:
			var payload struct {
				URL string `json:"url"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			monitor.Add(payload.URL)
			fmt.Println("URLを追加しました:", payload.URL)
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			url := r.URL.Query().Get("url")
			if url != "" {
				monitor.Remove(url)
				fmt.Println("URLを削除しました:", url)
				w.WriteHeader(http.StatusOK)
			}
		}
	})

	fmt.Println("サーバーを起動しました: http://localhost:8080")
	// 8080ポートでサーバーを立ち上げて待機
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Println("サーバーエラー:", err)
	}
}

// streamHandler はReactからの接続を受け付け、監視結果を流し続けます
func streamHandler(w http.ResponseWriter, r *http.Request) {
	// 1. CORS対応: React(別ポート)からのアクセスを許可する
	w.Header().Set("Access-Control-Allow-Origin", "*")
	// 2. SSE用のヘッダー: 「これは途切れないデータのストリームですよ」とブラウザに伝える
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// 5秒ごとに動く時計（Ticker）を作成
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// クライアント（ブラウザ）がタブを閉じたことを検知するための仕組み
	ctx := r.Context()

	fmt.Println("クライアントが接続しました。監視を開始します...")

	// 無限ループで定期的に処理を行う
	for {
		select {
		case <-ctx.Done(): // ブラウザが閉じられたらループを抜ける
			fmt.Println("クライアントが切断されました")
			return
		case <-ticker.C: // 5秒経過するごとにここが実行される
			// 毎回最新のリストを取得する
			urls := monitor.GetURLs()
			numJobs := len(urls)
			if numJobs == 0 {
				continue
			}

			// 1. チャネルの準備
			jobs := make(chan string, numJobs)        // 仕事(URL)を入れる箱
			resultsChan := make(chan Result, numJobs) // 結果を入れる箱

			// 2. ワーカーを起動する
			const workerCount = 3
			var wg sync.WaitGroup

			for i := 0; i < workerCount; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					// jobsチャネルからURLが送られてくるのを待機し、届いたら処理する
					for url := range jobs {
						resultsChan <- checkStatus(url)
					}
				}()
			}

			// 3. 仕事（URL）をチャネルに投入する
			for _, url := range urls {
				jobs <- url
			}
			close(jobs) // 「もう仕事はないよ」とワーカーに伝える（これでrangeが終了する）

			go func() {
				wg.Wait()
				close(resultsChan)
			}()

			// --- ここから下で、受け取った結果をフロントエンドに送信 ---
			for res := range resultsChan {
				// 1. 【ここを修正】何をもって「今回のチェックが失敗」とするか
				// エラーメッセージがある、もしくはステータスコードが400以上（リダイレクト3xxは許容）
				isCheckFailed := res.ErrorMessage != "" || (res.Status >= 400)

				monitor.mu.Lock()
				wasDown := monitor.isDown[res.URL]

				if isCheckFailed {
					// 失敗した場合：カウントを増やす
					monitor.failCounts[res.URL]++
					currentFailCount := monitor.failCounts[res.URL]

					// 3回連続失敗して初めて「ダウン状態」とみなす
					if currentFailCount >= 3 && !wasDown {
						go sendDiscordNotification("🚨 【障害確定】 " + res.URL + " が3回連続で失敗しました。")
						monitor.isDown[res.URL] = true
					}
				} else {
					// 成功した場合（200 OK や 301 Redirect など）
					if wasDown {
						go sendDiscordNotification("✅ 【復旧】 " + res.URL + " が回復しました。")
						monitor.isDown[res.URL] = false
					}
					// カウントをリセット
					monitor.failCounts[res.URL] = 0
				}
				monitor.mu.Unlock()

				// 構造体をJSON文字列に変換
				data, _ := json.Marshal(res)

				// SSEのフォーマットである "data: {JSON}\n\n" の形式で書き込む
				fmt.Fprintf(w, "data: %s\n\n", string(data))

				// バッファに溜め込まず、即座にフロントエンドに押し出す(Flush)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			fmt.Println("チェック完了。対象数:", len(urls))
		}
	}
}

// checkStatus は実際にHTTPリクエストを飛ばして結果を返す関数です
func checkStatus(url string) Result {
	start := time.Now()

	// タイムアウト設定付きのクライアント
	client := http.Client{
		Timeout: 10 * time.Second, // 余裕を持たせる
	}

	// 1. リクエストオブジェクトを作成
	req, _ := http.NewRequest("GET", url, nil)

	// 2. 「人間がブラウザで見ていますよ」というフリをする (User-Agent)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		// ここでエラー内容を詳しくログに出すと原因がわかります
		fmt.Printf("❌ ネットワークエラー: %v\n", err)
		return Result{URL: url, ErrorMessage: err.Error(), Latency: elapsed}
	}
	defer resp.Body.Close()

	return Result{URL: url, Status: resp.StatusCode, Latency: elapsed}
}

func sendDiscordNotification(message string) {
	url := os.Getenv("DISCORD_WEBHOOK_URL")
	if url == "" {
		fmt.Println("通知エラー: Webhook URLが設定されていません")
		return
	}

	payload := map[string]string{"content": message}
	data, _ := json.Marshal(payload)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		fmt.Println("Discord送信失敗:", err)
		return
	}
	defer resp.Body.Close()
}
