use std::collections::HashMap;
use std::net::SocketAddr;

use bunker::pb::Result as PbResult;
use bunker::pb::{
    EmbeddingResult, GenerateImageRequest, HealthMessage, MemoryUsageData, ModelOptions,
    PredictOptions, Reply, StatusResponse, TokenizationResponse, TranscriptRequest,
    TranscriptResult, TtsRequest,
};

use bunker::BackendService;
use tokio_stream::wrappers::ReceiverStream;
use tonic::{Request, Response, Status};

use async_trait::async_trait;

use tracing::{event, span, Level};
use tracing_subscriber::filter::LevelParseError;

use std::fs;
use std::process::{Command,id};

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
        
        // Here we do not need to cover the windows platform
        let mut breakdown = HashMap::new();
        let mut memory_usage: u64=0;

        #[cfg(target_os = "linux")]
        {
            let pid =id();
            let stat = fs::read_to_string(format!("/proc/{}/stat", pid)).expect("Failed to read stat file");

            let stats: Vec<&str> = stat.split_whitespace().collect();
            memory_usage = stats[23].parse::<u64>().expect("Failed to parse RSS");
        }

        #[cfg(target_os="macos")]
        {
            let output=Command::new("ps")
            .arg("-p")
            .arg(id().to_string())
            .arg("-o")
            .arg("rss=")
            .output()
            .expect("failed to execute process");
    
            memory_usage = String::from_utf8_lossy(&output.stdout)
            .trim()
            .parse::<u64>()
            .expect("Failed to parse memory usage");

        }
        breakdown.insert("RSS".to_string(), memory_usage);

        let memory_usage = Option::from(MemoryUsageData {
            total: memory_usage,
            breakdown,
        });

        let reponse = StatusResponse {
            state: 0, //TODO: add state https://github.com/mudler/LocalAI/blob/9b17af18b3aa0c3cab16284df2d6f691736c30c1/pkg/grpc/proto/backend.proto#L188C9-L188C9
            memory: memory_usage,
        };

        Ok(Response::new(reponse))
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
