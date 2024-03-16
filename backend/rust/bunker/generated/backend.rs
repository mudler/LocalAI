#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct HealthMessage {}
/// The request message containing the user's name.
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct PredictOptions {
    #[prost(string, tag = "1")]
    pub prompt: ::prost::alloc::string::String,
    #[prost(int32, tag = "2")]
    pub seed: i32,
    #[prost(int32, tag = "3")]
    pub threads: i32,
    #[prost(int32, tag = "4")]
    pub tokens: i32,
    #[prost(int32, tag = "5")]
    pub top_k: i32,
    #[prost(int32, tag = "6")]
    pub repeat: i32,
    #[prost(int32, tag = "7")]
    pub batch: i32,
    #[prost(int32, tag = "8")]
    pub n_keep: i32,
    #[prost(float, tag = "9")]
    pub temperature: f32,
    #[prost(float, tag = "10")]
    pub penalty: f32,
    #[prost(bool, tag = "11")]
    pub f16kv: bool,
    #[prost(bool, tag = "12")]
    pub debug_mode: bool,
    #[prost(string, repeated, tag = "13")]
    pub stop_prompts: ::prost::alloc::vec::Vec<::prost::alloc::string::String>,
    #[prost(bool, tag = "14")]
    pub ignore_eos: bool,
    #[prost(float, tag = "15")]
    pub tail_free_sampling_z: f32,
    #[prost(float, tag = "16")]
    pub typical_p: f32,
    #[prost(float, tag = "17")]
    pub frequency_penalty: f32,
    #[prost(float, tag = "18")]
    pub presence_penalty: f32,
    #[prost(int32, tag = "19")]
    pub mirostat: i32,
    #[prost(float, tag = "20")]
    pub mirostat_eta: f32,
    #[prost(float, tag = "21")]
    pub mirostat_tau: f32,
    #[prost(bool, tag = "22")]
    pub penalize_nl: bool,
    #[prost(string, tag = "23")]
    pub logit_bias: ::prost::alloc::string::String,
    #[prost(bool, tag = "25")]
    pub m_lock: bool,
    #[prost(bool, tag = "26")]
    pub m_map: bool,
    #[prost(bool, tag = "27")]
    pub prompt_cache_all: bool,
    #[prost(bool, tag = "28")]
    pub prompt_cache_ro: bool,
    #[prost(string, tag = "29")]
    pub grammar: ::prost::alloc::string::String,
    #[prost(string, tag = "30")]
    pub main_gpu: ::prost::alloc::string::String,
    #[prost(string, tag = "31")]
    pub tensor_split: ::prost::alloc::string::String,
    #[prost(float, tag = "32")]
    pub top_p: f32,
    #[prost(string, tag = "33")]
    pub prompt_cache_path: ::prost::alloc::string::String,
    #[prost(bool, tag = "34")]
    pub debug: bool,
    #[prost(int32, repeated, tag = "35")]
    pub embedding_tokens: ::prost::alloc::vec::Vec<i32>,
    #[prost(string, tag = "36")]
    pub embeddings: ::prost::alloc::string::String,
    #[prost(float, tag = "37")]
    pub rope_freq_base: f32,
    #[prost(float, tag = "38")]
    pub rope_freq_scale: f32,
    #[prost(float, tag = "39")]
    pub negative_prompt_scale: f32,
    #[prost(string, tag = "40")]
    pub negative_prompt: ::prost::alloc::string::String,
    #[prost(int32, tag = "41")]
    pub n_draft: i32,
}
/// The response message containing the result
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct Reply {
    #[prost(bytes = "vec", tag = "1")]
    pub message: ::prost::alloc::vec::Vec<u8>,
}
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct ModelOptions {
    #[prost(string, tag = "1")]
    pub model: ::prost::alloc::string::String,
    #[prost(int32, tag = "2")]
    pub context_size: i32,
    #[prost(int32, tag = "3")]
    pub seed: i32,
    #[prost(int32, tag = "4")]
    pub n_batch: i32,
    #[prost(bool, tag = "5")]
    pub f16_memory: bool,
    #[prost(bool, tag = "6")]
    pub m_lock: bool,
    #[prost(bool, tag = "7")]
    pub m_map: bool,
    #[prost(bool, tag = "8")]
    pub vocab_only: bool,
    #[prost(bool, tag = "9")]
    pub low_vram: bool,
    #[prost(bool, tag = "10")]
    pub embeddings: bool,
    #[prost(bool, tag = "11")]
    pub numa: bool,
    #[prost(int32, tag = "12")]
    pub ngpu_layers: i32,
    #[prost(string, tag = "13")]
    pub main_gpu: ::prost::alloc::string::String,
    #[prost(string, tag = "14")]
    pub tensor_split: ::prost::alloc::string::String,
    #[prost(int32, tag = "15")]
    pub threads: i32,
    #[prost(string, tag = "16")]
    pub library_search_path: ::prost::alloc::string::String,
    #[prost(float, tag = "17")]
    pub rope_freq_base: f32,
    #[prost(float, tag = "18")]
    pub rope_freq_scale: f32,
    #[prost(float, tag = "19")]
    pub rms_norm_eps: f32,
    #[prost(int32, tag = "20")]
    pub ngqa: i32,
    #[prost(string, tag = "21")]
    pub model_file: ::prost::alloc::string::String,
    /// AutoGPTQ
    #[prost(string, tag = "22")]
    pub device: ::prost::alloc::string::String,
    #[prost(bool, tag = "23")]
    pub use_triton: bool,
    #[prost(string, tag = "24")]
    pub model_base_name: ::prost::alloc::string::String,
    #[prost(bool, tag = "25")]
    pub use_fast_tokenizer: bool,
    /// Diffusers
    #[prost(string, tag = "26")]
    pub pipeline_type: ::prost::alloc::string::String,
    #[prost(string, tag = "27")]
    pub scheduler_type: ::prost::alloc::string::String,
    #[prost(bool, tag = "28")]
    pub cuda: bool,
    #[prost(float, tag = "29")]
    pub cfg_scale: f32,
    #[prost(bool, tag = "30")]
    pub img2img: bool,
    #[prost(string, tag = "31")]
    pub clip_model: ::prost::alloc::string::String,
    #[prost(string, tag = "32")]
    pub clip_subfolder: ::prost::alloc::string::String,
    #[prost(int32, tag = "33")]
    pub clip_skip: i32,
    /// RWKV
    #[prost(string, tag = "34")]
    pub tokenizer: ::prost::alloc::string::String,
    /// LLM (llama.cpp)
    #[prost(string, tag = "35")]
    pub lora_base: ::prost::alloc::string::String,
    #[prost(string, tag = "36")]
    pub lora_adapter: ::prost::alloc::string::String,
    #[prost(bool, tag = "37")]
    pub no_mul_mat_q: bool,
    #[prost(string, tag = "39")]
    pub draft_model: ::prost::alloc::string::String,
    #[prost(string, tag = "38")]
    pub audio_path: ::prost::alloc::string::String,
    /// vllm
    #[prost(string, tag = "40")]
    pub quantization: ::prost::alloc::string::String,
}
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct Result {
    #[prost(string, tag = "1")]
    pub message: ::prost::alloc::string::String,
    #[prost(bool, tag = "2")]
    pub success: bool,
}
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct EmbeddingResult {
    #[prost(float, repeated, tag = "1")]
    pub embeddings: ::prost::alloc::vec::Vec<f32>,
}
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct TranscriptRequest {
    #[prost(string, tag = "2")]
    pub dst: ::prost::alloc::string::String,
    #[prost(string, tag = "3")]
    pub language: ::prost::alloc::string::String,
    #[prost(uint32, tag = "4")]
    pub threads: u32,
}
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct TranscriptResult {
    #[prost(message, repeated, tag = "1")]
    pub segments: ::prost::alloc::vec::Vec<TranscriptSegment>,
    #[prost(string, tag = "2")]
    pub text: ::prost::alloc::string::String,
}
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct TranscriptSegment {
    #[prost(int32, tag = "1")]
    pub id: i32,
    #[prost(int64, tag = "2")]
    pub start: i64,
    #[prost(int64, tag = "3")]
    pub end: i64,
    #[prost(string, tag = "4")]
    pub text: ::prost::alloc::string::String,
    #[prost(int32, repeated, tag = "5")]
    pub tokens: ::prost::alloc::vec::Vec<i32>,
}
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct GenerateImageRequest {
    #[prost(int32, tag = "1")]
    pub height: i32,
    #[prost(int32, tag = "2")]
    pub width: i32,
    #[prost(int32, tag = "3")]
    pub mode: i32,
    #[prost(int32, tag = "4")]
    pub step: i32,
    #[prost(int32, tag = "5")]
    pub seed: i32,
    #[prost(string, tag = "6")]
    pub positive_prompt: ::prost::alloc::string::String,
    #[prost(string, tag = "7")]
    pub negative_prompt: ::prost::alloc::string::String,
    #[prost(string, tag = "8")]
    pub dst: ::prost::alloc::string::String,
    #[prost(string, tag = "9")]
    pub src: ::prost::alloc::string::String,
    /// Diffusers
    #[prost(string, tag = "10")]
    pub enable_parameters: ::prost::alloc::string::String,
    #[prost(int32, tag = "11")]
    pub clip_skip: i32,
}
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct TtsRequest {
    #[prost(string, tag = "1")]
    pub text: ::prost::alloc::string::String,
    #[prost(string, tag = "2")]
    pub model: ::prost::alloc::string::String,
    #[prost(string, tag = "3")]
    pub dst: ::prost::alloc::string::String,
}
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct TokenizationResponse {
    #[prost(int32, tag = "1")]
    pub length: i32,
    #[prost(int32, repeated, tag = "2")]
    pub tokens: ::prost::alloc::vec::Vec<i32>,
}
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct MemoryUsageData {
    #[prost(uint64, tag = "1")]
    pub total: u64,
    #[prost(map = "string, uint64", tag = "2")]
    pub breakdown: ::std::collections::HashMap<::prost::alloc::string::String, u64>,
}
#[allow(clippy::derive_partial_eq_without_eq)]
#[derive(Clone, PartialEq, ::prost::Message)]
pub struct StatusResponse {
    #[prost(enumeration = "status_response::State", tag = "1")]
    pub state: i32,
    #[prost(message, optional, tag = "2")]
    pub memory: ::core::option::Option<MemoryUsageData>,
}
/// Nested message and enum types in `StatusResponse`.
pub mod status_response {
    #[derive(
        Clone,
        Copy,
        Debug,
        PartialEq,
        Eq,
        Hash,
        PartialOrd,
        Ord,
        ::prost::Enumeration
    )]
    #[repr(i32)]
    pub enum State {
        Uninitialized = 0,
        Busy = 1,
        Ready = 2,
        Error = -1,
    }
    impl State {
        /// String value of the enum field names used in the ProtoBuf definition.
        ///
        /// The values are not transformed in any way and thus are considered stable
        /// (if the ProtoBuf definition does not change) and safe for programmatic use.
        pub fn as_str_name(&self) -> &'static str {
            match self {
                State::Uninitialized => "UNINITIALIZED",
                State::Busy => "BUSY",
                State::Ready => "READY",
                State::Error => "ERROR",
            }
        }
        /// Creates an enum from field names used in the ProtoBuf definition.
        pub fn from_str_name(value: &str) -> ::core::option::Option<Self> {
            match value {
                "UNINITIALIZED" => Some(Self::Uninitialized),
                "BUSY" => Some(Self::Busy),
                "READY" => Some(Self::Ready),
                "ERROR" => Some(Self::Error),
                _ => None,
            }
        }
    }
}
/// Generated server implementations.
pub mod backend_server {
    #![allow(unused_variables, dead_code, missing_docs, clippy::let_unit_value)]
    use tonic::codegen::*;
    /// Generated trait containing gRPC methods that should be implemented for use with BackendServer.
    #[async_trait]
    pub trait Backend: Send + Sync + 'static {
        async fn health(
            &self,
            request: tonic::Request<super::HealthMessage>,
        ) -> std::result::Result<tonic::Response<super::Reply>, tonic::Status>;
        async fn predict(
            &self,
            request: tonic::Request<super::PredictOptions>,
        ) -> std::result::Result<tonic::Response<super::Reply>, tonic::Status>;
        async fn load_model(
            &self,
            request: tonic::Request<super::ModelOptions>,
        ) -> std::result::Result<tonic::Response<super::Result>, tonic::Status>;
        /// Server streaming response type for the PredictStream method.
        type PredictStreamStream: tonic::codegen::tokio_stream::Stream<
                Item = std::result::Result<super::Reply, tonic::Status>,
            >
            + Send
            + 'static;
        async fn predict_stream(
            &self,
            request: tonic::Request<super::PredictOptions>,
        ) -> std::result::Result<
            tonic::Response<Self::PredictStreamStream>,
            tonic::Status,
        >;
        async fn embedding(
            &self,
            request: tonic::Request<super::PredictOptions>,
        ) -> std::result::Result<tonic::Response<super::EmbeddingResult>, tonic::Status>;
        async fn generate_image(
            &self,
            request: tonic::Request<super::GenerateImageRequest>,
        ) -> std::result::Result<tonic::Response<super::Result>, tonic::Status>;
        async fn audio_transcription(
            &self,
            request: tonic::Request<super::TranscriptRequest>,
        ) -> std::result::Result<
            tonic::Response<super::TranscriptResult>,
            tonic::Status,
        >;
        async fn tts(
            &self,
            request: tonic::Request<super::TtsRequest>,
        ) -> std::result::Result<tonic::Response<super::Result>, tonic::Status>;
        async fn tokenize_string(
            &self,
            request: tonic::Request<super::PredictOptions>,
        ) -> std::result::Result<
            tonic::Response<super::TokenizationResponse>,
            tonic::Status,
        >;
        async fn status(
            &self,
            request: tonic::Request<super::HealthMessage>,
        ) -> std::result::Result<tonic::Response<super::StatusResponse>, tonic::Status>;
    }
    #[derive(Debug)]
    pub struct BackendServer<T: Backend> {
        inner: _Inner<T>,
        accept_compression_encodings: EnabledCompressionEncodings,
        send_compression_encodings: EnabledCompressionEncodings,
        max_decoding_message_size: Option<usize>,
        max_encoding_message_size: Option<usize>,
    }
    struct _Inner<T>(Arc<T>);
    impl<T: Backend> BackendServer<T> {
        pub fn new(inner: T) -> Self {
            Self::from_arc(Arc::new(inner))
        }
        pub fn from_arc(inner: Arc<T>) -> Self {
            let inner = _Inner(inner);
            Self {
                inner,
                accept_compression_encodings: Default::default(),
                send_compression_encodings: Default::default(),
                max_decoding_message_size: None,
                max_encoding_message_size: None,
            }
        }
        pub fn with_interceptor<F>(
            inner: T,
            interceptor: F,
        ) -> InterceptedService<Self, F>
        where
            F: tonic::service::Interceptor,
        {
            InterceptedService::new(Self::new(inner), interceptor)
        }
        /// Enable decompressing requests with the given encoding.
        #[must_use]
        pub fn accept_compressed(mut self, encoding: CompressionEncoding) -> Self {
            self.accept_compression_encodings.enable(encoding);
            self
        }
        /// Compress responses with the given encoding, if the client supports it.
        #[must_use]
        pub fn send_compressed(mut self, encoding: CompressionEncoding) -> Self {
            self.send_compression_encodings.enable(encoding);
            self
        }
        /// Limits the maximum size of a decoded message.
        ///
        /// Default: `4MB`
        #[must_use]
        pub fn max_decoding_message_size(mut self, limit: usize) -> Self {
            self.max_decoding_message_size = Some(limit);
            self
        }
        /// Limits the maximum size of an encoded message.
        ///
        /// Default: `usize::MAX`
        #[must_use]
        pub fn max_encoding_message_size(mut self, limit: usize) -> Self {
            self.max_encoding_message_size = Some(limit);
            self
        }
    }
    impl<T, B> tonic::codegen::Service<http::Request<B>> for BackendServer<T>
    where
        T: Backend,
        B: Body + Send + 'static,
        B::Error: Into<StdError> + Send + 'static,
    {
        type Response = http::Response<tonic::body::BoxBody>;
        type Error = std::convert::Infallible;
        type Future = BoxFuture<Self::Response, Self::Error>;
        fn poll_ready(
            &mut self,
            _cx: &mut Context<'_>,
        ) -> Poll<std::result::Result<(), Self::Error>> {
            Poll::Ready(Ok(()))
        }
        fn call(&mut self, req: http::Request<B>) -> Self::Future {
            let inner = self.inner.clone();
            match req.uri().path() {
                "/backend.Backend/Health" => {
                    #[allow(non_camel_case_types)]
                    struct HealthSvc<T: Backend>(pub Arc<T>);
                    impl<T: Backend> tonic::server::UnaryService<super::HealthMessage>
                    for HealthSvc<T> {
                        type Response = super::Reply;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::HealthMessage>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as Backend>::health(&inner, request).await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let inner = inner.0;
                        let method = HealthSvc(inner);
                        let codec = tonic::codec::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/backend.Backend/Predict" => {
                    #[allow(non_camel_case_types)]
                    struct PredictSvc<T: Backend>(pub Arc<T>);
                    impl<T: Backend> tonic::server::UnaryService<super::PredictOptions>
                    for PredictSvc<T> {
                        type Response = super::Reply;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::PredictOptions>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as Backend>::predict(&inner, request).await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let inner = inner.0;
                        let method = PredictSvc(inner);
                        let codec = tonic::codec::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/backend.Backend/LoadModel" => {
                    #[allow(non_camel_case_types)]
                    struct LoadModelSvc<T: Backend>(pub Arc<T>);
                    impl<T: Backend> tonic::server::UnaryService<super::ModelOptions>
                    for LoadModelSvc<T> {
                        type Response = super::Result;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::ModelOptions>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as Backend>::load_model(&inner, request).await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let inner = inner.0;
                        let method = LoadModelSvc(inner);
                        let codec = tonic::codec::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/backend.Backend/PredictStream" => {
                    #[allow(non_camel_case_types)]
                    struct PredictStreamSvc<T: Backend>(pub Arc<T>);
                    impl<
                        T: Backend,
                    > tonic::server::ServerStreamingService<super::PredictOptions>
                    for PredictStreamSvc<T> {
                        type Response = super::Reply;
                        type ResponseStream = T::PredictStreamStream;
                        type Future = BoxFuture<
                            tonic::Response<Self::ResponseStream>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::PredictOptions>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as Backend>::predict_stream(&inner, request).await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let inner = inner.0;
                        let method = PredictStreamSvc(inner);
                        let codec = tonic::codec::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.server_streaming(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/backend.Backend/Embedding" => {
                    #[allow(non_camel_case_types)]
                    struct EmbeddingSvc<T: Backend>(pub Arc<T>);
                    impl<T: Backend> tonic::server::UnaryService<super::PredictOptions>
                    for EmbeddingSvc<T> {
                        type Response = super::EmbeddingResult;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::PredictOptions>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as Backend>::embedding(&inner, request).await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let inner = inner.0;
                        let method = EmbeddingSvc(inner);
                        let codec = tonic::codec::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/backend.Backend/GenerateImage" => {
                    #[allow(non_camel_case_types)]
                    struct GenerateImageSvc<T: Backend>(pub Arc<T>);
                    impl<
                        T: Backend,
                    > tonic::server::UnaryService<super::GenerateImageRequest>
                    for GenerateImageSvc<T> {
                        type Response = super::Result;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::GenerateImageRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as Backend>::generate_image(&inner, request).await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let inner = inner.0;
                        let method = GenerateImageSvc(inner);
                        let codec = tonic::codec::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/backend.Backend/AudioTranscription" => {
                    #[allow(non_camel_case_types)]
                    struct AudioTranscriptionSvc<T: Backend>(pub Arc<T>);
                    impl<
                        T: Backend,
                    > tonic::server::UnaryService<super::TranscriptRequest>
                    for AudioTranscriptionSvc<T> {
                        type Response = super::TranscriptResult;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::TranscriptRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as Backend>::audio_transcription(&inner, request).await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let inner = inner.0;
                        let method = AudioTranscriptionSvc(inner);
                        let codec = tonic::codec::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/backend.Backend/TTS" => {
                    #[allow(non_camel_case_types)]
                    struct TTSSvc<T: Backend>(pub Arc<T>);
                    impl<T: Backend> tonic::server::UnaryService<super::TtsRequest>
                    for TTSSvc<T> {
                        type Response = super::Result;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::TtsRequest>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as Backend>::tts(&inner, request).await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let inner = inner.0;
                        let method = TTSSvc(inner);
                        let codec = tonic::codec::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/backend.Backend/TokenizeString" => {
                    #[allow(non_camel_case_types)]
                    struct TokenizeStringSvc<T: Backend>(pub Arc<T>);
                    impl<T: Backend> tonic::server::UnaryService<super::PredictOptions>
                    for TokenizeStringSvc<T> {
                        type Response = super::TokenizationResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::PredictOptions>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as Backend>::tokenize_string(&inner, request).await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let inner = inner.0;
                        let method = TokenizeStringSvc(inner);
                        let codec = tonic::codec::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                "/backend.Backend/Status" => {
                    #[allow(non_camel_case_types)]
                    struct StatusSvc<T: Backend>(pub Arc<T>);
                    impl<T: Backend> tonic::server::UnaryService<super::HealthMessage>
                    for StatusSvc<T> {
                        type Response = super::StatusResponse;
                        type Future = BoxFuture<
                            tonic::Response<Self::Response>,
                            tonic::Status,
                        >;
                        fn call(
                            &mut self,
                            request: tonic::Request<super::HealthMessage>,
                        ) -> Self::Future {
                            let inner = Arc::clone(&self.0);
                            let fut = async move {
                                <T as Backend>::status(&inner, request).await
                            };
                            Box::pin(fut)
                        }
                    }
                    let accept_compression_encodings = self.accept_compression_encodings;
                    let send_compression_encodings = self.send_compression_encodings;
                    let max_decoding_message_size = self.max_decoding_message_size;
                    let max_encoding_message_size = self.max_encoding_message_size;
                    let inner = self.inner.clone();
                    let fut = async move {
                        let inner = inner.0;
                        let method = StatusSvc(inner);
                        let codec = tonic::codec::ProstCodec::default();
                        let mut grpc = tonic::server::Grpc::new(codec)
                            .apply_compression_config(
                                accept_compression_encodings,
                                send_compression_encodings,
                            )
                            .apply_max_message_size_config(
                                max_decoding_message_size,
                                max_encoding_message_size,
                            );
                        let res = grpc.unary(method, req).await;
                        Ok(res)
                    };
                    Box::pin(fut)
                }
                _ => {
                    Box::pin(async move {
                        Ok(
                            http::Response::builder()
                                .status(200)
                                .header("grpc-status", "12")
                                .header("content-type", "application/grpc")
                                .body(empty_body())
                                .unwrap(),
                        )
                    })
                }
            }
        }
    }
    impl<T: Backend> Clone for BackendServer<T> {
        fn clone(&self) -> Self {
            let inner = self.inner.clone();
            Self {
                inner,
                accept_compression_encodings: self.accept_compression_encodings,
                send_compression_encodings: self.send_compression_encodings,
                max_decoding_message_size: self.max_decoding_message_size,
                max_encoding_message_size: self.max_encoding_message_size,
            }
        }
    }
    impl<T: Backend> Clone for _Inner<T> {
        fn clone(&self) -> Self {
            Self(Arc::clone(&self.0))
        }
    }
    impl<T: std::fmt::Debug> std::fmt::Debug for _Inner<T> {
        fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
            write!(f, "{:?}", self.0)
        }
    }
    impl<T: Backend> tonic::server::NamedService for BackendServer<T> {
        const NAME: &'static str = "backend.Backend";
    }
}
