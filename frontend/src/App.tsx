import { useEffect, useState } from "react";

// Go側で定義したのと同じ型をTypeScriptでも定義します
interface Result {
  url: string;
  status: number;
  latency: number;
  error?: string;
  checked_at?: number;
}

function App() {
  const [results, setResults] = useState<Record<string, Result>>({});
  const [inputUrl, setInputUrl] = useState(''); // 入力フォーム用の状態
  const [isConnected, setIsConnected] = useState(false)

  useEffect(() => {
    // 1. 初回ロード: 現在の監視対象を取得して枠だけ出す
    fetch('http://localhost:8080/status')
      .then(res => res.json())
      .then((data: Result[]) => {
        const initial: Record<string, Result> = {};
        data.forEach(d => { initial[d.url] = d });
        setResults(initial)
      })

    // 1. Goのバックエンド（SSE）に接続
    const eventSource = new EventSource('http://localhost:8080/stream');

    eventSource.onopen = () => setIsConnected(true)
    eventSource.onmessage = (event) => {
      const data: Result = JSON.parse(event.data);
      setResults((prev) => ({
        ...prev,
        [data.url]: data,
      }));
    }
    eventSource.onerror = () => {
      setIsConnected(false);
      console.error('SSE Error: 接続が切れました');
    }

    // 3. クリーンアップ関数
    return () => {
      eventSource.close();
    };
  }, []);

  // URLを追加する関数
  const addUrl = async(e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault(); // 画面リロードを防止
    if(!inputUrl) return

    // 楽観的UI更新: 即座に追加
    setResults(prev => ({...prev, [inputUrl]: { url: inputUrl, status: 0, latency: 0 } }));

    await fetch('http://localhost:8080/urls', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: inputUrl }),
    });
    setInputUrl('');
  };

  // URLを削除する関数
  const removeUrl = async (url: string) => {
    // 楽観的UI更新
    setResults((prev) => {
      const newResults = { ...prev };
      delete newResults[url];
      return newResults;
    });

    await fetch(`http://localhost:8080/urls?url=${encodeURIComponent(url)}`, {
      method: 'DELETE',
    });
  };

  const getStatusColor = (res: Result) => {
    if (res.status === 0 && !res.error) return '#f7fafc'; // 未チェック
    if (res.error || res.status === -1 || res.status >= 400) return '#fff5f5'; // エラー
    if (res.status >= 300) return '#fffaf0'; // リダイレクト
    return '#f0fff4'; // 正常
  }

  return (
    <div style={{ padding: '40px', fontFamily: 'system-ui', maxWidth: '900px', margin: '0 auto' }}>
      <h1>🛰️ GoPulse Monitor {isConnected? '🟢' : '🔴'}</h1>

      <form onSubmit={addUrl} style={{ marginBottom: '30px', display: 'flex', gap: '10px' }}>
        <input
          type="url"
          value={inputUrl}
          onChange={(e) => setInputUrl(e.target.value)}
          placeholder="https://example.com"
          style={{ padding: '10px', flex: 1, borderRadius: '4px', border: '1px solid #ccc' }}
          required
        />
        <button type="submit" style={{ padding: '10px 20px', cursor: 'pointer' }}>
          監視を追加
        </button>
      </form>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(400px, 1fr))', gap: '20px' }}>
        {Object.values(results).sort((a,b) => a.url.localeCompare(b.url)).map((res) => (
          <div key={res.url} style={{
            padding: '20px', borderRadius: '12px', border: '1px solid #eee',
            boxShadow: '0 4px 6px rgba(0,0,0,0.05)', backgroundColor: getStatusColor(res), position: 'relative'
          }}>
            <button onClick={() => removeUrl(res.url)} style={{
              position: 'absolute', top: '10px', right: '10px', border: 'none',
              background: 'none', cursor: 'pointer', fontSize: '18px'
            }}>🗑️</button>

            <h3 style={{ margin: '0 0 10px 0', fontSize: '1rem', overflowWrap: 'anywhere' }}>{res.url}</h3>
            {res.status === 0 && !res.error ? (
              <p style={{ color: '#718096', margin: 0 }}>チェック待機中...</p>
            ) : res.error ? (
              <p style={{ color: '#e53e3e', margin: 0, fontSize: '0.9rem' }}>
                {res.status === -1 ? '接続失敗' : 'エラー'}: {res.error}
              </p>
            ) : (
              <div>
                <span style={{
                  padding: '4px 8px', borderRadius: '4px',
                  backgroundColor: res.status < 400 ? '#c6f6d5' : '#fed7d7',
                  fontSize: '0.8rem', fontWeight: 'bold'
                }}>{res.status}</span>
                <span style={{ marginLeft: '10px', color: '#666', fontSize: '0.9rem' }}>
                  {res.latency}ms
                </span>
                {res.checked_at && (
                  <p style={{ margin: '8px 0 0 0', fontSize: '0.75rem', color: '#a0aec0' }}>
                    {new Date(res.checked_at * 1000).toLocaleTimeString()}
                  </p>
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

export default App;