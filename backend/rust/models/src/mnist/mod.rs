use crate::LLM;

pub(crate) mod mnist;

use mnist::MNINST;

use bunker::pb::{ModelOptions, PredictOptions};

#[cfg(feature = "ndarray")]
pub type Backend = burn::backend::NdArrayBackend<f32>;

impl LLM for MNINST<Backend> {
    type Model = MNINST<Backend>;

    fn load_model(request: ModelOptions) -> Result<Self::Model, Box<dyn std::error::Error>> {
        let model = request.model_file;
        let instance= MNINST::<Backend>::new(&model);
        // check instance and return result
        Ok(instance)
    }

    fn predict(pre_ops: PredictOptions) -> Result<String, Box<dyn std::error::Error>> {
        // convert prost::alloc::string::String to &[f32]
        todo!()
    }
}
