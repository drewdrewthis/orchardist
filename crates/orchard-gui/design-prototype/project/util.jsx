// util.jsx — small helpers used across the prototype.

// Relative-time formatter that breathes with the live tick.
function relTime(ms, now) {
  const d = (now - ms) / 1000;
  if (d < 5) return 'now';
  if (d < 60) return `${Math.floor(d)}s ago`;
  if (d < 3600) return `${Math.floor(d / 60)}m ago`;
  if (d < 86400) return `${Math.floor(d / 3600)}h ago`;
  const days = Math.floor(d / 86400);
  if (days < 7) return `${days}d ago`;
  return new Date(ms).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}
function shortTime(ms) {
  return new Date(ms).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
}

// Tiny fuzzy match — returns score (higher better) + matched indices for highlighting.
// Strict prefix > word-prefix > subseq. Case-insensitive.
function fuzzyMatch(query, text) {
  if (!query) return { score: 1, idx: [] };
  const q = query.toLowerCase();
  const t = text.toLowerCase();
  // Direct substring boost
  const sub = t.indexOf(q);
  if (sub >= 0) {
    const idx = [];
    for (let i = 0; i < q.length; i++) idx.push(sub + i);
    const wordStart = sub === 0 || /\W/.test(t[sub - 1]) ? 50 : 0;
    return { score: 200 - sub + wordStart, idx };
  }
  // Subsequence
  let qi = 0; const idx = [];
  for (let i = 0; i < t.length && qi < q.length; i++) {
    if (t[i] === q[qi]) { idx.push(i); qi++; }
  }
  if (qi < q.length) return null;
  // tighter contiguous spans score higher
  let span = 0;
  for (let i = 1; i < idx.length; i++) if (idx[i] - idx[i - 1] === 1) span++;
  return { score: 60 + span - (idx[0] || 0) * 0.1, idx };
}

// Render a label with matched chars highlighted.
function HiLite({ text, idx }) {
  if (!idx || !idx.length) return <span>{text}</span>;
  const chars = [];
  const set = new Set(idx);
  let buf = '';
  let on = null;
  const flush = () => {
    if (!buf) return;
    chars.push(on
      ? <mark key={chars.length} style={{ background: 'transparent', color: 'var(--accent)', fontWeight: 600 }}>{buf}</mark>
      : <span key={chars.length}>{buf}</span>);
    buf = '';
  };
  for (let i = 0; i < text.length; i++) {
    const here = set.has(i);
    if (on === null) on = here;
    if (here !== on) { flush(); on = here; }
    buf += text[i];
  }
  flush();
  return <>{chars}</>;
}

// Sparkline mini-bar (20 values, stylised)
function Spark({ values, w = 56, h = 14, color = 'currentColor' }) {
  if (!values || !values.length) return <svg width={w} height={h} />;
  const max = Math.max(1, ...values);
  const step = w / values.length;
  const bars = values.map((v, i) => {
    const bh = Math.max(1, (v / max) * (h - 2));
    return <rect key={i} x={i * step + 0.5} y={h - bh} width={Math.max(1, step - 1)} height={bh}
                 rx="0.6" fill={color} opacity={0.5 + (v / max) * 0.5} />;
  });
  return <svg width={w} height={h}>{bars}</svg>;
}

// Sparkline as a line — for terminal scrollback / small contexts
function SparkLine({ values, w = 56, h = 14, color = 'currentColor' }) {
  if (!values || !values.length) return <svg width={w} height={h} />;
  const max = Math.max(1, ...values);
  const step = w / (values.length - 1 || 1);
  const pts = values.map((v, i) => `${i * step},${h - (v / max) * (h - 1) - 0.5}`).join(' ');
  return <svg width={w} height={h}><polyline fill="none" stroke={color} strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" points={pts} /></svg>;
}

// Resource bar (e.g. CPU %, quota).
function ResourceBar({ value, max = 100, w = 40, color = 'var(--fg-3)' }) {
  const pct = Math.min(100, (value / max) * 100);
  return (
    <span style={{ display: 'inline-block', width: w, height: 4, borderRadius: 2,
                   background: 'var(--line-2)', position: 'relative', overflow: 'hidden' }}>
      <i style={{ position: 'absolute', left: 0, top: 0, bottom: 0, width: pct + '%',
                  background: color, borderRadius: 2 }} />
    </span>
  );
}

// status → human label
const STATUS_LABEL = {
  attn: 'Needs attention',
  ok: 'Healthy',
  bad: 'Blocked',
  idle: 'Idle',
  stale: 'Stale',
};

// status → pip class
const STATUS_PIP = {
  attn: 'pip attn',
  ok: 'pip ok',
  bad: 'pip bad',
  idle: 'pip idle',
  stale: 'pip stale',
};

// Hash a string → consistent host color (used only for tiny host glyphs)
function hostHue(h) {
  let n = 0;
  for (const c of h) n = (n * 31 + c.charCodeAt(0)) | 0;
  return ((n % 360) + 360) % 360;
}

// HostGlyph — a tiny 14px tile with two-letter hostname initials, used in
// dense rows so 'where it lives' is recognisable without reading.
function HostGlyph({ host, size = 14, dim = false }) {
  const initials = host.split(/[-_.]/).map(s => s[0]).join('').slice(0, 2).toUpperCase();
  const hue = hostHue(host);
  return (
    <span style={{
      width: size, height: size, borderRadius: 3,
      background: `oklch(0.94 0.03 ${hue})`,
      color: `oklch(0.32 0.08 ${hue})`,
      fontFamily: 'Geist Mono, ui-monospace, monospace',
      fontSize: size * 0.55, fontWeight: 600,
      display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
      letterSpacing: 0,
      flex: 'none',
      opacity: dim ? 0.45 : 1,
      lineHeight: 1,
    }}>{initials}</span>
  );
}

// Same glyph, but theme-aware (used in dark mode)
function HostGlyphAuto({ host, size = 14, dim = false }) {
  const initials = host.split(/[-_.]/).map(s => s[0]).join('').slice(0, 2).toUpperCase();
  const hue = hostHue(host);
  return (
    <span style={{
      width: size, height: size, borderRadius: 3,
      background: `color-mix(in oklab, oklch(0.62 0.13 ${hue}) 22%, var(--surface-2))`,
      color: `color-mix(in oklab, oklch(0.62 0.13 ${hue}) 65%, var(--fg))`,
      fontFamily: 'Geist Mono, ui-monospace, monospace',
      fontSize: size * 0.55, fontWeight: 600,
      display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
      letterSpacing: 0,
      flex: 'none',
      opacity: dim ? 0.45 : 1,
      lineHeight: 1,
    }}>{initials}</span>
  );
}

// Send-status icon (pending, sent, delivered, read), variant determines style:
// 'minimal' = single dot/check pair; 'ticks' = single→double check; 'dots' = three dots.
function SendStatus({ status, variant = 'ticks' }) {
  const c = {
    pending:   'var(--fg-4)',
    sent:      'var(--fg-3)',
    delivered: 'var(--fg-2)',
    read:      'var(--accent)',
  }[status] || 'var(--fg-4)';
  if (variant === 'text') {
    return <span className="mono dimer" style={{ fontSize: 10, color: c }}>{status}</span>;
  }
  if (variant === 'dots') {
    const filled = { pending: 1, sent: 2, delivered: 3, read: 3 }[status] || 0;
    return (
      <span style={{ display: 'inline-flex', gap: 2 }}>
        {[0,1,2].map(i =>
          <i key={i} style={{
            width: 4, height: 4, borderRadius: '50%',
            background: i < filled ? c : 'var(--fg-4)',
            opacity: status === 'read' ? 1 : (i < filled ? 1 : 0.4),
          }} />
        )}
      </span>
    );
  }
  if (variant === 'minimal') {
    if (status === 'pending') return <i style={{ width: 4, height: 4, borderRadius: '50%', background: c, display: 'inline-block', opacity: 0.6 }} />;
    if (status === 'sent')    return <IconCheck s={11} style={{ color: c }} />;
    return <IconCheck s={11} style={{ color: c }} />;
  }
  // ticks (default)
  if (status === 'pending') {
    return <IconClock s={11} style={{ color: c }} />;
  }
  if (status === 'sent') {
    return <IconCheck s={11} style={{ color: c }} />;
  }
  return <IconCheckDouble s={13} style={{ color: c }} />;
}

// Avatar/initial for a chat speaker
function Avatar({ kind = 'agent', size = 22 }) {
  if (kind === 'user') {
    return <span style={{
      width: size, height: size, borderRadius: '50%',
      background: 'var(--surface-2)', border: '0.5px solid var(--line-2)',
      color: 'var(--fg-2)', fontSize: size * 0.42, fontWeight: 600,
      display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
      flex: 'none', lineHeight: 1,
    }}>D</span>;
  }
  return <span style={{
    width: size, height: size, borderRadius: '50%',
    background: 'var(--fg)', color: 'var(--bg)',
    display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
    fontSize: size * 0.5, flex: 'none', lineHeight: 1,
  }}>
    <svg width={size * 0.55} height={size * 0.55} viewBox="0 0 24 24" fill="none">
      <path d="M12 4 L19 8 V16 L12 20 L5 16 V8 Z" fill="currentColor" opacity="0.3" />
      <circle cx="12" cy="12" r="2.4" fill="currentColor" />
    </svg>
  </span>;
}

Object.assign(window, {
  relTime, shortTime, fuzzyMatch, HiLite, Spark, SparkLine,
  ResourceBar, STATUS_LABEL, STATUS_PIP, HostGlyph, HostGlyphAuto,
  SendStatus, Avatar,
});
