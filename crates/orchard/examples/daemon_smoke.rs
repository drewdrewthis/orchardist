use orchard::daemon::{fan_out, Client};

fn main() {
    let c = Client::local().expect("client");
    println!("URL: {}", c.url());
    let hosts = c.hosts().expect("hosts");
    let fanout = fan_out(&c, &hosts).expect("fan_out");
    let ok_peers = fanout
        .peer_results
        .iter()
        .filter(|p| p.sessions.is_ok())
        .count();
    let err_peers = fanout
        .peer_results
        .iter()
        .filter(|p| p.sessions.is_err())
        .count();
    println!(
        "FED: local={} peers_ok={} peers_err={}",
        fanout.local_sessions.len(),
        ok_peers,
        err_peers
    );
    let flat = fanout.flatten();
    println!("FLAT ROWS: {}", flat.len());
    for row in flat.iter().rev().take(8) {
        println!(
            " - [{}] {} attached={}",
            row.host_label, row.session.name, row.session.attached
        );
    }
    for peer in &fanout.peer_results {
        match &peer.sessions {
            Ok(s) => println!(
                "PEER {} via {}: {} sessions",
                peer.hostname,
                peer.address,
                s.len()
            ),
            Err(e) => println!("PEER {} via {} ERR: {e}", peer.hostname, peer.address),
        }
    }
}
