//! Defination of a mninst model and config of it.
//! The source code is from https://github.com/burn-rs/burn/blob/main/examples/mnist-inference-web/src/model.rs
//! The license is Apache-2.0 and MIT.
//! Adapter by Aisuko

pub(crate) mod inference;
use inference::*;

use burn::{
    module::Module,
    nn::{self, BatchNorm, PaddingConfig2d},
    tensor::{backend::Backend, Tensor},
};

const NUM_CLASSES: usize = 10;

#[derive(Module, Debug)]
/// A struct representing an ONNX model.
pub struct Model<B: Backend> {
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

impl<B: Backend> Model<B> {
    pub fn new() -> Self {
        todo!("Implement the Model::new() function")
    }

    pub fn forward(&self, input: Tensor<B, 3>) -> Tensor<B, 2> {
        todo!("Implement the Model::forward() function")
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
