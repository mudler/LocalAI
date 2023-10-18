
use tracing;
use tracing_subscriber;
use bunker::service::BackendService;

// implement BackendService trait in bunker

struct BurnBackend;

#[async_trait]
impl BackendService for BurnBackend{
    unimplemented!();
}


#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    unimplemented!();

    // let subscriber = tracing_subscriber::fmt()
    //     .compact()
    //     .with_file(true)
    //     .with_line_number(true)
    //     .with_target(true)
    //     .with_level(true)
    //     .finish();

    // tracing::subscriber::set_global_default(subscriber)
    //     .expect("setting default subscriber failed");

    // let addr = "[::1]:50052".parse().unwrap();

    // let backend = BackendService {};

    // let svc = BackendServer::new(backend);

    // Server::builder().add_service(svc).serve(addr).await?;

    // Ok(())
}
