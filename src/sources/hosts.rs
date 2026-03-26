/// Probes whether a remote host is reachable via SSH.
///
/// Returns `true` if the host responds, `false` if unreachable or if the
/// SSH connection times out.
pub fn probe_reachability(host: &str) -> bool {
    crate::remote::ssh_exec(host, "true").is_ok()
}
