// desktop-layout.jsx — desktop split: sidebar (fleet) + tabbed conversation pane.

function DesktopLayout(props) {
  const {
    items, hosts, account, lens, lensVariant, density, sidebarCollapsed, onToggleSidebar,
    selectedId, onSelect, view, onView, switcherVariant, statusVariant, peerTreatment,
    onOpenPalette, onLaunch, onToggleTheme, theme, now,
    conversation, terminalLines, sending, composeText, setComposeText, onSend,
    forkPreview, onStartFork, onCommitFork, onCancelFork,
    filters, onFilterRemove, onFilterClear,
    offline, onToggleOffline,
    tabs, activeTabId, onActivateTab, onCloseTab, onReorderTabs,
    fullscreen, onToggleFullscreen, onOpenInNewTab
  } = props;

  const visibleItems = applyFilters(items, filters);
  const selected = items.find((i) => i.id === selectedId);
  const sessionLive = selected?.session?.live;

  // Resizable sidebar with autocollapse threshold
  const COLLAPSE_AT = 200;
  const MIN_W = 240;
  const MAX_W = 460;
  const [sidebarW, setSidebarW] = React.useState(() => {
    const v = parseInt(localStorage.getItem('orchard:sidebarW') || '320', 10);
    return isNaN(v) ? 320 : v;
  });
  React.useEffect(() => { localStorage.setItem('orchard:sidebarW', String(sidebarW)); }, [sidebarW]);
  const onResizeStart = (e) => {
    e.preventDefault();
    const startX = e.clientX;
    const startW = sidebarW;
    const move = (ev) => {
      const next = startW + (ev.clientX - startX);
      if (next < COLLAPSE_AT) {
        if (!sidebarCollapsed) onToggleSidebar();
        return;
      }
      if (sidebarCollapsed) onToggleSidebar();
      setSidebarW(Math.max(MIN_W, Math.min(MAX_W, next)));
    };
    const up = () => {
      window.removeEventListener('mousemove', move);
      window.removeEventListener('mouseup', up);
      document.body.style.cursor = '';
    };
    window.addEventListener('mousemove', move);
    window.addEventListener('mouseup', up);
    document.body.style.cursor = 'col-resize';
  };

  return (
    <div className="desktop-frame" data-screen-label="Desktop" data-fullscreen={fullscreen ? '1' : '0'}
      style={{ '--sidebar-w': `${sidebarW}px` }}>
      {!fullscreen &&
      <FleetTopBar surface="desktop" onOpenPalette={onOpenPalette}
      onLaunch={onLaunch}
      onToggleSidebar={onToggleSidebar}
      peerHealth={hosts} account={account}
      onToggleTheme={onToggleTheme} theme={theme} />
      }
      {offline && !fullscreen && <OfflineBanner onDismiss={onToggleOffline} />}
      <div className="desktop-grid" data-sidebar-collapsed={sidebarCollapsed || fullscreen ? '1' : '0'} data-fullscreen={fullscreen ? '1' : '0'}>
        {!fullscreen &&
        <div className="desktop-sidebar" style={!sidebarCollapsed ? { width: sidebarW } : undefined}>
            {!sidebarCollapsed &&
          <>
                <div className="sidebar-controls" data-comment-anchor="3afab0f4d4-div-35-17">
                  <LensSelector value={lens} onChange={(v) => props.onLens(v)} variant={lensVariant} />
                </div>
                <FilterBar filters={filters} onRemove={onFilterRemove} onClear={onFilterClear} onAddFilter={props.onAddFilter}
            allItems={items} hosts={hosts} />
                <FleetList items={visibleItems} lens={lens} density={density}
            surface="desktop" selectedId={selectedId} onSelect={onSelect}
            now={now} hosts={hosts} />
              </>
          }
            {sidebarCollapsed && <CollapsedSidebar items={items} hosts={hosts}
          tabs={tabs} activeTabId={activeTabId} onActivateTab={onActivateTab}
          selectedId={selectedId} onSelect={onSelect}
          onHostFilter={(h) => props.onAddFilter && props.onAddFilter({ kind: 'host', value: h })} />}
            {!sidebarCollapsed && <div className="sidebar-resize" onMouseDown={onResizeStart} title="Drag to resize · collapses below 200px" />}
          </div>
        }
        <div className="desktop-pane">
          <PanesArea
            tabs={tabs} activeTabId={activeTabId} items={items}
            paneSizes={props.paneSizes} onResizePanes={props.onResizePanes}
            onActivate={onActivateTab} onClose={onCloseTab}
            onLaunch={onLaunch}
            fullscreen={fullscreen} onToggleFullscreen={onToggleFullscreen}
            view={view} onView={onView} switcherVariant={switcherVariant}
            conversation={conversation} terminalLines={terminalLines}
            statusVariant={statusVariant} now={now}
            sending={sending} onSend={onSend}
            composeText={composeText} setComposeText={setComposeText}
            forkPreview={forkPreview} onStartFork={onStartFork}
            onCommitFork={onCommitFork} onCancelFork={onCancelFork}
            onOpenPalette={onOpenPalette} />
        </div>
      </div>
    </div>);

}

// ─────────── PanesArea — side-by-side conversation panes with drop-to-split ───────────

function PanesArea({
  tabs, activeTabId, items, paneSizes, onResizePanes,
  onActivate, onClose, onLaunch,
  fullscreen, onToggleFullscreen,
  view, onView, switcherVariant,
  conversation, terminalLines, statusVariant, now,
  sending, onSend, composeText, setComposeText,
  forkPreview, onStartFork, onCommitFork, onCancelFork,
  onOpenPalette,
}) {
  const rowRef = React.useRef(null);

  if (tabs.length === 0) {
    return (
      <div className="panes-empty">
        <div className="panes-empty-inner">
          <ConvEmpty onOpen={onOpenPalette} hasTabs={false} onNew={onLaunch} />
        </div>
      </div>);
  }

  const sizes = (paneSizes && paneSizes.length === tabs.length)
    ? paneSizes
    : tabs.map(() => 1 / tabs.length);

  const startResize = (e, splitIdx) => {
    e.preventDefault();
    const row = rowRef.current;
    if (!row) return;
    const totalW = row.getBoundingClientRect().width || 1;
    const startX = e.clientX;
    const startSizes = [...sizes];
    const min = 0.12;
    const onMove = (ev) => {
      const dxFrac = (ev.clientX - startX) / totalW;
      const next = [...startSizes];
      let a = next[splitIdx] + dxFrac;
      let b = next[splitIdx + 1] - dxFrac;
      if (a < min) { b += a - min; a = min; }
      if (b < min) { a += b - min; b = min; }
      next[splitIdx] = a;
      next[splitIdx + 1] = b;
      onResizePanes && onResizePanes(next);
    };
    const onUp = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  };

  return (
    <div ref={rowRef} className="panes-row" data-count={tabs.length}>
      {tabs.map((tab, idx) => {
        const item = items.find((i) => i.id === tab.itemId);
        if (!item) return null;
        const active = tab.id === activeTabId;
        const sessionLive = item.session?.live;
        const flex = sizes[idx] || (1 / tabs.length);
        return (
          <React.Fragment key={tab.id}>
            {idx > 0 && (
              <div className="pane-resizer"
                onMouseDown={(e) => startResize(e, idx - 1)}
                role="separator" aria-orientation="vertical"
                title="Drag to resize" />
            )}
            <Pane
              item={item} tab={tab} active={active}
              isFirst={idx === 0} isLast={idx === tabs.length - 1}
              paneCount={tabs.length}
              flex={flex}
              onActivate={() => onActivate(tab.id)}
              onClose={() => onClose(tab.id)}
              fullscreen={idx === tabs.length - 1 ? fullscreen : null}
              onToggleFullscreen={idx === tabs.length - 1 ? onToggleFullscreen : null}
              view={active ? view : tab.view || 'chat'}
              onView={onView}
              switcherVariant={switcherVariant}
              sessionLive={sessionLive}
              conversation={conversation} terminalLines={terminalLines}
              statusVariant={statusVariant} now={now}
              sending={sending && active} onSend={onSend}
              composeText={composeText} setComposeText={setComposeText}
              forkPreview={forkPreview} onStartFork={onStartFork} onCommitFork={onCommitFork} onCancelFork={onCancelFork}
            />
          </React.Fragment>);
      })}
    </div>);
}

function Pane({
  item, active, paneCount, flex, onActivate, onClose,
  fullscreen, onToggleFullscreen,
  view, onView, switcherVariant, sessionLive,
  conversation, terminalLines, statusVariant, now,
  sending, onSend, composeText, setComposeText,
  forkPreview, onStartFork, onCommitFork, onCancelFork,
  isFirst, isLast,
}) {
  const isCompact = paneCount > 1;
  return (
    <div className={`pane ${active ? 'active' : ''}`} onMouseDown={onActivate}
      style={{ flex: `${flex || 1} 1 0`, minWidth: 0 }}>
      <div className="pane-header-bar">
        <button className="pane-close iconbtn" onClick={(e) => { e.stopPropagation(); onClose(); }} title="Close pane" aria-label="Close pane">
          <IconClose s={11} />
        </button>
        {isLast && onToggleFullscreen && (
          <button className="pane-fs iconbtn" onClick={(e) => { e.stopPropagation(); onToggleFullscreen(); }}
            title={fullscreen ? 'Exit focus mode (⌘⇧F)' : 'Focus mode (⌘⇧F)'}>
            {fullscreen ? <IconMinimize s={12} /> : <IconMaximize s={12} />}
          </button>
        )}
      </div>
      <div className="conv">
        <ConvHeader item={item} view={view} onView={onView}
          switcherVariant={switcherVariant}
          surface="desktop" sessionLive={sessionLive}
          onFork={onStartFork} compact={isCompact} />
        {view === 'chat' ?
          <ChatView item={item} conversation={conversation}
            statusVariant={statusVariant} surface="desktop" now={now}
            sending={sending} onSend={onSend}
            composeText={composeText} setComposeText={setComposeText}
            forkPreview={forkPreview} onFork={onCommitFork} onCancelFork={onCancelFork} /> :
          <TerminalView lines={terminalLines} item={item} surface="desktop" />
        }
      </div>
    </div>);
}

// ─────────── TabStrip (legacy, kept for fallback callers) ───────────

function TabStrip({ tabs, activeTabId, items, onActivate, onClose, onReorder, onNew, fullscreen, onToggleFullscreen }) {
  const [dragIdx, setDragIdx] = React.useState(null);
  const [overIdx, setOverIdx] = React.useState(null);

  return (
    <div className="tabstrip" data-fullscreen={fullscreen ? '1' : '0'}>
      <div className="tabstrip-tabs" role="tablist">
        {tabs.map((tab, idx) => {
          const item = items.find((i) => i.id === tab.itemId);
          if (!item) return null;
          const active = tab.id === activeTabId;
          const live = item.session?.live;
          return (
            <div key={tab.id}
            className={`tab ${active ? 'active' : ''} ${overIdx === idx ? 'drop-target' : ''}`}
            role="tab"
            aria-selected={active}
            draggable
            onDragStart={() => setDragIdx(idx)}
            onDragOver={(e) => {e.preventDefault();setOverIdx(idx);}}
            onDragLeave={() => setOverIdx(null)}
            onDrop={(e) => {e.preventDefault();if (dragIdx != null) onReorder(dragIdx, idx);setDragIdx(null);setOverIdx(null);}}
            onDragEnd={() => {setDragIdx(null);setOverIdx(null);}}
            onClick={(e) => {if (!e.target.closest('.tab-close')) onActivate(tab.id);}}
            onAuxClick={(e) => {if (e.button === 1) {e.preventDefault();onClose(tab.id);}}}
            title={`${item.title} — ${item.repo}`}>
              <span className={`tab-pip ${live ? 'live' : item.attention ? 'attn' : 'idle'}`} />
              <span className="tab-title">{item.title}</span>
              <span className="tab-host mono">{item.host}</span>
              <button className="tab-close" aria-label="Close tab"
              onClick={(e) => {e.stopPropagation();onClose(tab.id);}}>
                <IconClose s={11} />
              </button>
            </div>);

        })}
        <button className="tab-new" onClick={onNew} title="New conversation (⌘T)" aria-label="New conversation">
          <IconPlus s={13} />
        </button>
      </div>
      <button className="tab-fullscreen" onClick={onToggleFullscreen}
      title={fullscreen ? 'Exit focus mode (⌘⇧F)' : 'Focus mode (⌘⇧F)'}
      aria-label="Toggle fullscreen">
        {fullscreen ? <IconMinimize s={13} /> : <IconMaximize s={13} />}
      </button>
    </div>);

}

function ConvEmpty({ onOpen, hasTabs, onNew }) {
  return (
    <div className="conv-empty">
      <IconLogo s={28} style={{ color: 'var(--fg-4)' }} />
      <div style={{ fontSize: 14, fontWeight: 500, color: 'var(--fg-2)' }}>
        {hasTabs ? 'No tab selected' : 'No conversations open'}
      </div>
      <div className="mono" style={{ display: 'flex', gap: 8 }}>
        <button className="btn-tonal" onClick={onOpen}>
          <IconCommand s={13} /> Search <span className="kbd">⌘</span><span className="kbd">K</span>
        </button>
        <button className="btn-tonal" onClick={onNew}>
          <IconPlus s={13} /> New <span className="kbd">⌘</span><span className="kbd">T</span>
        </button>
      </div>
    </div>);

}

// Collapsed sidebar — a thin rail of OPEN CONVERSATIONS (open tabs) with
// host glyph + status pip. Hosts filter pops from a small button at the top.
function CollapsedSidebar({ items, hosts, tabs, activeTabId, selectedId, onSelect, onActivateTab, onHostFilter }) {
  const [hostMenu, setHostMenu] = React.useState(false);
  // Conversations to show: prefer open tabs (real "in flight" set); fall back
  // to selected item if no tabs are open.
  const tabItems = (tabs || []).
  map((t) => items.find((i) => i.id === t.itemId)).
  filter(Boolean);
  const list = tabItems.length ? tabItems : selectedId ? items.filter((i) => i.id === selectedId) : [];
  return (
    <div className="rail-collapsed" data-comment-anchor="88009c6086-div-152-5">
      <div className="rail-top">
        <button className="iconbtn" title="Filter by host"
        onClick={() => setHostMenu((v) => !v)} aria-expanded={hostMenu ? 'true' : 'false'}>
          <IconHost s={14} />
        </button>
        {hostMenu &&
        <div className="rail-host-menu glass-strong">
            <div className="dimer mono" style={{ fontSize: 10, padding: '4px 8px', letterSpacing: '0.06em' }}>FILTER · HOST</div>
            {hosts.map((h) =>
          <button key={h.id} className="rail-host-row"
          onClick={() => {onHostFilter && onHostFilter(h.hostname);setHostMenu(false);}}>
                <HostGlyphAuto host={h.hostname} size={14} />
                <span className="mono" style={{ fontSize: 11.5 }}>{h.hostname}</span>
                <span className={`pip ${h.reachable ? 'ok' : 'bad'}`} style={{ marginLeft: 'auto' }} />
              </button>
          )}
          </div>
        }
      </div>
      <div className="rail-list">
        {list.map((it) => {
          const tab = (tabs || []).find((t) => t.itemId === it.id);
          const active = tab ? tab.id === activeTabId : it.id === selectedId;
          return (
            <button key={it.id} className={`rail-conv ${active ? 'on' : ''}`}
            onClick={() => tab && onActivateTab ? onActivateTab(tab.id) : onSelect(it.id)}
            title={`${it.title} · ${it.host}`}>
              <span className="rail-active-bar" />
              <HostGlyphAuto host={it.host} size={18} />
              <span className={`pip ${it.status} rail-pip`} />
            </button>);

        })}
        {list.length === 0 &&
        <div className="rail-empty dimer mono" style={{ fontSize: 10, textAlign: 'center', marginTop: 12 }}>
            no open<br />tabs
          </div>
        }
      </div>
    </div>);

}

function OfflineBanner({ onDismiss }) {
  return (
    <div className="offline-banner">
      <IconWifiOff s={13} />
      <span>Daemon unreachable.</span>
      <span className="mono dimer">last sync · 38s</span>
      <span style={{ marginLeft: 8 }}><span className="gated-badge"><IconKey s={9} />mutations gated</span></span>
      <button className="btn-ghost" onClick={onDismiss} style={{ height: 22 }}>Dismiss</button>
    </div>);

}

Object.assign(window, { DesktopLayout, TabStrip, PanesArea, Pane, ConvEmpty, CollapsedSidebar, OfflineBanner });