# https://huggingface.co/hexgrad/Kokoro-82M/blob/main/istftnet.py
# https://github.com/yl4579/StyleTTS2/blob/main/Modules/istftnet.py
from scipy.signal import get_window
from torch.nn import Conv1d, ConvTranspose1d
from torch.nn.utils import weight_norm, remove_weight_norm
import numpy as np
import torch
import torch.nn as nn
import torch.nn.functional as F

# https://github.com/yl4579/StyleTTS2/blob/main/Modules/utils.py
def init_weights(m, mean=0.0, std=0.01):
    classname = m.__class__.__name__
    if classname.find("Conv") != -1:
        m.weight.data.normal_(mean, std)

def get_padding(kernel_size, dilation=1):
    return int((kernel_size*dilation - dilation)/2)

LRELU_SLOPE = 0.1

class AdaIN1d(nn.Module):
    def __init__(self, style_dim, num_features):
        super().__init__()
        self.norm = nn.InstanceNorm1d(num_features, affine=False)
        self.fc = nn.Linear(style_dim, num_features*2)

    def forward(self, x, s):
        h = self.fc(s)
        h = h.view(h.size(0), h.size(1), 1)
        gamma, beta = torch.chunk(h, chunks=2, dim=1)
        return (1 + gamma) * self.norm(x) + beta

class AdaINResBlock1(torch.nn.Module):
    def __init__(self, channels, kernel_size=3, dilation=(1, 3, 5), style_dim=64):
        super(AdaINResBlock1, self).__init__()
        self.convs1 = nn.ModuleList([
            weight_norm(Conv1d(channels, channels, kernel_size, 1, dilation=dilation[0],
                               padding=get_padding(kernel_size, dilation[0]))),
            weight_norm(Conv1d(channels, channels, kernel_size, 1, dilation=dilation[1],
                               padding=get_padding(kernel_size, dilation[1]))),
            weight_norm(Conv1d(channels, channels, kernel_size, 1, dilation=dilation[2],
                               padding=get_padding(kernel_size, dilation[2])))
        ])
        self.convs1.apply(init_weights)

        self.convs2 = nn.ModuleList([
            weight_norm(Conv1d(channels, channels, kernel_size, 1, dilation=1,
                               padding=get_padding(kernel_size, 1))),
            weight_norm(Conv1d(channels, channels, kernel_size, 1, dilation=1,
                               padding=get_padding(kernel_size, 1))),
            weight_norm(Conv1d(channels, channels, kernel_size, 1, dilation=1,
                               padding=get_padding(kernel_size, 1)))
        ])
        self.convs2.apply(init_weights)
        
        self.adain1 = nn.ModuleList([
            AdaIN1d(style_dim, channels),
            AdaIN1d(style_dim, channels),
            AdaIN1d(style_dim, channels),
        ])
        
        self.adain2 = nn.ModuleList([
            AdaIN1d(style_dim, channels),
            AdaIN1d(style_dim, channels),
            AdaIN1d(style_dim, channels),
        ])
        
        self.alpha1 = nn.ParameterList([nn.Parameter(torch.ones(1, channels, 1)) for i in range(len(self.convs1))])
        self.alpha2 = nn.ParameterList([nn.Parameter(torch.ones(1, channels, 1)) for i in range(len(self.convs2))])


    def forward(self, x, s):
        for c1, c2, n1, n2, a1, a2 in zip(self.convs1, self.convs2, self.adain1, self.adain2, self.alpha1, self.alpha2):
            xt = n1(x, s)
            xt = xt + (1 / a1) * (torch.sin(a1 * xt) ** 2)  # Snake1D
            xt = c1(xt)
            xt = n2(xt, s)
            xt = xt + (1 / a2) * (torch.sin(a2 * xt) ** 2)  # Snake1D
            xt = c2(xt)
            x = xt + x
        return x

    def remove_weight_norm(self):
        for l in self.convs1:
            remove_weight_norm(l)
        for l in self.convs2:
            remove_weight_norm(l)
            
class TorchSTFT(torch.nn.Module):
    def __init__(self, filter_length=800, hop_length=200, win_length=800, window='hann'):
        super().__init__()
        self.filter_length = filter_length
        self.hop_length = hop_length
        self.win_length = win_length
        self.window = torch.from_numpy(get_window(window, win_length, fftbins=True).astype(np.float32))

    def transform(self, input_data):
        forward_transform = torch.stft(
            input_data,
            self.filter_length, self.hop_length, self.win_length, window=self.window.to(input_data.device),
            return_complex=True)

        return torch.abs(forward_transform), torch.angle(forward_transform)

    def inverse(self, magnitude, phase):
        inverse_transform = torch.istft(
            magnitude * torch.exp(phase * 1j),
            self.filter_length, self.hop_length, self.win_length, window=self.window.to(magnitude.device))

        return inverse_transform.unsqueeze(-2)  # unsqueeze to stay consistent with conv_transpose1d implementation

    def forward(self, input_data):
        self.magnitude, self.phase = self.transform(input_data)
        reconstruction = self.inverse(self.magnitude, self.phase)
        return reconstruction
    
class SineGen(torch.nn.Module):
    """ Definition of sine generator
    SineGen(samp_rate, harmonic_num = 0,
            sine_amp = 0.1, noise_std = 0.003,
            voiced_threshold = 0,
            flag_for_pulse=False)
    samp_rate: sampling rate in Hz
    harmonic_num: number of harmonic overtones (default 0)
    sine_amp: amplitude of sine-wavefrom (default 0.1)
    noise_std: std of Gaussian noise (default 0.003)
    voiced_thoreshold: F0 threshold for U/V classification (default 0)
    flag_for_pulse: this SinGen is used inside PulseGen (default False)
    Note: when flag_for_pulse is True, the first time step of a voiced
        segment is always sin(np.pi) or cos(0)
    """

    def __init__(self, samp_rate, upsample_scale, harmonic_num=0,
                 sine_amp=0.1, noise_std=0.003,
                 voiced_threshold=0,
                 flag_for_pulse=False):
        super(SineGen, self).__init__()
        self.sine_amp = sine_amp
        self.noise_std = noise_std
        self.harmonic_num = harmonic_num
        self.dim = self.harmonic_num + 1
        self.sampling_rate = samp_rate
        self.voiced_threshold = voiced_threshold
        self.flag_for_pulse = flag_for_pulse
        self.upsample_scale = upsample_scale

    def _f02uv(self, f0):
        # generate uv signal
        uv = (f0 > self.voiced_threshold).type(torch.float32)
        return uv

    def _f02sine(self, f0_values):
        """ f0_values: (batchsize, length, dim)
            where dim indicates fundamental tone and overtones
        """
        # convert to F0 in rad. The integer part n can be ignored
        # because 2 * np.pi * n doesn't affect phase
        rad_values = (f0_values / self.sampling_rate) % 1

        # initial phase noise (no noise for fundamental component)
        rand_ini = torch.rand(f0_values.shape[0], f0_values.shape[2], \
                              device=f0_values.device)
        rand_ini[:, 0] = 0
        rad_values[:, 0, :] = rad_values[:, 0, :] + rand_ini

        # instantanouse phase sine[t] = sin(2*pi \sum_i=1 ^{t} rad)
        if not self.flag_for_pulse:
#             # for normal case

#             # To prevent torch.cumsum numerical overflow,
#             # it is necessary to add -1 whenever \sum_k=1^n rad_value_k > 1.
#             # Buffer tmp_over_one_idx indicates the time step to add -1.
#             # This will not change F0 of sine because (x-1) * 2*pi = x * 2*pi
#             tmp_over_one = torch.cumsum(rad_values, 1) % 1
#             tmp_over_one_idx = (padDiff(tmp_over_one)) < 0
#             cumsum_shift = torch.zeros_like(rad_values)
#             cumsum_shift[:, 1:, :] = tmp_over_one_idx * -1.0

#             phase = torch.cumsum(rad_values, dim=1) * 2 * np.pi
            rad_values = torch.nn.functional.interpolate(rad_values.transpose(1, 2), 
                                                         scale_factor=1/self.upsample_scale, 
                                                         mode="linear").transpose(1, 2)
    
#             tmp_over_one = torch.cumsum(rad_values, 1) % 1
#             tmp_over_one_idx = (padDiff(tmp_over_one)) < 0
#             cumsum_shift = torch.zeros_like(rad_values)
#             cumsum_shift[:, 1:, :] = tmp_over_one_idx * -1.0
    
            phase = torch.cumsum(rad_values, dim=1) * 2 * np.pi
            phase = torch.nn.functional.interpolate(phase.transpose(1, 2) * self.upsample_scale, 
                                                    scale_factor=self.upsample_scale, mode="linear").transpose(1, 2)
            sines = torch.sin(phase)
            
        else:
            # If necessary, make sure that the first time step of every
            # voiced segments is sin(pi) or cos(0)
            # This is used for pulse-train generation

            # identify the last time step in unvoiced segments
            uv = self._f02uv(f0_values)
            uv_1 = torch.roll(uv, shifts=-1, dims=1)
            uv_1[:, -1, :] = 1
            u_loc = (uv < 1) * (uv_1 > 0)

            # get the instantanouse phase
            tmp_cumsum = torch.cumsum(rad_values, dim=1)
            # different batch needs to be processed differently
            for idx in range(f0_values.shape[0]):
                temp_sum = tmp_cumsum[idx, u_loc[idx, :, 0], :]
                temp_sum[1:, :] = temp_sum[1:, :] - temp_sum[0:-1, :]
                # stores the accumulation of i.phase within
                # each voiced segments
                tmp_cumsum[idx, :, :] = 0
                tmp_cumsum[idx, u_loc[idx, :, 0], :] = temp_sum

            # rad_values - tmp_cumsum: remove the accumulation of i.phase
            # within the previous voiced segment.
            i_phase = torch.cumsum(rad_values - tmp_cumsum, dim=1)

            # get the sines
            sines = torch.cos(i_phase * 2 * np.pi)
        return sines

    def forward(self, f0):
        """ sine_tensor, uv = forward(f0)
        input F0: tensor(batchsize=1, length, dim=1)
                  f0 for unvoiced steps should be 0
        output sine_tensor: tensor(batchsize=1, length, dim)
        output uv: tensor(batchsize=1, length, 1)
        """
        f0_buf = torch.zeros(f0.shape[0], f0.shape[1], self.dim,
                             device=f0.device)
        # fundamental component
        fn = torch.multiply(f0, torch.FloatTensor([[range(1, self.harmonic_num + 2)]]).to(f0.device))

        # generate sine waveforms
        sine_waves = self._f02sine(fn) * self.sine_amp

        # generate uv signal
        # uv = torch.ones(f0.shape)
        # uv = uv * (f0 > self.voiced_threshold)
        uv = self._f02uv(f0)

        # noise: for unvoiced should be similar to sine_amp
        #        std = self.sine_amp/3 -> max value ~ self.sine_amp
        # .       for voiced regions is self.noise_std
        noise_amp = uv * self.noise_std + (1 - uv) * self.sine_amp / 3
        noise = noise_amp * torch.randn_like(sine_waves)

        # first: set the unvoiced part to 0 by uv
        # then: additive noise
        sine_waves = sine_waves * uv + noise
        return sine_waves, uv, noise


class SourceModuleHnNSF(torch.nn.Module):
    """ SourceModule for hn-nsf
    SourceModule(sampling_rate, harmonic_num=0, sine_amp=0.1,
                 add_noise_std=0.003, voiced_threshod=0)
    sampling_rate: sampling_rate in Hz
    harmonic_num: number of harmonic above F0 (default: 0)
    sine_amp: amplitude of sine source signal (default: 0.1)
    add_noise_std: std of additive Gaussian noise (default: 0.003)
        note that amplitude of noise in unvoiced is decided
        by sine_amp
    voiced_threshold: threhold to set U/V given F0 (default: 0)
    Sine_source, noise_source = SourceModuleHnNSF(F0_sampled)
    F0_sampled (batchsize, length, 1)
    Sine_source (batchsize, length, 1)
    noise_source (batchsize, length 1)
    uv (batchsize, length, 1)
    """

    def __init__(self, sampling_rate, upsample_scale, harmonic_num=0, sine_amp=0.1,
                 add_noise_std=0.003, voiced_threshod=0):
        super(SourceModuleHnNSF, self).__init__()

        self.sine_amp = sine_amp
        self.noise_std = add_noise_std

        # to produce sine waveforms
        self.l_sin_gen = SineGen(sampling_rate, upsample_scale, harmonic_num,
                                 sine_amp, add_noise_std, voiced_threshod)

        # to merge source harmonics into a single excitation
        self.l_linear = torch.nn.Linear(harmonic_num + 1, 1)
        self.l_tanh = torch.nn.Tanh()

    def forward(self, x):
        """
        Sine_source, noise_source = SourceModuleHnNSF(F0_sampled)
        F0_sampled (batchsize, length, 1)
        Sine_source (batchsize, length, 1)
        noise_source (batchsize, length 1)
        """
        # source for harmonic branch
        with torch.no_grad():
            sine_wavs, uv, _ = self.l_sin_gen(x)
        sine_merge = self.l_tanh(self.l_linear(sine_wavs))

        # source for noise branch, in the same shape as uv
        noise = torch.randn_like(uv) * self.sine_amp / 3
        return sine_merge, noise, uv
def padDiff(x):
    return F.pad(F.pad(x, (0,0,-1,1), 'constant', 0) - x, (0,0,0,-1), 'constant', 0)

    
class Generator(torch.nn.Module):
    def __init__(self, style_dim, resblock_kernel_sizes, upsample_rates, upsample_initial_channel, resblock_dilation_sizes, upsample_kernel_sizes, gen_istft_n_fft, gen_istft_hop_size):
        super(Generator, self).__init__()

        self.num_kernels = len(resblock_kernel_sizes)
        self.num_upsamples = len(upsample_rates)
        resblock = AdaINResBlock1

        self.m_source = SourceModuleHnNSF(
                    sampling_rate=24000,
                    upsample_scale=np.prod(upsample_rates) * gen_istft_hop_size,
                    harmonic_num=8, voiced_threshod=10)
        self.f0_upsamp = torch.nn.Upsample(scale_factor=np.prod(upsample_rates) * gen_istft_hop_size)
        self.noise_convs = nn.ModuleList()
        self.noise_res = nn.ModuleList()
        
        self.ups = nn.ModuleList()
        for i, (u, k) in enumerate(zip(upsample_rates, upsample_kernel_sizes)):
            self.ups.append(weight_norm(
                ConvTranspose1d(upsample_initial_channel//(2**i), upsample_initial_channel//(2**(i+1)),
                                k, u, padding=(k-u)//2)))

        self.resblocks = nn.ModuleList()
        for i in range(len(self.ups)):
            ch = upsample_initial_channel//(2**(i+1))
            for j, (k, d) in enumerate(zip(resblock_kernel_sizes,resblock_dilation_sizes)):
                self.resblocks.append(resblock(ch, k, d, style_dim))
                
            c_cur = upsample_initial_channel // (2 ** (i + 1))
            
            if i + 1 < len(upsample_rates):  #
                stride_f0 = np.prod(upsample_rates[i + 1:])
                self.noise_convs.append(Conv1d(
                    gen_istft_n_fft + 2, c_cur, kernel_size=stride_f0 * 2, stride=stride_f0, padding=(stride_f0+1) // 2))
                self.noise_res.append(resblock(c_cur, 7, [1,3,5], style_dim))
            else:
                self.noise_convs.append(Conv1d(gen_istft_n_fft + 2, c_cur, kernel_size=1))
                self.noise_res.append(resblock(c_cur, 11, [1,3,5], style_dim))
                
                
        self.post_n_fft = gen_istft_n_fft
        self.conv_post = weight_norm(Conv1d(ch, self.post_n_fft + 2, 7, 1, padding=3))
        self.ups.apply(init_weights)
        self.conv_post.apply(init_weights)
        self.reflection_pad = torch.nn.ReflectionPad1d((1, 0))
        self.stft = TorchSTFT(filter_length=gen_istft_n_fft, hop_length=gen_istft_hop_size, win_length=gen_istft_n_fft)
        
        
    def forward(self, x, s, f0):
        with torch.no_grad():
            f0 = self.f0_upsamp(f0[:, None]).transpose(1, 2)  # bs,n,t

            har_source, noi_source, uv = self.m_source(f0)
            har_source = har_source.transpose(1, 2).squeeze(1)
            har_spec, har_phase = self.stft.transform(har_source)
            har = torch.cat([har_spec, har_phase], dim=1)
        
        for i in range(self.num_upsamples):
            x = F.leaky_relu(x, LRELU_SLOPE)
            x_source = self.noise_convs[i](har)
            x_source = self.noise_res[i](x_source, s)

            x = self.ups[i](x)
            if i == self.num_upsamples - 1:
                x = self.reflection_pad(x)

            x = x + x_source
            xs = None
            for j in range(self.num_kernels):
                if xs is None:
                    xs = self.resblocks[i*self.num_kernels+j](x, s)
                else:
                    xs += self.resblocks[i*self.num_kernels+j](x, s)
            x = xs / self.num_kernels
        x = F.leaky_relu(x)
        x = self.conv_post(x)
        spec = torch.exp(x[:,:self.post_n_fft // 2 + 1, :])
        phase = torch.sin(x[:, self.post_n_fft // 2 + 1:, :])
        return self.stft.inverse(spec, phase)
    
    def fw_phase(self, x, s):
        for i in range(self.num_upsamples):
            x = F.leaky_relu(x, LRELU_SLOPE)
            x = self.ups[i](x)
            xs = None
            for j in range(self.num_kernels):
                if xs is None:
                    xs = self.resblocks[i*self.num_kernels+j](x, s)
                else:
                    xs += self.resblocks[i*self.num_kernels+j](x, s)
            x = xs / self.num_kernels
        x = F.leaky_relu(x)
        x = self.reflection_pad(x)
        x = self.conv_post(x)
        spec = torch.exp(x[:,:self.post_n_fft // 2 + 1, :])
        phase = torch.sin(x[:, self.post_n_fft // 2 + 1:, :])
        return spec, phase

    def remove_weight_norm(self):
        print('Removing weight norm...')
        for l in self.ups:
            remove_weight_norm(l)
        for l in self.resblocks:
            l.remove_weight_norm()
        remove_weight_norm(self.conv_pre)
        remove_weight_norm(self.conv_post)

        
class AdainResBlk1d(nn.Module):
    def __init__(self, dim_in, dim_out, style_dim=64, actv=nn.LeakyReLU(0.2),
                 upsample='none', dropout_p=0.0):
        super().__init__()
        self.actv = actv
        self.upsample_type = upsample
        self.upsample = UpSample1d(upsample)
        self.learned_sc = dim_in != dim_out
        self._build_weights(dim_in, dim_out, style_dim)
        self.dropout = nn.Dropout(dropout_p)
        
        if upsample == 'none':
            self.pool = nn.Identity()
        else:
            self.pool = weight_norm(nn.ConvTranspose1d(dim_in, dim_in, kernel_size=3, stride=2, groups=dim_in, padding=1, output_padding=1))
        
        
    def _build_weights(self, dim_in, dim_out, style_dim):
        self.conv1 = weight_norm(nn.Conv1d(dim_in, dim_out, 3, 1, 1))
        self.conv2 = weight_norm(nn.Conv1d(dim_out, dim_out, 3, 1, 1))
        self.norm1 = AdaIN1d(style_dim, dim_in)
        self.norm2 = AdaIN1d(style_dim, dim_out)
        if self.learned_sc:
            self.conv1x1 = weight_norm(nn.Conv1d(dim_in, dim_out, 1, 1, 0, bias=False))

    def _shortcut(self, x):
        x = self.upsample(x)
        if self.learned_sc:
            x = self.conv1x1(x)
        return x

    def _residual(self, x, s):
        x = self.norm1(x, s)
        x = self.actv(x)
        x = self.pool(x)
        x = self.conv1(self.dropout(x))
        x = self.norm2(x, s)
        x = self.actv(x)
        x = self.conv2(self.dropout(x))
        return x

    def forward(self, x, s):
        out = self._residual(x, s)
        out = (out + self._shortcut(x)) / np.sqrt(2)
        return out
    
class UpSample1d(nn.Module):
    def __init__(self, layer_type):
        super().__init__()
        self.layer_type = layer_type

    def forward(self, x):
        if self.layer_type == 'none':
            return x
        else:
            return F.interpolate(x, scale_factor=2, mode='nearest')

class Decoder(nn.Module):
    def __init__(self, dim_in=512, F0_channel=512, style_dim=64, dim_out=80, 
                resblock_kernel_sizes = [3,7,11],
                upsample_rates = [10, 6],
                upsample_initial_channel=512,
                resblock_dilation_sizes=[[1,3,5], [1,3,5], [1,3,5]],
                upsample_kernel_sizes=[20, 12], 
                gen_istft_n_fft=20, gen_istft_hop_size=5):
        super().__init__()
        
        self.decode = nn.ModuleList()
        
        self.encode = AdainResBlk1d(dim_in + 2, 1024, style_dim)
        
        self.decode.append(AdainResBlk1d(1024 + 2 + 64, 1024, style_dim))
        self.decode.append(AdainResBlk1d(1024 + 2 + 64, 1024, style_dim))
        self.decode.append(AdainResBlk1d(1024 + 2 + 64, 1024, style_dim))
        self.decode.append(AdainResBlk1d(1024 + 2 + 64, 512, style_dim, upsample=True))

        self.F0_conv = weight_norm(nn.Conv1d(1, 1, kernel_size=3, stride=2, groups=1, padding=1))
        
        self.N_conv = weight_norm(nn.Conv1d(1, 1, kernel_size=3, stride=2, groups=1, padding=1))
        
        self.asr_res = nn.Sequential(
            weight_norm(nn.Conv1d(512, 64, kernel_size=1)),
        )
        
        
        self.generator = Generator(style_dim, resblock_kernel_sizes, upsample_rates, 
                                   upsample_initial_channel, resblock_dilation_sizes, 
                                   upsample_kernel_sizes, gen_istft_n_fft, gen_istft_hop_size)
        
    def forward(self, asr, F0_curve, N, s):
        F0 = self.F0_conv(F0_curve.unsqueeze(1))
        N = self.N_conv(N.unsqueeze(1))
        
        x = torch.cat([asr, F0, N], axis=1)
        x = self.encode(x, s)
        
        asr_res = self.asr_res(asr)
        
        res = True
        for block in self.decode:
            if res:
                x = torch.cat([x, asr_res, F0, N], axis=1)
            x = block(x, s)
            if block.upsample_type != "none":
                res = False
                
        x = self.generator(x, s, F0_curve)
        return x
