use backend::backend_server::{Backend, BackendServer};
use backend::{HealthMessage, PredictOptions,Reply,ModelOptions, EmbeddingResult, GenerateImageRequest,TranscriptRequest,TranscriptResult,TtsRequest,TokenizationResponse};
use tonic::{Request, Response, Status};
use tokio_stream::{wrappers::ReceiverStream};

use tonic::transport::Server;

pub mod backend{
    tonic::include_proto!("backend");
}


#[derive(Debug)]
struct BackendService;

#[tonic::async_trait]
impl Backend for BackendService{

    // Result in proto/backend.rs is conflict with std::result::Result
    // So we need to use use the fully qualified name of the Result type in the protobuf file

    async fn health(&self, request: Request<HealthMessage>) -> Result<Response<Reply>,Status> {
        
        //TODO: Maybe we can move this to a logger
        println!("Got a request: {:?}", request);

        let reply = backend::Reply {
            message: format!("OK").into(),
        };

        Ok(Response::new(reply))
        
    }

    // implmenet the predict function
    async fn predict(&self, request: Request<PredictOptions>) -> Result<Response<Reply>,Status> {
        unimplemented!("Not implemented yet")
    }

    // implement the model function
    async fn load_model(&self, request: Request<ModelOptions>) -> Result<Response<backend::Result>,Status> {
        unimplemented!("Not implemented yet")
    }

    type PredictStreamStream = ReceiverStream<Result<Reply,Status>>;

    async fn predict_stream(&self, request: Request<PredictOptions>) -> Result<Response<Self::PredictStreamStream>,Status> {
        unimplemented!("Not implemented yet")
    }

    async fn embedding(&self, request: Request<PredictOptions>) -> Result<Response<EmbeddingResult>,Status> {
        unimplemented!("Not implemented yet")
    }

    async fn generate_image(&self, request: Request<GenerateImageRequest>) -> Result<Response<backend::Result>,Status> {
        unimplemented!("Not implemented yet")
    }

    async fn audio_transcription(&self, request: Request<TranscriptRequest>) -> Result<Response<TranscriptResult>,Status> {
        unimplemented!("Not implemented yet")
    }

    async fn tts(&self, request: Request<TtsRequest>) -> Result<Response<backend::Result>,Status> {
        unimplemented!("Not implemented yet")
    }

    async fn tokenize_string(&self, request: Request<PredictOptions>) -> Result<Response<TokenizationResponse>,Status> {
        unimplemented!("Not implemented yet")
    }

    async fn status(&self, request: Request<HealthMessage>) -> Result<Response<backend::StatusResponse>,Status> {
        unimplemented!("Not implemented yet")
    }

}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let addr = "[::1]:50052".parse().unwrap();

    let backend = BackendService {};

    let svc = BackendServer::new(backend);

    Server::builder().add_service(svc).serve(addr).await?;

    Ok(())
}
