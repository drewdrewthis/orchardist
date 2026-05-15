export type HostsList = {
    readonly "input": HostsList$input;
    readonly "result": HostsList$result | undefined;
};

export type HostsList$result = {
    /**
     * All hosts known to this daemon. v1: only the local host. Workstream F adds federated peers.
    */
    readonly hosts: ({
        /**
         * Stable orchard id — "Host:<machineId>".
        */
        readonly id: string;
        /**
         * Hostname as reported by the OS at boot.
        */
        readonly hostname: string;
        /**
         * Operating system family — 'darwin', 'linux', etc.
        */
        readonly os: string;
        /**
         * Kernel version string (e.g. 'Darwin 25.4.0', 'Linux 6.5.0'). Nullable when unavailable.
        */
        readonly kernel: string | null;
        /**
         * True when the daemon last heard from this host. v1: always true for local host.
        */
        readonly reachable: boolean;
        /**
         * RFC 3339 timestamp of the last contact. Empty string when the daemon has never reached this host; pair with `reachable=false` to detect 'never seen live'.
        */
        readonly lastSeenAt: string;
        /**
         * Live CPU, memory, disk, and load averages. Polled every 5s; null briefly at cold boot before the first sample lands.
        */
        readonly resourceLoad: {
            /**
             * Aggregate CPU usage 0..100.
            */
            readonly cpuPercent: number;
            /**
             * Used memory as a percentage of total physical memory, 0..100.
            */
            readonly memPercent: number;
            /**
             * Used disk on the root filesystem as a percentage, 0..100.
            */
            readonly diskPercent: number;
            /**
             * 1-minute load average.
            */
            readonly loadAvg1m: number;
            /**
             * 5-minute load average.
            */
            readonly loadAvg5m: number;
            /**
             * 15-minute load average.
            */
            readonly loadAvg15m: number;
        } | null;
    })[];
    /**
     * All Claude CLI accounts surfaced by the local daemon. v1 returns the single account `claude auth status` reports for; later workstreams may surface additional accounts.
    */
    readonly claudeAccounts: ({
        /**
         * Stable orchard id — "ClaudeAccount:<host>:<email>".
        */
        readonly id: string;
        /**
         * Email address `claude auth status` reports for the active session.
        */
        readonly email: string;
        /**
         * Quota consumed in the current window. Null when ccusage has not been able to report a number.
        */
        readonly quotaUsed: number | null;
        /**
         * Quota cap for the current window. Null when ccusage cannot determine the cap (e.g. fresh install with no traffic).
        */
        readonly quotaCap: number | null;
        /**
         * When the current quota window resets. Null when ccusage cannot determine the next reset.
        */
        readonly quotaResetsAt: string | null;
        /**
         * True when the quota numbers are estimated by `ccusage` rather than reported by a first-party Anthropic API. Always true in v1.
        */
        readonly quotaEstimated: boolean;
    })[];
};

export type HostsList$input = null;

export type HostsList$artifact = {
    "name": "HostsList";
    "kind": "HoudiniQuery";
    "hash": "f9559e766c49e2a7d510c11717652222e24a47e5f8259f651e4e791f205535ab";
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
}`;
    "rootType": "Query";
    "stripVariables": [];
    "selection": {
        "fields": {
            "hosts": {
                "type": "Host";
                "keyRaw": "hosts";
                "selection": {
                    "fields": {
                        "id": {
                            "type": "ID";
                            "keyRaw": "id";
                            "visible": true;
                        };
                        "hostname": {
                            "type": "String";
                            "keyRaw": "hostname";
                            "visible": true;
                        };
                        "os": {
                            "type": "String";
                            "keyRaw": "os";
                            "visible": true;
                        };
                        "kernel": {
                            "type": "String";
                            "keyRaw": "kernel";
                            "nullable": true;
                            "visible": true;
                        };
                        "reachable": {
                            "type": "Boolean";
                            "keyRaw": "reachable";
                            "visible": true;
                        };
                        "lastSeenAt": {
                            "type": "String";
                            "keyRaw": "lastSeenAt";
                            "visible": true;
                        };
                        "resourceLoad": {
                            "type": "ResourceLoad";
                            "keyRaw": "resourceLoad";
                            "nullable": true;
                            "selection": {
                                "fields": {
                                    "cpuPercent": {
                                        "type": "Float";
                                        "keyRaw": "cpuPercent";
                                        "visible": true;
                                    };
                                    "memPercent": {
                                        "type": "Float";
                                        "keyRaw": "memPercent";
                                        "visible": true;
                                    };
                                    "diskPercent": {
                                        "type": "Float";
                                        "keyRaw": "diskPercent";
                                        "visible": true;
                                    };
                                    "loadAvg1m": {
                                        "type": "Float";
                                        "keyRaw": "loadAvg1m";
                                        "visible": true;
                                    };
                                    "loadAvg5m": {
                                        "type": "Float";
                                        "keyRaw": "loadAvg5m";
                                        "visible": true;
                                    };
                                    "loadAvg15m": {
                                        "type": "Float";
                                        "keyRaw": "loadAvg15m";
                                        "visible": true;
                                    };
                                };
                            };
                            "visible": true;
                        };
                    };
                };
                "visible": true;
            };
            "claudeAccounts": {
                "type": "ClaudeAccount";
                "keyRaw": "claudeAccounts";
                "selection": {
                    "fields": {
                        "id": {
                            "type": "ID";
                            "keyRaw": "id";
                            "visible": true;
                        };
                        "email": {
                            "type": "String";
                            "keyRaw": "email";
                            "visible": true;
                        };
                        "quotaUsed": {
                            "type": "Float";
                            "keyRaw": "quotaUsed";
                            "nullable": true;
                            "visible": true;
                        };
                        "quotaCap": {
                            "type": "Float";
                            "keyRaw": "quotaCap";
                            "nullable": true;
                            "visible": true;
                        };
                        "quotaResetsAt": {
                            "type": "Time";
                            "keyRaw": "quotaResetsAt";
                            "nullable": true;
                            "visible": true;
                        };
                        "quotaEstimated": {
                            "type": "Boolean";
                            "keyRaw": "quotaEstimated";
                            "visible": true;
                        };
                    };
                };
                "visible": true;
            };
        };
    };
    "pluginData": {
        "houdini-svelte": {};
    };
    "policy": "CacheAndNetwork";
    "partial": false;
};