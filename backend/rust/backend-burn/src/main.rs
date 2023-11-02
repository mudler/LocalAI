use std::net::SocketAddr;

use bunker::pb::Result as PbResult;
use bunker::pb::{
    EmbeddingResult, GenerateImageRequest, HealthMessage, ModelOptions, PredictOptions, Reply,
    StatusResponse, TokenizationResponse, TranscriptRequest, TranscriptResult, TtsRequest,
};

use bunker::BackendService;
use tokio_stream::wrappers::ReceiverStream;
use tonic::{Request, Response, Status};

use async_trait::async_trait;

use tracing::{event, span, Level};

use models::*;
// implement BackendService trait in bunker

#[derive(Default, Debug)]
struct BurnBackend;

#[async_trait]
impl BackendService for BurnBackend {
    type PredictStreamStream = ReceiverStream<Result<Reply, Status>>;

    #[tracing::instrument]
    async fn health(&self, request: Request<HealthMessage>) -> Result<Response<Reply>, Status> {
        // return a Result<Response<Reply>,Status>
        let reply = Reply {
            message: "OK".into(),
        };
        let res = Response::new(reply);
        Ok(res)
    }

    #[tracing::instrument]
    async fn predict(&self, request: Request<PredictOptions>) -> Result<Response<Reply>, Status> {
        let mut models: Vec<Box<dyn LLM>> = vec![Box::new(models::MNINST::new())];
        let result = models[0].predict(request.into_inner());

        match result {
            Ok(res) => {
                let reply = Reply {
                    message: res.into(),
                };
                let res = Response::new(reply);
                Ok(res)
            }
            Err(e) => {
                let reply = Reply {
                    message: e.to_string().into(),
                };
                let res = Response::new(reply);
                Ok(res)
            }
        }
    }

    #[tracing::instrument]
    async fn load_model(
        &self,
        request: Request<ModelOptions>,
    ) -> Result<Response<PbResult>, Status> {
        todo!()
    }

    #[tracing::instrument]
    async fn predict_stream(
        &self,
        request: Request<PredictOptions>,
    ) -> Result<Response<ReceiverStream<Result<Reply, Status>>>, Status> {
        todo!()
    }

    #[tracing::instrument]
    async fn embedding(
        &self,
        request: Request<PredictOptions>,
    ) -> Result<Response<EmbeddingResult>, Status> {
        todo!()
    }

    #[tracing::instrument]
    async fn generate_image(
        &self,
        request: Request<GenerateImageRequest>,
    ) -> Result<Response<PbResult>, Status> {
        todo!()
    }

    #[tracing::instrument]
    async fn audio_transcription(
        &self,
        request: Request<TranscriptRequest>,
    ) -> Result<Response<TranscriptResult>, Status> {
        todo!()
    }

    #[tracing::instrument]
    async fn tts(&self, request: Request<TtsRequest>) -> Result<Response<PbResult>, Status> {
        todo!()
    }

    #[tracing::instrument]
    async fn tokenize_string(
        &self,
        request: Request<PredictOptions>,
    ) -> Result<Response<TokenizationResponse>, Status> {
        todo!()
    }

    #[tracing::instrument]
    async fn status(
        &self,
        request: Request<HealthMessage>,
    ) -> Result<Response<StatusResponse>, Status> {
        todo!()
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let subscriber = tracing_subscriber::fmt()
        .compact()
        .with_file(true)
        .with_line_number(true)
        .with_thread_ids(true)
        .with_target(false)
        .finish();

    tracing::subscriber::set_global_default(subscriber)?;

    // call bunker::run with BurnBackend
    let burn_backend = BurnBackend {};
    let addr = "[::1]:50051"
        .parse::<SocketAddr>()
        .expect("Failed to parse address");

    // Implmenet Into<SocketAddr> for addr
    let result = bunker::run(burn_backend, addr).await?;

    event!(Level::INFO, "Burn Server is starting");

    let span = span!(Level::INFO, "Burn Server");
    let _enter = span.enter();

    event!(Level::INFO, "Burn Server started successfully");

    Ok(result)
}
