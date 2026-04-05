use std::sync::{Arc, Mutex};
use tokio::sync::Mutex as TokioMutex;
use tokio_stream::wrappers::ReceiverStream;
use tonic::{Request, Response, Status};

use kokoros::tts::koko::{TTSKoko, TTSOpts};

use crate::backend;
use crate::backend::backend_server::Backend;

pub struct KokorosService {
    tts: Arc<TokioMutex<Option<TTSKoko>>>,
    language: Arc<Mutex<String>>,
    speed: Arc<Mutex<f32>>,
}

impl Default for KokorosService {
    fn default() -> Self {
        Self {
            tts: Arc::new(TokioMutex::new(None)),
            language: Arc::new(Mutex::new("en-us".to_string())),
            speed: Arc::new(Mutex::new(1.0)),
        }
    }
}

#[tonic::async_trait]
impl Backend for KokorosService {
    async fn health(
        &self,
        _req: Request<backend::HealthMessage>,
    ) -> Result<Response<backend::Reply>, Status> {
        Ok(Response::new(backend::Reply {
            message: b"OK".to_vec(),
            ..Default::default()
        }))
    }

    async fn load_model(
        &self,
        req: Request<backend::ModelOptions>,
    ) -> Result<Response<backend::Result>, Status> {
        let opts = req.into_inner();

        // Model path: join ModelPath + Model, or just Model
        let model_path = if !opts.model_path.is_empty() && !opts.model.is_empty() {
            format!("{}/{}", opts.model_path, opts.model)
        } else if !opts.model.is_empty() {
            opts.model.clone()
        } else {
            "checkpoints/kokoro-v1.0.onnx".to_string()
        };

        // Voices data path from AudioPath, or derive from model dir
        let voices_path = if !opts.audio_path.is_empty() {
            opts.audio_path.clone()
        } else {
            let model_dir = std::path::Path::new(&model_path)
                .parent()
                .map(|p| p.to_string_lossy().to_string())
                .unwrap_or_else(|| ".".to_string());
            format!("{}/voices-v1.0.bin", model_dir)
        };

        // Parse options (key:value pairs)
        for opt in &opts.options {
            if let Some((key, value)) = opt.split_once(':') {
                match key {
                    "lang_code" => *self.language.lock().unwrap() = value.to_string(),
                    "speed" => {
                        if let Ok(s) = value.parse::<f32>() {
                            *self.speed.lock().unwrap() = s;
                        }
                    }
                    _ => {}
                }
            }
        }

        tracing::info!("Loading Kokoros model from: {}", model_path);
        tracing::info!("Loading voices from: {}", voices_path);
        tracing::info!("Language: {}", self.language.lock().unwrap());

        let tts = TTSKoko::new(&model_path, &voices_path).await;
        *self.tts.lock().await = Some(tts);

        tracing::info!("Kokoros TTS model loaded successfully");
        Ok(Response::new(backend::Result {
            success: true,
            message: "Kokoros TTS model loaded".into(),
        }))
    }

    async fn tts(
        &self,
        req: Request<backend::TtsRequest>,
    ) -> Result<Response<backend::Result>, Status> {
        let req = req.into_inner();
        let tts_guard = self.tts.lock().await;
        let tts = tts_guard
            .as_ref()
            .ok_or_else(|| Status::failed_precondition("Model not loaded"))?;

        let voice = if req.voice.is_empty() {
            "af_heart"
        } else {
            &req.voice
        };
        let lang = req
            .language
            .unwrap_or_else(|| self.language.lock().unwrap().clone());
        let speed = *self.speed.lock().unwrap();

        tracing::debug!(
            text = req.text,
            voice = voice,
            lang = lang.as_str(),
            dst = req.dst,
            "TTS request"
        );

        match tts.tts(TTSOpts {
            txt: &req.text,
            lan: &lang,
            style_name: voice,
            save_path: &req.dst,
            mono: true,
            speed,
            initial_silence: None,
        }) {
            Ok(()) => Ok(Response::new(backend::Result {
                success: true,
                message: String::new(),
            })),
            Err(e) => {
                tracing::error!("TTS error: {}", e);
                Ok(Response::new(backend::Result {
                    success: false,
                    message: format!("TTS error: {}", e),
                }))
            }
        }
    }

    type TTSStreamStream = ReceiverStream<Result<backend::Reply, Status>>;

    async fn tts_stream(
        &self,
        req: Request<backend::TtsRequest>,
    ) -> Result<Response<Self::TTSStreamStream>, Status> {
        let req = req.into_inner();
        let tts_guard = self.tts.lock().await;
        let tts = tts_guard
            .as_ref()
            .ok_or_else(|| Status::failed_precondition("Model not loaded"))?
            .clone();

        let voice = if req.voice.is_empty() {
            "af_heart".to_string()
        } else {
            req.voice
        };
        let lang = req
            .language
            .unwrap_or_else(|| self.language.lock().unwrap().clone());
        let speed = *self.speed.lock().unwrap();
        let text = req.text;

        let (tx, rx) = tokio::sync::mpsc::channel(32);

        // Send sample rate info as first message
        let tx_clone = tx.clone();
        let _ = tx_clone
            .send(Ok(backend::Reply {
                message: br#"{"sample_rate":24000}"#.to_vec(),
                ..Default::default()
            }))
            .await;

        tokio::task::spawn_blocking(move || {
            let result = tts.tts_raw_audio_streaming(
                &text,
                &lang,
                &voice,
                speed,
                None,
                None,
                None,
                None,
                |audio_chunk: Vec<f32>| -> Result<(), Box<dyn std::error::Error>> {
                    // Convert f32 PCM to 16-bit PCM bytes (what LocalAI expects for streaming)
                    let bytes: Vec<u8> = audio_chunk
                        .iter()
                        .flat_map(|&s| {
                            let clamped = s.clamp(-1.0, 1.0);
                            let i16_val = (clamped * 32767.0) as i16;
                            i16_val.to_le_bytes()
                        })
                        .collect();
                    tx.blocking_send(Ok(backend::Reply {
                        audio: bytes,
                        ..Default::default()
                    }))
                    .map_err(|e| Box::new(e) as Box<dyn std::error::Error>)
                },
            );
            if let Err(e) = result {
                tracing::error!("TTSStream error: {}", e);
            }
        });

        Ok(Response::new(ReceiverStream::new(rx)))
    }

    async fn status(
        &self,
        _req: Request<backend::HealthMessage>,
    ) -> Result<Response<backend::StatusResponse>, Status> {
        let tts = self.tts.lock().await;
        let state = if tts.is_some() {
            backend::status_response::State::Ready as i32
        } else {
            backend::status_response::State::Uninitialized as i32
        };
        Ok(Response::new(backend::StatusResponse {
            state,
            memory: None,
        }))
    }

    async fn free(
        &self,
        _req: Request<backend::HealthMessage>,
    ) -> Result<Response<backend::Result>, Status> {
        *self.tts.lock().await = None;
        Ok(Response::new(backend::Result {
            success: true,
            message: "Model freed".into(),
        }))
    }

    // --- Unimplemented RPCs ---

    async fn predict(
        &self,
        _: Request<backend::PredictOptions>,
    ) -> Result<Response<backend::Reply>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    type PredictStreamStream = ReceiverStream<Result<backend::Reply, Status>>;

    async fn predict_stream(
        &self,
        _: Request<backend::PredictOptions>,
    ) -> Result<Response<Self::PredictStreamStream>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn embedding(
        &self,
        _: Request<backend::PredictOptions>,
    ) -> Result<Response<backend::EmbeddingResult>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn generate_image(
        &self,
        _: Request<backend::GenerateImageRequest>,
    ) -> Result<Response<backend::Result>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn generate_video(
        &self,
        _: Request<backend::GenerateVideoRequest>,
    ) -> Result<Response<backend::Result>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn audio_transcription(
        &self,
        _: Request<backend::TranscriptRequest>,
    ) -> Result<Response<backend::TranscriptResult>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn sound_generation(
        &self,
        _: Request<backend::SoundGenerationRequest>,
    ) -> Result<Response<backend::Result>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn tokenize_string(
        &self,
        _: Request<backend::PredictOptions>,
    ) -> Result<Response<backend::TokenizationResponse>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn detect(
        &self,
        _: Request<backend::DetectOptions>,
    ) -> Result<Response<backend::DetectResponse>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn stores_set(
        &self,
        _: Request<backend::StoresSetOptions>,
    ) -> Result<Response<backend::Result>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn stores_delete(
        &self,
        _: Request<backend::StoresDeleteOptions>,
    ) -> Result<Response<backend::Result>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn stores_get(
        &self,
        _: Request<backend::StoresGetOptions>,
    ) -> Result<Response<backend::StoresGetResult>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn stores_find(
        &self,
        _: Request<backend::StoresFindOptions>,
    ) -> Result<Response<backend::StoresFindResult>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn rerank(
        &self,
        _: Request<backend::RerankRequest>,
    ) -> Result<Response<backend::RerankResult>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn get_metrics(
        &self,
        _: Request<backend::MetricsRequest>,
    ) -> Result<Response<backend::MetricsResponse>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn vad(
        &self,
        _: Request<backend::VadRequest>,
    ) -> Result<Response<backend::VadResponse>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn audio_encode(
        &self,
        _: Request<backend::AudioEncodeRequest>,
    ) -> Result<Response<backend::AudioEncodeResult>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn audio_decode(
        &self,
        _: Request<backend::AudioDecodeRequest>,
    ) -> Result<Response<backend::AudioDecodeResult>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn model_metadata(
        &self,
        _: Request<backend::ModelOptions>,
    ) -> Result<Response<backend::ModelMetadataResponse>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn start_fine_tune(
        &self,
        _: Request<backend::FineTuneRequest>,
    ) -> Result<Response<backend::FineTuneJobResult>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    type FineTuneProgressStream = ReceiverStream<Result<backend::FineTuneProgressUpdate, Status>>;

    async fn fine_tune_progress(
        &self,
        _: Request<backend::FineTuneProgressRequest>,
    ) -> Result<Response<Self::FineTuneProgressStream>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn stop_fine_tune(
        &self,
        _: Request<backend::FineTuneStopRequest>,
    ) -> Result<Response<backend::Result>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn list_checkpoints(
        &self,
        _: Request<backend::ListCheckpointsRequest>,
    ) -> Result<Response<backend::ListCheckpointsResponse>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn export_model(
        &self,
        _: Request<backend::ExportModelRequest>,
    ) -> Result<Response<backend::Result>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn start_quantization(
        &self,
        _: Request<backend::QuantizationRequest>,
    ) -> Result<Response<backend::QuantizationJobResult>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    type QuantizationProgressStream =
        ReceiverStream<Result<backend::QuantizationProgressUpdate, Status>>;

    async fn quantization_progress(
        &self,
        _: Request<backend::QuantizationProgressRequest>,
    ) -> Result<Response<Self::QuantizationProgressStream>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn stop_quantization(
        &self,
        _: Request<backend::QuantizationStopRequest>,
    ) -> Result<Response<backend::Result>, Status> {
        Err(Status::unimplemented("Not supported"))
    }
}
