// new-conversation.jsx — modal for launching a new conversation.
// Anchor in the brief: pick a worktree (worktree anchor) + a host + optional cwd
// → daemon spawns a ClaudeInstance to fill the new ClaudeSession anchor.

function NewConversation({ open, onClose, items, hosts, surface, onLaunch }) {
  const [worktreeId, setWorktreeId] = React.useState(items[0]?.id || '');
  const [host, setHost]             = React.useState('');
  const [model, setModel]           = React.useState('claude-sonnet-4-5');
  const [task, setTask]             = React.useState('');
  const taRef = React.useRef(null);
  const inputRef = React.useRef(null);

  React.useEffect(() => {
    if (open) {
      setWorktreeId(items[0]?.id || '');
      setHost(items[0]?.host || hosts[0]?.hostname || '');
      setModel('claude-sonnet-4-5');
      setTask('');
      setTimeout(() => inputRef.current?.focus(), 30);
    }
  }, [open]);

  const wt = items.find(i => i.id === worktreeId);

  React.useEffect(() => {
    if (!open) return;
    const onKey = (e) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  if (!open) return null;
  const isMobile = surface === 'mobile';

  const cwd = wt?.path || '~/code';

  return (
    <div className={`nc-scrim ${isMobile ? 'mobile' : ''} fadeIn`} onClick={onClose}>
      <div className={`nc-sheet ${isMobile ? 'mobile' : ''} glass-strong scaleIn`} onClick={e => e.stopPropagation()}>
        <div className="nc-head">
          <div>
            <b style={{ fontSize: 15 }}>Launch new conversation</b>
            <div className="dimer" style={{ fontSize: 12 }}>This composes a new <span className="mono">ClaudeSession</span> anchor on the picked host.</div>
          </div>
          <button className="iconbtn" onClick={onClose}><IconClose s={14} /></button>
        </div>
        <div className="nc-body">
          {/* Worktree picker */}
          <div className="nc-row">
            <div className="nc-row-label">Worktree</div>
            <div className="nc-row-control">
              <WorktreePicker items={items} value={worktreeId} onChange={(id) => { setWorktreeId(id); const it = items.find(i => i.id === id); if (it) setHost(it.host); }} inputRef={inputRef} />
            </div>
          </div>
          {/* Host picker */}
          <div className="nc-row">
            <div className="nc-row-label">Host</div>
            <div className="nc-row-control">
              <div className="nc-host-grid">
                {hosts.map(h => (
                  <button key={h.id} className={`nc-host ${host === h.hostname ? 'on' : ''} ${!h.reachable ? 'down' : ''}`}
                          disabled={!h.reachable}
                          onClick={() => setHost(h.hostname)}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <HostGlyphAuto host={h.hostname} size={16} />
                      <b className="mono" style={{ fontSize: 12.5 }}>{h.hostname}</b>
                      {!h.reachable && <span className="chip bad" style={{ height: 16, fontSize: 10, padding: '0 6px' }}>down</span>}
                    </div>
                    <div className="dimest mono" style={{ fontSize: 10.5, marginTop: 6 }}>{h.os.split(' ')[0]}</div>
                    {h.reachable && (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 8 }}>
                        <span className="dimer mono" style={{ fontSize: 10 }}>cpu</span>
                        <ResourceBar value={h.load.cpu} w={48} color={h.load.cpu > 70 ? 'var(--attn)' : 'var(--fg-3)'} />
                        <span className="mono dimer tnum" style={{ fontSize: 10 }}>{h.load.cpu}%</span>
                      </div>
                    )}
                  </button>
                ))}
              </div>
            </div>
          </div>
          {/* CWD preview */}
          <div className="nc-row">
            <div className="nc-row-label">Working dir</div>
            <div className="nc-row-control">
              <div className="nc-cwd mono">{cwd}</div>
            </div>
          </div>
          {/* Model */}
          <div className="nc-row">
            <div className="nc-row-label">Model</div>
            <div className="nc-row-control">
              <div className="seg" style={{ width: 'fit-content' }}>
                <div className="seg-thumb" style={{ left: `calc(2px + ${['claude-haiku-4-5','claude-sonnet-4-5','claude-opus-4-1'].indexOf(model)} * (100% - 4px) / 3)`, width: 'calc((100% - 4px) / 3)' }} />
                {[
                  { v: 'claude-haiku-4-5', l: 'Haiku' },
                  { v: 'claude-sonnet-4-5', l: 'Sonnet' },
                  { v: 'claude-opus-4-1', l: 'Opus' },
                ].map(o => (
                  <button key={o.v} data-on={model === o.v ? '1' : '0'} onClick={() => setModel(o.v)}>{o.l}</button>
                ))}
              </div>
            </div>
          </div>
          {/* First task */}
          <div className="nc-row" style={{ alignItems: 'flex-start' }}>
            <div className="nc-row-label">First task</div>
            <div className="nc-row-control">
              <textarea ref={taRef} className="input" rows={3}
                        placeholder="What should the agent do? Optional — you can also start with no message."
                        value={task} onChange={(e) => setTask(e.target.value)}
                        style={{ height: 'auto', minHeight: 64, padding: 10, resize: 'vertical' }} />
            </div>
          </div>
        </div>
        <div className="nc-foot">
          <span className="dimer mono" style={{ fontSize: 11 }}>
            anchor: <span style={{ color: 'var(--accent)' }}>(host:{host}, cwd:{wt?.path?.split('/').slice(-2).join('/') || ''})</span>
          </span>
          <div style={{ display: 'flex', gap: 8 }}>
            <button className="btn-ghost" onClick={onClose}>Cancel</button>
            <button className="btn-primary" onClick={() => onLaunch({ worktreeId, host, model, task })} disabled={!host}>
              <IconSparkle s={13} /> Launch
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function WorktreePicker({ items, value, onChange, inputRef }) {
  const [q, setQ] = React.useState('');
  const [open, setOpen] = React.useState(false);
  const cur = items.find(i => i.id === value);
  const matches = q
    ? items.map(it => ({ it, m: fuzzyMatch(q, `${it.repo} ${it.branch} ${it.title}`) })).filter(x => x.m).sort((a,b) => b.m.score - a.m.score).slice(0, 8)
    : items.slice(0, 8).map(it => ({ it, m: { score: 0, idx: [] } }));
  return (
    <div className="wt-picker">
      <div className="wt-picker-input">
        <IconSearch s={13} />
        <input ref={inputRef} className="input" style={{ height: 32, background: 'transparent', border: 0, padding: 0 }}
               placeholder={cur ? `${cur.repo} · ${cur.branch}` : 'Pick a worktree…'}
               value={q} onChange={(e) => { setQ(e.target.value); setOpen(true); }}
               onFocus={() => setOpen(true)}
               onBlur={() => setTimeout(() => setOpen(false), 150)} />
        {cur && !q && (
          <div className="wt-picker-current">
            <span className="chip ghost" style={{ height: 18, fontSize: 11, padding: '0 6px' }}>
              <HostGlyphAuto host={cur.host} size={10} />
              <span className="mono">{cur.repo}</span>
              <span className="dimer">·</span>
              <span className="mono">{cur.branch}</span>
            </span>
          </div>
        )}
      </div>
      {open && (
        <div className="wt-picker-list scroll">
          {matches.map(({ it }) => (
            <div key={it.id} className={`wt-picker-item ${it.id === value ? 'on' : ''}`}
                 onMouseDown={() => { onChange(it.id); setOpen(false); setQ(''); }}>
              <HostGlyphAuto host={it.host} size={12} />
              <span className="mono dimer" style={{ fontSize: 11, width: 110 }}>{it.repo.split('/')[1]}</span>
              <span className="mono" style={{ fontSize: 12 }}>{it.branch}</span>
              <span className="dimest" style={{ fontSize: 11, marginLeft: 'auto' }}>{relTime(it.lastActivity, Date.now())}</span>
            </div>
          ))}
          {matches.length === 0 && <div className="wt-picker-empty dimer">No matches</div>}
        </div>
      )}
    </div>
  );
}

Object.assign(window, { NewConversation, WorktreePicker });
