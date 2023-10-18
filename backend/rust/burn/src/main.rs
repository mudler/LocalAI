use tokio_stream::wrappers::ReceiverStream;
use tonic::transport::Server;
use tonic::{async_trait, Request, Response, Status};
use tracing;
use tracing_subscriber;
use bunker::pb::{EmbeddingResult, GenerateImageRequest, HealthMessage, ModelOptions, PredictOptions, Reply, StatusResponse, TokenizationResponse, TranscriptRequest, TranscriptResult, TtsRequest};
use bunker::pb::Result as PbResult;
use bunker::service::BackendService;


// implement BackendService trait in bunker

struct BurnBackend;

#[async_trait]
impl BackendService<ReceiverStream<Result<Reply, Status>>> for BurnBackend{

    async fn health(&self, request: Request<HealthMessage>) -> Result<Response<Reply>,Status> {
        // return a Result<Response<Reply>,Status>
        let reply = Reply {
            message: "OK".into(),
        };
        let res=Response::new(reply);
        Ok(res)
    }

    async fn predict(&self, request: Request<PredictOptions>) -> Result<Response<Reply>,Status> {
        todo!()
    }

    async fn load_model(&self, request: Request<ModelOptions>) -> Result<Response<PbResult>, Status> {
        todo!()
    }

    async fn predict_stream(&self, request: Request<PredictOptions>) -> Result<Response<ReceiverStream<Result<Reply,Status>>>, Status> {
        todo!()
    }

    async fn embedding(&self, request: Request<PredictOptions>) -> Result<Response<EmbeddingResult>, Status> {
        todo!()
    }

    async fn generate_image(&self, request: Request<GenerateImageRequest>) -> Result<Response<PbResult>, Status> {
        todo!()
    }

    async fn audio_transcription(&self, request: Request<TranscriptRequest>) -> Result<Response<TranscriptResult>, Status> {
        todo!()
    }

    async fn tts(&self, request: Request<TtsRequest>) -> Result<Response<PbResult>, Status> {
        todo!()
    }

    async fn tokenize_string(&self, request: Request<PredictOptions>) -> Result<Response<TokenizationResponse>, Status> {
        todo!()
    }

    async fn status(&self, request: Request<HealthMessage>) -> Result<Response<StatusResponse>, Status> {
        todo!()
    }

}


#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {

    let subscriber = tracing_subscriber::fmt()
        .compact()
        .with_file(true)
        .with_line_number(true)
        .with_target(true)
        .with_level(true)
        .finish();

    tracing::subscriber::set_global_default(subscriber)
        .expect("setting default subscriber failed");

    let addr = "[::1]:50052".parse().unwrap();

    let backend = BackendService {};

    let svc = BurnBackend::new(backend);

    Server::builder().add_service(svc).serve(addr).await?;

    Ok(())
}
