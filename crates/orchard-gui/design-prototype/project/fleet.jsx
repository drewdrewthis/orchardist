// fleet.jsx — fleet view: top bar, lens selector, filters, grouped list.

// ── Top bar ─────────────────────────────────────────────────────────────────
function FleetTopBar({ surface = 'desktop', onOpenPalette, onLaunch, onToggleSidebar, peerHealth, account, offline, onToggleTheme, theme }) {
  const reachable = peerHealth.filter((h) => h.reachable).length;
  const total = peerHealth.length;
  const allOk = reachable === total;
  return (
    <div className="fleet-topbar glass-strong" data-surface={surface}>
      <div className="fleet-topbar-inner">
        {surface === 'desktop' && onToggleSidebar &&
        <button className="iconbtn" onClick={onToggleSidebar} aria-label="Toggle sidebar">
            <IconSidebar s={16} />
          </button>
        }
        <div className="fleet-brand no-select">
          <span className="fleet-brand-mark">
            <OrchardMark s={14} />
          </span>
          <span className="fleet-brand-name">Orchard</span>
        </div>
        <div className="fleet-topbar-spacer" />
        {/* Search */}
        <button className="fleet-search-btn" onClick={onOpenPalette} aria-label="Search and command palette">
          <IconSearch s={14} />
          <span className="fleet-search-placeholder">Search or jump to…</span>
          {surface === 'desktop' &&
          <span style={{ display: 'inline-flex', gap: 3, marginLeft: 'auto' }}>
              <span className="kbd">⌘</span><span className="kbd">K</span>
            </span>
          }
        </button>
        {/* Peer health — ambient cluster of host pips */}
        <PeerHealthCluster hosts={peerHealth} />
        {/* Quota */}
        <div className="fleet-quota" title={`${account.quotaUsed}/${account.quotaCap} requests · resets ${relTime(account.quotaResetsAt, Date.now())}`}>
          <span className="mono dimer" style={{ fontSize: 11 }}>{account.quotaUsed}/{account.quotaCap}</span>
          <ResourceBar value={account.quotaUsed} max={account.quotaCap} w={28}
          color={account.quotaUsed / account.quotaCap > 0.8 ? 'var(--attn)' : 'var(--fg-3)'} />
        </div>
        <button className="iconbtn" onClick={onToggleTheme} aria-label="Toggle theme">
          {theme === 'dark' ? <IconSun s={15} /> : <IconMoon s={15} />}
        </button>
          <button className="btn-primary iconbtn-primary" onClick={onLaunch} title="New conversation · ⌘T" aria-label="New conversation" data-comment-anchor="026e92af10-button-44-11">
            <IconPlus s={15} />
          </button>
      </div>
    </div>);

}

// ── Peer health cluster ─────────────────────────────────────────────────────
// Three treatments via the `treatment` prop, but the cluster itself is the
// 'ambient' one. The 'statusbar' lives at the bottom of the page (BackendBar).
// 'hidden-until-issue' just renders the cluster only when something is wrong.
function PeerHealthCluster({ hosts, treatment = 'ambient' }) {
  const anyDown = hosts.some((h) => !h.reachable);
  const overload = hosts.some((h) => h.reachable && h.load && h.load.cpu > 85);
  if (treatment === 'hidden' && !anyDown && !overload) return null;
  return (
    <div className="peer-cluster" title="Peer health">
      {hosts.map((h) =>
      <div key={h.id} className="peer-pip-wrap" data-down={!h.reachable ? '1' : '0'}>
          <span className={`pip ${h.reachable ? h.load.cpu > 85 ? 'attn' : 'ok' : 'bad'}`} />
          <div className="peer-tip glass-strong">
            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <HostGlyphAuto host={h.hostname} size={16} />
              <b className="mono" style={{ fontSize: 12 }}>{h.hostname}</b>
              <span className="dimer mono" style={{ fontSize: 11 }}>{h.os.split(' ')[0]}</span>
            </div>
            {h.reachable ?
          <div className="peer-tip-grid">
                <span className="dimer">CPU</span>
                <span className="mono tnum">{h.load.cpu}%</span>
                <span className="dimer">Mem</span>
                <span className="mono tnum">{h.load.mem}%</span>
                <span className="dimer">Disk</span>
                <span className="mono tnum">{h.load.disk}%</span>
              </div> :

          <div className="dimer mono" style={{ fontSize: 11, marginTop: 4 }}>
                unreachable · last seen {relTime(h.lastSeenAt, Date.now())}
              </div>
          }
          </div>
        </div>
      )}
    </div>);

}

// ── Lens selector ───────────────────────────────────────────────────────────
const LENSES = [
{ id: 'attention', label: 'Attention', hint: 'What needs eyes', icon: IconBell },
{ id: 'host', label: 'Host', hint: 'By machine', icon: IconHost },
{ id: 'tmux', label: 'Tmux', hint: 'By tmux session · window · pane', icon: IconTerminal },
{ id: 'activity', label: 'Activity', hint: 'Recent first', icon: IconClock },
{ id: 'repo', label: 'Repo', hint: 'By project', icon: IconGitBranch },
{ id: 'issue', label: 'Issue', hint: 'By GitHub issue', icon: IconIssue }];


function LensSelector({ value, onChange, variant = 'pills' }) {
  if (variant === 'pills') {
    const idx = Math.max(0, LENSES.findIndex((l) => l.id === value));
    return (
      <div className="lens-pills lens-pills-icon no-select" role="tablist" data-comment-anchor="ca65d747e2-div-104-7">
        <div className="lens-thumb" style={{ left: `calc(2px + ${idx} * (100% - 4px) / ${LENSES.length})`, width: `calc((100% - 4px) / ${LENSES.length})` }} />
        {LENSES.map((l) => {
          const Icn = l.icon;
          return (
            <button key={l.id} role="tab" aria-selected={l.id === value} data-on={l.id === value ? '1' : '0'}
            title={`${l.label} · ${l.hint}`}
            onClick={() => onChange(l.id)}>
              {Icn ? <Icn s={14} /> : l.label}
            </button>);
        })}
      </div>);

  }
  if (variant === 'tabs') {
    return (
      <div className="lens-tabs no-select" role="tablist">
        {LENSES.map((l) =>
        <button key={l.id} role="tab" aria-selected={l.id === value}
        className={l.id === value ? 'on' : ''}
        onClick={() => onChange(l.id)}>{l.label}</button>
        )}
      </div>);

  }
  // dropdown
  const cur = LENSES.find((l) => l.id === value) || LENSES[0];
  const [open, setOpen] = React.useState(false);
  React.useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    setTimeout(() => window.addEventListener('click', close, { once: true }), 0);
  }, [open]);
  return (
    <div className="lens-dropdown no-select">
      <button className="btn-tonal" onClick={(e) => {e.stopPropagation();setOpen((o) => !o);}}>
        <IconLayers s={13} />
        <span>Group · {cur.label}</span>
        <IconChevronDown s={12} style={{ marginLeft: 2 }} />
      </button>
      {open &&
      <div className="lens-dropdown-menu glass-strong scaleIn">
          {LENSES.map((l) =>
        <button key={l.id} className={`lens-dropdown-item ${l.id === value ? 'on' : ''}`}
        onClick={() => {setOpen(false);onChange(l.id);}}>
              <span>{l.label}</span>
              <span className="dimer" style={{ fontSize: 11 }}>{l.hint}</span>
              {l.id === value && <IconCheck s={13} style={{ color: 'var(--accent)', marginLeft: 'auto' }} />}
            </button>
        )}
        </div>
      }
    </div>);

}

// ── Filter bar ──────────────────────────────────────────────────────────────
function FilterBar({ filters, onClear, onRemove, onAddFilter, allItems, hosts }) {
  const list = Array.isArray(filters) ? filters : [];
  const total = allItems.length;
  const filteredCount = applyFilters(allItems, list).length;
  const [dialogOpen, setDialogOpen] = React.useState(false);
  if (list.length === 0) {
    return (
      <>
        <div className="filter-bar empty">
          <span className="dimest mono" style={{ fontSize: 11 }}>{total} items</span>
          <button className="filter-add-btn" onClick={() => setDialogOpen(true)} title="Filter & sort">
            <IconFilter s={11} />
            <span>Filter</span>
          </button>
        </div>
        {dialogOpen && <FilterDialog allItems={allItems} hosts={hosts} filters={list}
          onAddFilter={onAddFilter} onRemove={onRemove} onClear={onClear}
          onClose={() => setDialogOpen(false)} />}
      </>);
  }
  return (
    <>
      <div className="filter-bar">
        <span className="dimer mono" style={{ fontSize: 11 }}>{filteredCount} of {total}</span>
        {list.map((f, i) =>
        <button key={f.kind + ':' + f.value + ':' + i} className="filter-pill" onClick={() => onRemove(i)}>
            <span className="dimer mono" style={{ fontSize: 10 }}>{f.kind}:</span>
            <span className="mono">{f.value}</span>
            <IconClose s={10} style={{ marginLeft: 2 }} />
          </button>
        )}
        <button className="filter-add-btn" onClick={() => setDialogOpen(true)} title="Filter & sort">
          <IconFilter s={11} />
          <span>Filter</span>
        </button>
        <button className="btn-ghost" style={{ height: 22, padding: '0 8px', fontSize: 11.5, marginLeft: 'auto' }} onClick={onClear}>Clear</button>
      </div>
      {dialogOpen && <FilterDialog allItems={allItems} hosts={hosts} filters={list}
        onAddFilter={onAddFilter} onRemove={onRemove} onClear={onClear}
        onClose={() => setDialogOpen(false)} />}
    </>);
}

// Filter & sort dialog — checkboxes per facet, kept open so users can build a
// view incrementally. Sort selector is decorative for the demo.
function FilterDialog({ allItems, hosts, filters, onAddFilter, onRemove, onClose }) {
  const isActive = (kind, value) => filters.some((f) => f.kind === kind && f.value === value);
  const toggle = (kind, value) => {
    const idx = filters.findIndex((f) => f.kind === kind && f.value === value);
    if (idx >= 0) onRemove(idx);
    else onAddFilter && onAddFilter({ kind, value });
  };
  const hostOpts = [...new Set(allItems.filter((i) => i.host).map((i) => i.host))];
  const statusOpts = ['attn', 'bad', 'ok', 'idle', 'stale'];
  const repoOpts = [...new Set(allItems.filter((i) => i.repo).map((i) => i.repo))];
  return (
    <div className="overlay-backdrop fadeIn" onClick={onClose}>
      <div className="filter-dialog glass-strong scaleIn" onClick={(e) => e.stopPropagation()}>
        <div className="filter-dialog-header">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <IconFilter s={14} />
            <b style={{ fontSize: 13 }}>Filter & sort</b>
            <span className="dimest mono" style={{ fontSize: 11 }}>{filters.length} active</span>
          </div>
          <button className="iconbtn" onClick={onClose} aria-label="Close"><IconClose s={14} /></button>
        </div>
        <div className="filter-dialog-body">
          <FilterDialogSection title="Status" options={statusOpts} kind="status"
            isActive={isActive} toggle={toggle}
            renderLabel={(s) => <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
              <span className={STATUS_PIP[s] || 'pip idle'} />{STATUS_LABEL[s] || s}</span>} />
          <FilterDialogSection title="Host" options={hostOpts} kind="host"
            isActive={isActive} toggle={toggle}
            renderLabel={(h) => <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
              <HostGlyphAuto host={h} size={11} /><span className="mono">{h}</span></span>} />
          <FilterDialogSection title="Repo" options={repoOpts} kind="repo"
            isActive={isActive} toggle={toggle}
            renderLabel={(r) => <span className="mono" style={{ fontSize: 12 }}>{r}</span>} />
          <div className="filter-dialog-section">
            <div className="filter-dialog-section-title">Sort</div>
            <div className="filter-dialog-radios">
              {['Last activity', 'Status priority', 'Title (A→Z)'].map((s, i) =>
                <label key={s} className="filter-dialog-radio">
                  <input type="radio" name="sort" defaultChecked={i === 0} />
                  <span>{s}</span>
                </label>)}
            </div>
          </div>
        </div>
        <div className="filter-dialog-footer">
          <span className="dimer" style={{ fontSize: 11.5 }}>Filters persist while viewing</span>
          <button className="btn-ghost" onClick={onClose}>Done</button>
        </div>
      </div>
    </div>);
}

function FilterDialogSection({ title, options, kind, isActive, toggle, renderLabel }) {
  return (
    <div className="filter-dialog-section">
      <div className="filter-dialog-section-title">{title}</div>
      <div className="filter-dialog-options">
        {options.map((o) =>
          <label key={o} className={`filter-dialog-opt ${isActive(kind, o) ? 'on' : ''}`}>
            <input type="checkbox" checked={isActive(kind, o)} onChange={() => toggle(kind, o)} />
            {renderLabel ? renderLabel(o) : <span>{o}</span>}
          </label>)}
      </div>
    </div>);
}

function applyFilters(items, f) {
  if (!f || Array.isArray(f) && f.length === 0) return items;
  // Support both array of {kind,value} and the legacy {host,status,repo}.
  if (Array.isArray(f)) {
    const by = (k) => f.filter((x) => x.kind === k).map((x) => x.value);
    const host = by('host'),status = by('status'),repo = by('repo');
    return items.filter((it) =>
    (!host.length || host.includes(it.host)) && (
    !status.length || status.includes(it.status)) && (
    !repo.length || repo.includes(it.repo))
    );
  }
  return items.filter((it) => {
    if (f.host?.length && !f.host.includes(it.host)) return false;
    if (f.status?.length && !f.status.includes(it.status)) return false;
    if (f.repo?.length && !f.repo.includes(it.repo)) return false;
    return true;
  });
}

// ── Grouping ────────────────────────────────────────────────────────────────
function groupItems(items, lens, now) {
  const groups = new Map();
  const push = (key, label, item, sortKey = 0) => {
    if (!groups.has(key)) groups.set(key, { key, label, sortKey, items: [] });
    groups.get(key).items.push(item);
  };
  // Channels always group at the top, regardless of lens
  const channels = items.filter((it) => it.kind === 'channel');
  const rest = items.filter((it) => it.kind !== 'channel');
  for (const ch of channels) push('channels', 'Channels', ch, -1);
  if (lens === 'attention') {
    for (const it of rest) {
      if (it.status === 'attn') push('attn', 'Attention', it, 0);else
      if (it.status === 'bad') push('bad', 'Blocked', it, 1);else
      if (it.session?.live) push('active', 'Active', it, 2);else
      if (it.status === 'idle' || !it.session) push('idle', 'Idle', it, 3);else
      if (it.status === 'stale') push('stale', 'Stale', it, 4);else
      push('other', 'Other', it, 5);
    }
  } else if (lens === 'host') {
    for (const it of rest) push('host:' + it.host, it.host, it, 0);
  } else if (lens === 'tmux') {
    // Two-level nesting: Host · Session  →  Window · panes (items)
    for (const it of rest) {
      if (it.tmux) {
        const gKey = `tmux:${it.host}/${it.tmux.session}`;
        const gLabel = `${it.host} · ${it.tmux.session}`;
        if (!groups.has(gKey)) groups.set(gKey, { key: gKey, label: gLabel, sortKey: 0, items: [], subgroups: new Map(), kind: 'tmux-session', host: it.host, sessionName: it.tmux.session });
        const g = groups.get(gKey);
        const wKey = `w:${it.tmux.window.idx}`;
        if (!g.subgroups.has(wKey)) g.subgroups.set(wKey, { key: wKey, label: `window ${it.tmux.window.idx} · ${it.tmux.window.name}`, idx: it.tmux.window.idx, items: [] });
        g.subgroups.get(wKey).items.push(it);
        g.items.push(it);
      } else if (it.session && !it.session.live) {
        const gKey = 'tmux:detached';
        if (!groups.has(gKey)) groups.set(gKey, { key: gKey, label: 'Detached sessions', sortKey: 8, items: [], kind: 'detached' });
        groups.get(gKey).items.push(it);
      } else {
        const gKey = 'tmux:none';
        if (!groups.has(gKey)) groups.set(gKey, { key: gKey, label: 'No tmux', sortKey: 9, items: [], kind: 'none' });
        groups.get(gKey).items.push(it);
      }
    }
    // Convert subgroups Map → sorted array per session
    for (const g of groups.values()) {
      if (g.subgroups) g.subgroups = [...g.subgroups.values()].sort((a, b) => a.idx - b.idx);
    }
  } else if (lens === 'repo') {
    for (const it of rest) push('repo:' + it.repo, it.repo, it, 0);
  } else if (lens === 'issue') {
    for (const it of rest) {
      if (it.issue) push('issue:#' + it.issue.number, '#' + it.issue.number + ' · ' + (it.issue.title || it.repo), it, 0);
      else push('issue:none', 'No linked issue', it, 99);
    }
  } else if (lens === 'activity') {
    for (const it of rest) {
      const age = now - it.lastActivity;
      if (age < 10 * 60_000) push('a:now', 'Last 10 minutes', it, 0);else
      if (age < 60 * 60_000) push('a:hr', 'Last hour', it, 1);else
      if (age < 24 * 3_600_000) push('a:day', 'Today', it, 2);else
      push('a:older', 'Earlier', it, 3);
    }
  }
  // sort items inside each group: attention first, then by recency
  for (const g of groups.values()) {
    g.items.sort((a, b) => {
      const order = { attn: 0, bad: 1, ok: 2, idle: 3, stale: 4 };
      const oa = order[a.status] ?? 9,ob = order[b.status] ?? 9;
      if (oa !== ob) return oa - ob;
      return b.lastActivity - a.lastActivity;
    });
  }
  return [...groups.values()].sort((a, b) => a.sortKey - b.sortKey);
}

// ── Fleet item row ──────────────────────────────────────────────────────────
function FleetItem({ item, selected, onSelect, density, surface, now, peerDown }) {
  const isStale = item.status === 'stale' || peerDown;
  const liveDot = item.session?.live;
  const isChannel = item.kind === 'channel';
  const agents = isChannel ? (window.ORCHARD_AGENTS || []).filter((a) => item.participants?.includes(a.id)) : [];
  const main =
  <div className="fleet-item-main">
      {isChannel ?
        <span className="channel-hash" title="Channel">#</span>
        : <span className={STATUS_PIP[item.status] || 'pip idle'} title={STATUS_LABEL[item.status]} />
      }
      <div className="fleet-item-body">
        <div className="fleet-item-title-row">
          <span className="fleet-item-title" style={{ opacity: isStale ? 0.5 : 1 }}>{item.title}</span>
          <SignalRow item={item} surface={surface} />
        </div>
        <div className="fleet-item-sub">
          {isChannel ? (
            <>
              <AgentStack agents={agents} size={14} />
              <span className="dimer" style={{ fontSize: 11.5 }}>{agents.length} agent{agents.length === 1 ? '' : 's'}</span>
              {item.topic && (
                <>
                  <span className="dimest hide-mobile">·</span>
                  <span className="dimer hide-mobile" style={{ fontSize: 11.5, overflow: 'hidden', textOverflow: 'ellipsis' }}>{item.topic}</span>
                </>
              )}
            </>
          ) : (
            <>
              <HostGlyphAuto host={item.host} size={12} dim={isStale} />
              <span className="mono dimer hide-mobile">{item.host}</span>
              <span className="dimest hide-mobile">·</span>
              <span className="mono dimer branch-name">{item.branch}</span>
              {item.attentionReason && density === 'comfortable' && surface !== 'mobile' &&
              <>
                <span className="dimest">·</span>
                <span className="reason-inline" title={item.attentionReason}>
                  <IconAlert s={10} />
                  <span>{item.attentionReason}</span>
                </span>
              </>}
            </>
          )}
        </div>
      </div>
      <div className="fleet-item-meta">
        {liveDot && <span className="pip live" style={{ marginBottom: 2 }} title="Live" />}
        <span className="mono dimer tnum" style={{ fontSize: 11 }}>{relTime(item.lastActivity, now)}</span>
        {density !== 'compact' && surface !== 'mobile' && item.sparkline?.length > 0 &&
      <Spark values={item.sparkline} w={48} h={10} color={item.status === 'attn' ? 'var(--attn)' : 'var(--fg-3)'} />
      }
      </div>
    </div>;

  return (
    <div className={`row fleet-item ${isChannel ? 'is-channel' : ''}`} data-selected={selected ? '1' : '0'} data-density={density}
    data-stale={isStale ? '1' : '0'}
    onClick={() => onSelect(item.id)}>
      {main}
    </div>);

}

// Stacked agent avatars used in channels
function AgentStack({ agents, size = 16, max = 4 }) {
  const shown = agents.slice(0, max);
  const overflow = Math.max(0, agents.length - max);
  return (
    <span className="agent-stack" style={{ '--avatar-size': size + 'px' }}>
      {shown.map((a) => (
        <span key={a.id} className="agent-avatar" title={`${a.name} · ${a.role}`}
          style={{ background: `oklch(0.62 0.13 ${a.hue})` }}>
          {a.avatar}
        </span>
      ))}
      {overflow > 0 && <span className="agent-avatar agent-avatar-more">+{overflow}</span>}
    </span>
  );
}

// ── Signal row ──────────────────────────────────────────────────────────────
// Compact strip of icon-only glyphs that summarise everything that would
// otherwise be text: PR state, CI, reviews, comments, contract questions.
// Each glyph carries a tooltip; nothing has a label by default.
function SignalRow({ item, surface }) {
  const sigs = [];
  if (item.bare) sigs.push({ key: 'bare', el: <IconGitBranch s={11} className="dimer" />, title: 'Bare worktree (no session)' });
  if (item.session && !item.session.live) sigs.push({ key: 'detached', el: <IconCircleDash s={11} className="dimer" />, title: 'Session detached (pickup-able)' });
  if (item.pr) {
    const pr = item.pr;
    let prCol = 'var(--ok)';
    let prIcon = <IconPullRequest s={11} />;
    let prTitle = `PR #${pr.number} — open`;
    if (pr.state === 'merged') {prCol = 'var(--purple, oklch(0.62 0.16 295))';prTitle = `PR #${pr.number} — merged`;} else
    if (pr.state === 'draft') {prCol = 'var(--fg-3)';prIcon = <IconDraft s={11} />;prTitle = `PR #${pr.number} — draft`;}
    sigs.push({ key: 'pr', el: <span style={{ color: prCol, display: 'inline-flex' }}>{prIcon}</span>, title: prTitle, num: pr.number });
    if (pr.ci === 'failing') sigs.push({ key: 'ci', el: <IconCircleX s={11} className="bad" />, title: 'CI failing' });else
    if (pr.ci === 'pending') sigs.push({ key: 'ci', el: <IconCircleHalf s={11} className="attn" />, title: 'CI pending' });
    if (pr.reviews === 'changes-requested') sigs.push({ key: 'rev', el: <IconCircleX s={11} className="attn" />, title: 'Changes requested' });else
    if (pr.reviews === 'approved') sigs.push({ key: 'rev', el: <IconCircleCheck s={11} className="ok-fg" />, title: 'Approved' });else
    if (pr.reviews === 'commented') sigs.push({ key: 'rev', el: <IconMessage s={11} className="dimer" />, title: 'Reviewer commented' });
  }
  if (item.issue) sigs.push({ key: 'issue', el: <IconIssue s={11} className="dimer" />, title: `Issue #${item.issue.number}`, num: item.issue.number });
  if (item.unread > 0) sigs.push({ key: 'unread', el:
    <span className="unread-glyph" title={`${item.unread} unread`}>
      <IconMessage s={11} />
      <span className="unread-count tnum">{item.unread}</span>
    </span>,
    title: `${item.unread} unread` });
  if (item.contract?.openQuestions > 0) sigs.push({ key: 'q', el:
    <span style={{ color: 'var(--attn)', display: 'inline-flex' }}>
      <IconQuestion s={11} />
    </span>,
    title: `${item.contract.openQuestions} open question${item.contract.openQuestions > 1 ? 's' : ''}` });
  if (sigs.length === 0) return null;
  return (
    <span className="signal-row" data-surface={surface}>
      {sigs.map((s) => <span key={s.key} className="signal" title={s.title}>{s.el}</span>)}
    </span>);

}

// ── Fleet list ──────────────────────────────────────────────────────────────
function FleetList({ items, lens, density, surface, selectedId, onSelect, now, hosts }) {
  const grouped = groupItems(items, lens, now);
  const downHosts = new Set(hosts.filter((h) => !h.reachable).map((h) => h.hostname));
  return (
    <div className="fleet-list scroll" data-comment-anchor="6da3d48d23-div-341-5">
      {grouped.map((g) =>
      <div key={g.key} className="fleet-group" data-kind={g.kind || ''}>
          <div className={`group-header ${g.key === 'attn' ? 'attn' : ''}`}>
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
              {g.key === 'attn' && <IconAlert s={11} />}
              {g.key.startsWith('host:') && <HostGlyphAuto host={g.label} size={11} />}
              {g.kind === 'tmux-session' && <><HostGlyphAuto host={g.host} size={11} /><IconTerminal s={11} /></>}
              {(g.kind === 'detached' || g.kind === 'none') && <IconTerminal s={11} className="dimer" />}
              {g.key.startsWith('repo:') && <IconGitBranch s={11} />}
              {g.key.startsWith('a:') && <IconClock s={11} />}
              {(g.key === 'idle' || g.key === 'stale' || g.key === 'active' || g.key === 'bad' || g.key === 'other') && null}
              <span>{g.label}</span>
            </span>
            <span className="count">{g.items.length}</span>
          </div>
          {Array.isArray(g.subgroups) && g.subgroups.length > 0 ? (
            g.subgroups.map((sg) =>
              <div key={sg.key} className="fleet-subgroup">
                <div className="subgroup-header">
                  <span className="subgroup-rule" />
                  <span className="mono dimer" style={{ fontSize: 10.5, letterSpacing: 0.2 }}>{sg.label}</span>
                  <span className="dimest mono" style={{ fontSize: 10.5 }}>{sg.items.length}</span>
                </div>
                {sg.items.map((it) =>
                  <div key={it.id} className="fleet-nested">
                    <FleetItem item={it} selected={it.id === selectedId}
                      onSelect={onSelect} density={density} surface={surface} now={now}
                      peerDown={downHosts.has(it.host)} />
                  </div>
                )}
              </div>
            )
          ) : (
            g.items.map((it) =>
              <FleetItem key={it.id} item={it} selected={it.id === selectedId}
                onSelect={onSelect} density={density} surface={surface} now={now}
                peerDown={downHosts.has(it.host)} />
            )
          )}
        </div>
      )}
      {grouped.length === 0 &&
      <div className="fleet-empty">
          <span className="dimer">No items match these filters.</span>
        </div>
      }
    </div>);

}

Object.assign(window, {
  FleetTopBar, LensSelector, FilterBar, FleetList, applyFilters, groupItems, PeerHealthCluster, LENSES
});