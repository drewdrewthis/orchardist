// icons.jsx — shared SVG icons. Stroke-based, currentColor.
// Sized via prop `s` (default 16). Stroke 1.5 for crispness at small sizes.

// Orchard mark — stylized branching tree. Worktrees as growing branches.
function OrchardMark({ s = 16, style }) {
  return (
    <svg width={s} height={s} viewBox="0 0 24 24" fill="none" style={style} aria-hidden="true">
      <path d="M12 22V11" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      <path d="M12 14 7 9M12 14l5-5M12 9 9 6M12 9l3-3" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
      <circle cx="7"  cy="9" r="1.6" fill="currentColor" />
      <circle cx="17" cy="9" r="1.6" fill="currentColor" />
      <circle cx="9"  cy="6" r="1.2" fill="currentColor" />
      <circle cx="15" cy="6" r="1.2" fill="currentColor" />
      <circle cx="12" cy="3" r="1.4" fill="currentColor" />
    </svg>
  );
}
Object.assign(window, { OrchardMark });

const _Icon = ({ s = 16, children, style, ...rest }) => (
  <svg width={s} height={s} viewBox="0 0 24 24" fill="none"
       stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"
       style={{ flex: 'none', display: 'block', ...style }} {...rest}>
    {children}
  </svg>
);

const IconSearch  = (p) => <_Icon {...p}><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></_Icon>;
const IconClose   = (p) => <_Icon {...p}><path d="M6 6l12 12M18 6 6 18" /></_Icon>;
const IconChevronRight = (p) => <_Icon {...p}><path d="m9 6 6 6-6 6" /></_Icon>;
const IconChevronDown  = (p) => <_Icon {...p}><path d="m6 9 6 6 6-6" /></_Icon>;
const IconArrowUp = (p) => <_Icon {...p}><path d="M12 19V5M5 12l7-7 7 7" /></_Icon>;
const IconArrowRight = (p) => <_Icon {...p}><path d="M5 12h14M13 5l7 7-7 7" /></_Icon>;
const IconArrowLeft = (p) => <_Icon {...p}><path d="M19 12H5M11 5l-7 7 7 7" /></_Icon>;
const IconPlus    = (p) => <_Icon {...p}><path d="M12 5v14M5 12h14" /></_Icon>;
const IconCheck   = (p) => <_Icon {...p}><path d="m4 12 5 5L20 6" /></_Icon>;
const IconCheckDouble = (p) => <_Icon {...p}><path d="m4 12 4 4 8-10M11 16l1 1L22 6" /></_Icon>;
const IconClock   = (p) => <_Icon {...p}><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></_Icon>;
const IconDot     = (p) => <_Icon {...p}><circle cx="12" cy="12" r="3" fill="currentColor" stroke="none" /></_Icon>;
const IconBolt    = (p) => <_Icon {...p}><path d="M13 3 4 14h7l-1 7 9-11h-7l1-7Z" /></_Icon>;
const IconTerminal= (p) => <_Icon {...p}><path d="M5 9l3 3-3 3M11 15h7" /><rect x="2" y="4" width="20" height="16" rx="2" /></_Icon>;
const IconChat    = (p) => <_Icon {...p}><path d="M4 6.5A2.5 2.5 0 0 1 6.5 4h11A2.5 2.5 0 0 1 20 6.5V14a2.5 2.5 0 0 1-2.5 2.5H10l-4 3.5v-3.5h-.5A2.5 2.5 0 0 1 3 14V8" /></_Icon>;
const IconGitBranch=(p) => <_Icon {...p}><circle cx="6" cy="5" r="2" /><circle cx="6" cy="19" r="2" /><circle cx="18" cy="9" r="2" /><path d="M6 7v10M6 13a6 6 0 0 0 6-6h4" /></_Icon>;
const IconGitFork = (p) => <_Icon {...p}><circle cx="6" cy="5" r="2" /><circle cx="18" cy="5" r="2" /><circle cx="12" cy="19" r="2" /><path d="M6 7v2a3 3 0 0 0 3 3h6a3 3 0 0 0 3-3V7M12 12v5" /></_Icon>;
const IconHost    = (p) => <_Icon {...p}><rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" /></_Icon>;
const IconCpu     = (p) => <_Icon {...p}><rect x="6" y="6" width="12" height="12" rx="1.5" /><path d="M9 1v3M15 1v3M9 20v3M15 20v3M1 9h3M1 15h3M20 9h3M20 15h3" /></_Icon>;
const IconBell    = (p) => <_Icon {...p}><path d="M6 9a6 6 0 0 1 12 0c0 5 2 6 2 6H4s2-1 2-6Z" /><path d="M10 20a2 2 0 0 0 4 0" /></_Icon>;
const IconFilter  = (p) => <_Icon {...p}><path d="M3 5h18l-7 8v6l-4-2v-4Z" /></_Icon>;
const IconLayers  = (p) => <_Icon {...p}><path d="m12 3 9 5-9 5-9-5 9-5Z" /><path d="m3 13 9 5 9-5M3 18l9 5 9-5" /></_Icon>;
const IconSidebar = (p) => <_Icon {...p}><rect x="3" y="4" width="18" height="16" rx="2" /><path d="M9 4v16" /></_Icon>;
const IconCommand = (p) => <_Icon {...p}><path d="M9 9h6v6H9zM9 9V6a3 3 0 1 0-3 3h3Zm6 0V6a3 3 0 1 1 3 3h-3ZM9 15v3a3 3 0 1 1-3-3h3Zm6 0v3a3 3 0 1 0 3-3h-3Z" /></_Icon>;
const IconSparkle = (p) => <_Icon {...p}><path d="M12 3v3M12 18v3M3 12h3M18 12h3M5.5 5.5l2 2M16.5 16.5l2 2M5.5 18.5l2-2M16.5 7.5l2-2" /></_Icon>;
const IconQuestion = (p) => <_Icon {...p}><circle cx="12" cy="12" r="9" /><path d="M9.5 9a2.5 2.5 0 0 1 5 0c0 1.5-2.5 2-2.5 4M12 17h.01" /></_Icon>;
const IconAlert   = (p) => <_Icon {...p}><path d="M10.3 3.5 2 18a2 2 0 0 0 1.7 3h16.6a2 2 0 0 0 1.7-3L13.7 3.5a2 2 0 0 0-3.4 0Z" /><path d="M12 9v4M12 17h.01" /></_Icon>;
const IconRefresh = (p) => <_Icon {...p}><path d="M3 12a9 9 0 0 1 15.5-6.3L21 8M21 4v4h-4M21 12a9 9 0 0 1-15.5 6.3L3 16M3 20v-4h4" /></_Icon>;
const IconCopy    = (p) => <_Icon {...p}><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V6a2 2 0 0 1 2-2h9" /></_Icon>;
const IconArrowsLeftRight = (p) => <_Icon {...p}><path d="M3 8h14m0 0-3-3m3 3-3 3M21 16H7m0 0 3 3m-3-3 3-3" /></_Icon>;
const IconSortAsc = (p) => <_Icon {...p}><path d="M3 6h12M3 12h8M3 18h4M17 4v16m0 0 4-4m-4 4-4-4" /></_Icon>;
const IconSortDesc = (p) => <_Icon {...p}><path d="M3 6h4M3 12h8M3 18h12M17 20V4m0 0 4 4m-4-4-4 4" /></_Icon>;
const IconMaximize = (p) => <_Icon {...p}><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" /></_Icon>;
const IconMinimize = (p) => <_Icon {...p}><path d="M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5" /></_Icon>;
const IconMore    = (p) => <_Icon {...p}><circle cx="6" cy="12" r="1.2" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.2" fill="currentColor" stroke="none"/><circle cx="18" cy="12" r="1.2" fill="currentColor" stroke="none"/></_Icon>;
const IconSend    = (p) => <_Icon {...p}><path d="M5 12h14M14 6l6 6-6 6" /></_Icon>;
const IconAttach  = (p) => <_Icon {...p}><path d="M21 11.5 12.5 20a5 5 0 0 1-7-7L13.5 5a3.5 3.5 0 0 1 5 5L11 17.5a2 2 0 1 1-3-3l6.5-6.5" /></_Icon>;
const IconPullRequest = (p) => <_Icon {...p}><circle cx="6" cy="6" r="2" /><circle cx="6" cy="18" r="2" /><circle cx="18" cy="18" r="2" /><path d="M6 8v8M11 6h3a4 4 0 0 1 4 4v6M14 3l-3 3 3 3" /></_Icon>;
const IconIssue   = (p) => <_Icon {...p}><circle cx="12" cy="12" r="9" /><circle cx="12" cy="12" r="3" fill="currentColor" stroke="none"/></_Icon>;
const IconSun     = (p) => <_Icon {...p}><circle cx="12" cy="12" r="4" /><path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.5 5.5l1.5 1.5M17 17l1.5 1.5M5.5 18.5 7 17M17 7l1.5-1.5" /></_Icon>;
const IconMoon    = (p) => <_Icon {...p}><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z" /></_Icon>;
const IconWifi    = (p) => <_Icon {...p}><path d="M5 12.5a10 10 0 0 1 14 0M8.5 16a5 5 0 0 1 7 0M12 19.5h.01" /></_Icon>;
const IconWifiOff = (p) => <_Icon {...p}><path d="M2 8.8A14 14 0 0 1 22 8.8M5 12.5a10 10 0 0 1 4-2.7M19 12.5a10 10 0 0 0-4-2.7M8.5 16a5 5 0 0 1 7 0M12 19.5h.01M3 3l18 18" /></_Icon>;
const IconExternal=(p) => <_Icon {...p}><path d="M14 4h6v6M10 14 20 4M19 13v5a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h5" /></_Icon>;
const IconLogo    = (p) => (
  <_Icon {...p}><path d="M12 3v18M3 12h18" strokeWidth="2.2"/><circle cx="12" cy="12" r="9" /></_Icon>
);
const IconDocs    = (p) => <_Icon {...p}><path d="M5 4h10l4 4v12a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1Z" /><path d="M14 4v5h5M8 13h8M8 17h5" /></_Icon>;
const IconKey     = (p) => <_Icon {...p}><circle cx="8" cy="15" r="4" /><path d="m11 13 9-9 2 2-2 2 2 2-2 2-2-2-3 3" /></_Icon>;

// Compact, sub-12px-friendly signal glyphs. All draw on a 24-box but the
// shapes are simple enough to hold at 10–12px.
const IconCircleX = (p) => <_Icon {...p}><circle cx="12" cy="12" r="9" /><path d="m9 9 6 6M15 9l-6 6" /></_Icon>;
const IconCircleCheck = (p) => <_Icon {...p}><circle cx="12" cy="12" r="9" /><path d="m8 12 3 3 5-6" /></_Icon>;
const IconCircleDash = (p) => <_Icon {...p}><circle cx="12" cy="12" r="9" strokeDasharray="3 3" /></_Icon>;
const IconCircleHalf = (p) => <_Icon {...p}><circle cx="12" cy="12" r="9" /><path d="M12 3a9 9 0 0 1 0 18Z" fill="currentColor" stroke="none" /></_Icon>;
const IconMessage = (p) => <_Icon {...p}><path d="M4 6.5A2.5 2.5 0 0 1 6.5 4h11A2.5 2.5 0 0 1 20 6.5V14a2.5 2.5 0 0 1-2.5 2.5H10l-4 3.5v-3.5h-.5A2.5 2.5 0 0 1 3 14V8" /></_Icon>;
const IconPause = (p) => <_Icon {...p}><rect x="7" y="5" width="3" height="14" rx="0.5" /><rect x="14" y="5" width="3" height="14" rx="0.5" /></_Icon>;
const IconDraft = (p) => <_Icon {...p}><circle cx="6" cy="6" r="2" /><circle cx="6" cy="18" r="2" strokeDasharray="2 2" /><path d="M6 8v8" /></_Icon>;

Object.assign(window, {
  IconSearch, IconClose, IconChevronRight, IconChevronDown, IconArrowUp, IconArrowRight, IconArrowLeft,
  IconPlus, IconCheck, IconCheckDouble, IconClock, IconDot, IconBolt, IconTerminal, IconChat,
  IconGitBranch, IconGitFork, IconHost, IconCpu, IconBell, IconFilter, IconLayers, IconSidebar,
  IconCommand, IconSparkle, IconQuestion, IconAlert, IconRefresh, IconMore, IconSend, IconAttach,
  IconCopy, IconArrowsLeftRight, IconSortAsc, IconSortDesc,
  IconPullRequest, IconIssue, IconSun, IconMoon, IconWifi, IconWifiOff, IconExternal, IconLogo,
  IconDocs, IconKey,
  IconCircleX, IconCircleCheck, IconCircleDash, IconCircleHalf, IconMessage, IconPause, IconDraft,
  IconMaximize, IconMinimize,
});
