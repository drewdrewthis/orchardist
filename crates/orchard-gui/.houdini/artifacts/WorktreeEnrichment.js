export default {
    "name": "WorktreeEnrichment",
    "kind": "HoudiniFragment",
    "hash": "bf17ac5c42fb96972a6ae6b795017b1032f767da100889a6f89a9fb8911f05ad",

    "raw": `fragment WorktreeEnrichment on Worktree {
  id
  path
  branch
  host
  repo
  issue {
    number
    state
    title
    id
  }
  __typename
}`,

    "rootType": "Worktree",
    "stripVariables": [],

    "selection": {
        "fields": {
            "id": {
                "type": "ID",
                "keyRaw": "id",
                "visible": true
            },

            "path": {
                "type": "String",
                "keyRaw": "path",
                "visible": true
            },

            "branch": {
                "type": "String",
                "keyRaw": "branch",
                "visible": true
            },

            "host": {
                "type": "String",
                "keyRaw": "host",
                "visible": true
            },

            "repo": {
                "type": "String",
                "keyRaw": "repo",
                "nullable": true,
                "visible": true
            },

            "issue": {
                "type": "Issue",
                "keyRaw": "issue",
                "nullable": true,

                "selection": {
                    "fields": {
                        "number": {
                            "type": "Int",
                            "keyRaw": "number",
                            "visible": true
                        },

                        "state": {
                            "type": "IssueState",
                            "keyRaw": "state",
                            "visible": true
                        },

                        "title": {
                            "type": "String",
                            "keyRaw": "title",
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

"HoudiniHash=88e33518bcd14fe6c42b6634e332dd161b1be28d35b521dfa30e0da5e4084ea3";