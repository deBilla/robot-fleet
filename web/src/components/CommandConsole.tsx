import { useState, useRef, useEffect } from 'react';
import { api } from '../api';

interface ConsoleLine {
  timestamp: string;
  direction: 'send' | 'recv' | 'err' | 'info';
  message: string;
}

const HELP_TEXT = `Available commands:
  robots              - List all robots
  robot <id>          - Get robot details
  telemetry <id>      - Get robot telemetry
  move <id> <x> <y>   - Move robot to position
  stop <id>           - Stop robot
  estop <id>          - Emergency stop robot
  forward <id> [dist] - Move robot forward
  dance <id>          - Make robot dance
  wave <id>           - Make robot wave hello
  bow <id>            - Make robot bow
  sit <id>            - Make robot sit down
  jump <id>           - Make robot jump
  look <id>           - Make robot look around
  stretch <id>        - Make robot stretch
  semantic <id> <msg> - Natural language command (GR00T-style)
  infer <instruction> - Run AI inference
  metrics             - Get fleet metrics
  usage               - Get API usage
  health              - Check API health
  clear               - Clear console
  help                - Show this help`;

export function CommandConsole() {
  const [lines, setLines] = useState<ConsoleLine[]>([
    { timestamp: now(), direction: 'info', message: 'FleetOS Command Console v0.1.0' },
    { timestamp: now(), direction: 'info', message: 'Type "help" for available commands.' },
  ]);
  const [input, setInput] = useState('');
  const [cmdHistory, setCmdHistory] = useState<string[]>([]);
  const [historyIdx, setHistoryIdx] = useState(-1);
  const outputRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }
  }, [lines]);

  const addLine = (direction: ConsoleLine['direction'], message: string) => {
    setLines((prev) => [...prev, { timestamp: now(), direction, message }]);
  };

  const handleCommand = async (cmd: string) => {
    const trimmed = cmd.trim();
    if (!trimmed) return;

    setCmdHistory((prev) => [trimmed, ...prev.slice(0, 49)]);
    setHistoryIdx(-1);
    addLine('send', trimmed);

    const parts = trimmed.split(/\s+/);
    const command = parts[0].toLowerCase();

    try {
      switch (command) {
        case 'help':
          addLine('info', HELP_TEXT);
          break;

        case 'clear':
          setLines([]);
          break;

        case 'health': {
          const res = await api.healthz();
          addLine('recv', `[${res.status}] ${JSON.stringify(res.data)}`);
          break;
        }

        case 'robots': {
          const res = await api.listRobots(50);
          if (res.ok) {
            const robots = res.data.robots;
            if (robots && robots.length > 0) {
              addLine('recv', `Found ${res.data.total} robots:`);
              for (const r of robots) {
                addLine('recv', `  ${r.id}  status=${r.status}  battery=${Math.round(r.battery_level * 100)}%  pos=(${r.pos_x?.toFixed(2)}, ${r.pos_y?.toFixed(2)})`);
              }
            } else {
              addLine('recv', 'No robots found');
            }
          } else {
            addLine('err', `Error: ${JSON.stringify(res.data)}`);
          }
          break;
        }

        case 'robot': {
          const id = parts[1] || 'robot-0001';
          const res = await api.getRobot(id);
          addLine('recv', `[${res.status}] ${JSON.stringify(res.data, null, 2)}`);
          break;
        }

        case 'telemetry': {
          const id = parts[1] || 'robot-0001';
          const res = await api.getTelemetry(id);
          addLine('recv', `[${res.status}] ${JSON.stringify(res.data, null, 2)}`);
          break;
        }

        case 'move': {
          const id = parts[1] || 'robot-0001';
          const x = parseFloat(parts[2]) || 0;
          const y = parseFloat(parts[3]) || 0;
          const res = await api.sendCommand(id, 'move', { x, y });
          addLine('recv', `[${res.status}] ${JSON.stringify(res.data)}`);
          break;
        }

        case 'stop': {
          const id = parts[1] || 'robot-0001';
          const res = await api.sendCommand(id, 'stop', { emergency: false });
          addLine('recv', `[${res.status}] ${JSON.stringify(res.data)}`);
          break;
        }

        case 'estop': {
          const id = parts[1] || 'robot-0001';
          const res = await api.sendCommand(id, 'stop', { emergency: true });
          addLine('recv', `[${res.status}] ${JSON.stringify(res.data)}`);
          break;
        }

        case 'forward': {
          const id = parts[1] || 'robot-0001';
          const dist = parseFloat(parts[2]) || 1.0;
          const res = await api.sendCommand(id, 'move_relative', { direction: 'forward', distance: dist });
          addLine('recv', `[${res.status}] ${JSON.stringify(res.data)}`);
          break;
        }

        case 'dance':
        case 'wave':
        case 'bow':
        case 'sit':
        case 'jump':
        case 'look':
        case 'stretch': {
          const id = parts[1] || 'robot-0001';
          const actionType = command === 'look' ? 'look_around' : command;
          const res = await api.sendCommand(id, actionType, { instruction: command });
          addLine('recv', `[${res.status}] ${id} is now ${command}ing`);
          break;
        }

        case 'semantic': {
          const id = parts[1] || 'robot-0001';
          const instruction = parts.slice(2).join(' ') || 'walk forward';
          const res = await api.semanticCommand(id, instruction);
          addLine('recv', `[${res.status}] ${JSON.stringify(res.data, null, 2)}`);
          break;
        }

        case 'infer': {
          const instruction = parts.slice(1).join(' ') || 'pick up the red block';
          const res = await api.runInference('', instruction);
          addLine('recv', `[${res.status}] ${JSON.stringify(res.data, null, 2)}`);
          break;
        }

        case 'metrics': {
          const res = await api.getFleetMetrics();
          addLine('recv', `[${res.status}] ${JSON.stringify(res.data, null, 2)}`);
          break;
        }

        case 'usage': {
          const res = await api.getUsage();
          addLine('recv', `[${res.status}] ${JSON.stringify(res.data, null, 2)}`);
          break;
        }

        default:
          addLine('err', `Unknown command: ${command}. Type "help" for available commands.`);
      }
    } catch (e) {
      addLine('err', `Request failed: ${e instanceof Error ? e.message : 'unknown error'}`);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      handleCommand(input);
      setInput('');
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (cmdHistory.length > 0) {
        const newIdx = Math.min(historyIdx + 1, cmdHistory.length - 1);
        setHistoryIdx(newIdx);
        setInput(cmdHistory[newIdx]);
      }
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (historyIdx > 0) {
        const newIdx = historyIdx - 1;
        setHistoryIdx(newIdx);
        setInput(cmdHistory[newIdx]);
      } else {
        setHistoryIdx(-1);
        setInput('');
      }
    }
  };

  return (
    <div className="console">
      <div className="console-output" ref={outputRef}>
        {lines.map((line, i) => (
          <div className="console-line" key={i}>
            <span className="timestamp">{line.timestamp}</span>
            <span className={`direction ${line.direction}`}>
              {line.direction === 'send' ? '>' : line.direction === 'recv' ? '<' : line.direction === 'err' ? '!' : '*'}
            </span>
            <span style={{ whiteSpace: 'pre-wrap' }}>{line.message}</span>
          </div>
        ))}
      </div>
      <div className="console-input">
        <input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="Enter command... (try 'help')"
          autoFocus
        />
        <button className="btn btn-primary" onClick={() => { handleCommand(input); setInput(''); }}>
          Send
        </button>
      </div>
    </div>
  );
}

function now() {
  return new Date().toLocaleTimeString('en-US', { hour12: false });
}
