use crate::LLM;
use bunker::pb::{ModelOptions, PredictOptions};

pub(crate) mod mnist;

#[cfg(feature = "ndarray")]
pub type Backend = burn::backend::NdArrayBackend<f32>;

impl LLM for mnist::MNINST<Backend> {
    fn load_model(&mut self, request: ModelOptions) -> Result<String, Box<dyn std::error::Error>> {
        todo!("load model")
    }

    fn predict(&mut self, pre_ops: PredictOptions) -> Result<String, Box<dyn std::error::Error>> {
        // convert prost::alloc::string::String to &[f32]
        let input = pre_ops.prompt.as_bytes();
        let input = input.iter().map(|x| *x as f32).collect::<Vec<f32>>();

        let result = self.inference(&input);

        match result {
            Ok(output) => {
                let output = output
                    .iter()
                    .map(|f| f.to_string())
                    .collect::<Vec<String>>()
                    .join(",");
                Ok(output)
            }
            Err(e) => Err(e),
        }
    }
}
