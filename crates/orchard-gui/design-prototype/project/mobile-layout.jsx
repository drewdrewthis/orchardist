// mobile-layout.jsx — phone surface: stack of fleet + conversation + nav.

function MobileLayout(props) {
  const {
    items, hosts, account, lens, lensVariant, density, theme, onToggleTheme,
    selectedId, onSelect, onClearSelection, statusVariant, peerTreatment,
    onOpenPalette, onLaunch, onLens, now,
    conversation, sending, composeText, setComposeText, onSend,
    forkPreview, onStartFork, onCommitFork, onCancelFork,
    filters, onFilterRemove, onFilterClear, onAddFilter,
    offline, onToggleOffline
  } = props;

  const visibleItems = applyFilters(items, filters);
  const selected = items.find((i) => i.id === selectedId);

  return (
    <IOSDevice width={390} height={844} dark={theme === 'dark'} keyboard={false} title="">
      <div className="mobile-screen" data-screen-label="Mobile">
        {!selected ?
        <MobileFleet items={visibleItems} hosts={hosts} account={account} lens={lens}
        lensVariant={lensVariant}
        onLens={onLens} density="comfortable" onSelect={onSelect}
        onOpenPalette={onOpenPalette} onLaunch={onLaunch}
        filters={filters} onFilterRemove={onFilterRemove} onFilterClear={onFilterClear}
        onAddFilter={onAddFilter}
        allItems={items} now={now} offline={offline} onToggleOffline={onToggleOffline}
        theme={theme} onToggleTheme={onToggleTheme} /> :

        <MobileConv item={selected} conversation={conversation} now={now}
        onClose={onClearSelection} onStartFork={onStartFork}
        sending={sending} onSend={onSend}
        composeText={composeText} setComposeText={setComposeText}
        forkPreview={forkPreview} onCommitFork={onCommitFork} onCancelFork={onCancelFork}
        statusVariant={statusVariant} />
        }
      </div>
    </IOSDevice>);

}

function MobileFleet({ items, hosts, account, lens, lensVariant, onLens, density, onSelect,
  onOpenPalette, onLaunch, filters, onFilterRemove, onFilterClear, onAddFilter, allItems,
  now, offline, onToggleOffline, theme, onToggleTheme }) {
  return (
    <>
      {/* Status bar from IOSStatusBar */}
      <IOSStatusBar dark={theme === 'dark'} time="9:41" />
      {/* Mobile top header */}
      <div className="mobile-top">
        <div className="mobile-top-row">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="fleet-brand-mark" style={{ width: 22, height: 22 }}>
              <OrchardMark s={14} />
            </span>
            <span style={{ fontSize: 17, fontWeight: 600, letterSpacing: '-0.02em' }}>Orchard</span>
          </div>
          <div className="mobile-top-actions" data-comment-anchor="8c08ece0b3-div-57-11">
            <PeerHealthCluster hosts={hosts} />
            <button className="iconbtn" onClick={onToggleTheme} aria-label="Theme">
              {theme === 'dark' ? <IconSun s={15} /> : <IconMoon s={15} />}
            </button>
            <button className="iconbtn-primary" onClick={onLaunch} aria-label="New conversation">
              <IconPlus s={15} />
            </button>
          </div>
        </div>
        <div className="mobile-search-row" style={{ width: 'auto', padding: 0, justifyContent: 'flex-end' }}>
          <button className="iconbtn" onClick={onOpenPalette} aria-label="Search">
            <IconSearch s={16} />
          </button>
        </div>
        <div className="mobile-lens-row">
          <LensSelector value={lens} onChange={onLens} variant={lensVariant === 'tabs' ? 'pills' : lensVariant} />
        </div>
      </div>
      {offline &&
      <div className="offline-banner" style={{ flex: 'none' }}>
          <IconWifiOff s={13} /><span>Offline</span>
          <span className="mono dimer">last sync · 38s</span>
          <button className="btn-ghost" style={{ height: 22, marginLeft: 'auto' }} onClick={onToggleOffline}>OK</button>
        </div>
      }
      <FilterBar filters={filters} onRemove={onFilterRemove} onClear={onFilterClear} onAddFilter={onAddFilter}
      allItems={allItems} hosts={hosts} />
      <FleetList items={items} lens={lens} density={density}
      surface="mobile" selectedId={null} onSelect={onSelect}
      now={now} hosts={hosts} />
      <button className="mobile-fab" onClick={onLaunch} aria-label="New conversation">
        <IconPlus s={22} />
      </button>
    </>);

}

function MobileConv({ item, conversation, now, onClose, onStartFork, sending, onSend, composeText, setComposeText, forkPreview, onCommitFork, onCancelFork, statusVariant }) {
  return (
    <>
      <IOSStatusBar dark={false} time="9:41" />
      <div className="conv" style={{ flex: 1, minHeight: 0 }}>
        <ConvHeader item={item} surface="mobile" sessionLive={item.session?.live}
        onClose={onClose} onFork={onStartFork} />
        <ChatView item={item} conversation={conversation}
        statusVariant={statusVariant} surface="mobile" now={now}
        sending={sending} onSend={onSend}
        composeText={composeText} setComposeText={setComposeText}
        forkPreview={forkPreview} onFork={onCommitFork} onCancelFork={onCancelFork} />
      </div>
    </>);

}

function MobileTabbar() {
  const [tab, setTab] = React.useState('fleet');
  const items = [
  { id: 'fleet', label: 'Fleet', icon: IconLayers },
  { id: 'activity', label: 'Activity', icon: IconClock },
  { id: 'you', label: 'You', icon: IconCommand }];

  return (
    <div className="mobile-tabbar" data-comment-anchor="b7e67ca3c8-div-121-5">
      {items.map((i) => {
        const Ic = i.icon;
        return (
          <button key={i.id} className={tab === i.id ? 'on' : ''} onClick={() => setTab(i.id)}>
            <Ic s={18} />
            <span>{i.label}</span>
          </button>);

      })}
    </div>);

}

Object.assign(window, { MobileLayout, MobileFleet, MobileConv, MobileTabbar });