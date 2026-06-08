# DRAFT / SKELETON — not wired into the build yet.
#
# Target location once real: llama.cpp `conversion/openai_privacy_filter.py`, registered in
# `conversion/__init__.py` ("OpenAIPrivacyFilterForTokenClassification": "openai_privacy_filter").
#
# Converts openai/privacy-filter + OpenMed/privacy-filter-multilingual (HF
# `OpenAIPrivacyFilterForTokenClassification`, model_type `openai_privacy_filter`) to GGUF.
#
# It subclasses the gpt-oss converter to reuse the o200k/harmony BPE vocab and the gpt-oss
# tensor map, and overrides only what differs (verified against the vendored
# `conversion/gpt_oss.py` @ commit 22d66b56 and the HF modular source):
#   1. a new arch (MODEL_ARCH.OPENAI_PRIVACY_FILTER) — see INTEGRATION.md
#   2. experts: privacy-filter packs gate_up as CONCATENATED halves (chunk(2)), NOT gpt-oss's
#      INTERLEAVED ::2/1::2 — this is the single most important override (wrong split = wrong model)
#   3. a token-classification head: HF `score.{weight,bias}` -> ggml `cls.output.{weight,bias}`
#   4. pooling = TOKEN_CLS + classifier output labels (depends on PR #19725's writer API)
#   5. bidirectional encoder: no lm_head; runtime runs non-causal (see INTEGRATION.md / load_hparams)
#
# privacy-filter ships bf16 dense experts (NOT MXFP4), so we always hit the gpt-oss
# "not in MXFP4" dense path (transpose + split); none of the MXFP4 repack code runs.

from __future__ import annotations

from typing import Iterable, TYPE_CHECKING

if TYPE_CHECKING:
    from torch import Tensor

from .base import ModelBase, gguf, logger
from .gpt_oss import GptOssModel


@ModelBase.register("OpenAIPrivacyFilterForTokenClassification")
class OpenAIPrivacyFilterModel(GptOssModel):
    model_arch = gguf.MODEL_ARCH.OPENAI_PRIVACY_FILTER  # NEW — add in gguf-py/gguf/constants.py

    def set_vocab(self):
        # identical to gpt-oss: o200k_base / harmony special tokens via GPT2 BPE export
        self._set_vocab_gpt2()

    def set_gguf_parameters(self):
        # GptOssModel.set_gguf_parameters writes: base text params + sliding_window +
        # expert_feed_forward_length + (inherited) rope/yarn. We keep all of it.
        super().set_gguf_parameters()

        # --- token-classification head ---
        # PoolingType.TOKEN_CLS == 5 (added by PR #19725 to gguf-py constants).
        self.gguf_writer.add_pooling_type(gguf.PoolingType.TOKEN_CLS)
        # ordered label list, index 0 == "O"; writer key "*.classifier.output_labels".
        # NOTE: confirm the exact writer method name against PR #19725
        # (add_classifier_output_labels(...) is what its diff adds); n_cls_out is derived
        # from the label count by the loader.
        self.gguf_writer.add_classifier_output_labels(self._ordered_labels())  # TODO: verify API

        # Bidirectional encoder. There is no dedicated "is_causal" GGUF key today; we make the
        # runtime non-causal in load_hparams (causal_attn=false) + SYMMETRIC SWA. See
        # INTEGRATION.md §"non-causal banded mask". sliding_window (=128, the HALF-window) is
        # already written by super(); the loader maps it to n_swa=256 for SWA_TYPE_SYMMETRIC.

    def _ordered_labels(self) -> list[str]:
        # HF id2label is {"0": "O", "1": "B-private_person", ...}; emit in index order so the
        # GGUF label table lines up with the score-head rows. 33 (base) / 217 (multilingual).
        id2label = self.hparams["id2label"]
        return [id2label[str(i)] for i in range(len(id2label))]

    def modify_tensors(self, data_torch: "Tensor", name: str, bid: int | None) -> Iterable[tuple[str, "Tensor"]]:
        # --- classification head -> cls.output ---
        # Relies on a "score" -> MODEL_TENSOR.CLS_OUT entry in tensor_mapping.py (add it), so
        # map_tensor_name resolves both score.weight and score.bias.
        if name in ("score.weight", "score.bias"):
            yield from super(GptOssModel, self).modify_tensors(data_torch, name, bid)
            return

        # --- experts: CONCATENATED (chunk) split, NOT gpt-oss interleaved ---
        # gpt-oss dense path does: transpose(-1,-2) then gate=[:, ::2, :], up=[:, 1::2, :].
        # privacy-filter's _apply_gate uses gate_up.chunk(2, dim=-1): gate = first half,
        # up = second half. After transpose [E, 2*inter, hidden] -> gate=[:, :inter, :],
        # up=[:, inter:, :].
        if "gate_up_proj" in name:
            inter = self.hparams["intermediate_size"]  # 640
            if name.endswith("_bias"):
                gate_b, up_b = data_torch[..., :inter], data_torch[..., inter:]
                name_gate = name.replace("gate_up_proj_bias", "gate_proj.bias")
                name_up = name.replace("gate_up_proj_bias", "up_proj.bias")
                # bypass GptOssModel.modify_tensors (interleaved) -> straight to TextModel
                yield from super(GptOssModel, self).modify_tensors(gate_b, name_gate, bid)
                yield from super(GptOssModel, self).modify_tensors(up_b, name_up, bid)
                return
            if "_blocks" not in name and "_scales" not in name:  # dense bf16 (always true here)
                data_torch = data_torch.transpose(-1, -2)
                gate_w, up_w = data_torch[:, :inter, :], data_torch[:, inter:, :]
                name_gate = name.replace("gate_up_proj", "gate_proj.weight")
                name_up = name.replace("gate_up_proj", "up_proj.weight")
                yield from super(GptOssModel, self).modify_tensors(gate_w, name_gate, bid)
                yield from super(GptOssModel, self).modify_tensors(up_w, name_up, bid)
                return
            logger.warning(f"unexpected MXFP4 expert tensor in privacy-filter: {name}")

        # down_proj (dense bf16): naming + transpose, same as gpt-oss non-MXFP4 path
        if "down_proj" in name and not name.endswith("_bias"):
            name = name.replace("down_proj", "down_proj.weight")
            data_torch = data_torch.transpose(-1, -2)
            yield from super(GptOssModel, self).modify_tensors(data_torch, name, bid)
            return

        # everything else (q/k/v/o + biases, attn sinks, router + bias, norms, embeddings):
        # gpt-oss handles these correctly (note filter_tensors appends ".weight" to sinks).
        yield from super().modify_tensors(data_torch, name, bid)

    # NOTE: we do NOT emit `output` / lm_head. The base may try to tie/emit an output tensor;
    # ensure the arch's tensor list (constants.py) omits MODEL_TENSOR.OUTPUT so nothing expects it.
