# https://huggingface.co/hexgrad/Kokoro-82M/blob/main/plbert.py
# https://github.com/yl4579/StyleTTS2/blob/main/Utils/PLBERT/util.py
from transformers import AlbertConfig, AlbertModel

class CustomAlbert(AlbertModel):
    def forward(self, *args, **kwargs):
        # Call the original forward method
        outputs = super().forward(*args, **kwargs)
        # Only return the last_hidden_state
        return outputs.last_hidden_state

def load_plbert():
    plbert_config = {'vocab_size': 178, 'hidden_size': 768, 'num_attention_heads': 12, 'intermediate_size': 2048, 'max_position_embeddings': 512, 'num_hidden_layers': 12, 'dropout': 0.1}
    albert_base_configuration = AlbertConfig(**plbert_config)
    bert = CustomAlbert(albert_base_configuration)
    return bert
