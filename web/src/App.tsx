import { useState, useEffect } from 'react';
import { Dashboard } from './components/Dashboard';
import { ApiPlayground } from './components/ApiPlayground';
import { CommandConsole } from './components/CommandConsole';
import { Ros2Panel } from './components/Ros2Panel';

type Tab = 'dashboard' | 'playground' | 'console' | 'ros2';

function App() {
  const [tab, setTab] = useState<Tab>('dashboard');
  const [apiStatus, setApiStatus] = useState<'checking' | 'online' | 'offline'>('checking');

  useEffect(() => {
    const check = async () => {
      try {
        const res = await fetch('/healthz');
        setApiStatus(res.ok ? 'online' : 'offline');
      } catch {
        setApiStatus('offline');
      }
    };
    check();
    const interval = setInterval(check, 5000);
    return () => clearInterval(interval);
  }, []);

  return (
    <div className="app">
      <header className="header">
        <div className="header-left">
          <div className="logo">FleetOS <span>Playground</span></div>
          <div className="connection-status">
            <div className={`status-dot ${apiStatus === 'online' ? 'connected' : ''}`} />
            API: {apiStatus === 'checking' ? 'Checking...' : apiStatus === 'online' ? 'Connected' : 'Offline'}
          </div>
        </div>
        <nav className="nav">
          <button className={`nav-btn ${tab === 'dashboard' ? 'active' : ''}`} onClick={() => setTab('dashboard')}>
            Dashboard
          </button>
          <button className={`nav-btn ${tab === 'playground' ? 'active' : ''}`} onClick={() => setTab('playground')}>
            API Explorer
          </button>
          <button className={`nav-btn ${tab === 'console' ? 'active' : ''}`} onClick={() => setTab('console')}>
            Console
          </button>
          <button className={`nav-btn ${tab === 'ros2' ? 'active' : ''}`} onClick={() => setTab('ros2')}>
            ROS 2
          </button>
        </nav>
      </header>
      <main className="main">
        {apiStatus === 'offline' ? (
          <OfflineBanner />
        ) : (
          <>
            {tab === 'dashboard' && <Dashboard />}
            {tab === 'playground' && <ApiPlayground />}
            {tab === 'console' && <CommandConsole />}
            {tab === 'ros2' && <Ros2Panel />}
          </>
        )}
      </main>
    </div>
  );
}

function OfflineBanner() {
  return (
    <div style={{
      display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
      height: '60vh', gap: 16, color: 'var(--text-muted)',
    }}>
      <div style={{ fontSize: 48 }}>&#9888;</div>
      <div style={{ fontSize: 20, fontWeight: 600, color: 'var(--text-primary)' }}>
        API Server Offline
      </div>
      <div style={{ maxWidth: 480, textAlign: 'center', lineHeight: 1.6 }}>
        The FleetOS API is not reachable. Start the backend services:
      </div>
      <pre style={{ maxWidth: 500, width: '100%' }}>
{`# Start everything with Docker Compose
docker compose up -d

# Or run services individually
make run-ingestion   # Terminal 1
make run-api         # Terminal 2
make run-simulator   # Terminal 3`}
      </pre>
      <div style={{ fontSize: 12 }} className="pulse">
        Retrying every 5 seconds...
      </div>
    </div>
  );
}

export default App;
