import { useEffect, useState } from "react";
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer, ReferenceArea } from 'recharts';

// Go側で定義したのと同じ型をTypeScriptでも定義します
interface Result {
  url: string;
  status: number;
  latency: number;
  error?: string;
  checked_at?: number;
}

interface HistoryPoint {
  status: number;
  latency: number;
  error: string;
  checked_at: number;
}

interface ResultWithHistory extends Result {
  history?: HistoryPoint[];
  isDown?: boolean;
}

function App() {
  const [results, setResults] = useState<Record<string, ResultWithHistory>>({});
  const [inputUrl, setInputUrl] = useState(''); // 入力フォーム用の状態
  const [isConnected, setIsConnected] = useState(false)

  // 履歴を取得する関数
  const fetchHistory = async (url: string) => {
    try {
      const res = await fetch(`http://localhost:8080/history?url=${encodeURIComponent(url)}`);
      const history: HistoryPoint[] = await res.json();
      setResults(prev => ({
        ...prev,
        [url]: {...prev[url], history }
      }));
    } catch(e) {
      console.error('履歴の取得に失敗:', e);
    }

  };

  useEffect(() => {
    // 1. 初回ロード: 現在の監視対象を取得して枠だけ出す
    fetch('http://localhost:8080/status')
      .then(res => res.json())
      .then((data: Result[]) => {
        const initial: Record<string, ResultWithHistory> = {};
        data.forEach(d => { 
          initial[d.url] = { 
            ...d, 
            history: [],
            isDown: !!d.error || d.status >= 400 || d.status === -1
          };
          // 初回表示時に履歴も取る
          fetchHistory(d.url);
        });
        setResults(initial)
      })

    // 1. Goのバックエンド（SSE）に接続
    const eventSource = new EventSource('http://localhost:8080/stream');

    eventSource.onopen = () => setIsConnected(true)
    eventSource.onmessage = (event) => {
      const data: Result = JSON.parse(event.data);
      setResults((prev) => {
        const prevData = prev[data.url];
        // 新しい履歴ポイントを作る
        const newHistoryPoint: HistoryPoint = {
          status: data.status,
          latency: data.latency,
          error: data.error || '',
          checked_at: data.checked_at || Math.floor(Date.now() / 1000)
        };

        // 288→720に増やす。1時間分を5秒間隔で保持
        const updatedHistory = [...(prevData?.history || []), newHistoryPoint].slice(-720);

        const updated: ResultWithHistory = {
          ...data, // Resultのプロパティを展開
          history: updatedHistory, // historyを追加
          isDown: !!data.error || data.status >= 400 || data.status === -1
        };

        return {
          ...prev,
          [data.url]: updated
        };
      });
    }
    eventSource.onerror = () => {
      setIsConnected(false);
      console.error('SSE Error: 接続が切れました');
    }

    return () => eventSource.close();
  }, []);

  // URLを追加する関数
  const addUrl = async(e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault(); // 画面リロードを防止
    if(!inputUrl) return

    // 楽観的UI更新: 即座に追加
    setResults(prev => ({ ...prev, [inputUrl]: { url: inputUrl, status: 0, latency: 0, history: [], isDown: false } }));

    await fetch('http://localhost:8080/urls', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: inputUrl }),
    });
    setTimeout(() => fetchHistory(inputUrl), 1000); // 少し遅らせて履歴も取る
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

  const formatTime = (timestamp: number) => {
    return new Date(timestamp * 1000).toLocaleTimeString('ja-JP', {
      hour: '2-digit',
      minute: '2-digit',
    });
  }

  const getDownRanges = (history: HistoryPoint[]) => {
    const ranges: {x1: number, x2: number}[] = [];
    let start: number | null = null;

    history.forEach(point => {
      const isDown = point.error !== '' || point.status >= 400 || point.status === -1;
      if (isDown && start === null) {
        start = point.checked_at
      } else if (!isDown && start !== null) {
        ranges.push({ x1: start, x2: point.checked_at});
        start = null;
      }
    });
    if (start !== null && history.length > 0) {
      ranges.push({ x1: start, x2: history[history.length - 1].checked_at });
    }
    return ranges;
  }

  return (
    <div style={{ padding: '40px', fontFamily: 'system-ui', maxWidth: '1200px', margin: '0 auto' }}>
      <h1>🛰️ GoPulse Monitor {isConnected ? '🟢' : '🔴'}</h1>

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

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(500px, 1fr))', gap: '20px' }}>
        {Object.values(results).sort((a, b) => a.url.localeCompare(b.url)).map((res) => {
          const downRanges = res.history ? getDownRanges(res.history) : [];
          
          return (
            <div key={res.url} style={{
              padding: '20px', borderRadius: '12px', border: '1px solid #eee',
              boxShadow: '0 4px 6px rgba(0,0,0,0.05)', 
              backgroundColor: getStatusColor(res), 
              position: 'relative'
            }}>
              <button onClick={() => removeUrl(res.url)} style={{
                position: 'absolute', top: '10px', right: '10px', border: 'none',
                background: 'none', cursor: 'pointer', fontSize: '18px'
              }}>🗑️</button>

              <h3 style={{ margin: '0 0 10px 0', fontSize: '1rem', overflowWrap: 'anywhere' }}>
                {res.url}
                {res.isDown && <span style={{ marginLeft: '8px', color: '#e53e3e' }}>● DOWN</span>}
              </h3>
              
              {res.status === 0 && !res.error ? (
                <p style={{ color: '#718096', margin: 0 }}>チェック待機中...</p>
              ) : res.error ? (
                <p style={{ color: '#e53e3e', margin: 0, fontSize: '0.9rem' }}>
                  {res.status === -1 ? '接続失敗' : 'エラー'}: {res.error}
                </p>
              ) : (
                <div style={{ marginBottom: '12px' }}>
                  <span style={{
                    padding: '4px 8px', borderRadius: '4px',
                    backgroundColor: res.status < 400 ? '#c6f6d5' : '#fed7d7',
                    fontSize: '0.8rem', fontWeight: 'bold'
                  }}>{res.status}</span>
                  <span style={{ marginLeft: '10px', color: '#666', fontSize: '0.9rem' }}>
                    {res.latency}ms
                  </span>
                  {res.checked_at && (
                    <span style={{ marginLeft: '10px', fontSize: '0.75rem', color: '#a0aec0' }}>
                      {formatTime(res.checked_at)}
                    </span>
                  )}
                </div>
              )}

              {res.history && res.history.length > 1 && (
                <div style={{ width: '100%', height: '120px', marginTop: '12px' }}>
                  <ResponsiveContainer>
                    <LineChart data={res.history}>
                      <XAxis 
                        dataKey="checked_at" 
                        tickFormatter={(value) => formatTime(Number(value))}
                        stroke="#a0aec0"
                        fontSize={10}
                      />
                      <YAxis 
                        stroke="#a0aec0"
                        fontSize={10}
                        domain={['dataMin - 50', 'dataMax + 50']}
                      />
                      <Tooltip 
                        labelFormatter={(label) => formatTime(Number(label))}
                        formatter={(value) => [`${value}ms`, 'レイテンシ']}
                      />
                      {downRanges.map((range, i) => (
                        <ReferenceArea 
                          key={i}
                          x1={range.x1} 
                          x2={range.x2} 
                          fill="#fed7d7" 
                          fillOpacity={0.5}
                        />
                      ))}
                      <Line 
                        type="monotone" 
                        dataKey="latency" 
                        stroke="#38a169" 
                        strokeWidth={2} 
                        dot={false}
                        connectNulls
                      />
                    </LineChart>
                  </ResponsiveContainer>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

export default App;