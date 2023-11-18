use std::collections::HashMap;
use std::net::SocketAddr;
use std::process::{id, Command};
use std::sync::{Arc, Mutex};

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

use models::*;
// implement BackendService trait in bunker

#[derive(Default, Debug)]
pub struct BurnBackend;

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
        // TODO: How to get model from load_model function?
        let mut model= MNINST::new("model.bin");
        let result = model.predict(request.get_ref().clone());
        match result {
            Ok(output) => {
                let reply = Reply {
                    message: output.into_bytes(),
                };
                let res = Response::new(reply);
                Ok(res)
            }
            Err(e) => {
                let result = PbResult {
                    message: format!("Failed to predict: {}", e),
                    success: false,
                };
                Err(Status::internal(result.message))
            }
        }
    }

    #[tracing::instrument]
    async fn load_model(
        &self,
        request: Request<ModelOptions>,
    ) -> Result<Response<PbResult>, Status> {
        let result= match request.get_ref().model.as_str() {
            "mnist" => {
                let mut model = MNINST::new("model.bin");
                let result = model.load_model(request.get_ref().clone());
                match result {
                    Ok(_) => {
                        let model = Arc::new(Mutex::new(model));
                        let model = model.clone();
                        let result = PbResult {
                            message: "Model loaded successfully".into(),
                            success: true,
                        };
                        Ok(Response::new(result))
                    }
                    Err(e) => {
                        let result = PbResult {
                            message: format!("Failed to load model: {}", e),
                            success: false,
                        };
                        Err(Status::internal(result.message))
                    }
                }
            }
            _ => {
                let result = PbResult {
                    message: format!("Model {} not found", request.get_ref().model),
                    success: false,
                };
                Err(Status::internal(result.message))
            }
        };
        // TODO: add model to backend, how to transfer model to backend and let predict funciton can use it?
        result
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
        let mut memory_usage: u64 = 0;

        #[cfg(target_os = "linux")]
        {
            let pid = id();
            let stat = fs::read_to_string(format!("/proc/{}/stat", pid))
                .expect("Failed to read stat file");

            let stats: Vec<&str> = stat.split_whitespace().collect();
            memory_usage = stats[23].parse::<u64>().expect("Failed to parse RSS");
        }

        #[cfg(target_os = "macos")]
        {
            let output = Command::new("ps")
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

#[cfg(test)]
mod tests {
    use super::*;
    use tonic::Request;

    #[tokio::test]
    async fn test_health() {
        let backend = BurnBackend::default();
        let request = Request::new(HealthMessage {});
        let response = backend.health(request).await;

        assert!(response.is_ok());
        let response = response.unwrap();
        let message_str = String::from_utf8(response.get_ref().message.clone()).unwrap();
        assert_eq!(message_str, "OK");
    }
    #[tokio::test]
    async fn test_status() {
        let backend = BurnBackend::default();
        let request = Request::new(HealthMessage {});
        let response = backend.status(request).await;

        assert!(response.is_ok());
        let response = response.unwrap();
        let state = response.get_ref().state;
        assert_eq!(state, 0);
    }

    #[tokio::test]
    async fn test_load_model() {
        let backend = BurnBackend::default();
        let request = Request::new(ModelOptions {
            model: "test".to_string(),
            context_size: 0,
            seed: 0,
            n_batch: 0,
            f16_memory: false,
            m_lock: false,
            m_map: false,
            vocab_only: false,
            low_vram: false,
            embeddings: false,
            numa: false,
            ngpu_layers: 0,
            main_gpu: "".to_string(),
            tensor_split: "".to_string(),
            threads: 1,
            library_search_path: "".to_string(),
            rope_freq_base: 0.0,
            rope_freq_scale: 0.0,
            rms_norm_eps: 0.0,
            ngqa: 0,
            model_file: "".to_string(),
            device: "".to_string(),
            use_triton: false,
            model_base_name: "".to_string(),
            use_fast_tokenizer: false,
            pipeline_type: "".to_string(),
            scheduler_type: "".to_string(),
            cuda: false,
            cfg_scale: 0.0,
            img2img: false,
            clip_model: "".to_string(),
            clip_subfolder: "".to_string(),
            clip_skip: 0,
            tokenizer: "".to_string(),
            lora_base: "".to_string(),
            lora_adapter: "".to_string(),
            no_mul_mat_q: false,
            draft_model: "".to_string(),
            audio_path: "".to_string(),
            quantization: "".to_string(),
        });
        let response = backend.load_model(request).await;

        assert!(response.is_ok());
        let response = response.unwrap();
        //TO_DO: add test for response
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
