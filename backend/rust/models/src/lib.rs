use bunker::pb::{ModelOptions, PredictOptions};

pub(crate) mod mnist;
pub use mnist::mnist::MNINST;

/// Trait for implementing a Language Model.
pub trait LLM {

    type Model: LLM;

    /// Loads the model from the given options.
    fn load_model(request: ModelOptions) -> Result<Self::Model, Box<dyn std::error::Error>>;
    /// Predicts the output for the given input options.
    fn predict(request: PredictOptions) -> Result<String, Box<dyn std::error::Error>>;
}

