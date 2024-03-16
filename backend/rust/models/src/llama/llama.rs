//! The source code is from https://github.com/Gadersd/llama2-burn/blob/main/src/model.rs
//! The license is Special MIT License.(And the code will be replaced in the future, currently it is just a test.)
//! Adapter by Aisuko

use std::f32::NEG_INFINITY;

use burn::{
    config::Config,
    module::{Module, Param},
    nn,
    tensor::{
        activation::{sigmoid, softmax},
        backend::Backend,
        module::embedding,
        Data, Distribution, Int, Tensor,
    }, backend::wgpu::tensor,
};

#[derive(Config, Debug)]
pub struct LlamaConfig {
    n_vocab: usize,
    n_ctx: usize,
    n_state: usize,
    multiple_of: usize,
    ffn_dim_multiplier: Option<usize>,
    n_head: usize,
    n_kv_head: usize,
    n_layer: usize,
    #[config(default = 1e-6)]
    norm_eps: f64,
}

impl LlamaConfig {
    pub fn init<B: Backend>(&self) -> Llama<B> {
        let token_embedding = nn::EmbeddingConfig::new(self.n_vocab, self.n_state).init();
        let rotary_encoder =
            RotaryEncodingConfig::new(self.n_ctx, self.n_state / self.n_head, 10000.0).init();
        let blocks: Vec<_> = (0..self.n_layer)
            .into_iter()
            .map(|_| {
                ResidualDecoderAttentionBlockConfig::new(
                    self.n_state,
                    self.multiple_of,
                    self.n_head,
                    self.n_kv_head,
                    self.norm_eps,
                )
                .with_ffn_dim_multiplier(self.ffn_dim_multiplier)
                .init()
            })
            .collect();

        let norm = RMSNormConfig::new(self.n_state, self.norm_eps).init();
        let output = nn::LinearConfig::new(self.n_state, self.n_vocab)
            .with_bias(false)
            .init();

        let mask = attn_decoder_mask(self.n_ctx).into();

        let n_vocab = self.n_vocab;
        let n_ctx = self.n_ctx;

        Llama {
            token_embedding,
            rotary_encoder,
            blocks,
            norm,
            output,
            mask,
            n_vocab,
            n_ctx,
        }
    }
}

#[derive(Module, Debug)]
pub struct Llama<B: Backend> {
    token_embedding: nn::Embedding<B>,
    rotary_encoder: RotaryEncoding<B>,
    blocks: Vec<ResidualDecoderAttentionBlock<B>>,
    norm: RMSNorm<B>,
    output: nn::Linear<B>,
    mask: Param<Tensor<B, 2>>,
    n_vocab: usize,
    n_ctx: usize,
}

impl<B: Backend> Llama<B> {
    pub fn forward(&self, x: Tensor<B, 2, Int>) -> Tensor<B, 3> {
        let [n_batch, seq_len] = x.dims();

        assert!(
            seq_len <= self.n_ctx,
            "Token sequence length {} must not exceed {}.",
            seq_len,
            self.n_ctx
        );

        let x = self.token_embedding.forward(x);

        let mut x = x;
        for block in &self.blocks {
            x = block.forward(x, &self.rotary_encoder, self.mask.val());
        }

        self.output.forward(self.norm.forward(x))
    }
}

#[derive(Config)]
pub struct ResidualDecoderAttentionBlockConfig {
    n_state: usize,
    multiple_of: usize,
    ffn_dim_multiplier: Option<usize>,
    n_head: usize,
    n_kv_head: usize,
    norm_eps: f64,
}

impl ResidualDecoderAttentionBlockConfig {
    fn init<B: Backend>(&self) -> ResidualDecoderAttentionBlock<B> {
        let attn =
            MultiHeadSelfAttentionConfig::new(self.n_state, self.n_head, self.n_kv_head).init();
        let attn_norm = RMSNormConfig::new(self.n_state, self.norm_eps).init();

        let mlp = MLPConfig::new(self.n_state, 4 * self.n_state, self.multiple_of)
            .with_ffn_dim_multiplier(self.ffn_dim_multiplier)
            .init();
        let mlp_norm = RMSNormConfig::new(self.n_state, self.norm_eps).init();

        ResidualDecoderAttentionBlock {
            attn,
            attn_norm,
            mlp,
            mlp_norm,
        }
    }
}

#[derive(Module, Debug)]
pub struct ResidualDecoderAttentionBlock<B: Backend> {
    attn: MultiHeadSelfAttention<B>,
    attn_norm: RMSNorm<B>,
    mlp: MLP<B>,
    mlp_norm: RMSNorm<B>,
}

impl<B: Backend> ResidualDecoderAttentionBlock<B> {
    fn forward(
        &self,
        x: Tensor<B, 3>,
        rotary_encoder: &RotaryEncoding<B>,
        mask: Tensor<B, 2>,
    ) -> Tensor<B, 3> {
        let x = x.clone()
            + self
                .attn
                .forward(self.attn_norm.forward(x), rotary_encoder, Some(mask));
        let x = x.clone() + self.mlp.forward(self.mlp_norm.forward(x));
        return x;
    }
}

#[derive(Config)]
pub struct MLPConfig {
    n_state: usize,
    n_state_hidden: usize,
    multiple_of: usize,
    ffn_dim_multiplier: Option<usize>,
}

impl MLPConfig {
    fn init<B: Backend>(&self) -> MLP<B> {
        let mut hidden_dim = 2 * self.n_state_hidden / 3;
        if let Some(ffn_dim_multiplier) = self.ffn_dim_multiplier {
            hidden_dim = ffn_dim_multiplier * hidden_dim;
        }
        hidden_dim = self.multiple_of * ((hidden_dim + self.multiple_of - 1) / self.multiple_of);

        let w1 = nn::LinearConfig::new(self.n_state, hidden_dim)
            .with_bias(false)
            .init();
        let w2 = nn::LinearConfig::new(hidden_dim, self.n_state)
            .with_bias(false)
            .init();
        let w3 = nn::LinearConfig::new(self.n_state, hidden_dim)
            .with_bias(false)
            .init();

        let silu = SILU::new();

        MLP { w1, w2, w3, silu }
    }
}

#[derive(Module, Debug)]
pub struct MLP<B: Backend> {
    w1: nn::Linear<B>,
    w2: nn::Linear<B>,
    w3: nn::Linear<B>,
    silu: SILU,
}

impl<B: Backend> MLP<B> {
    fn forward(&self, x: Tensor<B, 3>) -> Tensor<B, 3> {
        self.w2
            .forward(self.silu.forward(self.w1.forward(x.clone())) * self.w3.forward(x))
    }
}

#[derive(Config)]
pub struct MultiHeadSelfAttentionConfig {
    n_state: usize,
    n_head: usize,
    n_kv_head: usize,
}

impl MultiHeadSelfAttentionConfig {
    fn init<B: Backend>(&self) -> MultiHeadSelfAttention<B> {
        assert!(
            self.n_state % self.n_head == 0,
            "State size {} must be a multiple of the number of heads {}",
            self.n_state,
            self.n_head
        );
        assert!(
            self.n_head % self.n_kv_head == 0,
            "The number of query heads {} must be a multiple of the number of k/v heads {}",
            self.n_head,
            self.n_kv_head
        );

        let n_head_dim = self.n_state / self.n_head;

        let n_head = self.n_head;
        let n_kv_head = self.n_kv_head;
        let query = nn::LinearConfig::new(self.n_state, self.n_state)
            .with_bias(false)
            .init();
        let key = nn::LinearConfig::new(self.n_state, n_kv_head * n_head_dim)
            .with_bias(false)
            .init();
        let value = nn::LinearConfig::new(self.n_state, n_kv_head * n_head_dim)
            .with_bias(false)
            .init();
        let out = nn::LinearConfig::new(self.n_state, self.n_state)
            .with_bias(false)
            .init();

        MultiHeadSelfAttention {
            n_head,
            n_kv_head,
            query,
            key,
            value,
            out,
        }
    }
}

#[derive(Module, Debug)]
pub struct MultiHeadSelfAttention<B: Backend> {
    n_head: usize,
    n_kv_head: usize,
    query: nn::Linear<B>,
    key: nn::Linear<B>,
    value: nn::Linear<B>,
    out: nn::Linear<B>,
}

impl<B: Backend> MultiHeadSelfAttention<B> {
    fn forward(
        &self,
        x: Tensor<B, 3>,
        rotary_encoder: &RotaryEncoding<B>,
        mask: Option<Tensor<B, 2>>,
    ) -> Tensor<B, 3> {
        let q = self.query.forward(x.clone());
        let k = self.key.forward(x.clone());
        let v = self.value.forward(x);

        let wv = qkv_attention_rotary(q, k, v, mask, self.n_head, self.n_kv_head, rotary_encoder);

        return self.out.forward(wv);
    }
}

fn qkv_attention_rotary<B: Backend>(
    q: Tensor<B, 3>,
    k: Tensor<B, 3>,
    v: Tensor<B, 3>,
    mask: Option<Tensor<B, 2>>,
    n_head: usize,
    n_kv_head: usize,
    rotary_encoder: &RotaryEncoding<B>,
) -> Tensor<B, 3> {
    let [n_batch, n_qctx, n_state] = q.dims();
    let [_, n_ctx, _] = k.dims();

    let n_hstate = n_state / n_head;
    let scale = (n_hstate as f64).powf(-0.25); // keeps the value weightings roughly normally distributed

    let q: Tensor<B, 4> = q.reshape([n_batch, n_qctx, n_head, n_hstate]);
    // interleave kv heads to match the number of q heads
    let n_repeat = n_head / n_kv_head;
    let k = repeat_kv(k.reshape([n_batch, n_ctx, n_kv_head, n_hstate]), n_repeat);
    let v = repeat_kv(v.reshape([n_batch, n_ctx, n_kv_head, n_hstate]), n_repeat);

    // the last two dims need to be (n_ctx, n_hstate)
    let q = rotary_encoder.forward(q.swap_dims(1, 2)) * scale;
    let k = rotary_encoder.forward(k.swap_dims(1, 2)) * scale;
    let v = v.swap_dims(1, 2);

    // compute value weightings
    let qk = q.matmul(k.transpose());

    // apply mask
    let qk = if let Some(mask) = mask {
        qk + mask.slice([0..n_qctx, 0..n_ctx]).unsqueeze::<4>()
    } else {
        qk
    };

    // normalize value weightings
    let w = softmax(qk, 3);
    let o = w.matmul(v).swap_dims(1, 2).flatten(2, 3);

    return o;
}

/// For a tensor of size (n_batch, n_ctx, n_kv_head, n_hstate), repeats the head keys or values in an interleaving manner so that the number
/// of heads is effectively multiplied by n_repeat
fn repeat_kv<B: Backend>(x: Tensor<B, 4>, n_repeat: usize) -> Tensor<B, 4> {
    if n_repeat > 1 {
        let [n_batch, n_ctx, n_kv_head, n_hstate] = x.dims();
        x.repeat(3, n_repeat)
            .reshape([n_batch, n_ctx, n_kv_head * n_repeat, n_hstate])
    } else {
        x
    }
}

/// Generates a strictly upper triangular matrix filled with -inf that when added to an attention weight matrix prevents
/// vectors from attending to other vectors further in the sequence, preventing future information from flowing into the past
pub fn attn_decoder_mask<B: Backend>(seq_length: usize) -> Tensor<B, 2> {
    let mut mask = Tensor::<B, 2>::zeros([seq_length, seq_length]);

    for i in 0..(seq_length - 1) {
        let values = Tensor::<B, 2>::zeros([1, seq_length - (i + 1)]).add_scalar(NEG_INFINITY);
        mask = mask.slice_assign([i..i + 1, i + 1..seq_length], values);
    }

    return mask;
}

#[derive(Config, Debug)]
pub struct RotaryEncodingConfig {
    max_sequence_length: usize,
    state_size: usize,
    theta: f64,
}

impl RotaryEncodingConfig {
    pub fn init<B: Backend>(&self) -> RotaryEncoding<B> {
        assert!(self.state_size % 2 == 0, "Head dims must be even.");
        assert!(self.theta > 0.0, "Theta must be positive.");

        let half_state_size = self.state_size / 2;

        let arange_m = Tensor::from_floats([[1.0, 0.0, 0.0, 1.0], [0.0, -1.0, 1.0, 0.0]]).into();

        let inv_freq = powto(
            self.theta,
            Tensor::arange(0..half_state_size).float() * (2.0 / self.state_size as f64),
        )
        .powf(-1.0);

        let periods = Tensor::arange(0..self.max_sequence_length)
            .float()
            .unsqueeze::<2>()
            .transpose()
            .repeat(1, half_state_size)
            * inv_freq.unsqueeze();

        let p_cos = periods.clone().cos();
        let p_sin = periods.sin();
        let tensor=Tensor::cat(vec![p_cos, p_sin], 1);
        
        let tensor2=tensor.reshape([self.max_sequence_length,2,half_state_size]);

        let tensor3=tensor2.transpose();

        let tensor41=tensor3.repeat(2, 2);

        let tensor5=tensor41.reshape([self.max_sequence_length,self.state_size,2]);

        let freq_cis=tensor5.into();

        RotaryEncoding { arange_m, freq_cis }
    }

}

fn powto<B: Backend, const D: usize>(base: f64, x: Tensor<B, D>) -> Tensor<B, D> {
    let logbase = base.ln();
    x.mul_scalar(logbase).exp()
}

/// Conceptually, pairs the values of a vector (v0 v1 v2 ... vn) into complex numbers (c0 c1 c2 ... cn/2)
/// which are then rotated counter-clockwise by the angle seq_index / theta^(2*pair_index/n).
/// This encodes sequence positions in a way that is agnostic to the maximum sequence length
/// which potentially allows for arbitrarily long sequences without retraining.
#[derive(Module, Debug)]
pub struct RotaryEncoding<B: Backend> {
    arange_m: Param<Tensor<B, 2>>,
    freq_cis: Param<Tensor<B, 3>>,
}

impl<B: Backend> RotaryEncoding<B> {
    /// Applies rotary positional encoding to a tensor of dimenions (..., seq_len, n_state)
    fn forward<const D: usize>(&self, x: Tensor<B, D>) -> Tensor<B, D> {
        assert!(D >= 2);
        let orig_shape = x.shape();
        let (n_ctx, n_state) = (orig_shape.dims[D - 2], orig_shape.dims[D - 1]);
        let dummy_dim_size = orig_shape.num_elements() / (n_ctx * n_state);

        let out = x
            .reshape([dummy_dim_size, n_ctx, n_state / 2, 2])
            .matmul(self.arange_m.val().unsqueeze())
            .reshape([dummy_dim_size, n_ctx, n_state, 2])
            * self.freq_cis.val().slice([0..n_ctx]).unsqueeze();

        out.sum_dim(D - 1).reshape(orig_shape)
    }
}

#[derive(Config)]
pub struct RMSNormConfig {
    layer_size: usize,
    eps: f64,
}

impl RMSNormConfig {
    fn init<B: Backend>(&self) -> RMSNorm<B> {
        assert!(self.eps > 0.0, "eps must be positive.");

        let weight = Tensor::ones([self.layer_size]);
        let eps = self.eps;

        RMSNorm { weight, eps }
    }
}

#[derive(Module, Debug)]
pub struct RMSNorm<B: Backend> {
    weight: Tensor<B, 1>,
    eps: f64,
}

impl<B: Backend> RMSNorm<B> {
    fn forward<const D: usize>(&self, x: Tensor<B, D>) -> Tensor<B, D> {
        let rms = (x.clone().powf(2.0).mean_dim(D - 1) + self.eps).sqrt();
        (x / rms) * self.weight.clone().unsqueeze()
    }
}

#[derive(Module, Clone, Debug)]
pub struct SILU {}

impl SILU {
    fn new() -> Self {
        Self {}
    }

    fn forward<B: Backend, const D: usize>(&self, x: Tensor<B, D>) -> Tensor<B, D> {
        x.clone() * sigmoid(x)
    }
}

use npy::{self, NpyData}; //TODO: NpyData is deprecated, use ndarray_npy instead, but before replace it, I want to make sure the project works well.
use num_traits::cast::ToPrimitive; // TODO: Same here.
use std::error::Error;
use std::io::Read;

use burn::tensor::ElementConversion;

fn numpy_to_tensor<B: Backend, const D: usize>(
    numpy_data: NpyData<f32>,
    device: &B::Device,
) -> Tensor<B, D> {
    let mut v = numpy_data.to_vec();

    let shape: Vec<_> = v[0..D].into_iter().map(|&v| v as usize).collect();
    let data: Vec<B::FloatElem> = v[D..].into_iter().map(|e| e.elem()).collect();

    Tensor::from_data_device(Data::new(data, shape.into()), device)
}

fn load_tensor<B: Backend, const D: usize>(
    name: &str,
    path: &str,
    device: &B::Device,
) -> Result<Tensor<B, D>, Box<dyn Error>> {
    let tensor_path = format!("{}/{}.npy", path, name);

    let mut buf = vec![];
    std::fs::File::open(&tensor_path)?.read_to_end(&mut buf)?;

    let tensor_numpy: NpyData<f32> = NpyData::from_bytes(&buf)?;

    let tensor = numpy_to_tensor(tensor_numpy, device);

    println!("{}", tensor_path);

    Ok(tensor)
}

fn load_f32<B: Backend>(name: &str, path: &str, device: &B::Device) -> Result<f32, Box<dyn Error>> {
    load_tensor::<B, 1>(name, path, device).map(|t| t.into_scalar().to_f32().unwrap())
}

fn load_usize<B: Backend>(
    name: &str,
    path: &str,
    device: &B::Device,
) -> Result<usize, Box<dyn Error>> {
    load_tensor::<B, 1>(name, path, device).map(|t| t.into_scalar().to_usize().unwrap())
}

fn load_linear<B: Backend>(
    path: &str,
    device: &B::Device,
) -> Result<nn::Linear<B>, Box<dyn Error>> {
    let weight = load_tensor::<B, 2>("weight", path, device)?;
    let bias = load_tensor::<B, 1>("bias", path, device).ok();

    let record = nn::LinearRecord {
        weight: weight.into(),
        bias: bias.map(|t| t.into()),
    };

    let linear: nn::Linear<B> = nn::LinearConfig::new(3, 3).init_with(record);
    Ok(linear)
}

fn load_rmsnorm<B: Backend>(path: &str, device: &B::Device) -> Result<RMSNorm<B>, Box<dyn Error>> {
    let weight = load_tensor::<B, 1>("weight", path, device)?;
    let eps = load_f32::<B>("eps", path, device)?.into();

    let rmsnorm = RMSNorm {
        weight: weight.into(),
        eps: eps,
    };

    Ok(rmsnorm)
}

fn load_attention<B: Backend>(
    path: &str,
    device: &B::Device,
) -> Result<MultiHeadSelfAttention<B>, Box<dyn Error>> {
    let query = load_linear(&format!("{}/{}", path, "wq"), device)?;
    let key = load_linear(&format!("{}/{}", path, "wk"), device)?;
    let value = load_linear(&format!("{}/{}", path, "wv"), device)?;
    let out = load_linear(&format!("{}/{}", path, "wo"), device)?;

    let n_head = load_usize::<B>("n_head", path, device)?;
    let n_kv_head = load_usize::<B>("n_kv_head", path, device)?;

    let attention = MultiHeadSelfAttention {
        n_head: n_head,
        n_kv_head: n_kv_head,
        query: query,
        key: key,
        value: value,
        out: out,
    };

    Ok(attention)
}

fn load_feedforward<B: Backend>(path: &str, device: &B::Device) -> Result<MLP<B>, Box<dyn Error>> {
    let w1 = load_linear(&format!("{}/{}", path, "w1"), device)?;
    let w2 = load_linear(&format!("{}/{}", path, "w2"), device)?;
    let w3 = load_linear(&format!("{}/{}", path, "w3"), device)?;

    let mlp = MLP {
        w1: w1,
        w2: w2,
        w3: w3,
        silu: SILU::new(),
    };

    Ok(mlp)
}

fn load_transformer_block<B: Backend>(
    path: &str,
    device: &B::Device,
) -> Result<ResidualDecoderAttentionBlock<B>, Box<dyn Error>> {
    let attn = load_attention(&format!("{}/{}", path, "attention"), device)?;
    let attn_norm = load_rmsnorm(&format!("{}/{}", path, "attention_norm"), device)?;
    let mlp = load_feedforward(&format!("{}/{}", path, "feedforward"), device)?;
    let mlp_norm = load_rmsnorm(&format!("{}/{}", path, "ffn_norm"), device)?;

    let block = ResidualDecoderAttentionBlock {
        attn: attn,
        attn_norm: attn_norm,
        mlp: mlp,
        mlp_norm: mlp_norm,
    };

    Ok(block)
}

use burn::nn::{EmbeddingConfig, EmbeddingRecord};

pub fn load_llama_dump<B: Backend>(
    path: &str,
    device: &B::Device,
) -> Result<(Llama<B>, LlamaConfig), Box<dyn Error>> {
    let mut blocks: Vec<ResidualDecoderAttentionBlock<B>> = vec![];
    let n_layer = load_usize::<B>("n_layer", path, device)?;
    for i in 0..n_layer {
        let block = load_transformer_block(&format!("{}/layer{}", path, i), device)?;
        blocks.push(block);
    }

    let n_ctx = load_usize::<B>("n_ctx", path, device)?;
    let theta = load_f32::<B>("theta", path, device)?;
    let multiple_of = load_usize::<B>("multiple_of", path, device)?;
    let ffn_dim_multiplier = load_usize::<B>("ffn_dim_multiplier", path, device).ok();

    let token_embedding = load_tensor("tok_embeddings/weight", path, device)?;
    let [n_vocab, n_state] = token_embedding.dims();
    let n_head = blocks[0].attn.n_head;
    let n_kv_head = blocks[0].attn.n_kv_head;
    let head_dim = n_state / n_head;

    let token_embedding = EmbeddingConfig::new(n_vocab, n_state).init_with(EmbeddingRecord {
        weight: token_embedding.into(),
    });
    let rotary_encoding = RotaryEncodingConfig::new(n_ctx, head_dim, theta.into()).init();

    let norm = load_rmsnorm(&format!("{}/{}", path, "norm"), device)?;
    let output = load_linear(&format!("{}/{}", path, "output"), device)?;
    let mask = attn_decoder_mask(n_ctx).into();

    let norm_eps = norm.eps;

    let llama = Llama {
        token_embedding: token_embedding,
        rotary_encoder: rotary_encoding,
        blocks: blocks,
        norm: norm,
        output: output,
        mask: mask,
        n_vocab: n_vocab,
        n_ctx: n_ctx,
    };

    let llama_config = LlamaConfig::new(
        n_vocab,
        n_ctx,
        n_state,
        multiple_of,
        n_head,
        n_kv_head,
        n_layer,
    )
    .with_norm_eps(norm_eps)
    .with_ffn_dim_multiplier(ffn_dim_multiplier);

    Ok((llama, llama_config))
}


#[cfg(test)]
mod tests{
    use super::*;

    #[test]
    fn test_feq_cis_reshape(){
        use burn::backend::WgpuBackend;
        use burn::backend::wgpu::{AutoGraphicsApi};

        type Backend = WgpuBackend<AutoGraphicsApi,f32,i32>;

        let config= RotaryEncodingConfig{
            max_sequence_length: 10,
            state_size: 4,
            theta: 1.0,
        };

        let encoding=config.init::<Backend>();

        assert_eq!(encoding.freq_cis.dims(),[10,4,2]);
        assert_eq!(encoding.arange_m.dims(),[2,4]);
    }

    #[test]
    fn test_rotary_encoding_config_init(){
        use burn::backend::WgpuBackend;
        use burn::backend::wgpu::{AutoGraphicsApi};

        type Backend = WgpuBackend<AutoGraphicsApi,f32,i32>;

        let config = RotaryEncodingConfig{
            state_size: 4,
            theta:1.0,
            max_sequence_length: 10,
        };

        let encoding=config.init::<Backend>();

        assert_eq!(encoding.arange_m.dims(),[2,4]);
        assert_eq!(encoding.freq_cis.dims(),[10,4,2]);

    }
}