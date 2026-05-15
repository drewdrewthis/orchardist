export default {
    "name": "PaneCard",
    "kind": "HoudiniFragment",
    "hash": "eb4459c3f96e7c24771dcbf25eee098eed7f08c990b62fdfe3540f50180b4d7b",

    "raw": `fragment PaneCard on TmuxPane {
  paneId
  title
  currentCommand
  currentPid
  window {
    id
    index
    name
    active
    session {
      id
      name
      attached
      activeAttached
      lastActivityAt
    }
  }
  claudeInstance {
    ...SessionCard
    id
  }
  process {
    pid
    cwd
    command
    worktree {
      ...WorktreeEnrichment
      id
    }
    id
  }
  id
  __typename
}

fragment SessionCard on ClaudeInstance {
  id
  sessionUuid
  state
  startedAt
  lastActivityAt
  rcEnabled
  account {
    email
    id
  }
  pane {
    paneId
    title
    currentCommand
    window {
      id
      index
      name
      active
      session {
        id
        name
        attached
        activeAttached
      }
    }
    id
  }
  process {
    pid
    cwd
    id
  }
  worktree {
    ...WorktreeEnrichment
    id
  }
  conversation {
    sessionUuid
    lastSeenAt
    agentName
    customTitle
    id
  }
  __typename
}

fragment WorktreeEnrichment on Worktree {
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

    "rootType": "TmuxPane",
    "stripVariables": [],

    "selection": {
        "fields": {
            "paneId": {
                "type": "String",
                "keyRaw": "paneId",
                "visible": true
            },

            "title": {
                "type": "String",
                "keyRaw": "title",
                "visible": true
            },

            "currentCommand": {
                "type": "String",
                "keyRaw": "currentCommand",
                "visible": true
            },

            "currentPid": {
                "type": "Int",
                "keyRaw": "currentPid",
                "nullable": true,
                "visible": true
            },

            "window": {
                "type": "TmuxWindow",
                "keyRaw": "window",

                "selection": {
                    "fields": {
                        "id": {
                            "type": "ID",
                            "keyRaw": "id",
                            "visible": true
                        },

                        "index": {
                            "type": "Int",
                            "keyRaw": "index",
                            "visible": true
                        },

                        "name": {
                            "type": "String",
                            "keyRaw": "name",
                            "visible": true
                        },

                        "active": {
                            "type": "Boolean",
                            "keyRaw": "active",
                            "visible": true
                        },

                        "session": {
                            "type": "TmuxSession",
                            "keyRaw": "session",

                            "selection": {
                                "fields": {
                                    "id": {
                                        "type": "ID",
                                        "keyRaw": "id",
                                        "visible": true
                                    },

                                    "name": {
                                        "type": "String",
                                        "keyRaw": "name",
                                        "visible": true
                                    },

                                    "attached": {
                                        "type": "Boolean",
                                        "keyRaw": "attached",
                                        "visible": true
                                    },

                                    "activeAttached": {
                                        "type": "Boolean",
                                        "keyRaw": "activeAttached",
                                        "visible": true
                                    },

                                    "lastActivityAt": {
                                        "type": "String",
                                        "keyRaw": "lastActivityAt",
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
            },

            "claudeInstance": {
                "type": "ClaudeInstance",
                "keyRaw": "claudeInstance",
                "nullable": true,

                "selection": {
                    "fields": {
                        "id": {
                            "type": "ID",
                            "keyRaw": "id",
                            "visible": true
                        },

                        "sessionUuid": {
                            "type": "String",
                            "keyRaw": "sessionUuid",
                            "nullable": true,
                            "visible": true
                        },

                        "state": {
                            "type": "InstanceState",
                            "keyRaw": "state",
                            "visible": true
                        },

                        "startedAt": {
                            "type": "String",
                            "keyRaw": "startedAt",
                            "nullable": true,
                            "visible": true
                        },

                        "lastActivityAt": {
                            "type": "String",
                            "keyRaw": "lastActivityAt",
                            "nullable": true,
                            "visible": true
                        },

                        "rcEnabled": {
                            "type": "Boolean",
                            "keyRaw": "rcEnabled",
                            "visible": true
                        },

                        "account": {
                            "type": "ClaudeAccount",
                            "keyRaw": "account",
                            "nullable": true,

                            "selection": {
                                "fields": {
                                    "email": {
                                        "type": "String",
                                        "keyRaw": "email",
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

                        "pane": {
                            "type": "TmuxPane",
                            "keyRaw": "pane",
                            "nullable": true,

                            "selection": {
                                "fields": {
                                    "paneId": {
                                        "type": "String",
                                        "keyRaw": "paneId",
                                        "visible": true
                                    },

                                    "title": {
                                        "type": "String",
                                        "keyRaw": "title",
                                        "visible": true
                                    },

                                    "currentCommand": {
                                        "type": "String",
                                        "keyRaw": "currentCommand",
                                        "visible": true
                                    },

                                    "window": {
                                        "type": "TmuxWindow",
                                        "keyRaw": "window",

                                        "selection": {
                                            "fields": {
                                                "id": {
                                                    "type": "ID",
                                                    "keyRaw": "id",
                                                    "visible": true
                                                },

                                                "index": {
                                                    "type": "Int",
                                                    "keyRaw": "index",
                                                    "visible": true
                                                },

                                                "name": {
                                                    "type": "String",
                                                    "keyRaw": "name",
                                                    "visible": true
                                                },

                                                "active": {
                                                    "type": "Boolean",
                                                    "keyRaw": "active",
                                                    "visible": true
                                                },

                                                "session": {
                                                    "type": "TmuxSession",
                                                    "keyRaw": "session",

                                                    "selection": {
                                                        "fields": {
                                                            "id": {
                                                                "type": "ID",
                                                                "keyRaw": "id",
                                                                "visible": true
                                                            },

                                                            "name": {
                                                                "type": "String",
                                                                "keyRaw": "name",
                                                                "visible": true
                                                            },

                                                            "attached": {
                                                                "type": "Boolean",
                                                                "keyRaw": "attached",
                                                                "visible": true
                                                            },

                                                            "activeAttached": {
                                                                "type": "Boolean",
                                                                "keyRaw": "activeAttached",
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

                                    "id": {
                                        "type": "ID",
                                        "keyRaw": "id",
                                        "visible": true
                                    }
                                }
                            },

                            "visible": true
                        },

                        "process": {
                            "type": "Process",
                            "keyRaw": "process",
                            "nullable": true,

                            "selection": {
                                "fields": {
                                    "pid": {
                                        "type": "Int",
                                        "keyRaw": "pid",
                                        "visible": true
                                    },

                                    "cwd": {
                                        "type": "String",
                                        "keyRaw": "cwd",
                                        "nullable": true,
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

                        "worktree": {
                            "type": "Worktree",
                            "keyRaw": "worktree",
                            "nullable": true,

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
                                },

                                "fragments": {
                                    "WorktreeEnrichment": {
                                        "arguments": {}
                                    }
                                }
                            },

                            "visible": true
                        },

                        "conversation": {
                            "type": "Conversation",
                            "keyRaw": "conversation",
                            "nullable": true,

                            "selection": {
                                "fields": {
                                    "sessionUuid": {
                                        "type": "String",
                                        "keyRaw": "sessionUuid",
                                        "visible": true
                                    },

                                    "lastSeenAt": {
                                        "type": "Time",
                                        "keyRaw": "lastSeenAt",
                                        "nullable": true,
                                        "visible": true
                                    },

                                    "agentName": {
                                        "type": "String",
                                        "keyRaw": "agentName",
                                        "nullable": true,
                                        "visible": true
                                    },

                                    "customTitle": {
                                        "type": "String",
                                        "keyRaw": "customTitle",
                                        "nullable": true,
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
                    },

                    "fragments": {
                        "SessionCard": {
                            "arguments": {}
                        }
                    }
                },

                "visible": true
            },

            "process": {
                "type": "Process",
                "keyRaw": "process",
                "nullable": true,

                "selection": {
                    "fields": {
                        "pid": {
                            "type": "Int",
                            "keyRaw": "pid",
                            "visible": true
                        },

                        "cwd": {
                            "type": "String",
                            "keyRaw": "cwd",
                            "nullable": true,
                            "visible": true
                        },

                        "command": {
                            "type": "String",
                            "keyRaw": "command",
                            "visible": true
                        },

                        "worktree": {
                            "type": "Worktree",
                            "keyRaw": "worktree",
                            "nullable": true,

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
                                },

                                "fragments": {
                                    "WorktreeEnrichment": {
                                        "arguments": {}
                                    }
                                }
                            },

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

"HoudiniHash=01703694f99de0159805ef39466ab15f878fe5abbe97e5014ed110caefb5cc75";