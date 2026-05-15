export default {
    "name": "WorktreePR",
    "kind": "HoudiniFragment",
    "hash": "cf283a2ca007687118cce9aa35d16f195aa7fe78d6beea8564bddf373ed543d0",

    "raw": `fragment WorktreePR on Worktree {
  pr {
    number
    state
    statusCheckRollup
    reviewDecision
    mergeable
    mergeStateStatus
    id
  }
  id
  __typename
}`,

    "rootType": "Worktree",
    "stripVariables": [],

    "selection": {
        "fields": {
            "pr": {
                "type": "PullRequest",
                "keyRaw": "pr",
                "nullable": true,

                "selection": {
                    "fields": {
                        "number": {
                            "type": "Int",
                            "keyRaw": "number",
                            "visible": true
                        },

                        "state": {
                            "type": "PullRequestState",
                            "keyRaw": "state",
                            "visible": true
                        },

                        "statusCheckRollup": {
                            "type": "CiStatus",
                            "keyRaw": "statusCheckRollup",
                            "visible": true
                        },

                        "reviewDecision": {
                            "type": "ReviewDecisionEnum",
                            "keyRaw": "reviewDecision",
                            "nullable": true,
                            "visible": true
                        },

                        "mergeable": {
                            "type": "MergeableState",
                            "keyRaw": "mergeable",
                            "visible": true
                        },

                        "mergeStateStatus": {
                            "type": "String",
                            "keyRaw": "mergeStateStatus",
                            "visible": true
                        },

                        "id": {
                            "type": "ID",
                            "keyRaw": "id",
                            "visible": true
                        }
                    }
                },

                "visible": true
            },

            "id": {
                "type": "ID",
                "keyRaw": "id",
                "visible": true
            },

            "__typename": {
                "type": "String",
                "keyRaw": "__typename",
                "visible": true
            }
        }
    },

    "pluginData": {
        "houdini-svelte": {}
    }
};

"HoudiniHash=7d2864cdb93bed32d6e8202d9c152950f21d5958b16efe9edc95bf43adbfec94";