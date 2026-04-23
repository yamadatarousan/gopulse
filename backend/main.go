package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Result は監視結果を格納する構造体です
type Result struct {
	URL     string
	Status  int
	Latency int64 // ミリ秒
	Error   error
}

func main() {
	// 1. 監視対象のURLリスト
	urls := []string{
		"https://www.google.com",
		"https://www.github.com",
		"https://go.dev",
		"https://非実在のサイト.com",
	}

	// 2. 結果を収集するためのチャネル
	resultsChan := make(chan Result, len(urls))

	// 3. 全ての処理が終わるのを待機するための WaitGroup
	var wg sync.WaitGroup

	fmt.Println("監視を開始します...")
	start := time.Now()

	// 4. 各URLに対してGoroutine（並行処理）を起動
	for _, url := range urls {
		wg.Add(1) // main関数を止める待ち行列を+1
		go func(u string) {
			defer wg.Done()               // 関数終了時に-1
			resultsChan <- checkStatus(u) // 結果をチャネルに送信
		}(url)
	}

	// 5. 「全てのGoroutineが終わったらチャネルを閉じる」処理をバックグラウンドで実行
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// 6. チャネルから結果を取り出して表示
	// close(resultsChan) されるまでループが回ります
	for res := range resultsChan {
		if res.Error != nil {
			fmt.Printf("[ERROR] %-25s | Error: %v\n", res.URL, res.Error)
		} else {
			fmt.Printf("[SUCCESS] %-24s | Status: %d | Latency: %dms\n", res.URL, res.Status, res.Latency)
		}
	}
	fmt.Printf("\n全ての監視が完了しました。合計時間: %v\n", time.Since(start))
}

// checkStatus は実際にHTTPリクエストを飛ばして結果を返す関数です
func checkStatus(url string) Result {
	start := time.Now()

	// タイムアウト設定付きのクライアント
	client := http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(url)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		return Result{URL: url, Error: err, Latency: elapsed}
	}
	defer resp.Body.Close()

	return Result{URL: url, Status: resp.StatusCode, Latency: elapsed}
}
