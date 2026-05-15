import { IssueLensStore } from "../plugins/houdini-svelte/stores/IssueLens";
import { PaneCardStore } from "../plugins/houdini-svelte/stores/PaneCard";
import { AttentionLensStore } from "../plugins/houdini-svelte/stores/AttentionLens";
import { HostsListStore } from "../plugins/houdini-svelte/stores/HostsList";
import { OpenPanelStore } from "../plugins/houdini-svelte/stores/OpenPanel";
import { WorktreePRStore } from "../plugins/houdini-svelte/stores/WorktreePR";
import { RecentLensStore } from "../plugins/houdini-svelte/stores/RecentLens";
import { SessionCardStore } from "../plugins/houdini-svelte/stores/SessionCard";
import { WorktreeEnrichmentStore } from "../plugins/houdini-svelte/stores/WorktreeEnrichment";
import { TmuxLensStore } from "../plugins/houdini-svelte/stores/TmuxLens";
import { WorktreeLensStore } from "../plugins/houdini-svelte/stores/WorktreeLens";
import { WorktreesListStore } from "../plugins/houdini-svelte/stores/WorktreesList";
import type { Cache as InternalCache } from "./cache/cache";
import type { CacheTypeDef } from "./generated";
import { Cache } from "./public";
export * from "./client";
export * from "./lib";

export function graphql(
    str: "query IssueLens {\n\tclaudeInstances {\n\t\t...SessionCard\n\t}\n\tworkView {\n\t\trepos {\n\t\t\tid\n\t\t\tslug\n\t\t\tworktrees {\n\t\t\t\t...WorktreeEnrichment\n\t\t\t\tclaudeInstances {\n\t\t\t\t\t...SessionCard\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t}\n}\n"
): IssueLensStore;

export function graphql(
    str: "fragment PaneCard on TmuxPane {\n\tpaneId\n\ttitle\n\tcurrentCommand\n\tcurrentPid\n\twindow {\n\t\tid\n\t\tindex\n\t\tname\n\t\tactive\n\t\tsession {\n\t\t\tid\n\t\t\tname\n\t\t\tattached\n\t\t\tactiveAttached\n\t\t\tlastActivityAt\n\t\t}\n\t}\n\tclaudeInstance {\n\t\t...SessionCard\n\t}\n\tprocess {\n\t\tpid\n\t\tcwd\n\t\t# `command` is the basename ps reports (e.g. \"claude\", \"zsh\"). More\n\t\t# reliable than `TmuxPane.currentCommand` which on macOS can be the\n\t\t# `claude --version` string instead of the executable name.\n\t\tcommand\n\t\tworktree {\n\t\t\t...WorktreeEnrichment\n\t\t}\n\t}\n}\n"
): PaneCardStore;

export function graphql(
    str: "query AttentionLens {\n\tworkView {\n\t\trepos {\n\t\t\tid\n\t\t\tslug\n\t\t\tworktrees {\n\t\t\t\t...WorktreeEnrichment\n\t\t\t\tclaudeInstances {\n\t\t\t\t\t...SessionCard\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t}\n}\n"
): AttentionLensStore;

export function graphql(
    str: "query HostsList {\n\thosts {\n\t\tid\n\t\thostname\n\t\tos\n\t\tkernel\n\t\treachable\n\t\tlastSeenAt\n\t\tresourceLoad {\n\t\t\tcpuPercent\n\t\t\tmemPercent\n\t\t\tdiskPercent\n\t\t\tloadAvg1m\n\t\t\tloadAvg5m\n\t\t\tloadAvg15m\n\t\t}\n\t}\n\tclaudeAccounts {\n\t\tid\n\t\temail\n\t\tquotaUsed\n\t\tquotaCap\n\t\tquotaResetsAt\n\t\tquotaEstimated\n\t}\n}\n"
): HostsListStore;

export function graphql(
    str: "query OpenPanel($paneIds: [String!]) {\n\ttmuxPanes(filter: { paneIdIn: $paneIds }) {\n\t\t...PaneCard\n\t}\n\tclaudeInstances {\n\t\t...SessionCard\n\t}\n\tconversations {\n\t\tsessionUuid\n\t\tlastSeenAt\n\t\tfirstSeenAt\n\t\tmessageCount\n\t\topen\n\t\trecap\n\t\tcwd\n\t\tjsonlPath\n\t\tagentName\n\t\tcustomTitle\n\t}\n\tworkView {\n\t\trepos {\n\t\t\tid\n\t\t\tslug\n\t\t\tworktrees {\n\t\t\t\t...WorktreeEnrichment\n\t\t\t}\n\t\t}\n\t}\n}\n"
): OpenPanelStore;

export function graphql(
    str: "fragment WorktreePR on Worktree {\n\tpr {\n\t\tnumber\n\t\tstate\n\t\tstatusCheckRollup\n\t\treviewDecision\n\t\tmergeable\n\t\tmergeStateStatus\n\t}\n}\n"
): WorktreePRStore;

export function graphql(
    str: "query RecentLens {\n\t# Every Claude conversation known to the daemon (incl. dead/dormant\n\t# ones that no longer have a live process). 363+ in a typical fleet —\n\t# this is the substrate Recent shows.\n\tconversations {\n\t\tid\n\t\tsessionUuid\n\t\tagentName\n\t\tcustomTitle\n\t\tcwd\n\t\tfirstSeenAt\n\t\tlastSeenAt\n\t\tmessageCount\n\t\topen\n\t\trecap\n\t}\n\t# Live Claude REPLs — enrichment overlay. Match by sessionUuid; when\n\t# present, lift the row's state/process/pane/worktree from here.\n\tclaudeInstances {\n\t\t...SessionCard\n\t}\n}\n"
): RecentLensStore;

export function graphql(
    str: "fragment SessionCard on ClaudeInstance {\n\tid\n\tsessionUuid\n\tstate\n\tstartedAt\n\tlastActivityAt\n\trcEnabled\n\taccount {\n\t\temail\n\t}\n\tpane {\n\t\tpaneId\n\t\ttitle\n\t\tcurrentCommand\n\t\twindow {\n\t\t\tid\n\t\t\tindex\n\t\t\tname\n\t\t\tactive\n\t\t\tsession {\n\t\t\t\tid\n\t\t\t\tname\n\t\t\t\tattached\n\t\t\t\tactiveAttached\n\t\t\t}\n\t\t}\n\t}\n\tprocess {\n\t\tpid\n\t\tcwd\n\t}\n\tworktree {\n\t\t...WorktreeEnrichment\n\t}\n\tconversation {\n\t\tsessionUuid\n\t\tlastSeenAt\n\t\tagentName\n\t\tcustomTitle\n\t}\n}\n"
): SessionCardStore;

export function graphql(
    str: "fragment WorktreeEnrichment on Worktree {\n\tid\n\tpath\n\tbranch\n\thost\n\trepo\n\t# NO `pr {…}` here — fetching it triggers PullRequestsForRepo (one\n\t# REST list call per repo, ~12s on a fleet with many open PRs in\n\t# langwatch/langwatch). Lens queries need to be FAST. PR fields are\n\t# fetched separately by the panel via WorktreePR when a row is\n\t# actively viewed.\n\tissue {\n\t\tnumber\n\t\tstate\n\t\ttitle\n\t}\n}\n"
): WorktreeEnrichmentStore;

export function graphql(
    str: "query TmuxLens {\n\ttmuxServer {\n\t\tid\n\t\talive\n\t\tsessions {\n\t\t\tid\n\t\t\tname\n\t\t\tattached\n\t\t\tactiveAttached\n\t\t\tlastActivityAt\n\t\t\twindows {\n\t\t\t\tid\n\t\t\t\tindex\n\t\t\t\tname\n\t\t\t\tactive\n\t\t\t\tpanes {\n\t\t\t\t\t...PaneCard\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t\tclients {\n\t\t\ttty\n\t\t\tcurrentPane {\n\t\t\t\tpaneId\n\t\t\t}\n\t\t}\n\t}\n}\n"
): TmuxLensStore;

export function graphql(
    str: "query WorktreeLens {\n\tworkView {\n\t\trepos {\n\t\t\tid\n\t\t\tslug\n\t\t\tworktrees {\n\t\t\t\t...WorktreeEnrichment\n\t\t\t\ttmuxPanes {\n\t\t\t\t\t...PaneCard\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t}\n}\n"
): WorktreeLensStore;

export function graphql(
    str: "query WorktreesList {\n\tworkView {\n\t\trepos {\n\t\t\tid\n\t\t\tslug\n\t\t\tworktrees {\n\t\t\t\tid\n\t\t\t\tpath\n\t\t\t\tbranch\n\t\t\t\tbare\n\t\t\t\thost\n\t\t\t\trepo\n\t\t\t}\n\t\t}\n\t}\n}\n"
): WorktreesListStore;

export declare function graphql<_Payload, _Result = _Payload>(str: TemplateStringsArray): _Result;
export declare const cache: Cache<CacheTypeDef>;
export declare function getCache(): InternalCache;