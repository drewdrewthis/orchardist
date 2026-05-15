import type { DataSource } from '$houdini/runtime'

export type Result<DataType> = {
	isFetching: boolean
	partial: boolean
	source?: DataSource | null
	data?: DataType | null
	error?: Error | null
}
export * from './AttentionLens'
export * from './HostsList'
export * from './IssueLens'
export * from './OpenPanel'
export * from './PaneCard'
export * from './RecentLens'
export * from './SessionCard'
export * from './TmuxLens'
export * from './WorktreeEnrichment'
export * from './WorktreeLens'
export * from './WorktreePR'
export * from './WorktreesList'