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

  return (
    <div style={{ padding: '20px', fontFamily: 'sans-serif', maxWidth: '800px', margin: '0 auto' }}>
      <h1>GoPulse Dashboard</h1>
      <p>バックエンドから5秒ごとにステータスを受信しています...</p>

      <div style={{ display: 'grid', gap: '15px' }}>
        {Object.values(results).map((res) => (
          <div
            key={res.url}
            style={{
              padding: '15px',
              border: '1px solid #ddd',
              borderRadius: '8px',
              backgroundColor: res.error || res.status >= 400 ? '#ffebee' : '#e8f5e9',
              boxShadow: '0 2px 4px rgba(0,0,0,0.1)'
            }}
          >
            <h3 style={{ margin: '0 0 10px 0', fontSize: '1.2rem' }}>{res.url}</h3>
            {res.error ? (
              <p style={{ color: '#c62828', margin: 0 }}><strong>Error:</strong> {res.error}</p>
            ) : (
              <p style={{ margin: 0, color: '#2e7d32' }}>
                Status: <strong>{res.status}</strong> | Latency: <strong>{res.latency}ms</strong>
              </p>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

export default App;