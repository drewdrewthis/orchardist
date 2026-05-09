// palette.jsx — ⌘K command palette overlay. Searches the merged corpus
// (worktrees, sessions, PRs, issues, contracts, hosts) and exposes actions.
// Filter syntax: host:drew-mac status:attn repo:orchard.

function parseFilters(q) {
  const filters = { host: [], status: [], repo: [] };
  let rest = q;
  rest = rest.replace(/(host|status|repo):([^\s]+)/gi, (m, k, v) => {
    filters[k.toLowerCase()].push(v.toLowerCase());
    return '';
  });
  return { rest: rest.trim(), filters };
}

function searchPalette(query, entries, actions) {
  const { rest, filters } = parseFilters(query);
  const out = [];
  // Actions only when query is non-empty or always (we'll rank later)
  for (const a of actions) {
    if (!rest && Object.values(filters).every(v => v.length === 0)) {
      out.push({ ...a, score: 50, idx: [] });
      continue;
    }
    const m = fuzzyMatch(rest, a.label) || fuzzyMatch(rest, a.keywords);
    if (m) out.push({ ...a, score: m.score - 5, idx: m.idx[0] != null && m.idx[m.idx.length - 1] < a.label.length ? m.idx : [] });
  }
  for (const e of entries) {
    if (filters.host.length && !filters.host.some(h => e.host?.toLowerCase().includes(h))) continue;
    if (filters.repo.length && !filters.repo.some(r => (e.sub || '').toLowerCase().includes(r) || e.label.toLowerCase().includes(r))) continue;
    if (!rest) {
      if (Object.values(filters).some(v => v.length > 0)) {
        out.push({ ...e, score: 30, idx: [] });
      }
      continue;
    }
    const m = fuzzyMatch(rest, e.label) || fuzzyMatch(rest, e.keywords);
    if (m) out.push({ ...e, score: m.score, idx: m.idx[0] != null && m.idx[m.idx.length - 1] < e.label.length ? m.idx : [] });
  }
  return out.sort((a, b) => b.score - a.score).slice(0, 50);
}

const KIND_ICON = {
  worktree: IconGitBranch,
  session: IconChat,
  pr: IconPullRequest,
  issue: IconIssue,
  contract: IconDocs,
  host: IconHost,
  action: IconBolt,
};

function Palette({ open, onClose, surface, onPick, items, actions }) {
  const [query, setQuery] = React.useState('');
  const [active, setActive] = React.useState(0);
  const inputRef = React.useRef(null);
  const listRef  = React.useRef(null);
  React.useEffect(() => {
    if (open) {
      setTimeout(() => inputRef.current?.focus(), 30);
      setQuery('');
      setActive(0);
    }
  }, [open]);

  const results = React.useMemo(() => searchPalette(query, items, actions), [query, items, actions]);
  React.useEffect(() => { setActive(0); }, [query]);

  React.useEffect(() => {
    if (!open) return;
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); onClose(); }
      else if (e.key === 'ArrowDown') { e.preventDefault(); setActive(a => Math.min(results.length - 1, a + 1)); }
      else if (e.key === 'ArrowUp')   { e.preventDefault(); setActive(a => Math.max(0, a - 1)); }
      else if (e.key === 'Enter')     {
        e.preventDefault();
        const r = results[active]; if (r) onPick(r);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, results, active, onClose, onPick]);

  // Scroll active into view inside the list — never on the page.
  React.useEffect(() => {
    if (!listRef.current) return;
    const list = listRef.current;
    const el = list.querySelector(`[data-i="${active}"]`);
    if (!el) return;
    const lr = list.getBoundingClientRect();
    const er = el.getBoundingClientRect();
    if (er.top < lr.top + 40) list.scrollTop -= (lr.top + 40 - er.top);
    else if (er.bottom > lr.bottom - 8) list.scrollTop += (er.bottom - lr.bottom + 8);
  }, [active]);

  if (!open) return null;
  // Group results by category — actions stay grouped together at the top.
  const grouped = (() => {
    const order = ['Actions', 'Worktrees', 'Sessions', 'Pull requests', 'Issues', 'Contracts', 'Hosts'];
    const map = new Map();
    for (let i = 0; i < results.length; i++) {
      const r = results[i];
      const g = r.kind === 'action' ? 'Actions' : r.group;
      if (!map.has(g)) map.set(g, []);
      map.get(g).push({ r, i });
    }
    return order.filter(o => map.has(o)).map(o => ({ name: o, rows: map.get(o) }));
  })();

  const isMobile = surface === 'mobile';
  const { rest, filters } = parseFilters(query);

  return (
    <div className={`palette-scrim ${isMobile ? 'mobile' : ''} fadeIn`} onClick={onClose}>
      <div className={`palette ${isMobile ? 'mobile' : ''} glass-strong scaleIn`} onClick={e => e.stopPropagation()}>
        <div className="palette-input-row">
          <IconSearch s={16} />
          <input ref={inputRef} className="palette-input"
                 placeholder="Search anchors or run a command…"
                 value={query} onChange={(e) => setQuery(e.target.value)} />
          <span className="kbd" style={{ fontSize: 10.5 }}>esc</span>
        </div>
        {(filters.host.length + filters.status.length + filters.repo.length > 0) && (
          <div className="palette-filters">
            {filters.host.map(h => <span key={'h'+h} className="filter-pill"><span className="dimer mono" style={{ fontSize: 10 }}>host:</span><span className="mono">{h}</span></span>)}
            {filters.status.map(s => <span key={'s'+s} className="filter-pill"><span className="dimer mono" style={{ fontSize: 10 }}>status:</span><span>{s}</span></span>)}
            {filters.repo.map(r => <span key={'r'+r} className="filter-pill"><span className="dimer mono" style={{ fontSize: 10 }}>repo:</span><span className="mono">{r}</span></span>)}
          </div>
        )}
        <div className="palette-results scroll" ref={listRef}>
          {grouped.map(g => (
            <div key={g.name} className="palette-group">
              <div className="palette-group-name">{g.name}</div>
              {g.rows.map(({ r, i }) => {
                const Icon = KIND_ICON[r.kind] || IconDot;
                return (
                  <div key={r.anchor || r.action || r.label} data-i={i}
                       className={`palette-row ${i === active ? 'active' : ''}`}
                       onMouseEnter={() => setActive(i)}
                       onClick={() => onPick(r)}>
                    <div className="palette-row-icon"><Icon s={14} /></div>
                    <div className="palette-row-body">
                      <div className="palette-row-label"><HiLite text={r.label} idx={r.idx} /></div>
                      {r.sub && <div className="palette-row-sub mono">{r.sub}</div>}
                    </div>
                    {r.host && r.kind !== 'action' && <HostGlyphAuto host={r.host} size={12} />}
                    {r.shortcut && <div className="palette-row-shortcut">{r.shortcut.map((s, j) => <span key={j} className="kbd">{s}</span>)}</div>}
                  </div>
                );
              })}
            </div>
          ))}
          {results.length === 0 && (
            <div className="palette-empty">
              <span className="dimer">No matches.</span>
              <span className="dimest" style={{ fontSize: 12 }}>Try <span className="mono">host:drew-mac</span> or <span className="mono">status:attn</span></span>
            </div>
          )}
        </div>
        <div className="palette-foot mono">
          <span><span className="kbd">↑</span><span className="kbd">↓</span> navigate</span>
          <span><span className="kbd">↵</span> open</span>
          <span><span className="kbd">⌘</span><span className="kbd">↵</span> open in new pane</span>
          <span style={{ marginLeft: 'auto' }}>{results.length} {results.length === 1 ? 'result' : 'results'}</span>
        </div>
      </div>
    </div>
  );
}

Object.assign(window, { Palette, parseFilters, searchPalette });
