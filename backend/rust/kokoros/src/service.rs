use std::sync::{Arc, Mutex};
use tokio::sync::Mutex as TokioMutex;
use tokio_stream::wrappers::ReceiverStream;
use tonic::{Request, Response, Status};

use kokoros::tts::koko::TTSKoko;

use crate::backend;
use crate::backend::backend_server::Backend;

/// Write f32 samples as a standard 44-byte PCM 16-bit WAV file.
/// LocalAI's audio pipeline assumes this exact header layout.
fn write_pcm16_wav(
    path: &str,
    samples: &[f32],
    sample_rate: u32,
) -> Result<(), Box<dyn std::error::Error>> {
    use std::fs::File;
    use std::io::Write;

    let num_samples = samples.len() as u32;
    let data_size = num_samples * 2; // 16-bit = 2 bytes per sample
    let file_size = 36 + data_size;

    let mut f = File::create(path)?;

    // RIFF header
    f.write_all(b"RIFF")?;
    f.write_all(&file_size.to_le_bytes())?;
    f.write_all(b"WAVE")?;

    // fmt chunk — standard 16-byte PCM format
    f.write_all(b"fmt ")?;
    f.write_all(&16u32.to_le_bytes())?; // chunk size
    f.write_all(&1u16.to_le_bytes())?; // audio format = PCM
    f.write_all(&1u16.to_le_bytes())?; // channels = mono
    f.write_all(&sample_rate.to_le_bytes())?;
    f.write_all(&(sample_rate * 2).to_le_bytes())?; // byte rate
    f.write_all(&2u16.to_le_bytes())?; // block align
    f.write_all(&16u16.to_le_bytes())?; // bits per sample

    // data chunk
    f.write_all(b"data")?;
    f.write_all(&data_size.to_le_bytes())?;

    for &s in samples {
        let clamped = s.clamp(-1.0, 1.0);
        let pcm = (clamped * 32767.0) as i16;
        f.write_all(&pcm.to_le_bytes())?;
    }

    Ok(())
}

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
            .filter(|l| !l.is_empty())
            .unwrap_or_else(|| self.language.lock().unwrap().clone());
        let speed = *self.speed.lock().unwrap();

        tracing::info!(
            text = req.text,
            voice = voice,
            lang = lang.as_str(),
            dst = req.dst,
            "TTS request received"
        );

        let start = std::time::Instant::now();
        match tts.tts_raw_audio(&req.text, &lang, voice, speed, None, None, None, None) {
            Ok(samples) => {
                let duration_secs = samples.len() as f64 / 24000.0;
                tracing::info!(
                    num_samples = samples.len(),
                    audio_duration = format!("{:.2}s", duration_secs),
                    inference_time = format!("{:.2}s", start.elapsed().as_secs_f64()),
                    dst = req.dst,
                    "TTS inference complete"
                );
                if let Err(e) = write_pcm16_wav(&req.dst, &samples, 24000) {
                    tracing::error!("Failed to write WAV to {}: {}", req.dst, e);
                    return Ok(Response::new(backend::Result {
                        success: false,
                        message: format!("Failed to write WAV: {}", e),
                    }));
                }
                Ok(Response::new(backend::Result {
                    success: true,
                    message: String::new(),
                }))
            }
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
            .filter(|l| !l.is_empty())
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

    type AudioTranscriptionStreamStream =
        ReceiverStream<Result<backend::TranscriptStreamResponse, Status>>;

    async fn audio_transcription_stream(
        &self,
        _: Request<backend::TranscriptRequest>,
    ) -> Result<Response<Self::AudioTranscriptionStreamStream>, Status> {
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

    async fn face_verify(
        &self,
        _: Request<backend::FaceVerifyRequest>,
    ) -> Result<Response<backend::FaceVerifyResponse>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn face_analyze(
        &self,
        _: Request<backend::FaceAnalyzeRequest>,
    ) -> Result<Response<backend::FaceAnalyzeResponse>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn voice_verify(
        &self,
        _: Request<backend::VoiceVerifyRequest>,
    ) -> Result<Response<backend::VoiceVerifyResponse>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn voice_analyze(
        &self,
        _: Request<backend::VoiceAnalyzeRequest>,
    ) -> Result<Response<backend::VoiceAnalyzeResponse>, Status> {
        Err(Status::unimplemented("Not supported"))
    }

    async fn voice_embed(
        &self,
        _: Request<backend::VoiceEmbedRequest>,
    ) -> Result<Response<backend::VoiceEmbedResponse>, Status> {
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn wav_header_is_standard_pcm16() {
        let samples = vec![0.0f32, 0.5, -0.5, 1.0, -1.0];
        let path = std::env::temp_dir().join("kokoros_test.wav");
        let path_str = path.to_str().unwrap();

        write_pcm16_wav(path_str, &samples, 24000).unwrap();

        let data = std::fs::read(&path).unwrap();
        std::fs::remove_file(&path).unwrap();

        // Must be exactly 44-byte header + data
        assert_eq!(data.len(), 44 + samples.len() * 2);

        // RIFF header
        assert_eq!(&data[0..4], b"RIFF");
        assert_eq!(&data[8..12], b"WAVE");

        // fmt chunk: 16 bytes, format=1 (PCM), channels=1, 16-bit
        assert_eq!(&data[12..16], b"fmt ");
        assert_eq!(u32::from_le_bytes(data[16..20].try_into().unwrap()), 16); // chunk size
        assert_eq!(u16::from_le_bytes(data[20..22].try_into().unwrap()), 1); // PCM format
        assert_eq!(u16::from_le_bytes(data[22..24].try_into().unwrap()), 1); // mono
        assert_eq!(u32::from_le_bytes(data[24..28].try_into().unwrap()), 24000); // sample rate
        assert_eq!(u16::from_le_bytes(data[34..36].try_into().unwrap()), 16); // bits per sample

        // data chunk
        assert_eq!(&data[36..40], b"data");
        assert_eq!(
            u32::from_le_bytes(data[40..44].try_into().unwrap()),
            (samples.len() * 2) as u32
        );

        // Verify sample values: 0.5 -> 16383, -0.5 -> -16383, 1.0 -> 32767, -1.0 -> -32767
        let s1 = i16::from_le_bytes(data[46..48].try_into().unwrap());
        assert_eq!(s1, 16383); // 0.5 * 32767
        let s3 = i16::from_le_bytes(data[50..52].try_into().unwrap());
        assert_eq!(s3, 32767); // 1.0 clamped
        let s4 = i16::from_le_bytes(data[52..54].try_into().unwrap());
        assert_eq!(s4, -32767); // -1.0 clamped
    }

    /// Integration test: runs actual TTS inference and validates the output audio.
    /// Skipped unless KOKOROS_MODEL_PATH is set to a directory containing
    /// kokoro-v1.0.onnx and voices-v1.0.bin.
    #[tokio::test]
    async fn tts_produces_valid_speech() {
        let model_dir = match std::env::var("KOKOROS_MODEL_PATH") {
            Ok(p) => p,
            Err(_) => {
                eprintln!("KOKOROS_MODEL_PATH not set, skipping integration test");
                return;
            }
        };

        let model_path = format!("{}/kokoro-v1.0.onnx", model_dir);
        let voices_path = format!("{}/voices-v1.0.bin", model_dir);

        if !std::path::Path::new(&model_path).exists() {
            eprintln!("Model file not found at {}, skipping", model_path);
            return;
        }

        let tts = TTSKoko::new(&model_path, &voices_path).await;

        let input_text = "Hello world, this is a test of speech synthesis.";
        let out_path = std::env::temp_dir().join("kokoros_integration_test.wav");
        let out_str = out_path.to_str().unwrap();

        let samples = tts
            .tts_raw_audio(input_text, "en-us", "af_heart", 1.0, None, None, None, None)
            .expect("tts_raw_audio failed");

        write_pcm16_wav(out_str, &samples, 24000).unwrap();

        let data = std::fs::read(&out_path).unwrap();
        std::fs::remove_file(&out_path).unwrap();

        // --- WAV header sanity ---
        assert_eq!(&data[0..4], b"RIFF");
        assert_eq!(&data[8..12], b"WAVE");
        assert_eq!(u16::from_le_bytes(data[20..22].try_into().unwrap()), 1); // PCM
        assert_eq!(u32::from_le_bytes(data[24..28].try_into().unwrap()), 24000); // sample rate
        assert_eq!(u16::from_le_bytes(data[34..36].try_into().unwrap()), 16); // 16-bit

        let num_samples = samples.len();
        let duration_secs = num_samples as f64 / 24000.0;

        // --- Duration check ---
        // ~10 words should produce roughly 2-8 seconds of speech
        assert!(
            duration_secs > 1.0,
            "Audio too short: {:.2}s for {} words",
            duration_secs,
            input_text.split_whitespace().count()
        );
        assert!(
            duration_secs < 15.0,
            "Audio too long: {:.2}s for {} words",
            duration_secs,
            input_text.split_whitespace().count()
        );

        // --- Energy check: not silence ---
        let rms = (samples.iter().map(|s| s * s).sum::<f32>() / num_samples as f32).sqrt();
        assert!(
            rms > 0.01,
            "Audio is near-silence: RMS = {:.6}",
            rms
        );

        // --- Not clipped/saturated: should have dynamic range ---
        let max_abs = samples.iter().map(|s| s.abs()).fold(0.0f32, f32::max);
        assert!(
            max_abs < 1.0,
            "Audio is fully saturated (max |sample| = {:.4})",
            max_abs
        );
        assert!(
            max_abs > 0.05,
            "Audio has very low amplitude (max |sample| = {:.4})",
            max_abs
        );

        // --- Speech-like spectral check ---
        // Speech should have significant energy variation (not white noise or DC).
        // Check that the signal has zero-crossings in a speech-like range (roughly
        // 50-400 crossings per 24000 samples = 100-8000 Hz fundamental range).
        let zero_crossings: usize = samples
            .windows(2)
            .filter(|w| (w[0] >= 0.0) != (w[1] >= 0.0))
            .count();
        let crossings_per_sec = zero_crossings as f64 / duration_secs;
        // White noise at 24kHz would have ~12000 crossings/sec.
        // Speech is typically 100-4000 crossings/sec.
        assert!(
            crossings_per_sec < 10000.0,
            "Too many zero crossings ({:.0}/s) — likely noise, not speech",
            crossings_per_sec
        );
        assert!(
            crossings_per_sec > 50.0,
            "Too few zero crossings ({:.0}/s) — likely DC or silence, not speech",
            crossings_per_sec
        );

        eprintln!(
            "Integration test passed: duration={:.2}s, rms={:.4}, max={:.4}, zero_crossings={:.0}/s",
            duration_secs, rms, max_abs, crossings_per_sec
        );
    }
}
