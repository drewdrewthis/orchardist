import type { Record } from "./public/record";
import { IssueLens$result, IssueLens$input } from "$houdini/artifacts/IssueLens";
import { IssueLensStore } from "../plugins/houdini-svelte/stores/IssueLens";
import { AttentionLens$result, AttentionLens$input } from "$houdini/artifacts/AttentionLens";
import { AttentionLensStore } from "../plugins/houdini-svelte/stores/AttentionLens";
import { HostsList$result, HostsList$input } from "$houdini/artifacts/HostsList";
import { HostsListStore } from "../plugins/houdini-svelte/stores/HostsList";
import { OpenPanel$result, OpenPanel$input } from "$houdini/artifacts/OpenPanel";
import { OpenPanelStore } from "../plugins/houdini-svelte/stores/OpenPanel";
import { RecentLens$result, RecentLens$input } from "$houdini/artifacts/RecentLens";
import { RecentLensStore } from "../plugins/houdini-svelte/stores/RecentLens";
import { TmuxLens$result, TmuxLens$input } from "$houdini/artifacts/TmuxLens";
import { TmuxLensStore } from "../plugins/houdini-svelte/stores/TmuxLens";
import { WorktreeLens$result, WorktreeLens$input } from "$houdini/artifacts/WorktreeLens";
import { WorktreeLensStore } from "../plugins/houdini-svelte/stores/WorktreeLens";
import { WorktreesList$result, WorktreesList$input } from "$houdini/artifacts/WorktreesList";
import { WorktreesListStore } from "../plugins/houdini-svelte/stores/WorktreesList";
import type { TmuxSessionSort } from "$houdini/graphql/enums";
import type { PullRequestState } from "$houdini/graphql/enums";
import type { IssueState } from "$houdini/graphql/enums";
import type { HostServiceState } from "$houdini/graphql/enums";
import type { ValueOf } from "$houdini/runtime/lib/types";
import type { ContractStatus } from "$houdini/graphql/enums";
import { PaneCard$data } from "$houdini/artifacts/PaneCard";
import { PaneCardStore } from "../plugins/houdini-svelte/stores/PaneCard";
import { WorktreePR$data } from "$houdini/artifacts/WorktreePR";
import { WorktreePRStore } from "../plugins/houdini-svelte/stores/WorktreePR";
import { SessionCard$data } from "$houdini/artifacts/SessionCard";
import { SessionCardStore } from "../plugins/houdini-svelte/stores/SessionCard";
import { WorktreeEnrichment$data } from "$houdini/artifacts/WorktreeEnrichment";
import { WorktreeEnrichmentStore } from "../plugins/houdini-svelte/stores/WorktreeEnrichment";

type ProcessFilter = {
    commandIn?: (string)[] | null | undefined;
    cwdPrefix?: string | null | undefined;
    pidIn?: (number)[] | null | undefined;
};

type ContractFilter = {
    ownerAgentName?: string | null | undefined;
    ownerSessionId?: string | null | undefined;
    parentContractId?: string | number | null | undefined;
    statuses?: (ValueOf<typeof ContractStatus>)[] | null | undefined;
};

type HostServiceFilter = {
    host?: string | null | undefined;
    name?: string | null | undefined;
    state?: ValueOf<typeof HostServiceState> | null | undefined;
};

type TmuxPaneFilter = {
    currentCommandIn?: (string)[] | null | undefined;
    dead?: boolean | null | undefined;
    paneIdIn?: (string)[] | null | undefined;
    sessionIn?: (string)[] | null | undefined;
    titleContains?: string | null | undefined;
};

type TmuxSessionFilter = {
    activeAttachedOnly?: boolean | null | undefined;
    attachedOnly?: boolean | null | undefined;
    nameIn?: (string)[] | null | undefined;
};

export declare type CacheTypeDef = {
    types: {
        ClaudeAccount: {
            idFields: {
                id: string;
            };
            fields: {
                email: {
                    type: string;
                    args: never;
                };
                host: {
                    type: Record<CacheTypeDef, "Host">;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                instances: {
                    type: (Record<CacheTypeDef, "ClaudeInstance">)[];
                    args: never;
                };
                quotaCap: {
                    type: number | null;
                    args: never;
                };
                quotaEstimated: {
                    type: boolean;
                    args: never;
                };
                quotaResetsAt: {
                    type: string | null;
                    args: never;
                };
                quotaUsed: {
                    type: number | null;
                    args: never;
                };
            };
            fragments: [];
        };
        ClaudeInstance: {
            idFields: {
                id: string;
            };
            fields: {
                account: {
                    type: Record<CacheTypeDef, "ClaudeAccount"> | null;
                    args: never;
                };
                conversation: {
                    type: Record<CacheTypeDef, "Conversation"> | null;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                inflightToolCount: {
                    type: number;
                    args: never;
                };
                lastActivityAt: {
                    type: string | null;
                    args: never;
                };
                model: {
                    type: string | null;
                    args: never;
                };
                pane: {
                    type: Record<CacheTypeDef, "TmuxPane"> | null;
                    args: never;
                };
                process: {
                    type: Record<CacheTypeDef, "Process"> | null;
                    args: never;
                };
                rcEnabled: {
                    type: boolean;
                    args: never;
                };
                rcUrl: {
                    type: string | null;
                    args: never;
                };
                sessionUuid: {
                    type: string | null;
                    args: never;
                };
                startedAt: {
                    type: string | null;
                    args: never;
                };
                state: {
                    type: InstanceState;
                    args: never;
                };
                worktree: {
                    type: Record<CacheTypeDef, "Worktree"> | null;
                    args: never;
                };
            };
            fragments: [[SessionCardStore, SessionCard$data, never]];
        };
        Contract: {
            idFields: {
                id: string;
            };
            fields: {
                contractId: {
                    type: string;
                    args: never;
                };
                createdAt: {
                    type: string;
                    args: never;
                };
                criteria: {
                    type: (string)[];
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                lastEventAt: {
                    type: string;
                    args: never;
                };
                openQuestions: {
                    type: (Record<CacheTypeDef, "ContractQuestion">)[];
                    args: never;
                };
                ownerAgentName: {
                    type: string;
                    args: never;
                };
                ownerSessionId: {
                    type: string;
                    args: never;
                };
                parentContractId: {
                    type: string | null;
                    args: never;
                };
                reportsTo: {
                    type: string | null;
                    args: never;
                };
                statement: {
                    type: string;
                    args: never;
                };
                status: {
                    type: ContractStatus;
                    args: never;
                };
                updatedAt: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        ContractQuestion: {
            idFields: never;
            fields: {
                askedAt: {
                    type: string;
                    args: never;
                };
                askedBy: {
                    type: string;
                    args: never;
                };
                blocksClose: {
                    type: boolean;
                    args: never;
                };
                deadline: {
                    type: string | null;
                    args: never;
                };
                questionId: {
                    type: string;
                    args: never;
                };
                text: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        Conversation: {
            idFields: {
                id: string;
            };
            fields: {
                agentName: {
                    type: string | null;
                    args: never;
                };
                customTitle: {
                    type: string | null;
                    args: never;
                };
                cwd: {
                    type: string | null;
                    args: never;
                };
                firstSeenAt: {
                    type: string | null;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                jsonlPath: {
                    type: string;
                    args: never;
                };
                lastSeenAt: {
                    type: string | null;
                    args: never;
                };
                messageCount: {
                    type: number;
                    args: never;
                };
                open: {
                    type: boolean;
                    args: never;
                };
                recap: {
                    type: string | null;
                    args: never;
                };
                sessionUuid: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        DaemonState: {
            idFields: never;
            fields: {
                providers: {
                    type: (Record<CacheTypeDef, "ProviderHealth">)[];
                    args: never;
                };
                startedAt: {
                    type: string;
                    args: never;
                };
                uptimeS: {
                    type: number;
                    args: never;
                };
            };
            fragments: [];
        };
        Health: {
            idFields: never;
            fields: {
                status: {
                    type: string;
                    args: never;
                };
                uptimeS: {
                    type: number;
                    args: never;
                };
            };
            fragments: [];
        };
        Host: {
            idFields: {
                id: string;
            };
            fields: {
                address: {
                    type: string | null;
                    args: never;
                };
                hostServices: {
                    type: (Record<CacheTypeDef, "HostService">)[];
                    args: never;
                };
                hostname: {
                    type: string;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                kernel: {
                    type: string | null;
                    args: never;
                };
                lastSeenAt: {
                    type: string;
                    args: never;
                };
                machineId: {
                    type: string;
                    args: never;
                };
                os: {
                    type: string;
                    args: never;
                };
                peers: {
                    type: (Record<CacheTypeDef, "Host">)[];
                    args: never;
                };
                processes: {
                    type: (Record<CacheTypeDef, "Process">)[];
                    args: {
                        filter?: ProcessFilter | null | undefined;
                    };
                };
                purpose: {
                    type: string | null;
                    args: never;
                };
                reachable: {
                    type: boolean;
                    args: never;
                };
                resourceLoad: {
                    type: Record<CacheTypeDef, "ResourceLoad"> | null;
                    args: never;
                };
                version: {
                    type: string | null;
                    args: never;
                };
            };
            fragments: [];
        };
        HostService: {
            idFields: {
                id: string;
            };
            fields: {
                exitCode: {
                    type: number | null;
                    args: never;
                };
                host: {
                    type: Record<CacheTypeDef, "Host">;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                logTail: {
                    type: string | null;
                    args: never;
                };
                name: {
                    type: string;
                    args: never;
                };
                since: {
                    type: string | null;
                    args: never;
                };
                state: {
                    type: HostServiceState;
                    args: never;
                };
            };
            fragments: [];
        };
        Issue: {
            idFields: {
                id: string;
            };
            fields: {
                authorLogin: {
                    type: string;
                    args: never;
                };
                blockedByIssues: {
                    type: (Record<CacheTypeDef, "Issue">)[];
                    args: never;
                };
                blockingIssues: {
                    type: (Record<CacheTypeDef, "Issue">)[];
                    args: never;
                };
                body: {
                    type: string;
                    args: never;
                };
                comments: {
                    type: (Record<CacheTypeDef, "IssueComment">)[] | null;
                    args: never;
                };
                createdAt: {
                    type: string;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                labels: {
                    type: (Record<CacheTypeDef, "Label">)[];
                    args: never;
                };
                number: {
                    type: number;
                    args: never;
                };
                parentIssue: {
                    type: Record<CacheTypeDef, "Issue"> | null;
                    args: never;
                };
                repoName: {
                    type: string;
                    args: never;
                };
                repoOwner: {
                    type: string;
                    args: never;
                };
                state: {
                    type: IssueState;
                    args: never;
                };
                subIssues: {
                    type: (Record<CacheTypeDef, "Issue">)[];
                    args: never;
                };
                title: {
                    type: string;
                    args: never;
                };
                updatedAt: {
                    type: string;
                    args: never;
                };
                url: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        IssueComment: {
            idFields: {
                id: string;
            };
            fields: {
                authorLogin: {
                    type: string;
                    args: never;
                };
                body: {
                    type: string;
                    args: never;
                };
                createdAt: {
                    type: string;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                updatedAt: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        Label: {
            idFields: never;
            fields: {
                color: {
                    type: string;
                    args: never;
                };
                description: {
                    type: string;
                    args: never;
                };
                name: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        Meta: {
            idFields: never;
            fields: {
                failureReason: {
                    type: string | null;
                    args: never;
                };
                lastSuccessfulFetchAt: {
                    type: string | null;
                    args: never;
                };
                provider: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        Process: {
            idFields: {
                id: string;
            };
            fields: {
                args: {
                    type: (string)[] | null;
                    args: never;
                };
                claudeInstance: {
                    type: Record<CacheTypeDef, "ClaudeInstance"> | null;
                    args: never;
                };
                command: {
                    type: string;
                    args: never;
                };
                cpuPercent: {
                    type: number;
                    args: never;
                };
                cwd: {
                    type: string | null;
                    args: never;
                };
                host: {
                    type: Record<CacheTypeDef, "Host">;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                memBytes: {
                    type: number;
                    args: never;
                };
                pid: {
                    type: number;
                    args: never;
                };
                ppid: {
                    type: number;
                    args: never;
                };
                startedAt: {
                    type: string;
                    args: never;
                };
                tty: {
                    type: string | null;
                    args: never;
                };
                worktree: {
                    type: Record<CacheTypeDef, "Worktree"> | null;
                    args: never;
                };
            };
            fragments: [];
        };
        ProviderHealth: {
            idFields: never;
            fields: {
                configured: {
                    type: boolean;
                    args: never;
                };
                failureCount: {
                    type: number;
                    args: never;
                };
                lastFailureReason: {
                    type: string | null;
                    args: never;
                };
                lastSuccessfulRefresh: {
                    type: string | null;
                    args: never;
                };
                name: {
                    type: string;
                    args: never;
                };
                refreshCount: {
                    type: number;
                    args: never;
                };
            };
            fragments: [];
        };
        PullRequest: {
            idFields: {
                id: string;
            };
            fields: {
                authorLogin: {
                    type: string;
                    args: never;
                };
                baseRef: {
                    type: string;
                    args: never;
                };
                body: {
                    type: string;
                    args: never;
                };
                comments: {
                    type: (Record<CacheTypeDef, "IssueComment">)[] | null;
                    args: never;
                };
                createdAt: {
                    type: string;
                    args: never;
                };
                draft: {
                    type: boolean;
                    args: never;
                };
                headRef: {
                    type: string;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                labels: {
                    type: (Record<CacheTypeDef, "Label">)[];
                    args: never;
                };
                mergeStateStatus: {
                    type: string;
                    args: never;
                };
                mergeable: {
                    type: MergeableState;
                    args: never;
                };
                number: {
                    type: number;
                    args: never;
                };
                repoName: {
                    type: string;
                    args: never;
                };
                repoOwner: {
                    type: string;
                    args: never;
                };
                reviewDecision: {
                    type: ReviewDecisionEnum | null;
                    args: never;
                };
                reviews: {
                    type: (Record<CacheTypeDef, "PullRequestReview">)[] | null;
                    args: never;
                };
                state: {
                    type: PullRequestState;
                    args: never;
                };
                statusCheckRollup: {
                    type: CiStatus;
                    args: never;
                };
                title: {
                    type: string;
                    args: never;
                };
                updatedAt: {
                    type: string;
                    args: never;
                };
                url: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        PullRequestReview: {
            idFields: {
                id: string;
            };
            fields: {
                authorLogin: {
                    type: string;
                    args: never;
                };
                body: {
                    type: string;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                state: {
                    type: string;
                    args: never;
                };
                submittedAt: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        __ROOT__: {
            idFields: {};
            fields: {
                claudeAccounts: {
                    type: (Record<CacheTypeDef, "ClaudeAccount">)[];
                    args: never;
                };
                claudeInstances: {
                    type: (Record<CacheTypeDef, "ClaudeInstance">)[];
                    args: never;
                };
                contract: {
                    type: Record<CacheTypeDef, "Contract"> | null;
                    args: {
                        id: string | number;
                    };
                };
                contracts: {
                    type: (Record<CacheTypeDef, "Contract">)[];
                    args: {
                        filter?: ContractFilter | null | undefined;
                    };
                };
                conversation: {
                    type: Record<CacheTypeDef, "Conversation"> | null;
                    args: {
                        id: string | number;
                    };
                };
                conversations: {
                    type: (Record<CacheTypeDef, "Conversation">)[];
                    args: never;
                };
                daemonState: {
                    type: Record<CacheTypeDef, "DaemonState">;
                    args: never;
                };
                gh: {
                    type: any | null;
                    args: {
                        query: string;
                        variables?: any | null | undefined;
                    };
                };
                health: {
                    type: Record<CacheTypeDef, "Health">;
                    args: never;
                };
                host: {
                    type: Record<CacheTypeDef, "Host">;
                    args: never;
                };
                hostServices: {
                    type: (Record<CacheTypeDef, "HostService">)[];
                    args: {
                        filter?: HostServiceFilter | null | undefined;
                    };
                };
                hosts: {
                    type: (Record<CacheTypeDef, "Host">)[];
                    args: never;
                };
                issue: {
                    type: Record<CacheTypeDef, "Issue"> | null;
                    args: {
                        number: number;
                        repo: string;
                    };
                };
                issues: {
                    type: (Record<CacheTypeDef, "Issue">)[] | null;
                    args: {
                        repo: string;
                        state?: ValueOf<typeof IssueState> | null | undefined;
                    };
                };
                node: {
                    type: Record<CacheTypeDef, "ClaudeAccount"> | Record<CacheTypeDef, "ClaudeInstance"> | Record<CacheTypeDef, "Contract"> | Record<CacheTypeDef, "Conversation"> | Record<CacheTypeDef, "Host"> | Record<CacheTypeDef, "HostService"> | Record<CacheTypeDef, "Issue"> | Record<CacheTypeDef, "Process"> | Record<CacheTypeDef, "PullRequest"> | Record<CacheTypeDef, "Repo"> | Record<CacheTypeDef, "TmuxClient"> | Record<CacheTypeDef, "TmuxPane"> | Record<CacheTypeDef, "TmuxServer"> | Record<CacheTypeDef, "TmuxSession"> | Record<CacheTypeDef, "TmuxWindow"> | Record<CacheTypeDef, "WorkflowRun"> | Record<CacheTypeDef, "Worktree"> | null;
                    args: {
                        id: string | number;
                    };
                };
                openPullRequests: {
                    type: (Record<CacheTypeDef, "PullRequest">)[];
                    args: {
                        author?: string | null | undefined;
                        repo: string;
                    };
                };
                peers: {
                    type: (Record<CacheTypeDef, "Host">)[];
                    args: never;
                };
                pullRequest: {
                    type: Record<CacheTypeDef, "PullRequest"> | null;
                    args: {
                        number: number;
                        repo: string;
                    };
                };
                pullRequests: {
                    type: (Record<CacheTypeDef, "PullRequest">)[] | null;
                    args: {
                        repo: string;
                        state?: ValueOf<typeof PullRequestState> | null | undefined;
                    };
                };
                repos: {
                    type: (Record<CacheTypeDef, "Repo">)[];
                    args: never;
                };
                schemaSDL: {
                    type: string;
                    args: never;
                };
                tmuxPanes: {
                    type: (Record<CacheTypeDef, "TmuxPane">)[];
                    args: {
                        filter?: TmuxPaneFilter | null | undefined;
                    };
                };
                tmuxServer: {
                    type: Record<CacheTypeDef, "TmuxServer"> | null;
                    args: never;
                };
                tmuxSessions: {
                    type: (Record<CacheTypeDef, "TmuxSession">)[];
                    args: {
                        filter?: TmuxSessionFilter | null | undefined;
                    };
                };
                version: {
                    type: string;
                    args: never;
                };
                workView: {
                    type: Record<CacheTypeDef, "WorkView">;
                    args: never;
                };
                workflowRuns: {
                    type: (Record<CacheTypeDef, "WorkflowRun">)[] | null;
                    args: {
                        repo: string;
                    };
                };
            };
            fragments: [];
        };
        Repo: {
            idFields: {
                id: string;
            };
            fields: {
                id: {
                    type: string;
                    args: never;
                };
                path: {
                    type: string;
                    args: never;
                };
                slug: {
                    type: string;
                    args: never;
                };
                worktrees: {
                    type: (Record<CacheTypeDef, "Worktree">)[];
                    args: never;
                };
            };
            fragments: [];
        };
        ResourceLoad: {
            idFields: never;
            fields: {
                cpuPercent: {
                    type: number;
                    args: never;
                };
                diskPercent: {
                    type: number;
                    args: never;
                };
                loadAvg1m: {
                    type: number;
                    args: never;
                };
                loadAvg5m: {
                    type: number;
                    args: never;
                };
                loadAvg15m: {
                    type: number;
                    args: never;
                };
                memPercent: {
                    type: number;
                    args: never;
                };
            };
            fragments: [];
        };
        TmuxClient: {
            idFields: {
                id: string;
            };
            fields: {
                attachedAt: {
                    type: string;
                    args: never;
                };
                currentPane: {
                    type: Record<CacheTypeDef, "TmuxPane"> | null;
                    args: never;
                };
                currentWindow: {
                    type: Record<CacheTypeDef, "TmuxWindow"> | null;
                    args: never;
                };
                hostname: {
                    type: string;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                lastActivityAt: {
                    type: string | null;
                    args: never;
                };
                readonly: {
                    type: boolean;
                    args: never;
                };
                server: {
                    type: Record<CacheTypeDef, "TmuxServer">;
                    args: never;
                };
                session: {
                    type: Record<CacheTypeDef, "TmuxSession">;
                    args: never;
                };
                termName: {
                    type: string;
                    args: never;
                };
                tty: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        TmuxPane: {
            idFields: {
                id: string;
            };
            fields: {
                claudeInstance: {
                    type: Record<CacheTypeDef, "ClaudeInstance"> | null;
                    args: never;
                };
                content: {
                    type: string;
                    args: {
                        lines?: number | null | undefined;
                        stripAnsi?: boolean | null | undefined;
                    };
                };
                contentFull: {
                    type: string;
                    args: {
                        stripAnsi?: boolean | null | undefined;
                    };
                };
                contentRange: {
                    type: string;
                    args: {
                        endLine: number;
                        startLine: number;
                        stripAnsi?: boolean | null | undefined;
                    };
                };
                currentCommand: {
                    type: string;
                    args: never;
                };
                currentPid: {
                    type: number | null;
                    args: never;
                };
                dead: {
                    type: boolean;
                    args: never;
                };
                height: {
                    type: number;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                paneId: {
                    type: string;
                    args: never;
                };
                process: {
                    type: Record<CacheTypeDef, "Process"> | null;
                    args: never;
                };
                title: {
                    type: string;
                    args: never;
                };
                watchingClients: {
                    type: (Record<CacheTypeDef, "TmuxClient">)[];
                    args: never;
                };
                width: {
                    type: number;
                    args: never;
                };
                window: {
                    type: Record<CacheTypeDef, "TmuxWindow">;
                    args: never;
                };
            };
            fragments: [[PaneCardStore, PaneCard$data, never]];
        };
        TmuxServer: {
            idFields: {
                id: string;
            };
            fields: {
                alive: {
                    type: boolean;
                    args: never;
                };
                clients: {
                    type: (Record<CacheTypeDef, "TmuxClient">)[];
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                pid: {
                    type: number | null;
                    args: never;
                };
                sessions: {
                    type: (Record<CacheTypeDef, "TmuxSession">)[];
                    args: {
                        sort?: ValueOf<typeof TmuxSessionSort> | null | undefined;
                    };
                };
                socketPath: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        TmuxSession: {
            idFields: {
                id: string;
            };
            fields: {
                activeAttached: {
                    type: boolean;
                    args: never;
                };
                attached: {
                    type: boolean;
                    args: never;
                };
                attachedClients: {
                    type: (Record<CacheTypeDef, "TmuxClient">)[];
                    args: never;
                };
                createdAt: {
                    type: string;
                    args: never;
                };
                currentWindow: {
                    type: Record<CacheTypeDef, "TmuxWindow"> | null;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                lastActivityAt: {
                    type: string | null;
                    args: never;
                };
                name: {
                    type: string;
                    args: never;
                };
                server: {
                    type: Record<CacheTypeDef, "TmuxServer">;
                    args: never;
                };
                windows: {
                    type: (Record<CacheTypeDef, "TmuxWindow">)[];
                    args: never;
                };
            };
            fragments: [];
        };
        TmuxWindow: {
            idFields: {
                id: string;
            };
            fields: {
                active: {
                    type: boolean;
                    args: never;
                };
                currentPane: {
                    type: Record<CacheTypeDef, "TmuxPane"> | null;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                index: {
                    type: number;
                    args: never;
                };
                name: {
                    type: string;
                    args: never;
                };
                panes: {
                    type: (Record<CacheTypeDef, "TmuxPane">)[];
                    args: never;
                };
                session: {
                    type: Record<CacheTypeDef, "TmuxSession">;
                    args: never;
                };
            };
            fragments: [];
        };
        WorkView: {
            idFields: never;
            fields: {
                claudeInstances: {
                    type: (Record<CacheTypeDef, "ClaudeInstance">)[];
                    args: never;
                };
                meta: {
                    type: Record<CacheTypeDef, "Meta">;
                    args: never;
                };
                repos: {
                    type: (Record<CacheTypeDef, "Repo">)[];
                    args: never;
                };
                tmuxSessions: {
                    type: (Record<CacheTypeDef, "TmuxSession">)[];
                    args: never;
                };
            };
            fragments: [];
        };
        WorkflowRun: {
            idFields: {
                id: string;
            };
            fields: {
                conclusion: {
                    type: string;
                    args: never;
                };
                createdAt: {
                    type: string;
                    args: never;
                };
                headBranch: {
                    type: string;
                    args: never;
                };
                headSHA: {
                    type: string;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                name: {
                    type: string;
                    args: never;
                };
                repoName: {
                    type: string;
                    args: never;
                };
                repoOwner: {
                    type: string;
                    args: never;
                };
                runId: {
                    type: number;
                    args: never;
                };
                status: {
                    type: string;
                    args: never;
                };
                updatedAt: {
                    type: string;
                    args: never;
                };
                url: {
                    type: string;
                    args: never;
                };
                workflowPath: {
                    type: string;
                    args: never;
                };
            };
            fragments: [];
        };
        Worktree: {
            idFields: {
                id: string;
            };
            fields: {
                ahead: {
                    type: number | null;
                    args: never;
                };
                bare: {
                    type: boolean;
                    args: never;
                };
                behind: {
                    type: number | null;
                    args: never;
                };
                branch: {
                    type: string;
                    args: never;
                };
                claudeInstances: {
                    type: (Record<CacheTypeDef, "ClaudeInstance">)[];
                    args: never;
                };
                head: {
                    type: string;
                    args: never;
                };
                host: {
                    type: string;
                    args: never;
                };
                id: {
                    type: string;
                    args: never;
                };
                issue: {
                    type: Record<CacheTypeDef, "Issue"> | null;
                    args: never;
                };
                path: {
                    type: string;
                    args: never;
                };
                pr: {
                    type: Record<CacheTypeDef, "PullRequest"> | null;
                    args: never;
                };
                processes: {
                    type: (Record<CacheTypeDef, "Process">)[];
                    args: never;
                };
                repo: {
                    type: string | null;
                    args: never;
                };
                tmuxPanes: {
                    type: (Record<CacheTypeDef, "TmuxPane">)[];
                    args: never;
                };
                tmuxSession: {
                    type: Record<CacheTypeDef, "TmuxSession"> | null;
                    args: never;
                };
            };
            fragments: [[WorktreeEnrichmentStore, WorktreeEnrichment$data, never], [WorktreePRStore, WorktreePR$data, never]];
        };
    };
    lists: {};
    queries: [[WorktreesListStore, WorktreesList$result, WorktreesList$input], [WorktreeLensStore, WorktreeLens$result, WorktreeLens$input], [TmuxLensStore, TmuxLens$result, TmuxLens$input], [RecentLensStore, RecentLens$result, RecentLens$input], [OpenPanelStore, OpenPanel$result, OpenPanel$input], [HostsListStore, HostsList$result, HostsList$input], [AttentionLensStore, AttentionLens$result, AttentionLens$input], [IssueLensStore, IssueLens$result, IssueLens$input]];
};