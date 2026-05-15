export default {
    "name": "WorktreesList",
    "kind": "HoudiniQuery",
    "hash": "962e203c9aaddb741765b8de930643d6b0cd21d0907e9d2e1374b7c2a351dfeb",

    "raw": `query WorktreesList {
  workView {
    repos {
      id
      slug
      worktrees {
        id
        path
        branch
        bare
        host
        repo
      }
    }
  }
}`,

    "rootType": "Query",
    "stripVariables": [],

    "selection": {
        "fields": {
            "workView": {
                "type": "WorkView",
                "keyRaw": "workView",

                "selection": {
                    "fields": {
                        "repos": {
                            "type": "Repo",
                            "keyRaw": "repos",

                            "selection": {
                                "fields": {
                                    "id": {
                                        "type": "ID",
                                        "keyRaw": "id",
                                        "visible": true
                                    },

                                    "slug": {
                                        "type": "String",
                                        "keyRaw": "slug",
                                        "visible": true
                                    },

                                    "worktrees": {
                                        "type": "Worktree",
                                        "keyRaw": "worktrees",

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

                                                "bare": {
                                                    "type": "Boolean",
                                                    "keyRaw": "bare",
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
                                                }
                                            }
                                        },

                                        "visible": true
                                    }
                                }
                            },

                            "visible": true
                        }
                    }
                },

                "visible": true
            }
        }
    },

    "pluginData": {
        "houdini-svelte": {}
    },

    "policy": "CacheAndNetwork",
    "partial": false
};

"HoudiniHash=34108dd47c4a4e66daf4b351ad30897bea762107a66b9c199e3b9666d7f36749";