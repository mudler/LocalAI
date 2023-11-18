use bunker::pb::{ModelOptions, PredictOptions};

pub(crate) mod mnist;
pub use mnist::mnist::MNINST;

/// Trait for implementing a Language Model.
pub trait LLM {
    /// Loads the model from the given options.
    fn load_model(&mut self, request: ModelOptions) -> Result<String, Box<dyn std::error::Error>>;
    /// Predicts the output for the given input options.
    fn predict(&mut self, request: PredictOptions) -> Result<String, Box<dyn std::error::Error>>;
}

pub struct LLModel {
    model: Box<dyn LLM + 'static>,
}
