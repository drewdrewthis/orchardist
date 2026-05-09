// conversation.jsx — chat + terminal views, view switcher variants, fork affordance.

// ── Conversation header ─────────────────────────────────────────────────────
function ConvHeader({ item, view, onView, switcherVariant, onFork, onClose, surface, sessionLive }) {
  if (item.kind === 'channel') {
    return <ChannelHeader item={item} onClose={onClose} onFork={onFork} surface={surface} view={view} onView={onView} switcherVariant={switcherVariant} />;
  }
  const tmuxName = item.session?.instance || (item.session?.uuid ? `claude-${item.session.uuid.slice(0, 4)}` : null);
  const attachCmd = tmuxName ? `tmux -L orchard attach -t ${tmuxName}` : null;
  const [copied, setCopied] = React.useState(null);
  const copy = (kind, text) => {
    if (!text) return;
    if (navigator.clipboard) navigator.clipboard.writeText(text).catch(() => {});
    setCopied(kind);
    setTimeout(() => setCopied(null), 1200);
  };
  return (
    <div className="conv-header">
      <div className="conv-header-row">
        {surface === 'mobile' &&
        <button className="iconbtn" onClick={onClose} aria-label="Back" style={{ marginLeft: -6 }}>
            <IconArrowLeft s={16} />
          </button>
        }
        <div className="conv-title-block">
          <div className="conv-title-row">
            <span className={STATUS_PIP[item.status] || 'pip idle'} />
            <span className="conv-title">{item.title}</span>
            {sessionLive && <span className="pip live" title="live" />}
            {item.attentionReason &&
            <span className="conv-attn-inline" title={item.attentionReason}>
                <IconAlert s={11} />
                <span>{item.attentionReason}</span>
              </span>
            }
          </div>
          <div className="conv-sub mono dimer" data-comment-anchor="8063d060ac-div-19-11">
            <a className="conv-chip" title={`Host · ${item.host}`}
              href={`#host:${item.host}`} onClick={(e) => e.preventDefault()}>
              <HostGlyphAuto host={item.host} size={11} />
              <span>{item.host}</span>
            </a>
            <a className="conv-chip" title={`Branch · ${item.branch}`}
              href={`#branch:${item.branch}`} onClick={(e) => e.preventDefault()}>
              <IconGitBranch s={10} />
              <span>{item.branch}</span>
            </a>
            {item.pr &&
            <a className="conv-chip" target="_blank" rel="noreferrer"
              title={`PR #${item.pr.number} · ${item.pr.state}`}
              href={`https://github.com/${item.repo}/pull/${item.pr.number}`}
              onClick={(e) => e.preventDefault()}>
              <IconPullRequest s={10} />
              <span>#{item.pr.number}</span>
            </a>
            }
            {item.issue &&
            <a className="conv-chip" target="_blank" rel="noreferrer"
              title={`Issue #${item.issue.number}`}
              href={`https://github.com/${item.repo}/issues/${item.issue.number}`}
              onClick={(e) => e.preventDefault()}>
              <IconIssue s={10} />
              <span>#{item.issue.number}</span>
            </a>
            }
            {tmuxName &&
            <button className="conv-chip conv-chip-tmux"
              title={`Click to copy: ${attachCmd}`}
              onClick={() => copy('tmux', attachCmd)}>
              <IconTerminal s={10} />
              <span>{tmuxName}</span>
              {copied === 'tmux' ? <IconCheck s={10} /> : <IconCopy s={10} />}
            </button>
            }
            {item.session?.uuid &&
            <button className="conv-chip"
              title={`Click to copy session UUID`}
              onClick={() => copy('uuid', item.session.uuid)}>
              <span style={{ opacity: 0.7 }}>id</span>
              <span>{item.session.uuid.slice(0, 6)}…</span>
              {copied === 'uuid' ? <IconCheck s={10} /> : <IconCopy s={10} />}
            </button>
            }
          </div>
        </div>
        <div className="conv-header-actions" data-comment-anchor="0f29cd70d2-div-31-9">
          {item.contract &&
          <button className="iconbtn contract-badge"
          onClick={() => (window.__orchardOpenContract || (() => {}))(item)}
          title={`Contract ${item.contract.id}${item.contract.openQuestions ? ` · ${item.contract.openQuestions} open` : ''}`}>
              <IconDocs s={14} />
              {item.contract.openQuestions > 0 &&
            <span className="contract-count tnum">{item.contract.openQuestions}</span>
            }
            </button>
          }
          <button className="iconbtn" onClick={onFork} title="Fork conversation (⌘⇧F)">
            <IconGitFork s={15} />
          </button>
          <button className="iconbtn" title="More">
            <IconMore s={15} />
          </button>
          {/* View switcher pulled to the far right so the header has clear left/right zones */}
          {surface === 'desktop' &&
          <>
            <span className="conv-header-divider" aria-hidden="true" />
            <ViewSwitcher value={view} onChange={onView} variant={switcherVariant} />
          </>
          }
        </div>
      </div>
    </div>);

}

// ── Channel header — replaces ConvHeader for kind === 'channel' ─────────────
// Shows participant agents as pills; each pill exposes the agent's tmux
// attach command. Operator can invite another agent into the room.
function ChannelHeader({ item, onClose, onFork, surface, view, onView, switcherVariant }) {
  const allAgents = window.ORCHARD_AGENTS || [];
  const participants = allAgents.filter((a) => item.participants?.includes(a.id));
  const others = allAgents.filter((a) => !item.participants?.includes(a.id));
  const [copied, setCopied] = React.useState(null);
  const [invOpen, setInvOpen] = React.useState(false);
  const copy = (key, text) => {
    if (!text) return;
    if (navigator.clipboard) navigator.clipboard.writeText(text).catch(() => {});
    setCopied(key);
    setTimeout(() => setCopied(null), 1200);
  };
  const tmuxFor = (a) => `agent-${a.id.replace(/^a\./, '')}`;
  const attachCmd = (a) => `tmux -L orchard attach -t ${tmuxFor(a)}`;
  return (
    <div className="conv-header is-channel">
      <div className="conv-header-row">
        {surface === 'mobile' &&
          <button className="iconbtn" onClick={onClose} aria-label="Back" style={{ marginLeft: -6 }}>
            <IconArrowLeft s={16} />
          </button>}
        <div className="conv-title-block">
          <div className="conv-title-row">
            <span className="channel-hash" style={{ width: 18, height: 18, fontSize: 12, lineHeight: '16px' }}>#</span>
            <span className="conv-title">{item.title.replace(/^#?\s*/, '').replace(/\s·\s.*/, '')}</span>
            {item.topic &&
              <span className="dimer" style={{ fontSize: 12, marginLeft: 6, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>· {item.topic}</span>}
          </div>
        </div>
        <div className="conv-header-actions">
          <button className="iconbtn" onClick={onFork} title="Fork conversation">
            <IconGitFork s={15} />
          </button>
          <button className="iconbtn" title="More">
            <IconMore s={15} />
          </button>
          {surface === 'desktop' &&
            <>
              <span className="conv-header-divider" aria-hidden="true" />
              <ViewSwitcher value={view} onChange={onView} variant={switcherVariant} />
            </>}
        </div>
      </div>
      <div className="channel-roster">
        {participants.map((a) => (
          <div key={a.id} className="channel-pill" title={`${a.name} · ${a.role} · ${a.host}`}>
            <span className="agent-avatar" style={{ background: `oklch(0.62 0.13 ${a.hue})`, width: 16, height: 16, fontSize: 9.5 }}>{a.avatar}</span>
            <span className="channel-pill-name">{a.name}</span>
            <span className="dimest mono" style={{ fontSize: 10 }}>{a.model}</span>
            <button className="channel-pill-jump"
              title={`Open ${a.name}'s session in a new pane`}
              onClick={() => (window.__orchardOpenAgentSession || (() => {}))(a.id)}>
              <IconChat s={10} />
              <span>session</span>
            </button>
            <button className="channel-pill-tmux"
              title={`Copy: ${attachCmd(a)}`}
              onClick={() => copy(a.id, attachCmd(a))}>
              <IconTerminal s={10} />
              <span className="mono">{tmuxFor(a)}</span>
              {copied === a.id ? <IconCheck s={10} /> : <IconCopy s={10} />}
            </button>
          </div>
        ))}
        <div className="channel-invite-wrap">
          <button className="channel-invite" onClick={() => setInvOpen((v) => !v)} title="Invite an agent">
            <IconPlus s={11} />
            <span>Invite</span>
          </button>
          {invOpen &&
            <div className="channel-invite-menu glass-strong" onMouseLeave={() => setInvOpen(false)}>
              <div className="dimer mono" style={{ fontSize: 10.5, padding: '6px 10px 4px', letterSpacing: '0.06em' }}>AVAILABLE AGENTS</div>
              {others.length === 0
                ? <div className="dimer" style={{ padding: '8px 10px', fontSize: 12 }}>All agents are already here.</div>
                : others.map((a) => (
                  <button key={a.id} className="channel-invite-item" onClick={() => setInvOpen(false)}>
                    <span className="agent-avatar" style={{ background: `oklch(0.62 0.13 ${a.hue})`, width: 18, height: 18, fontSize: 10 }}>{a.avatar}</span>
                    <span style={{ flex: 1 }}>{a.name}</span>
                    <span className="dimer" style={{ fontSize: 11 }}>{a.role}</span>
                    <span className="dimest mono" style={{ fontSize: 10.5 }}>{a.host}</span>
                  </button>
                ))}
            </div>}
        </div>
      </div>
    </div>);
}

// ── View switcher (3 variants for tweak) ────────────────────────────────────
function ViewSwitcher({ value, onChange, variant = 'segmented' }) {
  const items = [
  { id: 'chat', label: 'Chat', icon: <IconChat s={15} /> },
  { id: 'terminal', label: 'Terminal', icon: <IconTerminal s={15} /> }];

  if (variant === 'segmented') {
    const idx = items.findIndex((i) => i.id === value);
    return (
      <div className="seg seg-icon" role="tablist" data-comment-anchor="61e54c5978-div-69-7">
        <div className="seg-thumb" style={{ left: `calc(2px + ${idx} * (100% - 4px) / 2)`, width: `calc((100% - 4px) / 2)` }} />
        {items.map((i) =>
        <button key={i.id} role="tab" aria-selected={i.id === value} data-on={i.id === value ? '1' : '0'}
        title={i.label}
        onClick={() => onChange(i.id)}>
            {i.icon}
          </button>
        )}
      </div>);

  }
  if (variant === 'icon-toggle') {
    return (
      <button className="iconbtn" data-active="1" onClick={() => onChange(value === 'chat' ? 'terminal' : 'chat')}
      title={value === 'chat' ? 'Switch to terminal · ⌘T' : 'Switch to chat · ⌘T'}>
        {value === 'chat' ? <IconTerminal s={15} /> : <IconChat s={15} />}
      </button>);

  }
  // statusbar — a tiny text-link in the header right, with the inactive view available as a tap
  return (
    <div className="view-statusbar mono">
      <span style={{ color: 'var(--fg-3)' }}>view</span>
      {items.map((i) =>
      <button key={i.id} className="view-statusbar-btn" data-on={i.id === value ? '1' : '0'}
      onClick={() => onChange(i.id)}>
          {i.label}
        </button>
      )}
    </div>);

}

// ── Chat view ───────────────────────────────────────────────────────────────
function ChatView({ item, conversation, statusVariant, surface, now, onFork, sending, onSend, composeText, setComposeText, forkPreview, onCancelFork }) {
  const scrollRef = React.useRef(null);
  const [recapOpen, setRecapOpen] = React.useState(false); // start minimized
  React.useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [conversation.messages.length, item.id, sending]);
  return (
    <div className="chat">
      <div className={`chat-recap ${recapOpen ? 'open' : 'collapsed'}`} data-comment-anchor="81d7c02d4e-div-112-7">
        <button className="chat-recap-toggle" onClick={() => setRecapOpen((v) => !v)}
          aria-expanded={recapOpen ? 'true' : 'false'} title={recapOpen ? 'Hide recap' : 'Show recap'}>
          <IconDocs s={11} />
          <span className="dimest mono" style={{ fontSize: 10.5, fontWeight: 600, letterSpacing: '0.06em' }}>RECAP</span>
          {!recapOpen && <span className="chat-recap-peek dimer">{conversation.recap}</span>}
          <IconChevronDown s={11} className="chat-recap-chev" data-on={recapOpen ? '1' : '0'} />
        </button>
        {recapOpen && <p>{conversation.recap}</p>}
      </div>
      <div className="chat-scroll scroll" ref={scrollRef} data-comment-anchor="dc5e7ce59f-div-116-7">
        {conversation.messages.map((msg, i) => {
          const prev = conversation.messages[i - 1];
          const grouped = prev && prev.role === msg.role && msg.ts - prev.ts < 60_000 * 5;
          return <ChatMessage key={msg.id} msg={msg} grouped={grouped} idx={i}
            isChannel={item.kind === 'channel'}
            statusVariant={statusVariant}
            onForkFrom={(_i, m) => (window.__orchardForkFromMsg || (() => onFork && onFork()))(_i, m)}
            onReset={(_i, m) => (window.__orchardResetFromMsg || (() => {}))(_i, m)} />;
        })}
        {sending &&
        <ChatMessage msg={{ id: 'sending', role: 'agent', text: '', status: 'pending', ts: now, typing: true }} statusVariant={statusVariant} />
        }
        {forkPreview &&
        <div className="chat-fork-preview fadeIn">
            <div className="chat-fork-header">
              <IconGitFork s={13} />
              <b>Forking from message #{forkPreview.fromIdx + 1}</b>
              <span className="dimer" style={{ fontSize: 11 }}>creates new session anchor</span>
            </div>
            <div className="chat-fork-body">
              <div className="chat-fork-line">
                <span className="dimer" style={{ fontSize: 11, width: 56 }}>parent</span>
                <span className="mono" style={{ fontSize: 11.5 }}>{item.session.uuid}</span>
              </div>
              <div className="chat-fork-line">
                <span className="dimer" style={{ fontSize: 11, width: 56 }}>new</span>
                <span className="mono" style={{ fontSize: 11.5, color: 'var(--accent)' }}>fork-{item.session.uuid.slice(0, 4)}-…</span>
              </div>
              <textarea className="input"
            style={{ height: 60, padding: 8, resize: 'none', marginTop: 4 }}
            placeholder="Take this in a new direction…"
            defaultValue="Let's try a different approach — keep the namespacing of the existing cursor param and add cursor_v2 as a parallel option." />
            </div>
            <div className="chat-fork-actions">
              <button className="btn-ghost" onClick={onCancelFork}>Cancel</button>
              <button className="btn-primary" onClick={onFork}>Fork conversation</button>
            </div>
          </div>
        }
      </div>
      <Composer value={composeText} onChange={setComposeText} onSend={onSend} surface={surface} sending={sending} item={item} />
    </div>);

}

function ChatMessage({ msg, grouped, statusVariant, onCopy, onReset, onForkFrom, idx, isChannel }) {
  if (msg.text && /__contract__:/.test(msg.text)) return null;
  const isUser = msg.role === 'user';
  const agent = isChannel && msg.agentId
    ? (window.ORCHARD_AGENTS || []).find(a => a.id === msg.agentId)
    : null;
  const [copied, setCopied] = React.useState(false);
  const doCopy = () => {
    if (navigator.clipboard && msg.text) navigator.clipboard.writeText(msg.text).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 1100);
    onCopy && onCopy(msg);
  };
  return (
    <div className={`chat-msg ${grouped ? 'grouped' : ''} ${isUser ? 'is-user' : 'is-agent'} ${msg.isQuestion ? 'is-question' : ''} ${msg.isPaused ? 'is-paused' : ''} fadeIn`}>
      <div className="chat-msg-gutter">
        {!grouped && (agent
          ? <span className="agent-avatar chat-agent-avatar" title={`${agent.name} · ${agent.role}`} style={{ background: `oklch(0.62 0.13 ${agent.hue})`, width: 22, height: 22, fontSize: 11 }}>{agent.avatar}</span>
          : <Avatar kind={msg.role} size={22} />)}
      </div>
      <div className="chat-msg-body">
        {!grouped &&
        <div className="chat-msg-meta">
            <span className="chat-msg-name" style={agent ? { color: `oklch(0.78 0.13 ${agent.hue})` } : null}>
              {isUser ? 'Drew' : (agent ? agent.name : 'Agent')}
            </span>
            {agent && <span className="dimest mono" style={{ fontSize: 10.5 }}>{agent.model}</span>}
            <span className="dimest mono" style={{ fontSize: 10.5 }}>{shortTime(msg.ts)}</span>
            {msg.isQuestion && <span className="chip attn" style={{ height: 16, fontSize: 10, padding: '0 6px' }}><IconQuestion s={9} /> open question</span>}
            {msg.isPaused && <span className="chip" style={{ height: 16, fontSize: 10, padding: '0 6px' }}><IconClock s={9} /> paused</span>}
          </div>
        }
        <div className="chat-msg-bubble">
          {msg.typing ? <TypingDots /> : <span dangerouslySetInnerHTML={{ __html: linkifyMono(msg.text) }} />}
          {msg.tools?.length > 0 &&
          <div className="chat-msg-tools">
              {msg.tools.map((t, i) =>
            <span key={i} className="chip ghost" style={{ height: 18, fontSize: 10.5, padding: '0 6px' }}>
                  <IconBolt s={9} /><span className="mono">{t}</span>
                </span>
            )}
            </div>
          }
          {msg.diff &&
          <div className="chat-msg-diff mono">
              <span style={{ color: 'var(--ok-fg)' }}>+{msg.diff.plus}</span>
              <span style={{ color: 'var(--bad-fg)' }}>−{msg.diff.minus}</span>
              <span className="dimer">across {msg.diff.files} files</span>
              <button className="btn-ghost" style={{ height: 18, padding: '0 6px', fontSize: 11, marginLeft: 'auto' }}>view</button>
            </div>
          }
        </div>
        {isUser &&
        <div className="chat-msg-status">
            <SendStatus status={msg.status} variant={statusVariant} />
          </div>
        }
        {!msg.typing &&
        <div className="chat-msg-actions" role="group" aria-label="Message actions">
          <button className="chat-msg-action" onClick={doCopy} title={copied ? 'Copied' : 'Copy message'}>
            {copied ? <IconCheck s={11} /> : <IconCopy s={11} />}
          </button>
          <button className="chat-msg-action" onClick={() => onForkFrom && onForkFrom(idx, msg)} title="Fork from here">
            <IconGitFork s={11} />
          </button>
          <button className="chat-msg-action chat-msg-action-danger"
            onClick={() => onReset && onReset(idx, msg)} title="Reset conversation from here">
            <IconRefresh s={11} />
          </button>
        </div>
        }
      </div>
    </div>);

}

// Linkify #123 + paths in mono — keep it simple, escape first.
function linkifyMono(s) {
  const esc = s.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[c]);
  return esc.
  replace(/(`[^`]+`)/g, (m) => `<code class="mono inline-code">${m.slice(1, -1)}</code>`).
  replace(/(#\d+)/g, (m) => `<span class="mono" style="color:var(--accent);font-weight:500;">${m}</span>`).
  replace(/(__contract__:)/g, '<span class="mono" style="color:var(--attn-fg);font-weight:600;">contract:</span>');
}

function TypingDots() {
  return (
    <span className="typing-dots">
      <i /><i /><i />
    </span>);

}

// ── Composer ────────────────────────────────────────────────────────────────
function Composer({ value, onChange, onSend, surface, sending, item }) {
  const taRef = React.useRef(null);
  const onKey = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (value.trim()) onSend();
    }
  };
  React.useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = 'auto';
    ta.style.height = Math.min(140, Math.max(36, ta.scrollHeight)) + 'px';
  }, [value]);
  return (
    <div className="composer">
      <div className="composer-inner">
        <button className="iconbtn" title="Attach"><IconAttach s={15} /></button>
        <textarea ref={taRef}
        className="composer-ta"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKey}
        placeholder={item.kind === 'channel' ? `Message #${item.title}` : (item.session?.live ? 'Reply to agent…' : 'Pick up this session…')}
        rows={1} />
        <div className="composer-actions" data-comment-anchor="60b294b878-div-248-9">
          <button className="iconbtn-primary"
          disabled={!value.trim() || sending}
          onClick={onSend}
          aria-label="Send"
          title="Send · ⌘↵">
            <IconSend s={14} />
          </button>
        </div>
      </div>
    </div>);

}

// ── Terminal view ───────────────────────────────────────────────────────────
function TerminalView({ lines, item, surface }) {
  const ref = React.useRef(null);
  React.useEffect(() => {
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [lines.length]);
  return (
    <div className="term term-pane">
      <div className="term-statusbar mono">
        <span style={{ color: '#a1a1ac' }}>tmux</span>
        <span style={{ color: '#7d7d8a' }}>·</span>
        <span style={{ color: '#e9e9ee' }}>{item.host}</span>
        <span style={{ color: '#7d7d8a' }}>:</span>
        <span style={{ color: '#e9e9ee' }}>{item.branch}</span>
        <span style={{ color: '#7d7d8a' }}>·</span>
        <span style={{ color: '#a1a1ac' }}>{item.session?.instance || 'detached'}</span>
        <span style={{ marginLeft: 'auto', color: '#7d7d8a' }}>{item.session?.live ? '⏵ live' : '○ detached'}</span>
      </div>
      <div className="term-scroll scroll" ref={ref}>
        {lines.map((l, i) =>
        <div key={i} className="term-line" data-c={l.c}>
            {l.t || '\u00A0'}
          </div>
        )}
        {item.session?.live &&
        <div className="term-line"><span style={{ color: '#7e7e8a' }}>{'>'}</span>&nbsp;<span className="caret" /></div>
        }
      </div>
    </div>);

}

Object.assign(window, {
  ConvHeader, ViewSwitcher, ChatView, ChatMessage, Composer, TerminalView, ContractModal
});

function ContractModal({ item, onClose }) {
  if (!item || !item.contract) return null;
  const c = item.contract;
  const baseMsgs = window.ORCHARD_CONVERSATION?.messages || [];
  const questions = baseMsgs.filter((m) => m.text && /__contract__:/.test(m.text));
  return (
    <div className="overlay-backdrop fadeIn" onClick={onClose}>
      <div className="contract-modal glass-strong scaleIn" onClick={(e) => e.stopPropagation()}>
        <div className="contract-modal-header">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <IconDocs s={15} />
            <b style={{ fontSize: 14 }}>{c.id}</b>
            <span className="chip" style={{ height: 18, fontSize: 10.5 }}>{c.status}</span>
          </div>
          <button className="iconbtn" onClick={onClose} aria-label="Close"><IconClose s={14} /></button>
        </div>
        <div className="contract-modal-body">
          <div className="contract-line">
            <span className="dimer mono" style={{ fontSize: 11, width: 88 }}>statement</span>
            <span>{item.title}</span>
          </div>
          <div className="contract-line">
            <span className="dimer mono" style={{ fontSize: 11, width: 88 }}>owner</span>
            <span className="mono" style={{ fontSize: 12 }}>{item.session?.uuid ?? '—'}</span>
          </div>
          <div className="contract-line">
            <span className="dimer mono" style={{ fontSize: 11, width: 88 }}>host</span>
            <span className="mono" style={{ fontSize: 12 }}>{item.host}</span>
          </div>
          <div style={{ borderTop: '0.5px solid var(--line)', margin: '12px 0' }} />
          <div className="dimer mono" style={{ fontSize: 11, marginBottom: 6 }}>
            {c.openQuestions || 0} open question{c.openQuestions === 1 ? '' : 's'}
          </div>
          {questions.length === 0 ?
          <div className="dimer" style={{ fontSize: 12 }}>No active questions on this contract.</div> :

          <ul className="contract-questions">
              {questions.map((q) =>
            <li key={q.id} className="contract-question">
                  <IconQuestion s={12} style={{ color: 'var(--attn)', flex: 'none', marginTop: 2 }} />
                  <div>
                    <div style={{ fontSize: 13, lineHeight: 1.5 }}>{q.text.replace(/__contract__:\s*/, '')}</div>
                    <div className="dimest mono" style={{ fontSize: 10.5, marginTop: 4 }}>{shortTime(q.ts)}</div>
                  </div>
                </li>
            )}
            </ul>
          }
        </div>
      </div>
    </div>);

}
Object.assign(window, { ContractModal });