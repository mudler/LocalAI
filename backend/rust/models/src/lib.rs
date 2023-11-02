pub(crate) mod mnist;
pub use mnist::mnist::MNINST;

use bunker::pb::{ModelOptions, PredictOptions};

pub trait LLM {
    fn load_model(&mut self, request: ModelOptions) -> Result<String, Box<dyn std::error::Error>>;
    fn predict(&mut self, request: PredictOptions) -> Result<String, Box<dyn std::error::Error>>;
}
