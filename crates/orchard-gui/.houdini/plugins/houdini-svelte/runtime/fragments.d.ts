import { PaneCard$input, PaneCard$data } from "../../../artifacts/PaneCard";
import { PaneCardStore } from "../stores/PaneCard";
import { WorktreePR$input, WorktreePR$data } from "../../../artifacts/WorktreePR";
import { WorktreePRStore } from "../stores/WorktreePR";
import { SessionCard$input, SessionCard$data } from "../../../artifacts/SessionCard";
import { SessionCardStore } from "../stores/SessionCard";
import { WorktreeEnrichment$input, WorktreeEnrichment$data } from "../../../artifacts/WorktreeEnrichment";
import { WorktreeEnrichmentStore } from "../stores/WorktreeEnrichment";
import type { FragmentStoreInstance } from "./types";
import type { Fragment, FragmentArtifact } from "$houdini/runtime/lib/types";
import type { Readable } from "svelte/store";
import type { FragmentStore } from "./stores";
import type { FragmentStorePaginated } from "./stores/fragment";

export function fragment(
    initialValue: {
        " $fragments": {
            WorktreeEnrichment: any;
        };
    } | {
        "__typename": "non-exhaustive; don't match this";
    },
    document: WorktreeEnrichmentStore
): FragmentStoreInstance<WorktreeEnrichment$data, WorktreeEnrichment$input>;

export function fragment(
    initialValue: {
        " $fragments": {
            WorktreeEnrichment: any;
        };
    } | null | undefined | {
        "__typename": "non-exhaustive; don't match this";
    },
    document: WorktreeEnrichmentStore
): FragmentStoreInstance<WorktreeEnrichment$data | null, WorktreeEnrichment$input>;

export function fragment(
    initialValue: {
        " $fragments": {
            SessionCard: any;
        };
    } | {
        "__typename": "non-exhaustive; don't match this";
    },
    document: SessionCardStore
): FragmentStoreInstance<SessionCard$data, SessionCard$input>;

export function fragment(
    initialValue: {
        " $fragments": {
            SessionCard: any;
        };
    } | null | undefined | {
        "__typename": "non-exhaustive; don't match this";
    },
    document: SessionCardStore
): FragmentStoreInstance<SessionCard$data | null, SessionCard$input>;

export function fragment(
    initialValue: {
        " $fragments": {
            WorktreePR: any;
        };
    } | {
        "__typename": "non-exhaustive; don't match this";
    },
    document: WorktreePRStore
): FragmentStoreInstance<WorktreePR$data, WorktreePR$input>;

export function fragment(
    initialValue: {
        " $fragments": {
            WorktreePR: any;
        };
    } | null | undefined | {
        "__typename": "non-exhaustive; don't match this";
    },
    document: WorktreePRStore
): FragmentStoreInstance<WorktreePR$data | null, WorktreePR$input>;

export function fragment(
    initialValue: {
        " $fragments": {
            PaneCard: any;
        };
    } | {
        "__typename": "non-exhaustive; don't match this";
    },
    document: PaneCardStore
): FragmentStoreInstance<PaneCard$data, PaneCard$input>;

export function fragment(
    initialValue: {
        " $fragments": {
            PaneCard: any;
        };
    } | null | undefined | {
        "__typename": "non-exhaustive; don't match this";
    },
    document: PaneCardStore
): FragmentStoreInstance<PaneCard$data | null, PaneCard$input>;

export declare function fragment<_Fragment extends Fragment<any>>(ref: _Fragment, fragment: FragmentStore<_Fragment["shape"], {}>): Readable<Exclude<_Fragment["shape"], undefined>> & {
    data: Readable<_Fragment>;
    artifact: FragmentArtifact;
};

export declare function fragment<_Fragment extends Fragment<any>>(
    ref: _Fragment | null | undefined,
    fragment: FragmentStore<_Fragment["shape"], {}>
): Readable<Exclude<_Fragment["shape"], undefined> | null> & {
    data: Readable<_Fragment | null>;
    artifact: FragmentArtifact;
};

export declare function paginatedFragment<_Fragment extends Fragment<any>>(
    initialValue: _Fragment | null | undefined,
    document: FragmentStore<_Fragment["shape"], {}>
): FragmentStorePaginated<_Fragment["shape"], {}>;

export declare function paginatedFragment<_Fragment extends Fragment<any>>(initialValue: _Fragment, document: FragmentStore<_Fragment["shape"], {}>): FragmentStorePaginated<_Fragment["shape"], {}>;