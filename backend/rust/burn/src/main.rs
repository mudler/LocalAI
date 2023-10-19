use bunker::pb::Result as PbResult;
use bunker::pb::{
    EmbeddingResult, GenerateImageRequest, HealthMessage, ModelOptions, PredictOptions, Reply,
    StatusResponse, TokenizationResponse, TranscriptRequest, TranscriptResult, TtsRequest,
};
use bunker::service::BackendService;
use tokio_stream::wrappers::ReceiverStream;
use tonic::{Request, Response, Status};

use async_trait::async_trait;

// implement BackendService trait in bunker

struct BurnBackend;

#[async_trait]
impl BackendService<ReceiverStream<Result<Reply, Status>>> for BurnBackend {
    async fn health(&self, request: Request<HealthMessage>) -> Result<Response<Reply>, Status> {
        // return a Result<Response<Reply>,Status>
        let reply = Reply {
            message: "OK".into(),
        };
        let res = Response::new(reply);
        Ok(res)
    }

    async fn predict(&self, request: Request<PredictOptions>) -> Result<Response<Reply>, Status> {
        todo!()
    }

    async fn load_model(
        &self,
        request: Request<ModelOptions>,
    ) -> Result<Response<PbResult>, Status> {
        todo!()
    }

    async fn predict_stream(
        &self,
        request: Request<PredictOptions>,
    ) -> Result<Response<ReceiverStream<Result<Reply, Status>>>, Status> {
        todo!()
    }

    async fn embedding(
        &self,
        request: Request<PredictOptions>,
    ) -> Result<Response<EmbeddingResult>, Status> {
        todo!()
    }

    async fn generate_image(
        &self,
        request: Request<GenerateImageRequest>,
    ) -> Result<Response<PbResult>, Status> {
        todo!()
    }

    async fn audio_transcription(
        &self,
        request: Request<TranscriptRequest>,
    ) -> Result<Response<TranscriptResult>, Status> {
        todo!()
    }

    async fn tts(&self, request: Request<TtsRequest>) -> Result<Response<PbResult>, Status> {
        todo!()
    }

    async fn tokenize_string(
        &self,
        request: Request<PredictOptions>,
    ) -> Result<Response<TokenizationResponse>, Status> {
        todo!()
    }

    async fn status(
        &self,
        request: Request<HealthMessage>,
    ) -> Result<Response<StatusResponse>, Status> {
        todo!()
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    todo!()
}
