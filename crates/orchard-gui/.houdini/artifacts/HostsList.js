export default {
    "name": "HostsList",
    "kind": "HoudiniQuery",
    "hash": "f9559e766c49e2a7d510c11717652222e24a47e5f8259f651e4e791f205535ab",

    "raw": `query HostsList {
  hosts {
    id
    hostname
    os
    kernel
    reachable
    lastSeenAt
    resourceLoad {
      cpuPercent
      memPercent
      diskPercent
      loadAvg1m
      loadAvg5m
      loadAvg15m
    }
  }
  claudeAccounts {
    id
    email
    quotaUsed
    quotaCap
    quotaResetsAt
    quotaEstimated
  }
}`,

    "rootType": "Query",
    "stripVariables": [],

    "selection": {
        "fields": {
            "hosts": {
                "type": "Host",
                "keyRaw": "hosts",

                "selection": {
                    "fields": {
                        "id": {
                            "type": "ID",
                            "keyRaw": "id",
                            "visible": true
                        },

                        "hostname": {
                            "type": "String",
                            "keyRaw": "hostname",
                            "visible": true
                        },

                        "os": {
                            "type": "String",
                            "keyRaw": "os",
                            "visible": true
                        },

                        "kernel": {
                            "type": "String",
                            "keyRaw": "kernel",
                            "nullable": true,
                            "visible": true
                        },

                        "reachable": {
                            "type": "Boolean",
                            "keyRaw": "reachable",
                            "visible": true
                        },

                        "lastSeenAt": {
                            "type": "String",
                            "keyRaw": "lastSeenAt",
                            "visible": true
                        },

                        "resourceLoad": {
                            "type": "ResourceLoad",
                            "keyRaw": "resourceLoad",
                            "nullable": true,

                            "selection": {
                                "fields": {
                                    "cpuPercent": {
                                        "type": "Float",
                                        "keyRaw": "cpuPercent",
                                        "visible": true
                                    },

                                    "memPercent": {
                                        "type": "Float",
                                        "keyRaw": "memPercent",
                                        "visible": true
                                    },

                                    "diskPercent": {
                                        "type": "Float",
                                        "keyRaw": "diskPercent",
                                        "visible": true
                                    },

                                    "loadAvg1m": {
                                        "type": "Float",
                                        "keyRaw": "loadAvg1m",
                                        "visible": true
                                    },

                                    "loadAvg5m": {
                                        "type": "Float",
                                        "keyRaw": "loadAvg5m",
                                        "visible": true
                                    },

                                    "loadAvg15m": {
                                        "type": "Float",
                                        "keyRaw": "loadAvg15m",
                                        "visible": true
                                    }
                                }
                            },

                            "visible": true
                        }
                    }
                },

                "visible": true
            },

            "claudeAccounts": {
                "type": "ClaudeAccount",
                "keyRaw": "claudeAccounts",

                "selection": {
                    "fields": {
                        "id": {
                            "type": "ID",
                            "keyRaw": "id",
                            "visible": true
                        },

                        "email": {
                            "type": "String",
                            "keyRaw": "email",
                            "visible": true
                        },

                        "quotaUsed": {
                            "type": "Float",
                            "keyRaw": "quotaUsed",
                            "nullable": true,
                            "visible": true
                        },

                        "quotaCap": {
                            "type": "Float",
                            "keyRaw": "quotaCap",
                            "nullable": true,
                            "visible": true
                        },

                        "quotaResetsAt": {
                            "type": "Time",
                            "keyRaw": "quotaResetsAt",
                            "nullable": true,
                            "visible": true
                        },

                        "quotaEstimated": {
                            "type": "Boolean",
                            "keyRaw": "quotaEstimated",
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

"HoudiniHash=a3a5057340d360379696637352e821c66c44c9234c27cb39205557d7fa04bb3f";