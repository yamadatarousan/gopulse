import { useEffect, useState } from "react";

// Go側で定義したのと同じ型をTypeScriptでも定義します
interface Result {
  url: string;
  status: number;
  latency: number;
  error?: string;
}

function App() {
  const [results, setResults] = useState<Record<string, Result>>({});
  const [inputUrl, setInputUrl] = useState(''); // 入力フォーム用の状態

  useEffect(() => {
    // 1. Goのバックエンド（SSE）に接続
    const eventSource = new EventSource('http://localhost:8080/stream');

    // 2. メッセージを受信したときの処理
    eventSource.onmessage = (event) => {
      const data: Result = JSON.parse(event.data);
      setResults((prev) => ({
        ...prev,
        [data.url]: data,
      }));
    }

    // エラー時の処理
    eventSource.onerror = (error) => {
      console.error('SSE Error:', error);
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

    await fetch('http://localhost:8080/urls', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: inputUrl }),
    });

    setInputUrl('');
  };

  // URLを削除する関数
  const removeUrl = async (url: string) => {
    await fetch(`http://localhost:8080/urls?url=${encodeURIComponent(url)}`, {
      method: 'DELETE',
    });

    // 画面から即座に消去する（Goからの次の配信を待たずにUIをスッキリさせる）
    setResults((prev) => {
      const newResults = { ...prev };
      delete newResults[url];
      return newResults;
    });
  };

  return (
    <div style={{ padding: '40px', fontFamily: 'system-ui', maxWidth: '900px', margin: '0 auto' }}>
      <h1>🛰️ GoPulse Monitor</h1>

      {/* 追加フォーム */}
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

      {/* カード一覧 */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '20px' }}>
        {Object.values(results).map((res) => (
          <div
            key={res.url}
            style={{
              padding: '20px',
              borderRadius: '12px',
              border: '1px solid #eee',
              boxShadow: '0 4px 6px rgba(0,0,0,0.05)',
              backgroundColor: res.error ? '#fff5f5' : '#f8fff8',
              position: 'relative'
            }}
          >
            <button
              onClick={() => removeUrl(res.url)}
              style={{
                position: 'absolute',
                top: '10px',
                right: '10px',
                border: 'none',
                background: 'none',
                cursor: 'pointer',
                fontSize: '18px'
              }}
            >
              🗑️
            </button>

            <h3 style={{ margin: '0 0 10px 0', fontSize: '1rem', overflowWrap: 'anywhere' }}>{res.url}</h3>
            {res.error ? (
              <p style={{ color: '#e53e3e', margin: 0, fontSize: '0.9rem' }}>{res.error}</p>
            ) : (
              <div>
                <span style={{ 
                  padding: '4px 8px', 
                  borderRadius: '4px', 
                  backgroundColor: res.status === 200 ? '#c6f6d5' : '#fed7d7',
                  fontSize: '0.8rem',
                  fontWeight: 'bold'
                }}>
                    {res.status}
                </span>
                <span style={{ marginLeft: '10px', color: '#666', fontSize: '0.9rem' }}>
                  {res.latency}ms
                </span>
              </div>
            )}
          </div>
        ))}        
      </div>
    </div>
  );
}

export default App;