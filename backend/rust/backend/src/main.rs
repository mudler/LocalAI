use std::collections::HashMap;
use std::net::SocketAddr;
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


/// TODO: In order to use the model, we need to add some common attributes like: model, device, tokenizer, embeddings, etc.
/// And these attributes should be thread safe.
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
        todo!("How to get model from load_model function?")
    }

    #[tracing::instrument]
    async fn load_model(
        &self,
        request: Request<ModelOptions>,
    ) -> Result<Response<PbResult>, Status> {
        let result = match request.get_ref().model.as_str() {
            "mnist" => {
                let model = MNINST::load_model(request.get_ref().clone());
                let result= PbResult {
                    message: format!("Model {} loaded successfully", request.get_ref().model),
                    success: true,
                };
                Ok(Response::new(result))
            }
            _ => {
                let result = PbResult {
                    message: format!("Model {} not found", request.get_ref().model),
                    success: false,
                };
                Ok(Response::new(result))
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

        use nix::sys::resource::{getrusage, UsageWho};
        let usage = getrusage(UsageWho::RUSAGE_SELF).expect("Failed to fet usage");
        memory_usage = usage.as_ref().ru_maxrss as u64;
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
        let memory = response.get_ref().memory.clone();
        assert_eq!(state, 0);
        assert!(memory.is_some());
    }

    #[tokio::test]
    async fn test_load_model() {
        let backend = BurnBackend::default();
        let model_options = ModelOptions {
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
            model_file: "models/src/mnist/model.bin".to_string(),
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
        };

        // Load the wrong model
        let request = Request::new(model_options.clone());
        let response = backend.load_model(request).await;

        assert!(response.is_ok());
        let response = response.unwrap();
        let message_str = response.get_ref().message.clone();
        assert_eq!(
            message_str,
            format!("Model {} not found", model_options.model.clone())
        );

        // Load the correct model
        let mut model_options2=model_options.clone();
        model_options2.model="mnist".to_string();
        model_options2.model_file="models/src/mnist/model.bin".to_string();

        let request = Request::new(model_options2.clone());
        let response = backend.load_model(request).await;

        assert!(response.is_ok());
        let response = response.unwrap();
        let message_str = response.get_ref().message.clone();
        assert_eq!(
            message_str,
            format!("Model {} loaded successfully", model_options2.model.clone())
        );
    }

    #[tokio::test]
    async fn test_predict() {
        let backend = BurnBackend::default();
        let request = Request::new(PredictOptions {
            prompt: "test".to_string(),
            seed: 100,
            threads: 1,
            tokens: 10,
            temperature: 0.0,
            top_k: 0,
            top_p: 0.0,
            repeat: 0,
            batch: 1,
            n_keep: 0,
            penalty: 0.0,
            f16kv: false,
            debug_mode: false,
            stop_prompts: vec!["".to_string()],
            ignore_eos: false,
            tail_free_sampling_z: 0.0,
            typical_p: 0.0,
            frequency_penalty: 0.0,
            presence_penalty: 0.0,
            mirostat: 0,
            mirostat_eta: 0.0,
            mirostat_tau: 0.0,
            penalize_nl: false,
            logit_bias: "".to_string(),
            m_lock: false,
            m_map: false,
            prompt_cache_all: false,
            prompt_cache_ro: false,
            grammar: "".to_string(),
            main_gpu: "".to_string(),
            tensor_split: "".to_string(),
            prompt_cache_path: "".to_string(),
            debug: false,
            embedding_tokens: vec![0],
            embeddings: "".to_string(),
            rope_freq_base: 0.0,
            rope_freq_scale: 0.0,
            negative_prompt_scale: 0.0,
            negative_prompt: "".to_string(),
            n_draft: 0,
        });
        let response: Result<Response<Reply>, Status> = backend.predict(request).await;

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
