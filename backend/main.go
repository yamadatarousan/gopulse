package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Result フロントエンドにJSONとして送るため、タグ（`json:"..."`）を追加します
type Result struct {
	URL          string `json:"url"`
	Status       int    `json:"status"`
	Latency      int64  `json:"latency"` // ミリ秒
	ErrorMessage string `json:"error"`
	CheckedAt    int64  `json:"checked_at"` // 追加: いつの結果か分かるように
}

// === DBモデル追加 ===
type URLStatus struct {
	ID          uint      `gorm:"primaryKey" json:"-"`
	URL         string    `gorm:"uniqueIndex" json:"url"`
	IsDown      bool      `json:"is_down"`
	FailCount   int       `json:"fail_count"`
	LastStatus  int       `json:"last_status"`
	LastLatency int64     `json:"last_latency"`
	LastError   string    `json:"last_error"`
	LastCheck   int64     `json:"last_check"`
	CreatedAt   time.Time `json:"-"`
	UpdatedAt   time.Time `json:"-"`
}

type CheckHistory struct {
	ID        uint   `gorm:"primaryKey" json:"-"`
	URL       string `gorm:"index" json:"url"`
	Status    int    `json:"status"`
	Latency   int64  `json:"latency"`
	ErrorMsg  string `json:"error"`
	CheckedAt int64  `gorm:"index" json:"checked_at"`
}

var db *gorm.DB

// === MonitorはDB操作のラッパーにする ===
type Monitor struct{}

var monitor = &Monitor{}

// DB初期化
func initDB() {
	var err error
	db, err = gorm.Open(sqlite.Open("gopulse.db?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}

	var mode string
	db.Raw("PRAGMA journal_mode").Scan(&mode)
	fmt.Println("SQLite Journal Mode:", mode) // walって出ればOK

	// テーブル自動作成
	db.AutoMigrate(&URLStatus{}, &CheckHistory{})

	// インデックス追加: これが無いとフルスキャンになる
	db.Exec("CREATE INDEX IF NOT EXISTS idx_check_histories_url_checked_at ON check_histories(url, checked_at)")

	// 初回起動時にデフォルトURL入れる
	var count int64
	db.Model(&URLStatus{}).Count(&count)
	if count == 0 {
		db.Create(&URLStatus{URL: "https://go.dev"})
		db.Create(&URLStatus{URL: "https://google.com"})
		fmt.Println("デフォルトURLをDBに作成しました")
	}
}

// URL追加
func (m *Monitor) Add(url string) error {
	result := db.Create(&URLStatus{URL: url})
	return result.Error
}

// URL削除
func (m *Monitor) Remove(url string) error {
	return db.Where("url =?", url).Delete(&URLStatus{}).Error
}

func (m *Monitor) GetURLs() []string {
	var statuses []URLStatus
	db.Find(&statuses)
	urls := make([]string, len(statuses))
	for i, s := range statuses {
		urls[i] = s.URL
	}
	return urls
}

// 全URLの最新状態取得
func (m *Monitor) GetAllStatus() []URLStatus {
	var statuses []URLStatus
	db.Find(&statuses)
	return statuses
}

// チェック結果を更新
func (m *Monitor) UpdateResult(res Result) {
	start := time.Now()

	var status URLStatus
	if err := db.Where("url = ?", res.URL).First(&status).Error; err != nil {
		return // 削除済み
	}

	wasDown := status.IsDown
	isFailed := res.ErrorMessage != "" || (res.Status >= 400)

	if isFailed {
		status.FailCount++
		if status.FailCount >= 3 && !wasDown {
			go sendDiscordNotification("🚨 " + res.URL + " が3回連続で失敗しました。Status: " + fmt.Sprintf("%d", res.Status))
			status.IsDown = true
		}
	} else {
		if wasDown {
			go sendDiscordNotification("✅ " + res.URL + " が回復しました。")
			status.IsDown = false
		}
		status.FailCount = 0
	}

	status.LastStatus = res.Status
	status.LastLatency = res.Latency
	status.LastError = res.ErrorMessage
	status.LastCheck = res.CheckedAt
	db.Save(&status)

	// チェック履歴も保存する
	db.Create(&CheckHistory{
		URL:       res.URL,
		Status:    res.Status,
		Latency:   res.Latency,
		ErrorMsg:  res.ErrorMessage,
		CheckedAt: res.CheckedAt,
	})

	fmt.Printf("DB書き込み: %dms\n", time.Since(start).Milliseconds())
}

// CORSミドルウェア
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ===== SSE購読管理: ここをちゃんと使う =====
var subscribers = make(map[chan Result]struct{})
var subMu sync.Mutex

func subscribe() chan Result {
	ch := make(chan Result, 20) // バッファ少し増やす
	subMu.Lock()
	subscribers[ch] = struct{}{}
	subMu.Unlock()
	return ch
}

func unsubscribe(ch chan Result) {
	subMu.Lock()
	delete(subscribers, ch)
	close(ch)
	subMu.Unlock()
}

func broadcast(res Result) {
	subMu.Lock()
	defer subMu.Unlock()
	for ch := range subscribers {
		select {
		case ch <- res: // 送れるなら送る
		default: // 詰まってるクライアントは諦める。遅延防止
		}
	}
}

// === 監視ループを分離: サーバー起動時に1個だけ走らせる ===
func startMonitoringLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	fmt.Println("バックグラウンド監視ループ開始")

	for {
		select {
		case <-ctx.Done():
			fmt.Println("監視ループ停止")
			return
		case <-ticker.C:
			urls := monitor.GetURLs()
			if len(urls) == 0 {
				continue
			}

			// worker poolは毎回作らず使い回した方が良いが、まず動く版
			var wg sync.WaitGroup
			resultCh := make(chan Result, len(urls))

			const workerCount = 5
			jobs := make(chan string, len(urls))

			for i := 0; i < workerCount; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for url := range jobs {
						resultCh <- checkStatus(url)
					}
				}()
			}

			for _, url := range urls {
				jobs <- url
			}
			close(jobs)

			go func() {
				wg.Wait()
				close(resultCh)
			}()

			for res := range resultCh {
				monitor.UpdateResult(res) // 通知ロジックを分離
				broadcast(res)            // 全クライアントに配信
			}
		}
	}
}

func main() {
	_ = godotenv.Load()
	initDB() // ← ファイル読み込みの代わり

	// 1. バックグラウンド監視を開始
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go startMonitoringLoop(ctx)

	// ServeMuxでルーティング定義してミドルウェアでラップ
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", streamHandler)
	mux.HandleFunc("/urls", urlsHandler)
	mux.HandleFunc("/status", statusHandler)
	mux.HandleFunc("/history", historyHandler) // 追加: グラフ用

	handler := corsMiddleware(mux)

	fmt.Println("サーバーを起動しました: http://localhost:8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		fmt.Println("サーバーエラー;", err)
	}
}

// streamHandler はReactからの接続を受け付け、監視結果を流し続けます
func streamHandler(w http.ResponseWriter, r *http.Request) {
	// SSE用のヘッダー: 「これは途切れないデータのストリームですよ」とブラウザに伝える
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := subscribe()
	defer unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("クライアントが切断されました")
			return
		case res := <-ch:
			data, _ := json.Marshal(res)
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

// 初回レンダリング用: 現在の全URLの状態を返す
func statusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	statuses := monitor.GetAllStatus()
	results := make([]Result, len(statuses))
	for i, s := range statuses {
		results[i] = Result{
			URL:          s.URL,
			Status:       s.LastStatus,
			Latency:      s.LastLatency,
			ErrorMessage: s.LastError,
			CheckedAt:    s.LastCheck,
		}
	}
	json.NewEncoder(w).Encode(results)
}

// historyHandler: グラフ用に履歴返す
func historyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	url := r.URL.Query().Get("url")
	if url == "" {
		http.Error(w, "url required", http.StatusBadRequest)
		return
	}

	var histories []CheckHistory
	// 直近1時間分だけ返す。288点→12点
	since := time.Now().Add(-1 * time.Hour).Unix()
	db.Where("url = ? AND checked_at >= ?", url, since).
		Order("checked_at asc").
		Limit(720). // 最大720点 = 1時間分
		Find(&histories)

	json.NewEncoder(w).Encode(histories)
}

func urlsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var payload struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if payload.URL == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}

		if err := monitor.Add(payload.URL); err != nil {
			http.Error(w, "URL追加失敗: "+err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Println("URLを追加しました:", payload.URL)

		// 追加: 即座に1回チェックしてbroadcastする
		go func(url string) {
			res := checkStatus(url)
			monitor.UpdateResult(res)
			broadcast(res)
		}(payload.URL)

		w.WriteHeader(http.StatusCreated)

	case http.MethodDelete:
		url := r.URL.Query().Get("url")
		if url == "" {
			http.Error(w, "url query param is required", http.StatusBadRequest)
			return
		}
		if err := monitor.Remove(url); err != nil {
			http.Error(w, "URL削除失敗: "+err.Error(), http.StatusInternalServerError)
		}
		fmt.Println("URLを削除しました:", url)
		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// checkStatus: 間欠的タイムアウト対策版
func checkStatus(url string) Result {
	var lastErr error

	// 最大2回リトライ
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(500 * time.Millisecond) // 少し待ってからリトライ
		}

		res, err := doCheck(url)
		if err == nil {
			return res
		}
		lastErr = err

		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			fmt.Printf("⚠️ タイムアウト %s: リトライ %d/2\n", url, attempt+1)
			continue
		}
		break
	}

	return Result{
		URL:          url,
		Status:       -1,
		Latency:      8000,
		ErrorMessage: lastErr.Error(),
		CheckedAt:    time.Now().Unix(),
	}
}

func doCheck(url string) (Result, error) {
	start := time.Now()

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: -1, // KeepAlive無効
		}).DialContext,
		ForceAttemptHTTP2:     false, // HTTP2無効
		DisableKeepAlives:     true,  // 接続使いまわし無効
		MaxIdleConns:          -1,
		TLSHandshakeTimeout:   3 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second, // 3→5秒に緩和
		TLSClientConfig:       &tls.Config{},
	}

	client := http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("リダイレクトが多すぎます")
			}
			return nil
		},
	}

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0") // 本物ブラウザに偽装
	req.Close = true                            // 接続を閉じる指示

	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()

	res := Result{
		URL:       url,
		Latency:   elapsed,
		CheckedAt: time.Now().Unix(),
		Status:    0,
	}

	if err != nil {
		return res, err
	}
	defer resp.Body.Close()

	// Bodyは読まずに捨てる。ヘッダーだけみればOK
	io.Copy(io.Discard, resp.Body)

	res.Status = resp.StatusCode
	return res, nil
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
