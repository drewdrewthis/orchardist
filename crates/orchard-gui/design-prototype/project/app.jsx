// app.jsx — Orchard GUI 2 root. Manages all app state.

const { useState, useEffect, useMemo, useRef, useCallback } = React;

const TWEAK_DEFAULTS = /*EDITMODE-BEGIN*/{
  "theme": "dark",
  "surface": "desktop",
  "lensVariant": "pills",
  "switcherVariant": "segmented",
  "statusVariant": "ticks",
  "peerTreatment": "ambient",
  "density": "comfortable",
  "accentHue": 215,
  "offline": false,
  "sidebarCollapsed": false
}/*EDITMODE-END*/;

function App() {
  const [t, setTweak] = (typeof useTweaks === 'function') ? useTweaks(TWEAK_DEFAULTS) : [TWEAK_DEFAULTS, () => {}];

  // Tick "now" every 5 seconds so relative timestamps drift.
  const [now, setNow] = useState(window.ORCHARD_NOW || Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 5000);
    return () => clearInterval(id);
  }, []);

  // Items + a heartbeat field that ticks for one item to feel "live".
  const [items, setItems] = useState(window.ORCHARD_ITEMS);
  const hosts = window.ORCHARD_HOSTS;
  const account = window.ORCHARD_ACCOUNT;
  const terminalLines = window.ORCHARD_TERMINAL;

  // Selection / view / lens
  const [lens, setLens] = useState('attention');

  // Tabs (desktop) — each tab anchors a single item + view. Mobile uses a single
  // implicit tab so the same machinery powers both surfaces.
  const [tabs, setTabs] = useState(() => (
    t.surface === 'desktop'
      ? [{ id: 't1', itemId: 'w.orchard.api-pagination', view: 'chat' }]
      : []
  ));
  const [activeTabId, setActiveTabId] = useState(() => (t.surface === 'desktop' ? 't1' : null));
  const [fullscreen, setFullscreen] = useState(false);
  const [paneSizes, setPaneSizes] = useState(() => [1]);

  // Whenever the number of tabs changes, reset to an even split.
  // (Manual resize keeps the array stable in length.)
  React.useEffect(() => {
    setPaneSizes(prev => {
      if (prev.length === tabs.length) return prev;
      const n = Math.max(1, tabs.length);
      return Array(n).fill(1 / n);
    });
  }, [tabs.length]);

  const activeTab = tabs.find(x => x.id === activeTabId) || null;
  const selectedId = activeTab?.itemId || null;
  const view = activeTab?.view || 'chat';

  const setView = useCallback((v) => {
    setTabs(prev => prev.map(tab => tab.id === activeTabId
      ? { ...tab, view: typeof v === 'function' ? v(tab.view) : v }
      : tab));
  }, [activeTabId]);

  const tabIdRef = useRef(2);
  const MAX_PANES = 3;
  const openTab = useCallback((itemId, { newTab = false, focus = true, atIndex = null } = {}) => {
    if (!itemId) return;
    setTabs(prev => {
      const existing = prev.find(x => x.itemId === itemId);
      if (existing && !newTab) {
        if (focus) setActiveTabId(existing.id);
        return prev;
      }
      const id = 't' + (tabIdRef.current++);
      if (focus) setActiveTabId(id);
      const tab = { id, itemId, view: 'chat' };
      let next = [...prev];
      if (next.length >= MAX_PANES) {
        // At cap: replace the currently active pane with the new one
        const activeIdx = Math.max(0, next.findIndex(x => x.id === activeTabId));
        next.splice(activeIdx, 1, tab);
      } else if (atIndex == null || atIndex >= next.length) {
        next.push(tab);
      } else {
        next.splice(Math.max(0, atIndex), 0, tab);
      }
      return next;
    });
  }, [activeTabId]);

  const closeTab = useCallback((tabId) => {
    setTabs(prev => {
      const idx = prev.findIndex(x => x.id === tabId);
      if (idx < 0) return prev;
      const next = prev.filter(x => x.id !== tabId);
      if (tabId === activeTabId) {
        const fallback = next[Math.max(0, idx - 1)] || null;
        setActiveTabId(fallback?.id ?? null);
      }
      return next;
    });
  }, [activeTabId]);

  const reorderTabs = useCallback((fromIdx, toIdx) => {
    setTabs(prev => {
      if (fromIdx === toIdx || fromIdx < 0 || toIdx < 0) return prev;
      const next = [...prev];
      const [moved] = next.splice(fromIdx, 1);
      next.splice(toIdx, 0, moved);
      return next;
    });
  }, []);

  // When surface flips, reset tab model so each surface lands sane.
  const lastSurface = useRef(t.surface);
  useEffect(() => {
    if (lastSurface.current === t.surface) return;
    lastSurface.current = t.surface;
    if (t.surface === 'desktop') {
      const id = 't' + (tabIdRef.current++);
      setTabs([{ id, itemId: 'w.orchard.api-pagination', view: 'chat' }]);
      setActiveTabId(id);
    } else {
      setTabs([]); setActiveTabId(null);
    }
    setFullscreen(false);
  }, [t.surface]);

  // Palette / new conversation modals
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [newConvOpen, setNewConvOpen] = useState(false);
  const [contractItem, setContractItem] = useState(null);

  // Expose a tiny imperative hook so deeply-nested ConvHeader can summon the modal
  useEffect(() => { window.__orchardOpenContract = (it) => setContractItem(it); }, []);

  // Jump to an agent's running session from a channel pill. We don't have a
  // direct agent → worktree mapping in the demo data, so route to the first
  // worktree on the agent's host (good enough for a hi-fi demo).
  useEffect(() => {
    window.__orchardOpenAgentSession = (agentId) => {
      const agent = (window.ORCHARD_AGENTS || []).find(a => a.id === agentId);
      if (!agent) return;
      const target = items.find(it => it.kind !== 'channel' && it.host === agent.host && it.session)
        || items.find(it => it.kind !== 'channel' && it.host === agent.host);
      if (target) openTab(target.id, { newTab: true });
    };
  }, [items, openTab]);

  // Filters - array of { kind, value, label }
  const [filters, setFilters] = useState([]);

  // Compose / send state
  const [composeText, setComposeText] = useState('');
  const [sending, setSending] = useState(null); // {tempId, text, ts, status}
  // Pick conversation source: channels have multi-agent transcripts,
  // worktrees use the standard single-agent conversation.
  const activeItem = useMemo(() => items.find(it => it.id === selectedId) || null, [items, selectedId]);
  const baseConversation = useMemo(() => {
    if (activeItem?.kind === 'channel') {
      return window.ORCHARD_CHANNEL_CONVS?.[selectedId]
        || { itemId: selectedId, recap: '', isChannel: true, messages: [] };
    }
    return window.ORCHARD_CONVERSATION;
  }, [activeItem, selectedId]);
  const [conversation, setConversation] = useState(baseConversation);

  // Fork state
  const [forkPreview, setForkPreview] = useState(null); // {fromMessageId, basis} or null

  // Apply theme to root element
  useEffect(() => {
    document.documentElement.dataset.theme = t.theme;
    document.documentElement.style.setProperty('--accent-hue', String(t.accentHue ?? 215));
  }, [t.theme, t.accentHue]);

  // ⌘K binding
  useEffect(() => {
    function onKey(e) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen(p => !p);
      } else if (e.key === '/' && !paletteOpen && document.activeElement?.tagName !== 'TEXTAREA' && document.activeElement?.tagName !== 'INPUT') {
        e.preventDefault();
        setPaletteOpen(true);
      } else if (e.key === 'Escape') {
        setPaletteOpen(false);
        setNewConvOpen(false);
      } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'n') {
        e.preventDefault();
        setNewConvOpen(true);
      } else if ((e.metaKey || e.ctrlKey) && e.key === 'b' && t.surface === 'desktop') {
        e.preventDefault();
        setTweak('sidebarCollapsed', !t.sidebarCollapsed);
      } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'w' && t.surface === 'desktop') {
        e.preventDefault();
        if (activeTabId) closeTab(activeTabId);
      } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 't' && t.surface === 'desktop') {
        e.preventDefault();
        setNewConvOpen(true);
      } else if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key.toLowerCase() === 'f' && t.surface === 'desktop') {
        e.preventDefault();
        setFullscreen(f => !f);
      } else if ((e.metaKey || e.ctrlKey) && (e.key === ']' || e.key === '[') && t.surface === 'desktop') {
        e.preventDefault();
        const idx = tabs.findIndex(x => x.id === activeTabId);
        if (idx >= 0 && tabs.length > 1) {
          const dir = e.key === ']' ? 1 : -1;
          const nxt = tabs[(idx + dir + tabs.length) % tabs.length];
          setActiveTabId(nxt.id);
        }
      } else if ((e.metaKey || e.ctrlKey) && /^[1-9]$/.test(e.key) && t.surface === 'desktop') {
        const i = parseInt(e.key, 10) - 1;
        if (tabs[i]) { e.preventDefault(); setActiveTabId(tabs[i].id); }
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [paletteOpen, t.surface, t.sidebarCollapsed, activeTabId, tabs, closeTab]);

  // Reset conversation if selection changes
  useEffect(() => {
    setConversation(baseConversation);
    setSending(null);
    setForkPreview(null);
    setComposeText('');
    setView('chat');
  }, [selectedId, baseConversation]);

  // Fake "live update": every ~12s nudge an item's lastActivity
  useEffect(() => {
    const id = setInterval(() => {
      setItems(prev => {
        const next = [...prev];
        const idx = Math.floor(Math.random() * next.length);
        next[idx] = { ...next[idx], lastActivityAt: Date.now() - Math.floor(Math.random() * 30000), liveBlink: Date.now() };
        return next;
      });
    }, 12000);
    return () => clearInterval(id);
  }, []);

  // Send: optimistic message → pending → sent → delivered → read
  const onSend = useCallback(() => {
    if (!composeText.trim() || sending) return;
    const text = composeText.trim();
    const tempId = 'm.tmp.' + Date.now();
    const ts = Date.now();
    setComposeText('');
    setSending({ tempId, text, ts, status: 'pending' });

    const stages = [
      { delay: 350,  status: 'sent' },
      { delay: 800,  status: 'delivered' },
      { delay: 1700, status: 'read' },
    ];
    stages.forEach(s => {
      setTimeout(() => {
        setSending(prev => prev && prev.tempId === tempId ? { ...prev, status: s.status } : prev);
        if (s.status === 'read') {
          // Add to conversation messages, clear sending; agent reply after a moment.
          setTimeout(() => {
            setConversation(c => ({
              ...c,
              messages: [...c.messages, {
                id: tempId, role: 'user', text, ts, status: 'read',
              }],
            }));
            setSending(null);
            // Agent typing indicator → reply.
            setTimeout(() => {
              // In channels, pick a participant agent to respond.
              const isChannel = activeItem?.kind === 'channel';
              const participants = isChannel
                ? (window.ORCHARD_AGENTS || []).filter(a => activeItem.participants?.includes(a.id))
                : [];
              const responder = isChannel && participants.length
                ? participants[Math.floor(Math.random() * participants.length)]
                : null;
              setConversation(c => ({
                ...c,
                messages: [...c.messages, {
                  id: 'm.agent.' + Date.now(),
                  role: 'agent',
                  agentId: responder?.id,
                  text: agentReplyFor(text, responder),
                  ts: Date.now(),
                  status: 'read',
                }],
              }));
            }, 1400);
          }, 250);
        }
      }, s.delay);
    });
  }, [composeText, sending, activeItem]);

  // Fork actions
  const onStartFork = useCallback((fromMessageId) => {
    const id = fromMessageId || (conversation.messages[conversation.messages.length - 1]?.id);
    setForkPreview({ fromMessageId: id, basis: conversation.messages.find(m => m.id === id) });
  }, [conversation]);
  const onCancelFork = useCallback(() => setForkPreview(null), []);
  const onCommitFork = useCallback(() => setForkPreview(null), []);

  // Palette pick
  const onPalettePick = useCallback((entry) => {
    setPaletteOpen(false);
    if (!entry) return;
    if (entry.kind === 'action') {
      if (entry.action === 'new-conversation') setNewConvOpen(true);
      else if (entry.action?.startsWith('lens:')) setLens(entry.action.slice(5));
      else if (entry.action === 'toggle-offline') setTweak('offline', !t.offline);
      else if (entry.action === 'toggle-theme') setTweak('theme', t.theme === 'dark' ? 'light' : 'dark');
      else if (entry.action === 'toggle-view') setView(v => v === 'chat' ? 'terminal' : 'chat');
      else if (entry.action?.startsWith('filter:')) {
        const [, kind, value] = entry.action.split(':');
        setFilters(f => f.some(x => x.kind === kind && x.value === value) ? f : [...f, { kind, value, label: entry.label }]);
      }
      return;
    }
    if (entry.itemId) {
      openTab(entry.itemId);
      if (entry.view === 'terminal') setView('terminal');
    }
  }, [t.offline, t.theme]);

  const onLaunch = useCallback((spec) => {
    setNewConvOpen(false);
    if (spec?.worktreeId) openTab(spec.worktreeId, { newTab: true });
  }, [openTab]);

  const onFilterRemove = useCallback((idx) => setFilters(f => f.filter((_, i) => i !== idx)), []);
  const onFilterClear = useCallback(() => setFilters([]), []);
  const onAddFilter = useCallback((flt) => setFilters(f => f.some(x => x.kind === flt.kind && x.value === flt.value) ? f : [...f, { ...flt, label: flt.value }]), []);

  // Stitch any in-flight `sending` message into the rendered conversation
  const visibleConv = useMemo(() => {
    if (!sending) return conversation;
    return {
      ...conversation,
      messages: [...conversation.messages, {
        id: sending.tempId, role: 'user', text: sending.text, ts: sending.ts, status: sending.status,
      }],
    };
  }, [conversation, sending]);

  const sharedProps = {
    items, hosts, account, lens, lensVariant: t.lensVariant, density: t.density,
    selectedId, onSelect: (id, e) => {
      const force = !!(e && (e.metaKey || e.ctrlKey || e.button === 1));
      openTab(id, { newTab: force });
    },
    tabs, activeTabId, onActivateTab: setActiveTabId, onCloseTab: closeTab,
    onReorderTabs: reorderTabs,
    paneSizes, onResizePanes: setPaneSizes,
    fullscreen, onToggleFullscreen: () => setFullscreen(f => !f),
    onOpenInNewTab: (id) => openTab(id, { newTab: true }),
    statusVariant: t.statusVariant, peerTreatment: t.peerTreatment,
    onOpenPalette: () => setPaletteOpen(true),
    onLaunch: () => setNewConvOpen(true),
    onLens: setLens,
    onToggleTheme: () => setTweak('theme', t.theme === 'dark' ? 'light' : 'dark'),
    theme: t.theme,
    now,
    conversation: visibleConv,
    terminalLines,
    sending, composeText, setComposeText, onSend,
    forkPreview, onStartFork, onCommitFork, onCancelFork,
    filters, onFilterRemove, onFilterClear, onAddFilter,
    offline: t.offline,
    onToggleOffline: () => setTweak('offline', !t.offline),
  };

  return (
    <>
      {t.surface === 'desktop' ? (
        <DesktopLayout
          {...sharedProps}
          view={view} onView={setView} switcherVariant={t.switcherVariant}
          sidebarCollapsed={t.sidebarCollapsed}
          onToggleSidebar={() => setTweak('sidebarCollapsed', !t.sidebarCollapsed)}
        />
      ) : (
        <MobileLayout
          {...sharedProps}
          onClearSelection={() => { if (activeTabId) closeTab(activeTabId); }}
        />
      )}

      <Palette open={paletteOpen} onClose={() => setPaletteOpen(false)}
               surface={t.surface} onPick={onPalettePick}
               items={window.ORCHARD_PALETTE} actions={window.ORCHARD_ACTIONS} />

      <NewConversation open={newConvOpen} onClose={() => setNewConvOpen(false)}
                       items={items} hosts={hosts} surface={t.surface}
                       onLaunch={onLaunch} />

      <ContractModal item={contractItem} onClose={() => setContractItem(null)} />

      <OrchardTweaksPanel t={t} setTweak={setTweak} />
    </>
  );
}

function agentReplyFor(text, agent) {
  const lower = text.toLowerCase();
  // Tailor by agent role when responding inside a channel.
  if (agent?.role === 'Reviewer') return "Reading the diff now — I'll flag anything I'd block on before approving.";
  if (agent?.role === 'Tester')   return "Kicking off the relevant tests on my host. Will post counts when they settle.";
  if (agent?.role === 'Patcher')  return "On it — drafting the change against the worktree. Patch + tests incoming.";
  if (agent?.role === 'Planner')  return "Let me lay out the moves before we commit anyone's hands to a keyboard. Two options I see…";
  if (agent?.role === 'Researcher') return "Pulling references — I'll cross-check against the existing pattern in `list_runs.rs` and report back.";
  if (agent?.role === 'Writer')   return "I can capture the decision in ADR form once you all settle on the shape.";
  if (lower.includes('test')) return "Running the test suite — give me 30s. I'll report counts and any failures.";
  if (lower.includes('push') || lower.includes('pr')) return "I'll stage the change, run pre-push hooks, and push to the worktree's branch. Want me to open the PR after?";
  if (lower.includes('?')) return "Good question. Let me check the current state and come back with specifics rather than guess.";
  return "Got it — picking that up next. I'll keep you posted as I make progress.";
}

function OrchardTweaksPanel({ t, setTweak }) {
  if (typeof TweaksPanel !== 'function') return null;
  return (
    <TweaksPanel title="Tweaks">
      <TweakSection title="Surface">
        <TweakRadio label="Device" value={t.surface}
                    options={[{ value: 'desktop', label: 'Desktop' }, { value: 'mobile', label: 'Mobile' }]}
                    onChange={(v) => setTweak('surface', v)} />
        <TweakRadio label="Theme" value={t.theme}
                    options={[{ value: 'dark', label: 'Dark' }, { value: 'light', label: 'Light' }]}
                    onChange={(v) => setTweak('theme', v)} />
        <TweakColor label="Accent" value={`oklch(0.68 0.13 ${t.accentHue})`}
                    options={[
                      ['#5fa7ff', '#3b82f6'],
                      ['#8a90ff', '#6366f1'],
                      ['#5fb38f', '#10b981'],
                      ['#e8a64a', '#d97706'],
                      ['#ff8a73', '#ef4444'],
                    ]}
                    onChange={(palette) => {
                      const map = {'#5fa7ff':215,'#8a90ff':260,'#5fb38f':155,'#e8a64a':70,'#ff8a73':25};
                      setTweak('accentHue', map[palette[0]] ?? 215);
                    }} />
      </TweakSection>

      <TweakSection title="Fleet view">
        <TweakRadio label="Lens selector" value={t.lensVariant}
                    options={[{ value: 'pills', label: 'Pills' }, { value: 'tabs', label: 'Tabs' }, { value: 'dropdown', label: 'Drop' }]}
                    onChange={(v) => setTweak('lensVariant', v)} />
        <TweakRadio label="Density" value={t.density}
                    options={[{ value: 'comfortable', label: 'Comfy' }, { value: 'compact', label: 'Compact' }]}
                    onChange={(v) => setTweak('density', v)} />
        <TweakSelect label="Peer health" value={t.peerTreatment}
                     options={[
                       { value: 'ambient',  label: 'Ambient cluster (default)' },
                       { value: 'statusbar', label: 'Status bar across top' },
                       { value: 'hidden',   label: 'Hidden until issue' },
                     ]}
                     onChange={(v) => setTweak('peerTreatment', v)} />
      </TweakSection>

      <TweakSection title="Conversation">
        <TweakSelect label="View switcher" value={t.switcherVariant}
                     options={[
                       { value: 'segmented', label: 'Segmented control' },
                       { value: 'toggle',    label: 'Toggle button + ⌘\\' },
                       { value: 'statusbar', label: 'Status bar treatment' },
                     ]}
                     onChange={(v) => setTweak('switcherVariant', v)} />
        <TweakSelect label="Send status" value={t.statusVariant}
                     options={[
                       { value: 'ticks',   label: 'Ticks (default)' },
                       { value: 'dots',    label: 'Dots' },
                       { value: 'text',    label: 'Text labels' },
                       { value: 'minimal', label: 'Minimal (latest only)' },
                     ]}
                     onChange={(v) => setTweak('statusVariant', v)} />
      </TweakSection>

      <TweakSection title="State">
        <TweakToggle label="Backend offline" value={t.offline} onChange={(v) => setTweak('offline', v)} />
        {t.surface === 'desktop' && (
          <TweakToggle label="Collapse sidebar" value={t.sidebarCollapsed} onChange={(v) => setTweak('sidebarCollapsed', v)} />
        )}
      </TweakSection>
    </TweaksPanel>
  );
}

ReactDOM.createRoot(document.getElementById('root')).render(<App />);
