use clap::Parser;
use tonic::transport::Server;

mod auth;
mod service;

pub mod backend {
    tonic::include_proto!("backend");
}

#[derive(Parser, Debug)]
#[command(name = "kokoros-grpc")]
struct Cli {
    /// gRPC listen address (host:port)
    #[arg(long, default_value = "localhost:50051")]
    addr: String,
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info")),
        )
        .init();

    let cli = Cli::parse();
    let addr = cli.addr.parse()?;

    tracing::info!("Starting kokoros gRPC server on {}", addr);

    let mut builder = Server::builder();

    if let Some(interceptor) = auth::make_auth_interceptor() {
        tracing::info!("Bearer token authentication enabled");
        let svc = backend::backend_server::BackendServer::with_interceptor(
            service::KokorosService::default(),
            interceptor,
        );
        builder.add_service(svc).serve(addr).await?;
    } else {
        let svc = backend::backend_server::BackendServer::new(service::KokorosService::default())
            .max_decoding_message_size(50 * 1024 * 1024)
            .max_encoding_message_size(50 * 1024 * 1024);
        builder.add_service(svc).serve(addr).await?;
    }

    Ok(())
}
