/**
 * Live Houdini stores for the daemon-shape data the layout chrome
 * (top bar, mobile peer cluster, new-conversation dialog) consumes.
 *
 * Houdini's normalized cache IS the store — these singletons are just
 * the per-component handles. Each component fires `<store>.fetch()` in
 * its `onMount`; subsequent cache patches (driven by graphql-ws
 * subscriptions or another query touching the same nodes) update
 * `$<store>.data` reactively.
 *
 * The GraphQL fields here mirror the daemon schema 1:1 — no zero
 * defaults, no fake CPU/mem/quota numbers. When the daemon hasn't
 * sampled the resource yet, `resourceLoad` is null and the UI renders
 * a "—" instead of a fabricated value.
 */
import { HostsListStore, WorktreesListStore } from "$houdini";
import { parseTime } from "./lenses/client";

export const hostsStore = new HostsListStore();
export const worktreesStore = new WorktreesListStore();

/**
 * Schema-aligned host shape exposed to UI components. Mirrors
 * `Host` in the daemon's GraphQL schema — `resourceLoad` is null
 * whenever the daemon hasn't sampled CPU/mem/disk yet (e.g. cold
 * boot of a peer). Components must check before reading.
 */
export interface HostRow {
	id: string;
	hostname: string;
	os: string;
	kernel: string | null;
	reachable: boolean;
	lastSeenAt: number;
	resourceLoad: {
		cpuPercent: number;
		memPercent: number;
		diskPercent: number;
		loadAvg1m: number;
		loadAvg5m: number;
		loadAvg15m: number;
	} | null;
}

/**
 * Schema-aligned account shape. Quota fields are nullable in the
 * schema (ccusage may not have a sample yet); the bar in the topbar
 * only renders when `quotaCap` is a real number.
 */
export interface AccountRow {
	id: string;
	email: string;
	quotaUsed: number | null;
	quotaCap: number | null;
	quotaResetsAt: number | null;
	quotaEstimated: boolean;
}

export function buildHosts(
	data:
		| {
				hosts: Array<{
					id: string;
					hostname: string;
					os: string;
					kernel: string | null;
					reachable: boolean;
					lastSeenAt: string | null;
					resourceLoad: {
						cpuPercent: number;
						memPercent: number;
						diskPercent: number;
						loadAvg1m: number;
						loadAvg5m: number;
						loadAvg15m: number;
					} | null;
				}>;
		  }
		| null
		| undefined,
): HostRow[] {
	if (!data) return [];
	return data.hosts.map((h) => ({
		id: h.id,
		hostname: h.hostname,
		os: h.os,
		kernel: h.kernel,
		reachable: h.reachable,
		lastSeenAt: parseTime(h.lastSeenAt),
		resourceLoad: h.resourceLoad,
	}));
}

export function buildAccount(
	data:
		| {
				claudeAccounts: Array<{
					id: string;
					email: string;
					quotaUsed: number | null;
					quotaCap: number | null;
					quotaResetsAt: string | null;
					quotaEstimated: boolean;
				}>;
		  }
		| null
		| undefined,
): AccountRow | null {
	if (!data) return null;
	const acc = data.claudeAccounts[0];
	if (!acc) return null;
	return {
		id: acc.id,
		email: acc.email,
		quotaUsed: acc.quotaUsed,
		quotaCap: acc.quotaCap,
		quotaResetsAt: parseTime(acc.quotaResetsAt) || null,
		quotaEstimated: acc.quotaEstimated,
	};
}

/**
 * A flat list of worktrees for pickers — schema-aligned. The legacy
 * `WorktreeItem` shape (with sparkline/unread/contract/session-instance
 * synthesis) has been retired; the picker only needs identity, host,
 * and branch.
 */
export interface WorktreePickerRow {
	id: string;
	path: string;
	branch: string;
	bare: boolean;
	host: string;
	repo: string;
}

export function buildWorktreePickerRows(
	data:
		| {
				workView: {
					repos: Array<{
						slug: string;
						worktrees: Array<{
							id: string;
							path: string;
							branch: string;
							bare: boolean;
							host: string;
							repo: string | null;
						}>;
					}>;
				};
		  }
		| null
		| undefined,
): WorktreePickerRow[] {
	if (!data) return [];
	return data.workView.repos.flatMap((r) =>
		r.worktrees
			.filter((w) => !w.bare)
			.map((w) => ({
				id: w.id,
				path: w.path,
				branch: w.branch,
				bare: w.bare,
				host: w.host,
				repo: w.repo || r.slug,
			})),
	);
}
