/// Import the code() backend.rs was generated from backend.proto
pub mod pb {
    include!("../generated/backend.rs");
}

use std::net::SocketAddr;
use tonic::transport::Server;

pub use crate::pb::backend_server::Backend as BackendService;
use crate::pb::backend_server::BackendServer;

// Run the backend with the default behavior
pub async fn run(
    backend: impl BackendService,
    addr: impl Into<SocketAddr>,
) -> Result<(), Box<dyn std::error::Error>> {
    let svc = BackendServer::new(backend);

    let r = Server::builder()
        .add_service(svc)
        .serve(addr.into())
        .await?;

    Ok(r)
}
