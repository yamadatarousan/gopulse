package main

import (
	"encoding/json"
	"fmt"
	"net/http"
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

func main() {
	// "/stream" というURLにアクセスが来たら、streamHandler関数を実行する
	http.HandleFunc("/stream", streamHandler)

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
	w.Header().Set("Cache-Control", "no-chache")
	w.Header().Set("Connection", "keep-alive")

	urls := []string{
		"https://www.google.com",
		"https://www.github.com",
		"https://go.dev",
		"https://非実在のサイト.com",
	}

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

			// --- ここから下は先ほど学んだ並行処理 ---
			resultsChan := make(chan Result, len(urls))
			var wg sync.WaitGroup

			for _, url := range urls {
				wg.Add(1)
				go func(u string) {
					defer wg.Done()
					resultsChan <- checkStatus(u)
				}(url)
			}

			go func() {
				wg.Wait()
				close(resultsChan)
			}()

			// --- ここから下で、受け取った結果をフロントエンドに送信 ---
			for res := range resultsChan {
				// 構造体をJSON文字列に変換
				data, _ := json.Marshal(res)

				// SSEのフォーマットである "data: {JSON}\n\n" の形式で書き込む
				fmt.Fprintf(w, "data: %s\n\n", string(data))

				// バッファに溜め込まず、即座にフロントエンドに押し出す(Flush)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			fmt.Println("1ターン分のチェック結果を送信しました")
		}
	}
}

// checkStatus は実際にHTTPリクエストを飛ばして結果を返す関数です
func checkStatus(url string) Result {
	start := time.Now()

	// タイムアウト設定付きのクライアント
	client := http.Client{
		Timeout: 3 * time.Second,
	}

	resp, err := client.Get(url)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		return Result{URL: url, ErrorMessage: err.Error(), Latency: elapsed}
	}
	defer resp.Body.Close()

	return Result{URL: url, Status: resp.StatusCode, Latency: elapsed}
}
