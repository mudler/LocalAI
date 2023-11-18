//! Defination of a mninst model and config of it.
//! The source code is from https://github.com/burn-rs/burn/blob/main/examples/mnist-inference-web/src/model.rs
//! The license is Apache-2.0 and MIT.
//! Adapter by Aisuko

use burn::{
    backend::wgpu::{AutoGraphicsApi, WgpuDevice},
    module::Module,
    nn::{self, BatchNorm, PaddingConfig2d},
    record::{BinBytesRecorder, FullPrecisionSettings, Recorder},
    tensor::{backend::Backend, Tensor},
};

// https://github.com/burn-rs/burn/blob/main/examples/mnist-inference-web/model.bin

const NUM_CLASSES: usize = 10;

#[derive(Module, Debug)]
/// A struct representing an MNINST model.
pub struct MNINST<B: Backend> {
    /// The first convolutional block of the model.
    conv1: ConvBlock<B>,
    /// The second convolutional block of the model.
    conv2: ConvBlock<B>,
    /// The third convolutional block of the model.
    conv3: ConvBlock<B>,
    /// A dropout layer used in the model.
    dropout: nn::Dropout,
    /// The first fully connected layer of the model.
    fc1: nn::Linear<B>,
    /// The second fully connected layer of the model.
    fc2: nn::Linear<B>,
    /// The activation function used in the model.
    activation: nn::GELU,
}

impl<B: Backend> MNINST<B> {
    pub fn new(model_name: &str) -> Self {
        let conv1 = ConvBlock::new([1, 8], [3, 3]); // 1 input channel, 8 output channels, 3x3 kernel size
        let conv2 = ConvBlock::new([8, 16], [3, 3]); // 8 input channels, 16 output channels, 3x3 kernel size
        let conv3 = ConvBlock::new([16, 24], [3, 3]); // 16 input channels, 24 output channels, 3x3 kernel size
        let hidden_size = 24 * 22 * 22;
        let fc1 = nn::LinearConfig::new(hidden_size, 32)
            .with_bias(false)
            .init();
        let fc2 = nn::LinearConfig::new(32, NUM_CLASSES)
            .with_bias(false)
            .init();

        let dropout = nn::DropoutConfig::new(0.5).init();

        let instance = Self {
            conv1: conv1,
            conv2: conv2,
            conv3: conv3,
            dropout: dropout,
            fc1: fc1,
            fc2: fc2,
            activation: nn::GELU::new(),
        };
        let state_encoded: &[u8] = &std::fs::read(model_name).expect("Failed to load model");
        let record = BinBytesRecorder::<FullPrecisionSettings>::default()
            .load(state_encoded.to_vec())
            .expect("Failed to decode state");

        instance.load_record(record)
    }

    /// Applies the forward pass of the neural network on the given input tensor.
    ///
    /// # Arguments
    ///
    /// * `input` - A 3-dimensional tensor of shape [batch_size, height, width].
    ///
    /// # Returns
    ///
    /// A 2-dimensional tensor of shape [batch_size, num_classes] containing the output of the neural network.
    pub fn forward(&self, input: Tensor<B, 3>) -> Tensor<B, 2> {
        // Get the dimensions of the input tensor
        let [batch_size, height, width] = input.dims();
        // Reshape the input tensor to have a shape of [batch_size, 1, height, width] and detach it
        let x = input.reshape([batch_size, 1, height, width]).detach();
        // Apply the first convolutional layer to the input tensor
        let x = self.conv1.forward(x);
        // Apply the second convolutional layer to the output of the first convolutional layer
        let x = self.conv2.forward(x);
        // Apply the third convolutional layer to the output of the second convolutional layer
        let x = self.conv3.forward(x);

        // Get the dimensions of the output tensor from the third convolutional layer
        let [batch_size, channels, height, width] = x.dims();
        // Reshape the output tensor to have a shape of [batch_size, channels*height*width]
        let x = x.reshape([batch_size, channels * height * width]);

        // Apply dropout to the output of the third convolutional layer
        let x = self.dropout.forward(x);
        // Apply the first fully connected layer to the output of the dropout layer
        let x = self.fc1.forward(x);
        // Apply the activation function to the output of the first fully connected layer
        let x = self.activation.forward(x);

        // Apply the second fully connected layer to the output of the activation function
        self.fc2.forward(x)
    }

    pub fn inference(&mut self, input: &[f32]) -> Result<Vec<f32>, Box<dyn std::error::Error>> {
        // Reshape from the 1D array to 3d tensor [batch, height, width]
        let input: Tensor<B, 3> = Tensor::from_floats(input).reshape([1, 28, 28]);

        // Normalize input: make between [0,1] and make the mean=0 and std=1
        // values mean=0.1307, std=0.3081
        // Source: https://github.com/pytorch/examples/blob/54f4572509891883a947411fd7239237dd2a39c3/mnist/main.py#L122
        let input = ((input / 255) - 0.1307) / 0.3081;

        // Run the tensor input through the model
        let output: Tensor<B, 2> = self.forward(input);

        // Convert the model output into probalibility distribution using softmax formula
        let output = burn::tensor::activation::softmax(output, 1);

        // Flatten oupuut tensor with [1,10] shape into boxed slice of [f32]
        let output = output.into_data().convert::<f32>().value;

        Ok(output)
    }
}

/// A struct representing a convolutional block in a neural network model.
#[derive(Module, Debug)]
pub struct ConvBlock<B: Backend> {
    /// A 2D convolutional layer.
    conv: nn::conv::Conv2d<B>,
    /// A batch normalization layer.
    norm: BatchNorm<B, 2>,
    /// A GELU activation function.
    activation: nn::GELU,
}

/// A convolutional block with batch normalization and GELU activation.
impl<B: Backend> ConvBlock<B> {
    /// Creates a new `ConvBlock` with the given number of output channels and kernel size.
    pub fn new(channels: [usize; 2], kernel_size: [usize; 2]) -> Self {
        // Initialize a 2D convolutional layer with the given output channels and kernel size,
        // and set the padding to "valid".
        let conv = nn::conv::Conv2dConfig::new(channels, kernel_size)
            .with_padding(PaddingConfig2d::Valid)
            .init();

        // Initialize a batch normalization layer with the number of channels in the second dimension of the output.
        let norm = nn::BatchNormConfig::new(channels[1]).init();

        // Create a new `ConvBlock` with the initialized convolutional and batch normalization layers,
        // and a GELU activation function.
        Self {
            conv: conv,
            norm: norm,
            activation: nn::GELU::new(),
        }
    }

    /// Applies the convolutional block to the given input tensor.
    pub fn forward(&self, input: Tensor<B, 4>) -> Tensor<B, 4> {
        // Apply the convolutional layer to the input tensor.
        let x = self.conv.forward(input);

        // Apply the batch normalization layer to the output of the convolutional layer.
        let x = self.norm.forward(x);

        // Apply the GELU activation function to the output of the batch normalization layer.
        self.activation.forward(x)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[cfg(feature = "ndarray")]
    pub type Backend = burn::backend::NdArrayBackend<f32>;
    #[test]
    fn test_inference() {
        let mut model = MNINST::<Backend>::new("model.bin");
        let output = model.inference(&[0.0; 28 * 28]).unwrap();
        assert_eq!(output.len(), 10);
    }
}
